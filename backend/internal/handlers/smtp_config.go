package handlers

// D2 — SMTP configuration management API.
// Stores SMTP connection settings (host, port, username, password encrypted at
// rest, from address) in the smtp_config singleton row and mirrors the
// configuration into Keycloak's realm SMTP settings so outbound emails from
// Keycloak (password-reset, MFA) use the organisation's mail relay.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/httpx"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/notify"
)

// SMTPConfigResponse is returned by GET /api/v1/settings/smtp.
type SMTPConfigResponse struct {
	Host               string    `json:"host"`
	Port               string    `json:"port"`
	Username           string    `json:"username"`
	FromAddress        string    `json:"fromAddress"`
	PasswordConfigured bool      `json:"passwordConfigured"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// UpsertSMTPConfigRequest is the body for PUT /api/v1/settings/smtp.
type UpsertSMTPConfigRequest struct {
	Host        string `json:"host"`
	Port        string `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"` // plaintext; encrypted at rest
	FromAddress string `json:"fromAddress"`
}

// TestSMTPEmailRequest is the body for POST /api/v1/settings/smtp/test.
type TestSMTPEmailRequest struct {
	To string `json:"to"`
}

// GetSMTPConfig returns the stored SMTP settings (password is never returned).
// Route: GET /api/v1/settings/smtp
func (h *Handler) GetSMTPConfig(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	var (
		host        string
		port        string
		username    string
		fromAddress string
		passwordEnc *string
		updatedAt   time.Time
	)
	// "Configured" is derived from the encrypted value's presence. The legacy
	// password_hash column (unsalted SHA-256) is cleared by migration and
	// never written again.
	err := h.db.QueryRow(r.Context(),
		`SELECT host, port, username, from_address, password_enc, updated_at
		 FROM smtp_config WHERE id = 1`,
	).Scan(&host, &port, &username, &fromAddress, &passwordEnc, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusOK, SMTPConfigResponse{Port: "587"})
			return
		}
		h.logger.Error("get smtp config: query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	respondJSON(w, http.StatusOK, SMTPConfigResponse{
		Host:               host,
		Port:               port,
		Username:           username,
		FromAddress:        fromAddress,
		PasswordConfigured: passwordEnc != nil && *passwordEnc != "",
		UpdatedAt:          updatedAt,
	})
}

// SMTP password write modes used by UpsertSMTPConfig.
const (
	// smtpPasswordSet stores the supplied (encrypted) password.
	smtpPasswordSet = iota
	// smtpPasswordKeep leaves the stored password untouched.
	smtpPasswordKeep
	// smtpPasswordClear removes the stored password.
	smtpPasswordClear
)

// UpsertSMTPConfig saves SMTP connection settings and syncs them to Keycloak.
// Changing the host requires the password to be re-entered when one is
// stored, otherwise the old secret would silently follow the relay to the
// new destination (test and send both reuse the stored password).
// Route: PUT /api/v1/settings/smtp
func (h *Handler) UpsertSMTPConfig(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	var req UpsertSMTPConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Host = strings.TrimSpace(req.Host)
	req.Port = strings.TrimSpace(req.Port)
	if req.Port == "" {
		req.Port = "587"
	}
	if req.Host != "" {
		if err := validateSMTPHost(req.Host); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validateSMTPPort(req.Port); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	var pwEnc *string
	if req.Password != "" {
		enc, err := encryptSecret(SecretPurposeSMTPPassword, req.Password)
		if err != nil {
			h.logger.Error("upsert smtp config: encrypt password failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to encrypt password")
			return
		}
		pwEnc = &enc
	}

	// Capture the previous row so a Keycloak failure can be compensated by
	// restoring it — otherwise the DB and Keycloak end up describing two
	// different relays.
	prevHost, prevPort, prevUsername, prevFrom := "", "", "", ""
	// The legacy password_hash column is still selected so the row shape is
	// stable, but its value is deliberately discarded: it is cleared by
	// migration, never written, and "configured" comes from password_enc.
	var prevPwEnc, prevPwHashIgnored *string
	prevErr := h.db.QueryRow(ctx,
		`SELECT host, port, username, from_address, password_enc, password_hash FROM smtp_config WHERE id = 1`,
	).Scan(&prevHost, &prevPort, &prevUsername, &prevFrom, &prevPwEnc, &prevPwHashIgnored)
	if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
		h.logger.Error("upsert smtp config: read previous config failed", zap.Error(prevErr))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// A host-only update reuses the stored password on test/send, so a new
	// destination requires the password alongside it. Clearing the host is
	// exempt: with no relay configured nothing is sent anywhere.
	storedPassword := prevPwEnc != nil && *prevPwEnc != ""
	if req.Host != "" && storedPassword && req.Password == "" &&
		!strings.EqualFold(strings.TrimSpace(prevHost), req.Host) {
		respondError(w, http.StatusBadRequest, "changing host to a different relay requires the password in the same request")
		return
	}

	// writeConfig persists the SMTP config. mode decides what happens to
	// the stored password:
	//   smtpPasswordSet   — store enc (a new password was supplied),
	//   smtpPasswordKeep  — leave the stored password untouched,
	//   smtpPasswordClear — remove the stored password.
	// No mode writes a password hash: the legacy password_hash column is
	// only ever cleared (NULL), never given a digest value.
	writeConfig := func(host, port, username, from string, enc *string, mode int) error {
		switch mode {
		case smtpPasswordSet:
			_, err := h.db.Exec(ctx,
				`INSERT INTO smtp_config (id, host, port, username, password_enc, password_hash, from_address, updated_at)
				 VALUES (1, $1, $2, $3, $4, NULL, $5, NOW())
				 ON CONFLICT (id) DO UPDATE SET
				     host = EXCLUDED.host,
				     port = EXCLUDED.port,
				     username = EXCLUDED.username,
				     password_enc = EXCLUDED.password_enc,
				     password_hash = NULL,
				     from_address = EXCLUDED.from_address,
				     updated_at = NOW()`,
				host, port, username, enc, from,
			)
			return err
		case smtpPasswordClear:
			_, err := h.db.Exec(ctx,
				`INSERT INTO smtp_config (id, host, port, username, password_enc, password_hash, from_address, updated_at)
				 VALUES (1, $1, $2, $3, NULL, NULL, $4, NOW())
				 ON CONFLICT (id) DO UPDATE SET
				     host = EXCLUDED.host,
				     port = EXCLUDED.port,
				     username = EXCLUDED.username,
				     password_enc = NULL,
				     password_hash = NULL,
				     from_address = EXCLUDED.from_address,
				     updated_at = NOW()`,
				host, port, username, from,
			)
			return err
		default:
			_, err := h.db.Exec(ctx,
				`INSERT INTO smtp_config (id, host, port, username, from_address, updated_at)
				 VALUES (1, $1, $2, $3, $4, NOW())
				 ON CONFLICT (id) DO UPDATE SET
				     host = EXCLUDED.host,
				     port = EXCLUDED.port,
				     username = EXCLUDED.username,
				     from_address = EXCLUDED.from_address,
				     updated_at = NOW()`,
				host, port, username, from,
			)
			return err
		}
	}

	writeMode := smtpPasswordKeep
	if pwEnc != nil {
		writeMode = smtpPasswordSet
	}
	if err := writeConfig(req.Host, req.Port, req.Username, req.FromAddress, pwEnc, writeMode); err != nil {
		h.logger.Error("upsert smtp config: exec failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Mirror settings into Keycloak so its outbound emails use this relay.
	//
	// The realm SMTP password is only sent when one is actually configured:
	// sending an EMPTY password would wipe the password Keycloak already
	// holds while the DB kept the old one, silently breaking Keycloak's
	// outbound mail (password reset, MFA). When the request carries no
	// password, the stored one is re-sent so both sides stay in sync.
	kcPassword := req.Password
	if kcPassword == "" && prevPwEnc != nil && *prevPwEnc != "" {
		plaintext, rotated, decErr := decryptSecret(SecretPurposeSMTPPassword, *prevPwEnc)
		if decErr != nil {
			h.logger.Warn("upsert smtp config: decrypt stored password failed; keycloak password left untouched",
				zap.Error(decErr))
		} else {
			kcPassword = plaintext
			h.reencryptSMTPPasswordBestEffort(ctx, plaintext, rotated)
		}
	}
	kcCfg := keycloak.SMTPConfig{
		Host:     req.Host,
		Port:     req.Port,
		From:     req.FromAddress,
		Auth:     req.Username != "",
		User:     req.Username,
		Password: kcPassword,
		SSL:      req.Port == "465",
		StartTLS: req.Port == "587",
	}
	if kcErr := h.keycloak.UpdateRealmSMTP(ctx, kcCfg); kcErr != nil {
		// Fail closed: the Keycloak realm is now out of sync with the DB, so
		// the operator has to know. Restore the previous DB row so the two
		// sides describe the same relay again.
		h.logger.Error("upsert smtp config: keycloak update failed; restoring previous config", zap.Error(kcErr))
		restoreMode := smtpPasswordKeep
		if prevPwEnc != nil {
			restoreMode = smtpPasswordSet
		} else {
			restoreMode = smtpPasswordClear
		}
		if restoreErr := writeConfig(prevHost, prevPort, prevUsername, prevFrom, prevPwEnc, restoreMode); restoreErr != nil {
			h.logger.Error("upsert smtp config: failed to restore previous config", zap.Error(restoreErr))
		}
		respondError(w, http.StatusInternalServerError, "failed to apply SMTP settings to the identity provider")
		return
	}

	auditDetails := map[string]interface{}{"host": req.Host, "port": req.Port, "password_updated": req.Password != ""}
	if !strings.EqualFold(strings.TrimSpace(prevHost), req.Host) {
		// Repoint audit: old and new host, never the password.
		auditDetails["old_host"] = strings.TrimSpace(prevHost)
		auditDetails["new_host"] = req.Host
	}
	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "smtp_config.upsert", "smtp_config", "1",
		auditDetails); err != nil {
		h.logger.Warn("failed to write SMTP config audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// reencryptSMTPPasswordBestEffort rewrites an SMTP password sealed under the
// previous master key, completing a rotation opportunistically.
func (h *Handler) reencryptSMTPPasswordBestEffort(ctx context.Context, plaintext string, rotated bool) {
	if !rotated || h.db == nil {
		return
	}
	fresh, err := encryptSecret(SecretPurposeSMTPPassword, plaintext)
	if err != nil {
		h.logger.Warn("smtp config: re-encrypt password failed", zap.Error(err))
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE smtp_config SET password_enc = $1, updated_at = NOW() WHERE id = 1`,
		fresh,
	); err != nil {
		h.logger.Warn("smtp config: re-encrypt password failed", zap.Error(err))
	}
}

// smtpHostLabelPattern matches one DNS label (RFC 1035): alphanumerics and
// interior hyphens. Single-label names (compose services, intranet relays)
// are allowed — they are just one label.
var smtpHostLabelPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// validateSMTPHost rejects hosts that are not a bare IP literal or hostname:
// no userinfo, scheme, path, port or whitespace. Loopback, link-local and
// unspecified addresses are blocked outside development/test; private
// (RFC1918/ULA) relays stay allowed everywhere because that is where
// corporate mail relays normally live.
func validateSMTPHost(host string) error {
	h := host
	if strings.HasPrefix(h, "[") {
		// Bracketed IPv6 literal.
		if !strings.HasSuffix(h, "]") {
			return errors.New("host must be a valid hostname or IP address")
		}
		h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	}
	if ip := net.ParseIP(h); ip != nil {
		if !httpx.IsDevOrTestEnv() && httpx.IsBlockedLoopbackOrLinkLocal(ip) {
			return errors.New("host must not target a loopback or link-local address")
		}
		return nil
	}
	if len(h) == 0 || len(h) > 253 {
		return errors.New("host must be a valid hostname or IP address")
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if !smtpHostLabelPattern.MatchString(label) {
			return errors.New("host must be a valid hostname or IP address")
		}
	}
	return httpx.ValidateHostAddresses("host", h, httpx.IsBlockedLoopbackOrLinkLocal)
}

// validateSMTPPort accepts TCP ports 1-65535.
func validateSMTPPort(port string) error {
	n, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || n < 1 || n > 65535 {
		return errors.New("port must be a number between 1 and 65535")
	}
	return nil
}

// TestSMTPEmail sends a test email to the provided address using the stored config.
// Route: POST /api/v1/settings/smtp/test
func (h *Handler) TestSMTPEmail(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	var req TestSMTPEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.To = strings.TrimSpace(req.To)
	if req.To == "" {
		respondError(w, http.StatusBadRequest, "to address is required")
		return
	}
	// The message is assembled by raw string concatenation, so an address
	// containing CR/LF would inject arbitrary SMTP headers (Bcc, Subject,
	// ...). Reject anything that is not a plain mail address.
	if err := validateSMTPTestRecipient(req.To); err != nil {
		respondError(w, http.StatusBadRequest, "invalid 'to' address: "+err.Error())
		return
	}

	ctx := r.Context()

	var (
		host        string
		port        string
		username    string
		fromAddress string
		pwEncVal    *string
	)
	err := h.db.QueryRow(ctx,
		`SELECT host, port, username, from_address, password_enc FROM smtp_config WHERE id = 1`,
	).Scan(&host, &port, &username, &fromAddress, &pwEncVal)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusOK, map[string]interface{}{"sent": false, "error": "SMTP not configured"})
			return
		}
		h.logger.Error("test smtp email: query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if host == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"sent": false, "error": "SMTP host not configured"})
		return
	}

	var password string
	if pwEncVal != nil && *pwEncVal != "" {
		pw, rotated, decErr := decryptSecret(SecretPurposeSMTPPassword, *pwEncVal)
		if decErr != nil {
			h.logger.Warn("test smtp email: decrypt password failed", zap.Error(decErr))
		} else {
			password = pw
			h.reencryptSMTPPasswordBestEffort(ctx, pw, rotated)
		}
	}

	addr := host + ":" + port
	var auth smtp.Auth
	if username != "" && password != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}

	// The From address is operator-supplied configuration, but it lands in a
	// raw header too — a CR/LF there would inject headers as well.
	if err := notify.ValidateMailAddress(fromAddress); err != nil {
		h.logger.Warn("test smtp email: configured from address is invalid", zap.Error(err))
		respondJSON(w, http.StatusOK, map[string]interface{}{"sent": false, "error": "configured from address is invalid"})
		return
	}

	body := "From: " + fromAddress + "\r\n" +
		"To: " + req.To + "\r\n" +
		"Subject: FreeCloud SMTP test\r\n" +
		"\r\n" +
		"This is a test email from FreeCloud to verify your SMTP configuration.\r\n"

	// notify.SendMail bounds the dial and the SMTP conversation by the
	// request context, so a hung relay cannot pin this handler goroutine
	// (net/smtp's SendMail had neither a timeout nor context support).
	sendCtx, cancel := context.WithTimeout(ctx, smtpSendTimeout)
	defer cancel()
	sendErr := notify.SendMail(sendCtx, addr, auth, fromAddress, []string{req.To}, []byte(body))
	if sendErr != nil {
		// The transport error can carry internal topology (relay address,
		// TLS details); the detail stays in the server log and the caller
		// gets a generic failure.
		h.logger.Warn("test smtp email: send failed", zap.Error(sendErr))
		respondJSON(w, http.StatusOK, map[string]interface{}{"sent": false, "error": "failed to send test email"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"sent": true})
}

// smtpSendTimeout bounds a single SMTP test send.
const smtpSendTimeout = 15 * time.Second

// validateSMTPTestRecipient rejects anything that is not a bare mail
// address. A display name ("Alice <a@b.com>") is rejected as well: it would
// have to be re-encoded into the To header, and the simple form is what the
// rest of this codebase stores.
func validateSMTPTestRecipient(addr string) error {
	if strings.ContainsAny(addr, "\r\n") {
		return errors.New("address must not contain a line break")
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil {
		return errors.New("not a valid mail address")
	}
	if parsed.Address != addr {
		return errors.New("must be a bare mail address without a display name")
	}
	return nil
}
