package handlers

// D3 — Identity provider (social / OIDC / SAML) management API.
// Identity providers are stored in Keycloak — no DB table is needed.
// Client secrets go directly to Keycloak via the service account and are
// never persisted locally.

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// IdentityProviderRequest is the body for POST/PUT identity provider routes.
type IdentityProviderRequest struct {
	Alias        string            `json:"alias"`
	DisplayName  string            `json:"displayName"`
	ProviderType string            `json:"providerType"` // "google", "github", "oidc", "saml"
	Config       map[string]string `json:"config"`
}

// validIDPTypes is the set of supported Keycloak identity provider types.
var validIDPTypes = map[string]bool{
	"google": true,
	"github": true,
	"oidc":   true,
	"saml":   true,
}

// ListIdentityProviders returns all identity providers configured in Keycloak.
// Route: GET /api/v1/settings/identity-providers
func (h *Handler) ListIdentityProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.keycloak.ListIdentityProviders(r.Context())
	if err != nil {
		h.logger.Error("list identity providers: keycloak failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, providers)
}

// CreateIdentityProvider creates a new identity provider in Keycloak.
// Route: POST /api/v1/settings/identity-providers
func (h *Handler) CreateIdentityProvider(w http.ResponseWriter, r *http.Request) {
	var req IdentityProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Alias == "" {
		respondError(w, http.StatusBadRequest, "alias is required")
		return
	}
	if !validIDPTypes[req.ProviderType] {
		respondError(w, http.StatusBadRequest, "providerType must be google, github, oidc, or saml")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	if err := h.keycloak.CreateIdentityProvider(ctx, req.Alias, req.DisplayName, req.ProviderType, req.Config); err != nil {
		h.logger.Error("create identity provider: keycloak failed", zap.Error(err), zap.String("alias", req.Alias))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "identity_provider.create", "identity_provider", req.Alias,
		map[string]interface{}{"provider_type": req.ProviderType})

	respondJSON(w, http.StatusCreated, map[string]string{"alias": req.Alias})
}

// UpdateIdentityProvider updates an existing identity provider in Keycloak.
// Route: PUT /api/v1/settings/identity-providers/{alias}
func (h *Handler) UpdateIdentityProvider(w http.ResponseWriter, r *http.Request) {
	alias := chi.URLParam(r, "alias")
	if alias == "" {
		respondError(w, http.StatusBadRequest, "alias is required")
		return
	}

	var req IdentityProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	if err := h.keycloak.UpdateIdentityProvider(ctx, alias, req.DisplayName, req.Config); err != nil {
		h.logger.Error("update identity provider: keycloak failed", zap.Error(err), zap.String("alias", alias))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "identity_provider.update", "identity_provider", alias, nil)

	respondJSON(w, http.StatusOK, map[string]string{"alias": alias})
}

// DeleteIdentityProvider removes an identity provider from Keycloak.
// Route: DELETE /api/v1/settings/identity-providers/{alias}
func (h *Handler) DeleteIdentityProvider(w http.ResponseWriter, r *http.Request) {
	alias := chi.URLParam(r, "alias")
	if alias == "" {
		respondError(w, http.StatusBadRequest, "alias is required")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	if err := h.keycloak.DeleteIdentityProvider(ctx, alias); err != nil {
		h.logger.Error("delete identity provider: keycloak failed", zap.Error(err), zap.String("alias", alias))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "identity_provider.delete", "identity_provider", alias, nil)

	respondJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
