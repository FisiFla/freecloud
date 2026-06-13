package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// CreateAppRequest is the JSON request body for creating an app.
type CreateAppRequest struct {
	Name         string   `json:"name"`
	Protocol     string   `json:"protocol"`
	RedirectURIs []string `json:"redirectURIs"`
	BaseURL      string   `json:"baseURL"`
}

// CreateAppResponse is the JSON response for creating an app.
type CreateAppResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	KeycloakClientID string `json:"keycloakClientId"`
}

// AssignAppRequest is the JSON request body for assigning a user to an app.
type AssignAppRequest struct {
	UserID string `json:"userId"`
}

// ConnectedApp represents a row in the connected_apps table.
type ConnectedApp struct {
	ID               string `json:"id"`
	KeycloakClientID string `json:"keycloakClientId,omitempty"`
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	BaseURL          string `json:"baseUrl,omitempty"`
	Enabled          bool   `json:"enabled"`
	CreatedAt        string `json:"createdAt,omitempty"`
}

// AuditLogEntry represents a row in the audit_logs table.
type AuditLogEntry struct {
	ID         string                 `json:"id"`
	ActorID    string                 `json:"actorId"`
	Action     string                 `json:"action"`
	TargetType string                 `json:"targetType,omitempty"`
	TargetID   string                 `json:"targetId,omitempty"`
	Details    map[string]interface{} `json:"details"`
	CreatedAt  string                 `json:"createdAt,omitempty"`
}

// CreateApp creates a new connected app (Keycloak client).
func (h *Handler) CreateApp(w http.ResponseWriter, r *http.Request) {
	var req CreateAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" || req.Protocol == "" {
		respondError(w, http.StatusBadRequest, "name and protocol are required")
		return
	}

	if req.Protocol != "OIDC" && req.Protocol != "SAML" {
		respondError(w, http.StatusBadRequest, "protocol must be OIDC or SAML")
		return
	}

	if len(req.Name) > 255 {
		respondError(w, http.StatusBadRequest, "name must be ≤ 255 characters")
		return
	}
	if req.Protocol == "OIDC" && len(req.RedirectURIs) == 0 {
		respondError(w, http.StatusBadRequest, "at least one redirect URI is required for OIDC apps")
		return
	}
	for _, uri := range req.RedirectURIs {
		if !strings.HasPrefix(uri, "https://") && !strings.HasPrefix(uri, "http://localhost") {
			respondError(w, http.StatusBadRequest, "redirect URI must use https:// or http://localhost")
			return
		}
	}

	ctx := r.Context()

	// Create client in Keycloak
	keycloakClientID, err := h.keycloak.CreateClient(ctx, req.Name, req.Protocol, req.RedirectURIs, req.BaseURL)
	if err != nil {
		h.logger.Error("failed to create keycloak client", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create app in Keycloak")
		return
	}

	// Ensure we clean up the Keycloak client if DB operations fail
	dbSucceeded := false
	defer func() {
		if !dbSucceeded {
			h.logger.Warn("cleaning up orphaned Keycloak client", zap.String("kc_client_id", keycloakClientID))
			if delErr := h.keycloak.DeleteClient(ctx, keycloakClientID); delErr != nil {
				h.logger.Error("failed to clean up orphaned Keycloak client",
					zap.String("kc_client_id", keycloakClientID),
					zap.Error(delErr),
				)
			}
		}
	}()

	// Store in local database
	var appID string
	err = h.db.QueryRow(ctx,
		`INSERT INTO connected_apps (keycloak_client_id, name, protocol, base_url)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		keycloakClientID, req.Name, req.Protocol, req.BaseURL,
	).Scan(&appID)
	if err != nil {
		h.logger.Error("failed to store connected app", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to store app")
		return
	}
	dbSucceeded = true

	// Write audit log
	actorID := middleware.GetActorID(r.Context())
	_, auditErr := h.db.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		actorID, "app_create", "app", appID,
		map[string]interface{}{"name": req.Name, "protocol": req.Protocol},
	)
	if auditErr != nil {
		h.logger.Warn("failed to write audit log", zap.Error(auditErr))
	}

	respondJSON(w, http.StatusOK, CreateAppResponse{
		ID:               appID,
		Name:             req.Name,
		KeycloakClientID: keycloakClientID,
	})
}

// AssignApp assigns a user to an app.
func (h *Handler) AssignApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		respondError(w, http.StatusBadRequest, "appId is required")
		return
	}

	var req AssignAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.UserID == "" {
		respondError(w, http.StatusBadRequest, "userId is required")
		return
	}

	ctx := r.Context()

	// Get the Keycloak client ID from the connected_apps table
	var keycloakClientID string
	err := h.db.QueryRow(ctx,
		`SELECT keycloak_client_id FROM connected_apps WHERE id = $1`,
		appID,
	).Scan(&keycloakClientID)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "app not found")
			return
		}
		h.logger.Error("failed to query app", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Assign in Keycloak
	err = h.keycloak.AssignUserToClient(ctx, req.UserID, keycloakClientID)
	if err != nil {
		h.logger.Error("failed to assign user to keycloak client", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to assign user to app")
		return
	}

	// Record in app_assignments
	actorID := middleware.GetActorID(r.Context())
	_, err = h.db.Exec(ctx,
		`INSERT INTO app_assignments (app_id, user_id, assigned_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (app_id, user_id) DO NOTHING`,
		appID, req.UserID, actorID,
	)
	if err != nil {
		h.logger.Warn("failed to record app assignment", zap.Error(err))
	}

	// Write audit log
	_, auditErr := h.db.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		actorID, "app_assign", "app", appID,
		map[string]interface{}{"user_id": req.UserID},
	)
	if auditErr != nil {
		h.logger.Warn("failed to write audit log", zap.Error(auditErr))
	}

	respondJSON(w, http.StatusOK, map[string]bool{"assigned": true})
}

// ListApps lists all connected apps from the local database.
func (h *Handler) ListApps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Query(ctx,
		`SELECT id, keycloak_client_id, name, protocol, COALESCE(base_url, ''), enabled, created_at
		 FROM connected_apps
		 ORDER BY created_at DESC`,
	)
	if err != nil {
		h.logger.Error("failed to query connected apps", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var apps []ConnectedApp
	for rows.Next() {
		var app ConnectedApp
		var createdAt time.Time
		if err := rows.Scan(&app.ID, &app.KeycloakClientID, &app.Name, &app.Protocol, &app.BaseURL, &app.Enabled, &createdAt); err != nil {
			h.logger.Error("failed to scan app", zap.Error(err))
			continue
		}
		app.CreatedAt = createdAt.Format(time.RFC3339)
		apps = append(apps, app)
	}

	if apps == nil {
		apps = []ConnectedApp{}
	}

	respondJSON(w, http.StatusOK, apps)
}

// ListAuditLogs queries audit logs with optional filters.
func (h *Handler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorFilter := r.URL.Query().Get("actor")
	actionFilter := r.URL.Query().Get("action")
	limitStr := r.URL.Query().Get("limit")

	limit := 100
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 1000 {
			limit = v
		}
	}

	query := `SELECT id, actor_id, action, COALESCE(target_type, ''), COALESCE(target_id, ''), details, created_at
		 FROM audit_logs WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if actorFilter != "" {
		query += ` AND actor_id = $` + strconv.Itoa(argIdx)
		args = append(args, actorFilter)
		argIdx++
	}
	if actionFilter != "" {
		query += ` AND action = $` + strconv.Itoa(argIdx)
		args = append(args, actionFilter)
		argIdx++
	}

	query += ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(argIdx)
	args = append(args, limit)

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.logger.Error("failed to query audit logs", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var entries []AuditLogEntry
	for rows.Next() {
		var entry AuditLogEntry
		var detailsJSON []byte
		var createdAt time.Time
		if err := rows.Scan(&entry.ID, &entry.ActorID, &entry.Action, &entry.TargetType, &entry.TargetID, &detailsJSON, &createdAt); err != nil {
			h.logger.Error("failed to scan audit log", zap.Error(err))
			continue
		}
		if len(detailsJSON) > 0 {
			json.Unmarshal(detailsJSON, &entry.Details)
		}
		if entry.Details == nil {
			entry.Details = make(map[string]interface{})
		}
		entry.CreatedAt = createdAt.Format(time.RFC3339)
		entries = append(entries, entry)
	}

	if entries == nil {
		entries = []AuditLogEntry{}
	}

	respondJSON(w, http.StatusOK, entries)
}
