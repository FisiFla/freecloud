package handlers

// C1 — LDAP/AD federation source CRUD + test + sync

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/httpx"
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

	// The global env-only LDAP_BIND_PASSWORD is handed to Keycloak together
	// with this URL, so the destination is validated and bound to the
	// operator's allow-list before anything is created: otherwise an admin
	// could exfiltrate the bind password to any host.
	connHost, err := validateFederationConnectionURL(req.ConnectionURL)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	vendor, err := validateFederationVendor(req.Vendor)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Vendor = vendor
	if err := checkLDAPAllowedHost(connHost); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
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

	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "federation_source.created", "federation_source", fs.ID, map[string]interface{}{
		"name": req.Name, "vendor": req.Vendor, "host": connHost,
	}); err != nil {
		h.logger.Warn("failed to write federation source creation audit log", zap.Error(err))
	}
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
		h.respondFederationDBError(w, err, "federation source not found")
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
		h.respondFederationDBError(w, err, "federation source not found")
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

	// Newly supplied destinations are validated; an unchanged stored URL
	// passes through so legacy rows still accept unrelated updates (rename,
	// DN changes). Repointing to a NEW host additionally requires the
	// operator's LDAP_ALLOWED_HOSTS entry — the stored bind password would
	// otherwise follow the URL to an unapproved destination.
	newConnHost := ""
	if req.ConnectionURL != nil {
		host, err := validateFederationConnectionURL(newConnURL)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		newConnHost = host
		if ldapURLHostChanged(cur.Config["connectionUrl"], newConnURL) {
			if err := checkLDAPAllowedHost(newConnHost); err != nil {
				respondError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}
	if req.Vendor != nil {
		vendor, err := validateFederationVendor(newVendor)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		newVendor = vendor
	}

	bindPassword := h.ldapBindPassword
	if cur.KeycloakComponentID != "" && bindPassword != "" {
		// Fail closed: if Keycloak still holds the old bind DN / URL while
		// the DB row shows the new ones, the two sides disagree about where
		// the directory lives. The request must not report success.
		if err := h.keycloak.UpdateFederationComponent(ctx, cur.KeycloakComponentID, newName, newConnURL, newBindDN, bindPassword, newUsersDN, newVendor); err != nil {
			h.logger.Error("failed to update keycloak federation component",
				zap.String("component_id", cur.KeycloakComponentID), zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to update federation source in identity provider")
			return
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

	updateAudit := map[string]interface{}{"name": newName}
	if req.ConnectionURL != nil {
		// Repoint audit: old and new host, never the bind password.
		oldHost, _ := ldapURLHostString(cur.Config["connectionUrl"])
		updateAudit["old_host"] = oldHost
		updateAudit["new_host"] = newConnHost
	}
	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "federation_source.updated", "federation_source", id, updateAudit); err != nil {
		h.logger.Warn("failed to write federation source update audit log", zap.Error(err))
	}
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
		h.respondFederationDBError(w, err, "federation source not found")
		return
	}

	if componentID != "" {
		// Fail closed: deleting the DB row while the Keycloak component
		// still exists orphans the component forever — nothing references
		// component_id any more, so it can never be cleaned up.
		if err := h.keycloak.DeleteFederationComponent(ctx, componentID); err != nil {
			h.logger.Error("failed to delete keycloak federation component",
				zap.String("component_id", componentID), zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to delete federation source in identity provider")
			return
		}
	}

	if _, err := h.db.Exec(ctx, `DELETE FROM federation_sources WHERE id = $1 AND org_id = $2`, id, oc.OrgID); err != nil {
		h.logger.Error("failed to delete federation source", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to delete federation source")
		return
	}

	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "federation_source.deleted", "federation_source", id, map[string]interface{}{"keycloak_component_id": componentID}); err != nil {
		h.logger.Warn("failed to write federation source deletion audit log", zap.Error(err))
	}
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
		h.respondFederationDBError(w, err, "federation source not found")
		return
	}
	_ = json.Unmarshal([]byte(configStr), &fs.Config)
	connURL, _ := fs.Config["connectionUrl"].(string)
	bindDN, _ := fs.Config["bindDn"].(string)
	bindPassword := h.ldapBindPassword

	// The test sends the global bind password to the stored destination, so
	// the stored URL is validated first: a non-LDAP or blocked URL must
	// never receive the secret.
	if _, err := validateFederationConnectionURL(connURL); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if testErr := h.keycloak.TestLDAPConnection(ctx, fs.KeycloakComponentID, connURL, bindDN, bindPassword); testErr != nil {
		// The LDAP error can contain the bind DN and the connection URL —
		// internal topology that must not be echoed to the caller. The
		// detail goes to the log and to the audit trail.
		h.logger.Warn("ldap test connection failed", zap.String("id", id), zap.Error(testErr))
		if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "federation_source.test_connection", "federation_source", id,
			map[string]interface{}{"success": false, "error": testErr.Error()}); err != nil {
			h.logger.Warn("failed to write federation test audit log", zap.Error(err))
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": "connection test failed"})
		return
	}

	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "federation_source.test_connection", "federation_source", id,
		map[string]interface{}{"success": true}); err != nil {
		h.logger.Warn("failed to write federation test audit log", zap.Error(err))
	}
	respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// federationSyncActions is the allowlist of sync actions the Keycloak admin
// API accepts. The action is interpolated into the admin URL, so an
// unvalidated value would let a caller inject arbitrary query parameters
// (action=x&foo=bar).
var federationSyncActions = map[string]bool{
	"triggerFullSync":         true,
	"triggerChangedUsersSync": true,
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
	// Whitelist the action: it flows verbatim into the Keycloak admin URL.
	if !federationSyncActions[action] {
		respondError(w, http.StatusBadRequest, "action must be triggerFullSync or triggerChangedUsersSync")
		return
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
		h.respondFederationDBError(w, err, "federation source not found")
		return
	}
	if componentID == "" {
		respondError(w, http.StatusBadRequest, "federation source has no associated Keycloak component")
		return
	}

	syncStatus := "success"
	if syncErr := h.keycloak.TriggerFederationSync(ctx, componentID, action); syncErr != nil {
		// The Keycloak error can carry the bind DN / directory URL, so only
		// a generic status is stored and returned; the detail goes to the log.
		h.logger.Warn("federation sync failed", zap.String("id", id), zap.Error(syncErr))
		syncStatus = "failed"
		if _, dbErr := h.db.Exec(ctx,
			`UPDATE federation_sources SET last_sync_at=NOW(), last_sync_status=$1 WHERE id=$2 AND org_id=$3`, syncStatus, id, oc.OrgID); dbErr != nil {
			h.logger.Error("federation sync: failed to record failure status", zap.Error(dbErr))
		}
		if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "federation_source.sync", "federation_source", id,
			map[string]interface{}{"action": action, "status": "failed"}); err != nil {
			h.logger.Warn("failed to write federation sync audit log", zap.Error(err))
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"synced": false, "status": syncStatus})
		return
	}

	if _, dbErr := h.db.Exec(ctx,
		`UPDATE federation_sources SET last_sync_at=NOW(), last_sync_status=$1 WHERE id=$2 AND org_id=$3`, syncStatus, id, oc.OrgID); dbErr != nil {
		h.logger.Error("federation sync: failed to record success status", zap.Error(dbErr))
	}
	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "federation_source.sync", "federation_source", id,
		map[string]interface{}{"action": action, "status": "success"}); err != nil {
		h.logger.Warn("failed to write federation sync audit log", zap.Error(err))
	}
	respondJSON(w, http.StatusOK, map[string]bool{"synced": true})
}

// respondFederationDBError maps a database error to a response: 404 only when
// the row genuinely does not exist (pgx.ErrNoRows), 500 for every other
// error. A real outage must not be reported as "not found" — that sends
// operators and callers down the wrong debugging path and hides incidents.
func (h *Handler) respondFederationDBError(w http.ResponseWriter, err error, notFoundMsg string) {
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, http.StatusNotFound, notFoundMsg)
		return
	}
	h.logger.Error("federation: database query failed", zap.Error(err))
	respondError(w, http.StatusInternalServerError, "internal error")
}

// federationVendors is the allow-list of Keycloak LDAP provider vendor
// values. Anything else is rejected before it reaches Keycloak.
var federationVendors = map[string]bool{
	"ad":         true,
	"rhds":       true,
	"tivoli":     true,
	"edirectory": true,
	"other":      true,
}

// validateFederationVendor accepts the Keycloak LDAP vendor values,
// case-insensitively, and returns the normalized lowercase form.
func validateFederationVendor(vendor string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(vendor))
	if !federationVendors[normalized] {
		return "", errors.New("vendor must be one of ad, rhds, tivoli, edirectory, other")
	}
	return normalized, nil
}

// validateFederationConnectionURL validates an LDAP connection URL and
// returns its normalized host for the LDAP_ALLOWED_HOSTS check. Only ldap
// and ldaps schemes are accepted, and only ldaps outside
// development/test: the component this URL is handed to binds with the
// global LDAP_BIND_PASSWORD in cleartext over plain ldap (no StartTLS is
// negotiated), so cleartext has no place in production. Userinfo and
// fragments are rejected; an explicit port must be a TCP port. Loopback,
// link-local/metadata and unspecified addresses are blocked outside
// development/test, while private (RFC1918/ULA) directory servers stay
// allowed everywhere — that is where corporate LDAP normally lives.
func validateFederationConnectionURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errors.New("connectionUrl must be a valid absolute LDAP URL")
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "ldaps":
		// always ok
	case "ldap":
		// Plain LDAP only in development/test (E2E binds against a local
		// OpenLDAP without TLS); production must use ldaps.
		if !httpx.IsDevOrTestEnv() {
			return "", errors.New("connectionUrl must use ldaps outside development/test")
		}
	default:
		return "", errors.New("connectionUrl scheme must be ldap or ldaps")
	}
	if u.User != nil {
		return "", errors.New("connectionUrl must not contain userinfo")
	}
	if u.Fragment != "" {
		return "", errors.New("connectionUrl must not contain a fragment")
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("connectionUrl must include a host")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", errors.New("connectionUrl port must be between 1 and 65535")
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if !httpx.IsDevOrTestEnv() && httpx.IsBlockedLoopbackOrLinkLocal(ip) {
			return "", errors.New("connectionUrl must not target a loopback or link-local address")
		}
		return normalizeLDAPHost(host), nil
	}
	if len(host) > 253 {
		return "", errors.New("connectionUrl must contain a valid hostname or IP address")
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if !smtpHostLabelPattern.MatchString(label) {
			return "", errors.New("connectionUrl must contain a valid hostname or IP address")
		}
	}
	if err := httpx.ValidateHostAddresses("connectionUrl", host, httpx.IsBlockedLoopbackOrLinkLocal); err != nil {
		return "", err
	}
	return normalizeLDAPHost(host), nil
}

// normalizeLDAPHost lowercases a validated LDAP host and strips a trailing
// dot so allow-list comparison is canonical.
func normalizeLDAPHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// ldapURLHostString extracts the normalized host from a stored connectionUrl
// value of unknown shape, reporting whether it parsed.
func ldapURLHostString(v interface{}) (string, bool) {
	raw, ok := v.(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	return normalizeLDAPHost(u.Hostname()), true
}

// ldapURLHostChanged reports whether newRaw points at a different host than
// the stored value. An unparseable stored URL counts as changed (fail
// closed): the destination cannot be proven to be the same one.
func ldapURLHostChanged(stored interface{}, newRaw string) bool {
	oldHost, ok := ldapURLHostString(stored)
	if !ok {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(newRaw))
	if err != nil || u.Hostname() == "" {
		return true
	}
	return normalizeLDAPHost(u.Hostname()) != oldHost
}

// checkLDAPAllowedHost binds LDAP_BIND_PASSWORD to its destination: outside
// development/test, the connection host must appear in the operator's
// LDAP_ALLOWED_HOSTS allow-list (comma-separated hostnames). An unset or
// empty list refuses every new host (fail closed); development/test is
// unrestricted. The allow-list is read with os.Getenv because config/ wiring
// belongs to another lane (see the follow-ups in the final report).
func checkLDAPAllowedHost(host string) error {
	if httpx.IsDevOrTestEnv() {
		return nil
	}
	raw := os.Getenv("LDAP_ALLOWED_HOSTS")
	if strings.TrimSpace(raw) == "" {
		return errors.New("LDAP_ALLOWED_HOSTS is not configured; set it to the allowed LDAP hostnames before creating or repointing a federation source")
	}
	for _, entry := range strings.Split(raw, ",") {
		if normalizeLDAPHost(strings.TrimSpace(entry)) == host && host != "" {
			return nil
		}
	}
	return errors.New("connectionUrl host is not in LDAP_ALLOWED_HOSTS")
}
