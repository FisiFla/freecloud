package handlers

// D1 — Fleet configuration management API.
// Stores Fleet server URL and API token (encrypted at rest) in the fleet_config
// singleton row. The test endpoint creates a one-shot Fleet client to verify
// connectivity without restarting the server.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/httpx"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// FleetConfigResponse is returned by GET /api/v1/settings/fleet.
type FleetConfigResponse struct {
	ServerURL          string    `json:"serverUrl"`
	APITokenConfigured bool      `json:"apiTokenConfigured"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// UpsertFleetConfigRequest is the body for PUT /api/v1/settings/fleet.
type UpsertFleetConfigRequest struct {
	ServerURL string `json:"serverUrl"`
	APIToken  string `json:"apiToken,omitempty"` // plaintext; encrypted at rest, never echoed back
}

// GetFleetConfig returns the stored Fleet server URL and whether an API token is configured.
// Route: GET /api/v1/settings/fleet
func (h *Handler) GetFleetConfig(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	var (
		serverURL   string
		apiTokenEnc *string
		updatedAt   time.Time
	)
	// "Configured" is derived from the encrypted value's presence. The legacy
	// api_token_hash column (unsalted SHA-256) is cleared by migration and
	// never written again.
	err := h.db.QueryRow(r.Context(),
		`SELECT server_url, api_token_enc, updated_at FROM fleet_config WHERE id = 1`,
	).Scan(&serverURL, &apiTokenEnc, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Row should always exist (seeded by migration), but handle gracefully.
			respondJSON(w, http.StatusOK, FleetConfigResponse{})
			return
		}
		h.logger.Error("get fleet config: query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	respondJSON(w, http.StatusOK, FleetConfigResponse{
		ServerURL:          serverURL,
		APITokenConfigured: apiTokenEnc != nil && *apiTokenEnc != "",
		UpdatedAt:          updatedAt,
	})
}

// UpsertFleetConfig saves the Fleet server URL and (optionally) API token.
// The API token is AES-256-GCM encrypted (v1 envelope, fleet-token AAD);
// "configured" is derived from the encrypted value's presence. The plaintext
// token is never echoed back. Repointing serverUrl to a different host
// requires the API token in the same request, otherwise a stored token would
// silently follow the URL to the new destination.
// Route: PUT /api/v1/settings/fleet
func (h *Handler) UpsertFleetConfig(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	var req UpsertFleetConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// SSRF guard: the backend fetches this URL server-side, so a super-admin
	// could otherwise point it at the cloud metadata service or an internal
	// compose service. The provisioning endpoint URL is validated by the
	// same guard; the Fleet server URL must be too.
	req.ServerURL = strings.TrimSpace(req.ServerURL)
	if req.ServerURL != "" {
		if err := validateOutboundURL("serverUrl", req.ServerURL); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	// A URL-only update keeps the stored token, so changing the host without
	// a token would send the existing secret to the new destination. Read
	// the current row and require the token alongside any host change.
	var prevURL string
	var prevTokenEnc *string
	err := h.db.QueryRow(ctx,
		`SELECT server_url, api_token_enc FROM fleet_config WHERE id = 1`,
	).Scan(&prevURL, &prevTokenEnc)
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		// Fresh config: no stored secret can follow the URL.
	default:
		h.logger.Error("upsert fleet config: read current row failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	storedToken := prevTokenEnc != nil && *prevTokenEnc != ""
	if storedToken && req.APIToken == "" && !sameOutboundHost(prevURL, req.ServerURL) {
		respondError(w, http.StatusBadRequest, "changing serverUrl to a different host requires the API token in the same request")
		return
	}

	var tokenEnc *string
	if req.APIToken != "" {
		enc, err := encryptSecret(SecretPurposeFleetToken, req.APIToken)
		if err != nil {
			h.logger.Error("upsert fleet config: encrypt token failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to encrypt API token")
			return
		}
		tokenEnc = &enc
	}

	if tokenEnc != nil {
		_, err := h.db.Exec(ctx,
			`INSERT INTO fleet_config (id, server_url, api_token_enc, api_token_hash, updated_at)
			 VALUES (1, $1, $2, NULL, NOW())
			 ON CONFLICT (id) DO UPDATE SET
			     server_url = EXCLUDED.server_url,
			     api_token_enc = EXCLUDED.api_token_enc,
			     api_token_hash = NULL,
			     updated_at = NOW()`,
			req.ServerURL, tokenEnc,
		)
		if err != nil {
			h.logger.Error("upsert fleet config: exec failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		_, err := h.db.Exec(ctx,
			`INSERT INTO fleet_config (id, server_url, updated_at)
			 VALUES (1, $1, NOW())
			 ON CONFLICT (id) DO UPDATE SET
			     server_url = EXCLUDED.server_url,
			     updated_at = NOW()`,
			req.ServerURL,
		)
		if err != nil {
			h.logger.Error("upsert fleet config: exec (url-only) failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	auditDetails := map[string]interface{}{"server_url": req.ServerURL, "token_updated": req.APIToken != ""}
	if !sameOutboundHost(prevURL, req.ServerURL) {
		// Repoint audit: old and new host, never the token.
		auditDetails["old_host"] = urlHostLower(prevURL)
		auditDetails["new_host"] = urlHostLower(req.ServerURL)
	}
	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "fleet_config.upsert", "fleet_config", "1",
		auditDetails); err != nil {
		h.logger.Warn("failed to write fleet config audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// urlHostLower returns the lowercased hostname of raw, or "" when raw is not
// a parseable URL. Unparseable hosts compare unequal to everything (fail
// closed in sameOutboundHost).
func urlHostLower(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// sameOutboundHost reports whether two URLs share a hostname. Empty hosts
// only equal each other when both URLs are empty.
func sameOutboundHost(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return strings.TrimSpace(a) == "" && strings.TrimSpace(b) == ""
	}
	ha, hb := urlHostLower(a), urlHostLower(b)
	return ha != "" && ha == hb
}

// fleetEndpointCredentials resolves the effective Fleet server URL and API
// token: the saved fleet_config row wins, the FLEET_URL / FLEET_API_TOKEN
// environment variables are the fallback — the same precedence the dynamic
// Fleet client uses. fromDB reports whether the URL came from the database.
//
// Token scoping is strict: the environment fallback token is only ever
// returned together with the environment URL. When a DB URL is configured,
// only the DB token is used — substituting the env token there would send a
// secret the admin never attached to that destination.
func (h *Handler) fleetEndpointCredentials(ctx context.Context) (string, string, bool, error) {
	fallbackURL := strings.TrimSpace(os.Getenv("FLEET_URL"))
	fallbackToken := strings.TrimSpace(os.Getenv("FLEET_API_TOKEN"))

	if h.db != nil {
		var serverURL string
		var tokenEnc *string
		err := h.db.QueryRow(ctx,
			`SELECT server_url, api_token_enc FROM fleet_config WHERE id = 1`,
		).Scan(&serverURL, &tokenEnc)
		switch {
		case err == nil:
			if trimmed := strings.TrimSpace(serverURL); trimmed != "" {
				token := ""
				if tokenEnc != nil && *tokenEnc != "" {
					plaintext, rotated, decErr := decryptSecret(SecretPurposeFleetToken, *tokenEnc)
					if decErr != nil {
						h.logger.Warn("fleet config: decrypt token failed", zap.Error(decErr))
					} else {
						token = plaintext
						h.reencryptFleetTokenBestEffort(ctx, plaintext, rotated)
					}
				}
				return strings.TrimRight(trimmed, "/"), token, true, nil
			}
			// Saved row without a URL — fall through to the environment.
		case errors.Is(err, pgx.ErrNoRows):
			// No saved row — keep the environment fallback.
		default:
			return "", "", false, fmt.Errorf("read fleet config: %w", err)
		}
	}
	if fallbackURL == "" {
		return "", "", false, errors.New("no fleet server URL configured")
	}
	return strings.TrimRight(fallbackURL, "/"), fallbackToken, false, nil
}

// reencryptFleetTokenBestEffort rewrites a Fleet token that was sealed under
// the previous master key, completing a rotation opportunistically.
func (h *Handler) reencryptFleetTokenBestEffort(ctx context.Context, plaintext string, rotated bool) {
	if !rotated || h.db == nil {
		return
	}
	fresh, err := encryptSecret(SecretPurposeFleetToken, plaintext)
	if err != nil {
		h.logger.Warn("fleet config: re-encrypt token failed", zap.Error(err))
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE fleet_config SET api_token_enc = $1, updated_at = NOW() WHERE id = 1`,
		fresh,
	); err != nil {
		h.logger.Warn("fleet config: re-encrypt token failed", zap.Error(err))
	}
}

// deleteFleetTeam removes a Fleet team by ID.
//
// It is the compensating action for a failed fleet_team_orgs mapping
// insert: without it the Fleet team is orphaned — invisible to non-admins
// and still consuming a globally unique team name, which makes the
// operator's retry fail with "name already taken".
func (h *Handler) deleteFleetTeam(ctx context.Context, teamID int) error {
	baseURL, token, fromDB, err := h.fleetEndpointCredentials(ctx)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/api/v1/fleet/teams/%d", baseURL, teamID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build fleet delete request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// A DB-configured URL is admin-controlled: use the hardened outbound
	// client so a repointed URL cannot reach a blocked destination.
	client := &http.Client{Timeout: 15 * time.Second}
	if fromDB {
		client = httpx.NewOutboundClient(15 * time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fleet delete team request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fleet delete team returned %d", resp.StatusCode)
	}
	return nil
}

// TestFleetConfig pings Fleet using the stored config (or env vars as fallback).
// Creates a temporary fleet client — does NOT use h.fleet which is wired at startup.
// Route: POST /api/v1/settings/fleet/test
func (h *Handler) TestFleetConfig(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()

	var (
		serverURL   string
		tokenEncVal *string
	)
	err := h.db.QueryRow(ctx,
		`SELECT server_url, api_token_enc FROM fleet_config WHERE id = 1`,
	).Scan(&serverURL, &tokenEncVal)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.logger.Error("test fleet config: query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var decryptedToken string
	if tokenEncVal != nil && *tokenEncVal != "" {
		t, rotated, decErr := decryptSecret(SecretPurposeFleetToken, *tokenEncVal)
		if decErr != nil {
			h.logger.Warn("test fleet config: decrypt token failed", zap.Error(decErr))
			// Fall through with empty token
		} else {
			decryptedToken = t
			h.reencryptFleetTokenBestEffort(ctx, t, rotated)
		}
	}

	if serverURL == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": "no fleet server URL configured"})
		return
	}

	// The tested URL is the DB-configured (admin-controlled) one: use the
	// hardened outbound client.
	client := fleet.NewOutboundClient(serverURL, decryptedToken)
	pingErr := client.Ping(ctx)
	if pingErr != nil {
		// The raw Fleet error can contain the server URL and the request
		// path — internal topology that must not be echoed to the caller.
		// The detail goes to the log; the client gets a generic message.
		h.logger.Error("test fleet config: ping failed", zap.String("server_url", serverURL), zap.Error(pingErr))
		respondJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": "fleet server is unreachable"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
