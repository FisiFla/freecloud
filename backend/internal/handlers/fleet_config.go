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
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
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
		serverURL      string
		apiTokenHash   *string
		updatedAt      time.Time
	)
	err := h.db.QueryRow(r.Context(),
		`SELECT server_url, api_token_hash, updated_at FROM fleet_config WHERE id = 1`,
	).Scan(&serverURL, &apiTokenHash, &updatedAt)
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
		APITokenConfigured: apiTokenHash != nil && *apiTokenHash != "",
		UpdatedAt:          updatedAt,
	})
}

// UpsertFleetConfig saves the Fleet server URL and (optionally) API token.
// The API token is AES-256-GCM encrypted and its SHA-256 hash stored for
// "configured" detection. The plaintext token is never echoed back.
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

	var tokenEnc *string
	var tokenHash *string
	if req.APIToken != "" {
		enc, err := encryptProvisioningToken(req.APIToken)
		if err != nil {
			h.logger.Error("upsert fleet config: encrypt token failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to encrypt API token")
			return
		}
		hash := tokenSHA256(req.APIToken)
		tokenEnc = &enc
		tokenHash = &hash
	}

	if tokenEnc != nil {
		_, err := h.db.Exec(ctx,
			`INSERT INTO fleet_config (id, server_url, api_token_enc, api_token_hash, updated_at)
			 VALUES (1, $1, $2, $3, NOW())
			 ON CONFLICT (id) DO UPDATE SET
			     server_url = EXCLUDED.server_url,
			     api_token_enc = EXCLUDED.api_token_enc,
			     api_token_hash = EXCLUDED.api_token_hash,
			     updated_at = NOW()`,
			req.ServerURL, tokenEnc, tokenHash,
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

	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "fleet_config.upsert", "fleet_config", "1",
		map[string]interface{}{"server_url": req.ServerURL, "token_updated": req.APIToken != ""}); err != nil {
		h.logger.Warn("failed to write fleet config audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// fleetEndpointCredentials resolves the effective Fleet server URL and API
// token: the saved fleet_config row wins, the FLEET_URL / FLEET_API_TOKEN
// environment variables are the fallback — the same precedence the dynamic
// Fleet client uses.
func (h *Handler) fleetEndpointCredentials(ctx context.Context) (string, string, error) {
	baseURL := strings.TrimSpace(os.Getenv("FLEET_URL"))
	token := strings.TrimSpace(os.Getenv("FLEET_API_TOKEN"))

	if h.db != nil {
		var serverURL string
		var tokenEnc *string
		err := h.db.QueryRow(ctx,
			`SELECT server_url, api_token_enc FROM fleet_config WHERE id = 1`,
		).Scan(&serverURL, &tokenEnc)
		switch {
		case err == nil:
			if trimmed := strings.TrimSpace(serverURL); trimmed != "" {
				baseURL = trimmed
			}
			if tokenEnc != nil && *tokenEnc != "" {
				plaintext, decErr := decryptProvisioningToken(*tokenEnc)
				if decErr != nil {
					h.logger.Warn("fleet config: decrypt token failed; using the fallback token", zap.Error(decErr))
				} else {
					token = plaintext
				}
			}
		case errors.Is(err, pgx.ErrNoRows):
			// No saved row — keep the environment fallback.
		default:
			return "", "", fmt.Errorf("read fleet config: %w", err)
		}
	}
	if baseURL == "" {
		return "", "", errors.New("no fleet server URL configured")
	}
	return strings.TrimRight(baseURL, "/"), token, nil
}

// deleteFleetTeam removes a Fleet team by ID.
//
// It is the compensating action for a failed fleet_team_orgs mapping
// insert: without it the Fleet team is orphaned — invisible to non-admins
// and still consuming a globally unique team name, which makes the
// operator's retry fail with "name already taken".
func (h *Handler) deleteFleetTeam(ctx context.Context, teamID int) error {
	baseURL, token, err := h.fleetEndpointCredentials(ctx)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/fleet/teams/%d", baseURL, teamID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("build fleet delete request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
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
		t, decErr := decryptProvisioningToken(*tokenEncVal)
		if decErr != nil {
			h.logger.Warn("test fleet config: decrypt token failed", zap.Error(decErr))
			// Fall through with empty token
		} else {
			decryptedToken = t
		}
	}

	if serverURL == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": "no fleet server URL configured"})
		return
	}

	client := fleet.NewClient(serverURL, decryptedToken)
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
