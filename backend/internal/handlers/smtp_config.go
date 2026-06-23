package handlers

// D2 — SMTP configuration management API.
// Stores SMTP connection settings (host, port, username, password encrypted at
// rest, from address) in the smtp_config singleton row and mirrors the
// configuration into Keycloak's realm SMTP settings so outbound emails from
// Keycloak (password-reset, MFA) use the organisation's mail relay.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// SMTPConfigResponse is returned by GET /api/v1/settings/smtp.
type SMTPConfigResponse struct {
	Host                string    `json:"host"`
	Port                string    `json:"port"`
	Username            string    `json:"username"`
	FromAddress         string    `json:"fromAddress"`
	PasswordConfigured  bool      `json:"passwordConfigured"`
	UpdatedAt           time.Time `json:"updatedAt"`
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
		host          string
		port          string
		username      string
		fromAddress   string
		passwordHash  *string
		updatedAt     time.Time
	)
	err := h.db.QueryRow(r.Context(),
		`SELECT host, port, username, from_address, password_hash, updated_at
		 FROM smtp_config WHERE id = 1`,
	).Scan(&host, &port, &username, &fromAddress, &passwordHash, &updatedAt)
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
		PasswordConfigured: passwordHash != nil && *passwordHash != "",
		UpdatedAt:          updatedAt,
	})
}

// UpsertSMTPConfig saves SMTP connection settings and syncs them to Keycloak.
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

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	var pwEnc *string
	var pwHash *string
	if req.Password != "" {
		enc, err := encryptProvisioningToken(req.Password)
		if err != nil {
			h.logger.Error("upsert smtp config: encrypt password failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to encrypt password")
			return
		}
		hash := tokenSHA256(req.Password)
		pwEnc = &enc
		pwHash = &hash
	}

	if req.Port == "" {
		req.Port = "587"
	}

	if pwEnc != nil {
		_, err := h.db.Exec(ctx,
			`INSERT INTO smtp_config (id, host, port, username, password_enc, password_hash, from_address, updated_at)
			 VALUES (1, $1, $2, $3, $4, $5, $6, NOW())
			 ON CONFLICT (id) DO UPDATE SET
			     host = EXCLUDED.host,
			     port = EXCLUDED.port,
			     username = EXCLUDED.username,
			     password_enc = EXCLUDED.password_enc,
			     password_hash = EXCLUDED.password_hash,
			     from_address = EXCLUDED.from_address,
			     updated_at = NOW()`,
			req.Host, req.Port, req.Username, pwEnc, pwHash, req.FromAddress,
		)
		if err != nil {
			h.logger.Error("upsert smtp config: exec failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		_, err := h.db.Exec(ctx,
			`INSERT INTO smtp_config (id, host, port, username, from_address, updated_at)
			 VALUES (1, $1, $2, $3, $4, NOW())
			 ON CONFLICT (id) DO UPDATE SET
			     host = EXCLUDED.host,
			     port = EXCLUDED.port,
			     username = EXCLUDED.username,
			     from_address = EXCLUDED.from_address,
			     updated_at = NOW()`,
			req.Host, req.Port, req.Username, req.FromAddress,
		)
		if err != nil {
			h.logger.Error("upsert smtp config: exec (no-password) failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Mirror settings into Keycloak so its outbound emails use this relay.
	kcCfg := keycloak.SMTPConfig{
		Host:     req.Host,
		Port:     req.Port,
		From:     req.FromAddress,
		Auth:     req.Username != "",
		User:     req.Username,
		Password: req.Password,
		SSL:      req.Port == "465",
		StartTLS: req.Port == "587",
	}
	if kcErr := h.keycloak.UpdateRealmSMTP(ctx, kcCfg); kcErr != nil {
		h.logger.Warn("upsert smtp config: keycloak update failed", zap.Error(kcErr))
		// Best-effort — do not fail the whole request.
	}

	_ = h.writeAuditEntryBestEffort(actorID, "smtp_config.upsert", "smtp_config", "1",
		map[string]interface{}{"host": req.Host, "port": req.Port, "password_updated": req.Password != ""})

	respondJSON(w, http.StatusOK, map[string]bool{"saved": true})
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
		pw, decErr := decryptProvisioningToken(*pwEncVal)
		if decErr != nil {
			h.logger.Warn("test smtp email: decrypt password failed", zap.Error(decErr))
		} else {
			password = pw
		}
	}

	addr := host + ":" + port
	var auth smtp.Auth
	if username != "" && password != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}

	body := "From: " + fromAddress + "\r\n" +
		"To: " + req.To + "\r\n" +
		"Subject: FreeCloud SMTP test\r\n" +
		"\r\n" +
		"This is a test email from FreeCloud to verify your SMTP configuration.\r\n"

	sendErr := smtp.SendMail(addr, auth, fromAddress, []string{req.To}, []byte(body))
	if sendErr != nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"sent": false, "error": sendErr.Error()})
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"sent": true})
}
