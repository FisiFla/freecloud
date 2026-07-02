package handlers

// A4 — Provisioning management API.
// Per-app provisioning config (GET/PUT) and per-user sync status endpoints.
// All writes are audited; bearer tokens are encrypted at rest (AES-256-GCM).

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/provisioning"
)

// ProvisioningConfigRequest is the body for PUT /api/v1/apps/{appId}/provisioning.
type ProvisioningConfigRequest struct {
	Enabled       bool              `json:"enabled"`
	ConnectorType string            `json:"connectorType"` // "scim", "slack", "github"
	EndpointURL   string            `json:"endpointUrl,omitempty"`
	BearerToken   string            `json:"bearerToken,omitempty"` // plaintext; encrypted at rest, never echoed back
	AttributeMap  map[string]string `json:"attributeMap,omitempty"`
}

// ProvisioningConfigResponse is returned by GET and PUT provisioning config.
type ProvisioningConfigResponse struct {
	AppID                 string            `json:"appId"`
	Enabled               bool              `json:"enabled"`
	ConnectorType         string            `json:"connectorType"`
	EndpointURL           string            `json:"endpointUrl,omitempty"`
	BearerTokenConfigured bool              `json:"bearerTokenConfigured"`
	AttributeMap          map[string]string `json:"attributeMap"`
}

// ProvisioningStateEntry represents one provisioning_state row.
type ProvisioningStateEntry struct {
	ID          string  `json:"id"`
	UserID      string  `json:"userId"`
	UserEmail   string  `json:"userEmail,omitempty"`
	RemoteID    string  `json:"remoteId,omitempty"`
	Status      string  `json:"status"`
	LastSyncAt  *string `json:"lastSyncAt,omitempty"`
	LastError   string  `json:"lastError,omitempty"`
	RetryCount  int     `json:"retryCount"`
	NextRetryAt *string `json:"nextRetryAt,omitempty"`
}

// GetProvisioningConfig returns the provisioning config for an app.
func (h *Handler) GetProvisioningConfig(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if !isValidUUID(appID) {
		respondError(w, http.StatusBadRequest, "invalid appId")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireAppInCallerOrg(w, r, appID) {
		return
	}

	var (
		enabled           bool
		connectorType     string
		endpointURL       string
		bearerTokenHash   *string
		attributeMapBytes []byte
	)
	err := h.db.QueryRow(r.Context(),
		`SELECT enabled, connector_type, COALESCE(endpoint_url, ''), bearer_token_hash, attribute_map
		 FROM app_provisioning_config WHERE app_id = $1`,
		appID,
	).Scan(&enabled, &connectorType, &endpointURL, &bearerTokenHash, &attributeMapBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondJSON(w, http.StatusOK, ProvisioningConfigResponse{
				AppID:                 appID,
				Enabled:               false,
				ConnectorType:         "scim",
				BearerTokenConfigured: false,
				AttributeMap:          map[string]string{},
			})
			return
		}
		h.logger.Error("get provisioning config: query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	attrMap := make(map[string]string)
	if len(attributeMapBytes) > 0 {
		_ = json.Unmarshal(attributeMapBytes, &attrMap)
	}

	respondJSON(w, http.StatusOK, ProvisioningConfigResponse{
		AppID:                 appID,
		Enabled:               enabled,
		ConnectorType:         connectorType,
		EndpointURL:           endpointURL,
		BearerTokenConfigured: bearerTokenHash != nil && *bearerTokenHash != "",
		AttributeMap:          attrMap,
	})
}

// UpsertProvisioningConfig creates or updates the provisioning config for an app.
// The bearer token is encrypted at rest (AES-256-GCM) and its SHA-256 hash stored
// for "configured" detection. The plaintext is never echoed back.
func (h *Handler) UpsertProvisioningConfig(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if !isValidUUID(appID) {
		respondError(w, http.StatusBadRequest, "invalid appId")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireAppInCallerOrg(w, r, appID) {
		return
	}

	var req ProvisioningConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.ConnectorType = strings.TrimSpace(req.ConnectorType)
	if req.ConnectorType == "" {
		req.ConnectorType = "scim"
	}
	validConnectors := map[string]bool{"scim": true, "slack": true, "github": true}
	if !validConnectors[req.ConnectorType] {
		respondError(w, http.StatusBadRequest, "connectorType must be scim, slack, or github")
		return
	}

	attrMapJSON := []byte(`{}`)
	if len(req.AttributeMap) > 0 {
		b, err := json.Marshal(req.AttributeMap)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid attributeMap")
			return
		}
		attrMapJSON = b
	}

	// Encrypt the bearer token if provided.
	var tokenHash *string
	var tokenEnc *string
	if req.BearerToken != "" {
		h256 := sha256.Sum256([]byte(req.BearerToken))
		hStr := fmt.Sprintf("%x", h256)
		tokenHash = &hStr

		enc, err := encryptProvisioningToken(req.BearerToken)
		if err != nil {
			h.logger.Error("upsert provisioning config: encrypt token failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to encrypt bearer token")
			return
		}
		tokenEnc = &enc
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	_, err := h.db.Exec(ctx,
		`INSERT INTO app_provisioning_config
		   (app_id, enabled, connector_type, endpoint_url, bearer_token_hash, bearer_token_enc, attribute_map, org_id)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8)
		 ON CONFLICT (app_id) DO UPDATE
		   SET enabled         = EXCLUDED.enabled,
		       connector_type  = EXCLUDED.connector_type,
		       endpoint_url    = EXCLUDED.endpoint_url,
		       bearer_token_hash = COALESCE(EXCLUDED.bearer_token_hash, app_provisioning_config.bearer_token_hash),
		       bearer_token_enc  = COALESCE(EXCLUDED.bearer_token_enc,  app_provisioning_config.bearer_token_enc),
		       attribute_map   = EXCLUDED.attribute_map,
		       updated_at      = NOW()`,
		appID, req.Enabled, req.ConnectorType, req.EndpointURL, tokenHash, tokenEnc, string(attrMapJSON), oc.OrgID,
	)
	if err != nil {
		h.logger.Error("upsert provisioning config: exec failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to save provisioning config")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "provisioning_config.upsert", "app", appID, map[string]interface{}{
		"enabled":        req.Enabled,
		"connector_type": req.ConnectorType,
		"token_changed":  req.BearerToken != "",
	})
	if h.provisionEngine != nil {
		if err := ReloadProvisioningConnectors(ctx, h.provisionEngine, h.db, h.logger); err != nil {
			h.logger.Warn("upsert provisioning config: reload connectors failed", zap.Error(err))
		}
	}

	tokenConfigured := tokenHash != nil
	if !tokenConfigured {
		var savedHash *string
		if err := h.db.QueryRow(ctx,
			`SELECT bearer_token_hash FROM app_provisioning_config WHERE app_id = $1`,
			appID,
		).Scan(&savedHash); err == nil && savedHash != nil && *savedHash != "" {
			tokenConfigured = true
		}
	}

	attrMap := req.AttributeMap
	if attrMap == nil {
		attrMap = map[string]string{}
	}
	respondJSON(w, http.StatusOK, ProvisioningConfigResponse{
		AppID:                 appID,
		Enabled:               req.Enabled,
		ConnectorType:         req.ConnectorType,
		EndpointURL:           req.EndpointURL,
		BearerTokenConfigured: tokenConfigured,
		AttributeMap:          attrMap,
	})
}

// ListProvisioningState returns per-user provisioning state for an app.
func (h *Handler) ListProvisioningState(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if !isValidUUID(appID) {
		respondError(w, http.StatusBadRequest, "invalid appId")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireAppInCallerOrg(w, r, appID) {
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT ps.id::TEXT, ps.user_id::TEXT, COALESCE(u.email, ''), COALESCE(ps.remote_id, ''),
		        ps.status, ps.last_sync_at, COALESCE(ps.last_error, ''), ps.retry_count, ps.next_retry_at
		 FROM provisioning_state ps
		 LEFT JOIN users u ON u.keycloak_user_id = ps.user_id
		 WHERE ps.app_id = $1
		 ORDER BY ps.updated_at DESC`,
		appID,
	)
	if err != nil {
		h.logger.Error("list provisioning state: query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var entries []ProvisioningStateEntry
	for rows.Next() {
		var e ProvisioningStateEntry
		var lastSyncAt *time.Time
		var nextRetryAt *time.Time
		if err := rows.Scan(&e.ID, &e.UserID, &e.UserEmail, &e.RemoteID, &e.Status, &lastSyncAt, &e.LastError, &e.RetryCount, &nextRetryAt); err != nil {
			h.logger.Warn("list provisioning state: scan failed", zap.Error(err))
			continue
		}
		if lastSyncAt != nil {
			s := lastSyncAt.Format(time.RFC3339)
			e.LastSyncAt = &s
		}
		if nextRetryAt != nil {
			s := nextRetryAt.Format(time.RFC3339)
			e.NextRetryAt = &s
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("list provisioning state: iterate failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if entries == nil {
		entries = []ProvisioningStateEntry{}
	}
	respondJSON(w, http.StatusOK, entries)
}

// ResyncUser triggers a manual provisioning resync for one user×app pair.
// The sync runs asynchronously; the handler returns 202 immediately.
func (h *Handler) ResyncUser(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	userID := chi.URLParam(r, "userId")
	if !isValidUUID(appID) || !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "invalid appId or userId")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireAppInCallerOrg(w, r, appID) {
		return
	}
	if !h.requireUserInCallerOrg(w, r, userID) {
		return
	}
	if h.provisionEngine == nil {
		respondError(w, http.StatusServiceUnavailable, "provisioning engine not configured")
		return
	}
	if !h.provisionEngine.HasConnector(appID) {
		respondError(w, http.StatusServiceUnavailable, "provisioning connector not configured")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	// Load user profile.
	var email, firstName, lastName, department string
	err := h.db.QueryRow(ctx,
		`SELECT email, first_name, last_name, COALESCE(department, '') FROM users WHERE keycloak_user_id = $1`,
		userID,
	).Scan(&email, &firstName, &lastName, &department)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("resync user: load user failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "provisioning.resync", "user", userID,
		map[string]interface{}{"app_id": appID})

	capturedAppID := appID
	capturedUser := provisioning.ProvisionableUser{
		ID:         userID,
		Email:      email,
		FirstName:  firstName,
		LastName:   lastName,
		Department: department,
	}
	go func() {
		resyncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.provisionEngine.ProvisionUser(resyncCtx, capturedAppID, capturedUser); err != nil {
			h.logger.Warn("resync user: provisioning failed",
				zap.String("app_id", capturedAppID), zap.String("user_id", userID), zap.Error(err))
		}
	}()

	respondJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

// DryRunProvisioningRequest is the body for POST /api/v1/apps/{appId}/provisioning/dry-run.
type DryRunProvisioningRequest struct {
	UserID string `json:"userId"`
}

// DryRunProvisioningResponse is returned by the dry-run endpoint.
type DryRunProvisioningResponse struct {
	UserID        string            `json:"userId"`
	ConnectorType string            `json:"connectorType"`
	EndpointURL   string            `json:"endpointUrl,omitempty"`
	Payload       map[string]any    `json:"payload"`
	AttributeMap  map[string]string `json:"attributeMap"`
}

// DryRunProvisioning previews what payload would be sent for a given user without calling the connector.
// Route: POST /api/v1/apps/{appId}/provisioning/dry-run
func (h *Handler) DryRunProvisioning(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if !isValidUUID(appID) {
		respondError(w, http.StatusBadRequest, "invalid appId")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireAppInCallerOrg(w, r, appID) {
		return
	}

	var req DryRunProvisioningRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !isValidUUID(req.UserID) {
		respondError(w, http.StatusBadRequest, "invalid userId")
		return
	}
	if !h.requireUserInCallerOrg(w, r, req.UserID) {
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	// Load config.
	var (
		connectorType     string
		endpointURL       string
		attributeMapBytes []byte
	)
	err := h.db.QueryRow(ctx,
		`SELECT connector_type, COALESCE(endpoint_url, ''), attribute_map
		 FROM app_provisioning_config WHERE app_id = $1`,
		appID,
	).Scan(&connectorType, &endpointURL, &attributeMapBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondError(w, http.StatusNotFound, "provisioning config not found for this app")
			return
		}
		h.logger.Error("dry-run provisioning: load config failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	attrMap := make(map[string]string)
	if len(attributeMapBytes) > 0 {
		_ = json.Unmarshal(attributeMapBytes, &attrMap)
	}

	// Load user.
	var email, firstName, lastName, department string
	err = h.db.QueryRow(ctx,
		`SELECT email, first_name, last_name, COALESCE(department, '') FROM users WHERE keycloak_user_id = $1`,
		req.UserID,
	).Scan(&email, &firstName, &lastName, &department)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("dry-run provisioning: load user failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	user := provisioning.ProvisionableUser{
		ID:         req.UserID,
		Email:      email,
		FirstName:  firstName,
		LastName:   lastName,
		Department: department,
	}
	payload := provisioning.ApplyAttributeMap(user, attrMap)

	_ = h.writeAuditEntryBestEffort(actorID, "provisioning.dry_run", "app", appID,
		map[string]interface{}{"user_id": req.UserID})

	respondJSON(w, http.StatusOK, DryRunProvisioningResponse{
		UserID:        req.UserID,
		ConnectorType: connectorType,
		EndpointURL:   endpointURL,
		Payload:       payload,
		AttributeMap:  attrMap,
	})
}

// ReconcileAllHandler triggers reconciliation for all error/pending provisioning records for an app.
// Route: POST /api/v1/apps/{appId}/provisioning/reconcile-all
func (h *Handler) ReconcileAllHandler(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if !isValidUUID(appID) {
		respondError(w, http.StatusBadRequest, "invalid appId")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireAppInCallerOrg(w, r, appID) {
		return
	}
	if h.provisionEngine == nil {
		respondError(w, http.StatusServiceUnavailable, "provisioning engine not configured")
		return
	}
	if !h.provisionEngine.HasConnector(appID) {
		respondError(w, http.StatusServiceUnavailable, "provisioning connector not configured")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	// Reset next_retry_at so ReconcileAll picks up error/pending rows immediately.
	_, err := h.db.Exec(ctx,
		`UPDATE provisioning_state SET next_retry_at = NULL WHERE app_id = $1 AND status IN ('error', 'pending')`,
		appID,
	)
	if err != nil {
		h.logger.Error("reconcile-all: reset retry failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "provisioning.reconcile_all", "app", appID,
		map[string]interface{}{})

	go func() {
		reconcileCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := h.provisionEngine.ReconcileAll(reconcileCtx); err != nil {
			h.logger.Warn("reconcile-all: engine error", zap.Error(err))
		}
	}()

	respondJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

// tokenSHA256 returns a hex-encoded SHA-256 digest of the given plaintext.
// Used to store a "configured" indicator without persisting the plaintext.
func tokenSHA256(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return fmt.Sprintf("%x", h)
}

// decryptProvisioningToken reverses encryptProvisioningToken.
// In development/test only, a missing PROVISIONING_MASTER_KEY treats the input
// as plain base64 so local tests do not require secret setup.
func decryptProvisioningToken(ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	key, err := provisioningMasterKey()
	if err != nil {
		return "", err
	}
	if key == nil {
		// Dev/test mode: the "ciphertext" is just base64(plaintext).
		return string(raw), nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertextBytes := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// encryptProvisioningToken encrypts a plaintext bearer token using AES-256-GCM
// with the key from PROVISIONING_MASTER_KEY (base64-encoded 32 bytes).
// In development/test only, a missing key returns a plain base64 encoding.
func encryptProvisioningToken(plaintext string) (string, error) {
	key, err := provisioningMasterKey()
	if err != nil {
		return "", err
	}
	if key == nil {
		// Dev/test mode: no encryption, just base64-encode.
		return base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func provisioningMasterKey() ([]byte, error) {
	keyB64 := os.Getenv("PROVISIONING_MASTER_KEY")
	if keyB64 == "" {
		env := os.Getenv("APP_ENV")
		if env == "development" || env == "test" {
			return nil, nil
		}
		return nil, fmt.Errorf("PROVISIONING_MASTER_KEY must be set outside development/test")
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("PROVISIONING_MASTER_KEY must be a base64-encoded 32-byte key")
	}
	return key, nil
}
