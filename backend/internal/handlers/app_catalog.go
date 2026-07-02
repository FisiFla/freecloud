package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// AppTemplate describes a pre-built application template in the catalog.
type AppTemplate struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	Protocol            string             `json:"protocol"`
	Description         string             `json:"description"`
	LogoURL             string             `json:"logoUrl,omitempty"`
	RequiredFields      []AppTemplateField `json:"requiredFields"`
	DefaultRedirectURIs []string           `json:"defaultRedirectURIs"`
	Notes               string             `json:"notes,omitempty"`
}

// AppTemplateField describes a single required field for a template.
type AppTemplateField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
}

// CreateAppFromTemplateRequest is the request body for POST /api/v1/apps/templates/{templateId}/create.
type CreateAppFromTemplateRequest struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"` // key = AppTemplateField.Name
}

// appTemplates is the static catalog of pre-built app templates.
var appTemplates = []AppTemplate{
	{
		ID:          "google-workspace",
		Name:        "Google Workspace",
		Protocol:    "SAML",
		Description: "SSO into Google Workspace (G Suite) via SAML 2.0.",
		LogoURL:     "",
		RequiredFields: []AppTemplateField{
			{Name: "baseURL", Label: "Base URL", Placeholder: "https://www.google.com/a/<your-domain>", Required: true},
			{Name: "acsURL", Label: "ACS URL (SP)", Placeholder: "https://www.google.com/a/<your-domain>/acs", Required: true},
		},
		DefaultRedirectURIs: []string{},
		Notes:               "Set the SP Entity ID to your Google Workspace domain URL. ACS URL is your domain's assertion consumer service.",
	},
	{
		ID:          "github",
		Name:        "GitHub Enterprise",
		Protocol:    "SAML",
		Description: "SSO into GitHub Enterprise Server via SAML 2.0.",
		LogoURL:     "",
		RequiredFields: []AppTemplateField{
			{Name: "baseURL", Label: "GitHub Enterprise URL", Placeholder: "https://github.yourdomain.com", Required: true},
			{Name: "acsURL", Label: "ACS URL", Placeholder: "https://github.yourdomain.com/saml/consume", Required: true},
		},
		DefaultRedirectURIs: []string{},
		Notes:               "Enable SAML in GitHub Enterprise Admin Console, then paste the IdP metadata URL from FreeCloud.",
	},
	{
		ID:          "slack",
		Name:        "Slack",
		Protocol:    "SAML",
		Description: "SSO into Slack via SAML 2.0 (Slack Business+ or Enterprise Grid).",
		LogoURL:     "",
		RequiredFields: []AppTemplateField{
			{Name: "baseURL", Label: "Slack Workspace URL", Placeholder: "https://yourworkspace.slack.com", Required: true},
			{Name: "acsURL", Label: "ACS URL", Placeholder: "https://yourworkspace.slack.com/sso/saml", Required: true},
		},
		DefaultRedirectURIs: []string{},
		Notes:               "In Slack Admin, go to Settings → Authentication → Configure SAML.",
	},
	{
		ID:          "aws",
		Name:        "AWS IAM Identity Center",
		Protocol:    "SAML",
		Description: "Federate AWS IAM Identity Center (SSO) via SAML 2.0.",
		LogoURL:     "",
		RequiredFields: []AppTemplateField{
			{Name: "baseURL", Label: "AWS SSO Start URL", Placeholder: "https://d-xxxx.awsapps.com/start", Required: true},
			{Name: "acsURL", Label: "ACS URL", Placeholder: "https://us-east-1.signin.aws.amazon.com/saml", Required: true},
		},
		DefaultRedirectURIs: []string{},
		Notes:               "In AWS IAM Identity Center, create a custom SAML application and paste the IdP metadata.",
	},
	{
		ID:          "generic-oidc",
		Name:        "Generic OIDC Application",
		Protocol:    "OIDC",
		Description: "Connect any OIDC-compatible application.",
		LogoURL:     "",
		RequiredFields: []AppTemplateField{
			{Name: "baseURL", Label: "Application URL", Placeholder: "https://myapp.example.com", Required: true},
			{Name: "redirectURI", Label: "Redirect URI", Placeholder: "https://myapp.example.com/auth/callback", Required: true},
		},
		DefaultRedirectURIs: []string{},
		Notes:               "Use the client ID and client secret from the created application in your OIDC provider config.",
	},
}

// findTemplate looks up a template by ID. Returns nil if not found.
func findTemplate(id string) *AppTemplate {
	for i := range appTemplates {
		if appTemplates[i].ID == id {
			return &appTemplates[i]
		}
	}
	return nil
}

// ListAppTemplates returns the full static catalog.
// GET /api/v1/apps/templates — requires PermReadApps.
func (h *Handler) ListAppTemplates(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, appTemplates)
}

// CreateAppFromTemplate creates a connected app from a catalog template.
// POST /api/v1/apps/templates/{templateId}/create — requires PermManageApps.
func (h *Handler) CreateAppFromTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := chi.URLParam(r, "templateId")
	tmpl := findTemplate(templateID)
	if tmpl == nil {
		respondError(w, http.StatusNotFound, "template not found")
		return
	}

	var req CreateAppFromTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Determine the app name: use provided name or fall back to template name.
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = tmpl.Name
	}

	// Validate name length (reuse same rule as CreateApp).
	if len(name) > 255 {
		respondError(w, http.StatusBadRequest, "name must be ≤ 255 characters")
		return
	}

	// Validate all required fields are present and non-empty.
	if req.Fields == nil {
		req.Fields = map[string]string{}
	}
	for _, f := range tmpl.RequiredFields {
		if f.Required && strings.TrimSpace(req.Fields[f.Name]) == "" {
			respondError(w, http.StatusBadRequest, "required field missing: "+f.Name)
			return
		}
	}

	baseURL := strings.TrimSpace(req.Fields["baseURL"])
	if baseURL != "" {
		if err := validateRedirectURI(baseURL); err != nil {
			respondError(w, http.StatusBadRequest, "invalid baseURL: "+err.Error())
			return
		}
	}

	// Build redirect URIs from template-specific fields.
	var redirectURIs []string
	if tmpl.Protocol == "SAML" {
		acsURL := strings.TrimSpace(req.Fields["acsURL"])
		if acsURL != "" {
			if err := validateRedirectURI(acsURL); err != nil {
				respondValidationErrors(w, []ValidationError{{
					Field:   "acsURL",
					Message: err.Error(),
				}})
				return
			}
			redirectURIs = []string{acsURL}
		}
	} else {
		// OIDC
		redirectURI := strings.TrimSpace(req.Fields["redirectURI"])
		if redirectURI != "" {
			if err := validateRedirectURI(redirectURI); err != nil {
				respondValidationErrors(w, []ValidationError{{
					Field:   "redirectURI",
					Message: err.Error(),
				}})
				return
			}
			redirectURIs = []string{redirectURI}
		}
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()

	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	// Create client in Keycloak. Template-created apps use default SAML options.
	keycloakClientID, err := h.keycloak.CreateClient(ctx, name, tmpl.Protocol, redirectURIs, baseURL, nil)
	if err != nil {
		h.logger.Error("failed to create keycloak client from template", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create app in Keycloak")
		return
	}

	// Clean up the Keycloak client if the DB write fails.
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

	// Persist to connected_apps.
	var appID string
	err = h.db.QueryRow(ctx,
		`INSERT INTO connected_apps (keycloak_client_id, name, protocol, base_url, org_id)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		keycloakClientID, name, tmpl.Protocol, baseURL, oc.OrgID,
	).Scan(&appID)
	if err != nil {
		h.logger.Error("failed to store connected app from template", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to store app")
		return
	}
	dbSucceeded = true

	// Write audit log.
	actorID := middleware.GetActorID(r.Context())
	if auditErr := h.writeAuditEntry(ctx, actorID, "app_create_from_template", "app", appID, map[string]interface{}{
		"name":        name,
		"protocol":    tmpl.Protocol,
		"template_id": tmpl.ID,
	}); auditErr != nil {
		h.logger.Warn("failed to write audit log", zap.Error(auditErr))
	}

	resp := CreateAppResponse{
		ID:               appID,
		Name:             name,
		KeycloakClientID: keycloakClientID,
	}
	// Surface SAML SP metadata so admins can configure their SP.
	if tmpl.Protocol == "SAML" {
		entityID := baseURL
		if entityID == "" {
			entityID = name
		}
		acsURL := ""
		if len(redirectURIs) > 0 {
			acsURL = redirectURIs[0]
		}
		resp.SAMLEntityID = entityID
		resp.SAMLAcsURL = acsURL
	}
	respondJSON(w, http.StatusOK, resp)
}
