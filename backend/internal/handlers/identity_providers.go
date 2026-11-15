package handlers

// D3 — Identity provider (social / OIDC / SAML) management API.
// Identity providers are stored in Keycloak — no DB table is needed.
// Client secrets go directly to Keycloak via the service account and are
// never persisted locally.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/Nerzal/gocloak/v13"
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
// Secret-bearing config keys are redacted to a fixed mask before responding:
// Keycloak returns the stored clientSecret verbatim, and the settings UI has
// no business seeing it.
// Route: GET /api/v1/settings/identity-providers
func (h *Handler) ListIdentityProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.keycloak.ListIdentityProviders(r.Context())
	if err != nil {
		h.logger.Error("list identity providers: keycloak failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, p := range providers {
		redactIdentityProviderSecrets(p)
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
	if err := validateIdentityProviderAlias(req.Alias); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
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

	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "identity_provider.create", "identity_provider", req.Alias,
		map[string]interface{}{"provider_type": req.ProviderType}); err != nil {
		h.logger.Warn("failed to write identity provider creation audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusCreated, map[string]string{"alias": req.Alias})
}

// UpdateIdentityProvider updates an existing identity provider in Keycloak.
// Route: PUT /api/v1/settings/identity-providers/{alias}
func (h *Handler) UpdateIdentityProvider(w http.ResponseWriter, r *http.Request) {
	alias := chi.URLParam(r, "alias")
	if err := validateIdentityProviderAlias(alias); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req IdentityProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	// The settings UI round-trips the list response, so secret fields come
	// back as the mask. Resolve each masked secret against the stored
	// config: writing the literal mask would lock the provider out with a
	// wrong secret (and the real secret would be lost).
	config, err := h.resolveIdentityProviderSecrets(ctx, alias, req.Config)
	if err != nil {
		h.logger.Warn("update identity provider: cannot resolve masked secrets", zap.String("alias", alias), zap.Error(err))
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.keycloak.UpdateIdentityProvider(ctx, alias, req.DisplayName, config); err != nil {
		h.logger.Error("update identity provider: keycloak failed", zap.Error(err), zap.String("alias", alias))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "identity_provider.update", "identity_provider", alias, nil); err != nil {
		h.logger.Warn("failed to write identity provider update audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]string{"alias": alias})
}

// DeleteIdentityProvider removes an identity provider from Keycloak.
// Route: DELETE /api/v1/settings/identity-providers/{alias}
func (h *Handler) DeleteIdentityProvider(w http.ResponseWriter, r *http.Request) {
	alias := chi.URLParam(r, "alias")
	if err := validateIdentityProviderAlias(alias); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	if err := h.keycloak.DeleteIdentityProvider(ctx, alias); err != nil {
		h.logger.Error("delete identity provider: keycloak failed", zap.Error(err), zap.String("alias", alias))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.writeAuditEntryBestEffort(r.Context(), actorID, "identity_provider.delete", "identity_provider", alias, nil); err != nil {
		h.logger.Warn("failed to write identity provider deletion audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// identityProviderAliasPattern is the allowed IdP alias shape: 1-128 chars
// of letters, digits, dot, underscore and hyphen.
var identityProviderAliasPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// validateIdentityProviderAlias rejects empty, overlong, and path-shaped
// aliases before they reach Keycloak (the alias is interpolated into admin
// URLs and used as the audit target id).
func validateIdentityProviderAlias(alias string) error {
	if !identityProviderAliasPattern.MatchString(alias) {
		return errors.New("alias must be 1-128 characters of letters, digits, dot, underscore or hyphen")
	}
	return nil
}

// identityProviderSecretMask is the fixed value secret-bearing config keys
// are replaced with on list. Updates may send it back to mean "unchanged".
const identityProviderSecretMask = "********"

// isIdentityProviderSecretKey reports whether a config key bears a secret:
// clientSecret, and any key whose lowercase name contains secret, password
// or token.
func isIdentityProviderSecretKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "secret") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "token")
}

// redactIdentityProviderSecrets replaces every secret-bearing config value
// with the fixed mask, copying the map so the Keycloak client's object is
// left untouched.
func redactIdentityProviderSecrets(p *gocloak.IdentityProviderRepresentation) {
	if p == nil || p.Config == nil {
		return
	}
	redacted := make(map[string]string, len(*p.Config))
	for k, v := range *p.Config {
		if isIdentityProviderSecretKey(k) {
			redacted[k] = identityProviderSecretMask
		} else {
			redacted[k] = v
		}
	}
	p.Config = &redacted
}

// resolveIdentityProviderSecrets returns the config to store for an update:
// every secret-bearing key submitted as the mask is restored from the
// provider's current Keycloak config ("unchanged"). A mask with no stored
// value to restore is rejected — the caller must re-enter the secret —
// because writing the literal mask would destroy it. Configs without any
// mask pass through untouched, without a Keycloak read.
func (h *Handler) resolveIdentityProviderSecrets(ctx context.Context, alias string, submitted map[string]string) (map[string]string, error) {
	masked := false
	for k, v := range submitted {
		if v == identityProviderSecretMask && isIdentityProviderSecretKey(k) {
			masked = true
			break
		}
	}
	if !masked {
		return submitted, nil
	}
	providers, err := h.keycloak.ListIdentityProviders(ctx)
	if err != nil {
		return nil, errors.New("cannot verify masked secrets: identity provider lookup failed")
	}
	var stored map[string]string
	for _, p := range providers {
		if p != nil && p.Alias != nil && *p.Alias == alias && p.Config != nil {
			stored = *p.Config
			break
		}
	}
	resolved := make(map[string]string, len(submitted))
	for k, v := range submitted {
		if v == identityProviderSecretMask && isIdentityProviderSecretKey(k) {
			real, ok := stored[k]
			if !ok || real == "" {
				return nil, errors.New("secret for " + k + " must be re-entered: no stored value to keep")
			}
			resolved[k] = real
			continue
		}
		resolved[k] = v
	}
	return resolved, nil
}
