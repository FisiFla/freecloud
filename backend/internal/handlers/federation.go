package handlers

// C1 — LDAP/AD federation source CRUD + test + sync

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// FederationSource represents a persisted LDAP/AD federation configuration.
type FederationSource struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	ProviderType        string                 `json:"providerType"`
	Vendor              string                 `json:"vendor"`
	Config              map[string]interface{} `json:"config"`
	KeycloakComponentID string                 `json:"keycloakComponentId,omitempty"`
	LastSyncAt          string                 `json:"lastSyncAt,omitempty"`
	LastSyncStatus      string                 `json:"lastSyncStatus,omitempty"`
	CreatedAt           string                 `json:"createdAt,omitempty"`
	UpdatedAt           string                 `json:"updatedAt,omitempty"`
}

// CreateFederationSourceRequest is the JSON body for POST /api/v1/federation/sources.
type CreateFederationSourceRequest struct {
	Name          string `json:"name"`
	Vendor        string `json:"vendor"` // "other" or "ad"
	ConnectionURL string `json:"connectionUrl"`
	BindDN        string `json:"bindDn"`
	UsersDN       string `json:"usersDn"`
}

// UpdateFederationSourceRequest is the JSON body for PATCH /api/v1/federation/sources/{id}.
type UpdateFederationSourceRequest struct {
	Name          *string `json:"name,omitempty"`
	Vendor        *string `json:"vendor,omitempty"`
	ConnectionURL *string `json:"connectionUrl,omitempty"`
	BindDN        *string `json:"bindDn,omitempty"`
	UsersDN       *string `json:"usersDn,omitempty"`
}

// CreateFederationSource — POST /api/v1/federation/sources
func (h *Handler) CreateFederationSource(w http.ResponseWriter, r *http.Request) {
	actorID := middleware.GetActorID(r.Context())
	var req CreateFederationSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.ConnectionURL == "" || req.BindDN == "" || req.UsersDN == "" {
		respondError(w, http.StatusBadRequest, "name, connectionUrl, bindDn, and usersDn are required")
		return
	}
	if req.Vendor == "" {
		req.Vendor = "other"
	}

	bindPassword := h.ldapBindPassword
	if bindPassword == "" {
		respondError(w, http.StatusBadRequest, "LDAP_BIND_PASSWORD is not configured; set the environment variable before creating an LDAP source")
		return
	}

	ctx := r.Context()

	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	componentID, err := h.keycloak.CreateFederationComponent(ctx, req.Name, req.ConnectionURL, req.BindDN, bindPassword, req.UsersDN, req.Vendor)
	if err != nil {
		h.logger.Error("failed to create keycloak ldap component", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create federation source in identity provider")
		return
	}

	configJSON, _ := json.Marshal(map[string]string{
		"connectionUrl": req.ConnectionURL,
		"bindDn":        req.BindDN,
		"usersDn":       req.UsersDN,
	})

	var fs FederationSource
	if h.db != nil {
		var configStr string
		var createdAt, updatedAt time.Time
		err = h.db.QueryRow(ctx,
			`INSERT INTO federation_sources (name, provider_type, vendor, config, keycloak_component_id, org_id)
			 VALUES ($1, 'ldap', $2, $3, $4, $5)
			 RETURNING id, name, provider_type, vendor, config::text, keycloak_component_id, created_at, updated_at`,
			req.Name, req.Vendor, string(configJSON), componentID, oc.OrgID,
		).Scan(&fs.ID, &fs.Name, &fs.ProviderType, &fs.Vendor, &configStr, &fs.KeycloakComponentID, &createdAt, &updatedAt)
		if err != nil {
			h.logger.Error("failed to persist federation source", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "federation source created in Keycloak but failed to persist locally")
			return
		}
		_ = json.Unmarshal([]byte(configStr), &fs.Config)
		fs.CreatedAt = createdAt.Format(time.RFC3339)
		fs.UpdatedAt = updatedAt.Format(time.RFC3339)
	} else {
		fs = FederationSource{
			Name:                req.Name,
			ProviderType:        "ldap",
			Vendor:              req.Vendor,
			KeycloakComponentID: componentID,
			Config:              map[string]interface{}{"connectionUrl": req.ConnectionURL, "bindDn": req.BindDN, "usersDn": req.UsersDN},
		}
	}

	_ = h.writeAuditEntryBestEffort(actorID, "federation_source.created", "federation_source", fs.ID, map[string]interface{}{
		"name": req.Name, "vendor": req.Vendor,
	})
	respondJSON(w, http.StatusCreated, fs)
}

// ListFederationSources — GET /api/v1/federation/sources
func (h *Handler) ListFederationSources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.db == nil {
		respondJSON(w, http.StatusOK, []FederationSource{})
		return
	}
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}
	rows, err := h.db.Query(ctx,
		`SELECT id, name, provider_type, vendor, config::text,
		        COALESCE(keycloak_component_id, ''),
		        COALESCE(last_sync_at::TEXT, ''),
		        COALESCE(last_sync_status, ''),
		        created_at, updated_at
		 FROM federation_sources WHERE org_id = $1 ORDER BY created_at DESC`,
		oc.OrgID)
	if err != nil {
		h.logger.Error("failed to list federation sources", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var sources []FederationSource
	for rows.Next() {
		var fs FederationSource
		var configStr string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&fs.ID, &fs.Name, &fs.ProviderType, &fs.Vendor, &configStr,
			&fs.KeycloakComponentID, &fs.LastSyncAt, &fs.LastSyncStatus, &createdAt, &updatedAt); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(configStr), &fs.Config)
		fs.CreatedAt = createdAt.Format(time.RFC3339)
		fs.UpdatedAt = updatedAt.Format(time.RFC3339)
		sources = append(sources, fs)
	}
	if sources == nil {
		sources = []FederationSource{}
	}
	respondJSON(w, http.StatusOK, sources)
}

// GetFederationSource — GET /api/v1/federation/sources/{id}
func (h *Handler) GetFederationSource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	if h.db == nil {
		respondError(w, http.StatusNotFound, "federation source not found")
		return
	}
	if !h.requireFederationSourceInCallerOrg(w, r, id) {
		return
	}
	oc := middleware.GetOrgContext(ctx)
	var fs FederationSource
	var configStr string
	var createdAt, updatedAt time.Time
	err := h.db.QueryRow(ctx,
		`SELECT id, name, provider_type, vendor, config::text,
		        COALESCE(keycloak_component_id, ''),
		        COALESCE(last_sync_at::TEXT, ''),
		        COALESCE(last_sync_status, ''),
		        created_at, updated_at
		 FROM federation_sources WHERE id = $1 AND org_id = $2`, id, oc.OrgID,
	).Scan(&fs.ID, &fs.Name, &fs.ProviderType, &fs.Vendor, &configStr,
		&fs.KeycloakComponentID, &fs.LastSyncAt, &fs.LastSyncStatus, &createdAt, &updatedAt)
	if err != nil {
		respondError(w, http.StatusNotFound, "federation source not found")
		return
	}
	_ = json.Unmarshal([]byte(configStr), &fs.Config)
	fs.CreatedAt = createdAt.Format(time.RFC3339)
	fs.UpdatedAt = updatedAt.Format(time.RFC3339)
	respondJSON(w, http.StatusOK, fs)
}

// UpdateFederationSource — PATCH /api/v1/federation/sources/{id}
func (h *Handler) UpdateFederationSource(w http.ResponseWriter, r *http.Request) {
	actorID := middleware.GetActorID(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	var req UpdateFederationSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireFederationSourceInCallerOrg(w, r, id) {
		return
	}
	oc := middleware.GetOrgContext(ctx)

	var cur FederationSource
	var configStr string
	var createdAt, updatedAt time.Time
	err := h.db.QueryRow(ctx,
		`SELECT id, name, provider_type, vendor, config::text,
		        COALESCE(keycloak_component_id, ''),
		        COALESCE(last_sync_at::TEXT, ''),
		        COALESCE(last_sync_status, ''),
		        created_at, updated_at
		 FROM federation_sources WHERE id = $1 AND org_id = $2`, id, oc.OrgID,
	).Scan(&cur.ID, &cur.Name, &cur.ProviderType, &cur.Vendor, &configStr,
		&cur.KeycloakComponentID, &cur.LastSyncAt, &cur.LastSyncStatus, &createdAt, &updatedAt)
	if err != nil {
		respondError(w, http.StatusNotFound, "federation source not found")
		return
	}
	_ = json.Unmarshal([]byte(configStr), &cur.Config)

	newName := cur.Name
	newVendor := cur.Vendor
	newConnURL, _ := cur.Config["connectionUrl"].(string)
	newBindDN, _ := cur.Config["bindDn"].(string)
	newUsersDN, _ := cur.Config["usersDn"].(string)
	if req.Name != nil {
		newName = *req.Name
	}
	if req.Vendor != nil {
		newVendor = *req.Vendor
	}
	if req.ConnectionURL != nil {
		newConnURL = *req.ConnectionURL
	}
	if req.BindDN != nil {
		newBindDN = *req.BindDN
	}
	if req.UsersDN != nil {
		newUsersDN = *req.UsersDN
	}

	bindPassword := h.ldapBindPassword
	if cur.KeycloakComponentID != "" && bindPassword != "" {
		if err := h.keycloak.UpdateFederationComponent(ctx, cur.KeycloakComponentID, newName, newConnURL, newBindDN, bindPassword, newUsersDN, newVendor); err != nil {
			h.logger.Warn("failed to update keycloak federation component", zap.Error(err))
		}
	}

	newConfigJSON, _ := json.Marshal(map[string]string{"connectionUrl": newConnURL, "bindDn": newBindDN, "usersDn": newUsersDN})
	if _, err := h.db.Exec(ctx,
		`UPDATE federation_sources SET name=$1, vendor=$2, config=$3, updated_at=NOW() WHERE id=$4 AND org_id=$5`,
		newName, newVendor, string(newConfigJSON), id, oc.OrgID); err != nil {
		h.logger.Error("failed to update federation source", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to update federation source")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "federation_source.updated", "federation_source", id, map[string]interface{}{"name": newName})
	respondJSON(w, http.StatusOK, FederationSource{
		ID: cur.ID, Name: newName, ProviderType: cur.ProviderType, Vendor: newVendor,
		KeycloakComponentID: cur.KeycloakComponentID,
		Config:              map[string]interface{}{"connectionUrl": newConnURL, "bindDn": newBindDN, "usersDn": newUsersDN},
		LastSyncAt:          cur.LastSyncAt, LastSyncStatus: cur.LastSyncStatus,
		CreatedAt: createdAt.Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
	})
}

// DeleteFederationSource — DELETE /api/v1/federation/sources/{id}
func (h *Handler) DeleteFederationSource(w http.ResponseWriter, r *http.Request) {
	actorID := middleware.GetActorID(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireFederationSourceInCallerOrg(w, r, id) {
		return
	}
	oc := middleware.GetOrgContext(ctx)

	var componentID string
	err := h.db.QueryRow(ctx,
		`SELECT COALESCE(keycloak_component_id, '') FROM federation_sources WHERE id = $1 AND org_id = $2`, id, oc.OrgID,
	).Scan(&componentID)
	if err != nil {
		respondError(w, http.StatusNotFound, "federation source not found")
		return
	}

	if componentID != "" {
		if err := h.keycloak.DeleteFederationComponent(ctx, componentID); err != nil {
			h.logger.Warn("failed to delete keycloak federation component",
				zap.String("component_id", componentID), zap.Error(err))
		}
	}

	if _, err := h.db.Exec(ctx, `DELETE FROM federation_sources WHERE id = $1 AND org_id = $2`, id, oc.OrgID); err != nil {
		h.logger.Error("failed to delete federation source", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to delete federation source")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "federation_source.deleted", "federation_source", id, map[string]interface{}{"keycloak_component_id": componentID})
	respondJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// TestFederationConnection — POST /api/v1/federation/sources/{id}/test
func (h *Handler) TestFederationConnection(w http.ResponseWriter, r *http.Request) {
	actorID := middleware.GetActorID(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireFederationSourceInCallerOrg(w, r, id) {
		return
	}
	oc := middleware.GetOrgContext(ctx)

	var fs FederationSource
	var configStr string
	err := h.db.QueryRow(ctx,
		`SELECT id, COALESCE(keycloak_component_id, ''), config::text FROM federation_sources WHERE id = $1 AND org_id = $2`, id, oc.OrgID,
	).Scan(&fs.ID, &fs.KeycloakComponentID, &configStr)
	if err != nil {
		respondError(w, http.StatusNotFound, "federation source not found")
		return
	}
	_ = json.Unmarshal([]byte(configStr), &fs.Config)
	connURL, _ := fs.Config["connectionUrl"].(string)
	bindDN, _ := fs.Config["bindDn"].(string)
	bindPassword := h.ldapBindPassword

	if testErr := h.keycloak.TestLDAPConnection(ctx, fs.KeycloakComponentID, connURL, bindDN, bindPassword); testErr != nil {
		h.logger.Warn("ldap test connection failed", zap.String("id", id), zap.Error(testErr))
		_ = h.writeAuditEntryBestEffort(actorID, "federation_source.test_connection", "federation_source", id,
			map[string]interface{}{"success": false, "error": testErr.Error()})
		respondJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": testErr.Error()})
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "federation_source.test_connection", "federation_source", id,
		map[string]interface{}{"success": true})
	respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// TriggerFederationSync — POST /api/v1/federation/sources/{id}/sync
// Query param: action=triggerFullSync|triggerChangedUsersSync (defaults to triggerFullSync)
func (h *Handler) TriggerFederationSync(w http.ResponseWriter, r *http.Request) {
	actorID := middleware.GetActorID(r.Context())
	id := chi.URLParam(r, "id")
	action := r.URL.Query().Get("action")
	if action == "" {
		action = "triggerFullSync"
	}
	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireFederationSourceInCallerOrg(w, r, id) {
		return
	}
	oc := middleware.GetOrgContext(ctx)

	var componentID string
	err := h.db.QueryRow(ctx, `SELECT COALESCE(keycloak_component_id, '') FROM federation_sources WHERE id = $1 AND org_id = $2`, id, oc.OrgID).Scan(&componentID)
	if err != nil {
		respondError(w, http.StatusNotFound, "federation source not found")
		return
	}
	if componentID == "" {
		respondError(w, http.StatusBadRequest, "federation source has no associated Keycloak component")
		return
	}

	syncStatus := "success"
	if syncErr := h.keycloak.TriggerFederationSync(ctx, componentID, action); syncErr != nil {
		h.logger.Warn("federation sync failed", zap.String("id", id), zap.Error(syncErr))
		syncStatus = "failed: " + syncErr.Error()
		_, _ = h.db.Exec(ctx,
			`UPDATE federation_sources SET last_sync_at=NOW(), last_sync_status=$1 WHERE id=$2 AND org_id=$3`, syncStatus, id, oc.OrgID)
		_ = h.writeAuditEntryBestEffort(actorID, "federation_source.sync", "federation_source", id,
			map[string]interface{}{"action": action, "status": "failed"})
		respondJSON(w, http.StatusOK, map[string]interface{}{"synced": false, "status": syncStatus})
		return
	}

	_, _ = h.db.Exec(ctx,
		`UPDATE federation_sources SET last_sync_at=NOW(), last_sync_status=$1 WHERE id=$2 AND org_id=$3`, syncStatus, id, oc.OrgID)
	_ = h.writeAuditEntryBestEffort(actorID, "federation_source.sync", "federation_source", id,
		map[string]interface{}{"action": action, "status": "success"})
	respondJSON(w, http.StatusOK, map[string]bool{"synced": true})
}
