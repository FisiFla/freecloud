package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// SAMLAttributeMappingRequest is one entry in the attribute mappings list.
type SAMLAttributeMappingRequest struct {
	UserAttribute     string `json:"userAttribute"`
	SAMLAttributeName string `json:"samlAttributeName"`
}

// SAMLOptionsRequest carries optional advanced SAML configuration for app creation.
type SAMLOptionsRequest struct {
	SigningAlgorithm  string                        `json:"signingAlgorithm,omitempty"`
	EncryptAssertions bool                          `json:"encryptAssertions"`
	NameIDFormat      string                        `json:"nameIDFormat,omitempty"`
	AttributeMappings []SAMLAttributeMappingRequest `json:"attributeMappings,omitempty"`
}

// CreateAppRequest is the JSON request body for creating an app.
type CreateAppRequest struct {
	Name         string              `json:"name"`
	Protocol     string              `json:"protocol"`
	RedirectURIs []string            `json:"redirectURIs"`
	BaseURL      string              `json:"baseURL"`
	SAMLOptions  *SAMLOptionsRequest `json:"samlOptions,omitempty"`
}

// toKeycloakSAMLOptions converts the request DTO to the keycloak package type.
func toKeycloakSAMLOptions(req *SAMLOptionsRequest) *keycloak.SAMLOptions {
	if req == nil {
		return nil
	}
	opts := &keycloak.SAMLOptions{
		SigningAlgorithm:  req.SigningAlgorithm,
		EncryptAssertions: req.EncryptAssertions,
		NameIDFormat:      req.NameIDFormat,
	}
	for _, m := range req.AttributeMappings {
		opts.AttributeMappings = append(opts.AttributeMappings, keycloak.SAMLAttributeMapping{
			UserAttribute:     m.UserAttribute,
			SAMLAttributeName: m.SAMLAttributeName,
		})
	}
	return opts
}

// CreateAppResponse is the JSON response for creating an app.
type CreateAppResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	KeycloakClientID string `json:"keycloakClientId"`
	// SAMLEntityID, SAMLAcsURL, and SAMLIdPInitiatedURL are only set when protocol is SAML.
	SAMLEntityID        string `json:"samlEntityId,omitempty"`
	SAMLAcsURL          string `json:"samlAcsUrl,omitempty"`
	SAMLIdPInitiatedURL string `json:"samlIdpInitiatedUrl,omitempty"`
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

	req.Name = strings.TrimSpace(req.Name)
	req.Protocol = strings.ToUpper(strings.TrimSpace(req.Protocol))
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	for i := range req.RedirectURIs {
		req.RedirectURIs[i] = strings.TrimSpace(req.RedirectURIs[i])
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
	if req.BaseURL != "" {
		if err := validateRedirectURI(req.BaseURL); err != nil {
			respondError(w, http.StatusBadRequest, "invalid baseURL: "+err.Error())
			return
		}
	}
	if len(req.RedirectURIs) > 20 {
		respondValidationErrors(w, []ValidationError{{
			Field:   "redirectURIs",
			Message: "maximum 20 redirect URIs allowed",
		}})
		return
	}
	for _, uri := range req.RedirectURIs {
		if len(uri) > 2000 {
			respondValidationErrors(w, []ValidationError{{
				Field:   "redirectURIs",
				Message: "redirect URI must be ≤ 2000 characters",
			}})
			return
		}
		if err := validateRedirectURI(uri); err != nil {
			respondValidationErrors(w, []ValidationError{{
				Field:   "redirectURIs",
				Message: err.Error(),
			}})
			return
		}
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()

	// Create client in Keycloak
	keycloakClientID, err := h.keycloak.CreateClient(ctx, req.Name, req.Protocol, req.RedirectURIs, req.BaseURL, toKeycloakSAMLOptions(req.SAMLOptions))
	if err != nil {
		h.logger.Error("failed to create keycloak client", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create app in Keycloak")
		return
	}

	// Ensure we clean up the Keycloak client if DB operations fail.
	// Use a fresh context (not the request context) so cleanup still runs if
	// the client disconnects mid-request — otherwise the orphaned Keycloak
	// client would never be deleted.
	dbSucceeded := false
	defer func() {
		if !dbSucceeded {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			h.logger.Warn("cleaning up orphaned Keycloak client", zap.String("kc_client_id", keycloakClientID))
			if delErr := h.keycloak.DeleteClient(cleanupCtx, keycloakClientID); delErr != nil {
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
	if auditErr := h.writeAuditEntry(ctx, actorID, "app_create", "app", appID, map[string]interface{}{
		"name":     req.Name,
		"protocol": req.Protocol,
	}); auditErr != nil {
		h.logger.Warn("failed to write audit log", zap.Error(auditErr))
	}

	resp := CreateAppResponse{
		ID:               appID,
		Name:             req.Name,
		KeycloakClientID: keycloakClientID,
	}
	// Surface SAML SP metadata so admins can configure their SP.
	if req.Protocol == "SAML" {
		entityID := req.BaseURL
		if entityID == "" {
			entityID = req.Name
		}
		acsURL := ""
		if len(req.RedirectURIs) > 0 {
			acsURL = req.RedirectURIs[0]
		}
		resp.SAMLEntityID = entityID
		resp.SAMLAcsURL = acsURL
	}
	respondJSON(w, http.StatusOK, resp)
}

// AssignApp assigns a user to an app.
func (h *Handler) AssignApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		respondError(w, http.StatusBadRequest, "appId is required")
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
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
	if !isValidUUID(req.UserID) {
		respondError(w, http.StatusBadRequest, "userId must be a valid UUID")
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

	actorID := middleware.GetActorID(r.Context())
	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.compensateAppAssignment(req.UserID, keycloakClientID)
		h.logger.Error("failed to begin app assignment transaction", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to record app assignment")
		return
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`INSERT INTO app_assignments (app_id, user_id, assigned_by)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (app_id, user_id) DO NOTHING`,
		appID, req.UserID, actorID,
	)
	insertedAssignment := tag.RowsAffected() > 0
	if err != nil {
		h.compensateAppAssignment(req.UserID, keycloakClientID)
		h.logger.Error("failed to record app assignment", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to record app assignment")
		return
	}

	if err := writeAuditEntry(ctx, tx, actorID, "app_assign", "app", appID, map[string]interface{}{
		"user_id": req.UserID,
	}); err != nil {
		if insertedAssignment {
			h.compensateAppAssignment(req.UserID, keycloakClientID)
		}
		h.logger.Error("failed to write app assignment audit log", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to record app assignment")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		if insertedAssignment {
			h.compensateAppAssignment(req.UserID, keycloakClientID)
		}
		h.logger.Error("failed to commit app assignment", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to record app assignment")
		return
	}

	respondJSON(w, http.StatusOK, map[string]bool{"assigned": true})
}

func (h *Handler) compensateAppAssignment(userID, keycloakClientID string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.keycloak.UnassignUserFromClient(cleanupCtx, userID, keycloakClientID); err != nil {
		h.logger.Error("failed to compensate Keycloak app assignment",
			zap.String("user_id", userID),
			zap.String("kc_client_id", keycloakClientID),
			zap.Error(err),
		)
	}
}

// ListApps lists all connected apps from the local database.
func (h *Handler) ListApps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.db == nil {
		respondJSON(w, http.StatusOK, []ConnectedApp{})
		return
	}

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

	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating apps", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if apps == nil {
		apps = []ConnectedApp{}
	}

	respondJSON(w, http.StatusOK, apps)
}

// ListAuditLogs queries audit logs with optional filters.
// Supported query params: actor, action, from (RFC3339), to (RFC3339), limit, offset.
func (h *Handler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.db == nil {
		respondJSON(w, http.StatusOK, []AuditLogEntry{})
		return
	}

	actorFilter := r.URL.Query().Get("actor")
	actionFilter := r.URL.Query().Get("action")
	limitStr := r.URL.Query().Get("limit")

	limit := 100
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 1000 {
			limit = v
		}
	}

	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if v, err := strconv.Atoi(offsetStr); err == nil && v >= 0 {
			offset = v
		}
	}

	var fromTime, toTime time.Time
	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			fromTime = t
		} else {
			respondError(w, http.StatusBadRequest, "invalid 'from' param: use RFC3339 format")
			return
		}
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			toTime = t
		} else {
			respondError(w, http.StatusBadRequest, "invalid 'to' param: use RFC3339 format")
			return
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
	if !fromTime.IsZero() {
		query += ` AND created_at >= $` + strconv.Itoa(argIdx)
		args = append(args, fromTime)
		argIdx++
	}
	if !toTime.IsZero() {
		query += ` AND created_at < $` + strconv.Itoa(argIdx)
		args = append(args, toTime)
		argIdx++
	}

	query += ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(argIdx)
	args = append(args, limit)
	argIdx++
	query += ` OFFSET $` + strconv.Itoa(argIdx)
	args = append(args, offset)

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
			if err := json.Unmarshal(detailsJSON, &entry.Details); err != nil {
				h.logger.Warn("failed to unmarshal audit log details, using empty map",
					zap.String("entry_id", entry.ID),
					zap.Error(err),
				)
			}
		}
		if entry.Details == nil {
			entry.Details = make(map[string]interface{})
		}
		entry.CreatedAt = createdAt.Format(time.RFC3339)
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating audit logs", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if entries == nil {
		entries = []AuditLogEntry{}
	}

	respondJSON(w, http.StatusOK, entries)
}

// GetSAMLIdPInitiatedURL returns the Keycloak IdP-initiated SSO URL for a SAML app.
// GET /api/v1/apps/{appId}/saml/idp-url — requires PermReadApps.
func (h *Handler) GetSAMLIdPInitiatedURL(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		respondError(w, http.StatusBadRequest, "appId is required")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	ctx := r.Context()

	var keycloakClientID, protocol string
	err := h.db.QueryRow(ctx,
		`SELECT keycloak_client_id, protocol FROM connected_apps WHERE id = $1`,
		appID,
	).Scan(&keycloakClientID, &protocol)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "app not found")
			return
		}
		h.logger.Error("failed to query app for saml idp url", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if strings.ToUpper(protocol) != "SAML" {
		respondError(w, http.StatusBadRequest, "app is not a SAML application")
		return
	}

	ssoURL, err := h.keycloak.GetSAMLIdPInitiatedURL(ctx, keycloakClientID)
	if err != nil {
		h.logger.Error("failed to get saml idp-initiated url", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to retrieve IdP-initiated SSO URL")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"url": ssoURL})
}

// GetSAMLMetadata serves the Keycloak SAML IdP metadata XML for a configured SAML app.
// The SP admin imports this XML to configure trust with FreeCloud as the IdP.
// GET /api/v1/apps/{appId}/saml/metadata — requires PermReadApps.
func (h *Handler) GetSAMLMetadata(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		respondError(w, http.StatusBadRequest, "appId is required")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	ctx := r.Context()

	var protocol string
	err := h.db.QueryRow(ctx,
		`SELECT protocol FROM connected_apps WHERE id = $1`,
		appID,
	).Scan(&protocol)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "app not found")
			return
		}
		h.logger.Error("failed to query app for saml metadata", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if strings.ToUpper(protocol) != "SAML" {
		respondError(w, http.StatusBadRequest, "app is not a SAML application")
		return
	}

	xml, err := h.keycloak.GetSAMLMetadataXML(ctx)
	if err != nil {
		h.logger.Error("failed to fetch saml metadata", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to fetch SAML metadata")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="saml-idp-metadata.xml"`)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, xml)
}

// validateRedirectURI checks that a redirect URI is valid and secure.
func validateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("redirect URI must be absolute")
	}

	if u.Scheme == "https" {
		return nil
	}

	if u.Scheme != "http" {
		return fmt.Errorf("redirect URI must use https or localhost http")
	}

	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" {
		return fmt.Errorf("http redirect URI must use localhost or 127.0.0.1")
	}

	if port := u.Port(); port != "" {
		if _, err := strconv.Atoi(port); err != nil {
			return fmt.Errorf("localhost redirect URI port must be numeric")
		}
	}

	return nil
}
