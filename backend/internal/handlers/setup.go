package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// SetupStatus handles GET /api/v1/setup/status.
// Returns {"provisioned": bool} — unauthenticated, read-only.
// "Provisioned" is derived from Keycloak state: true when at least one user
// holds the "admin" realm role (no migration required).
func (h *Handler) SetupStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provisioned, err := h.keycloak.HasAdminUser(ctx)
	if err != nil {
		h.logger.Error("setup status check failed")
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"provisioned": provisioned})
}

// Setup handles POST /api/v1/setup.
// Body: {adminEmail, adminPassword, orgName}
// Fail-closed: returns 409 once provisioned.
func (h *Handler) Setup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Fail-closed: check provisioned FIRST.
	provisioned, err := h.keycloak.HasAdminUser(ctx)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if provisioned {
		respondError(w, http.StatusConflict, "already provisioned")
		return
	}

	var body struct {
		AdminEmail    string `json:"adminEmail"`
		AdminPassword string `json:"adminPassword"`
		OrgName       string `json:"orgName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var errs []ValidationError
	if !isValidEmail(body.AdminEmail) {
		errs = append(errs, ValidationError{Field: "adminEmail", Message: "invalid email address"})
	}
	if !isStrongEnough(body.AdminPassword) {
		errs = append(errs, ValidationError{Field: "adminPassword", Message: "password must be at least 8 characters"})
	}
	if strings.TrimSpace(body.OrgName) == "" {
		errs = append(errs, ValidationError{Field: "orgName", Message: "organization name is required"})
	}
	if len(errs) > 0 {
		respondValidationErrors(w, errs)
		return
	}

	_, err = h.keycloak.CreateAdminUser(ctx, body.AdminEmail, body.AdminPassword)
	if err != nil {
		h.logger.Error("setup: create admin user failed")
		respondError(w, http.StatusInternalServerError, "failed to create admin user")
		return
	}

	respondJSON(w, http.StatusCreated, map[string]string{"message": "provisioned"})
}

// isStrongEnough checks that the password has at least 8 characters and is not
// purely whitespace.
func isStrongEnough(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}
