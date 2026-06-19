package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// APITokenResponse is returned on token creation — the only time the plaintext
// token is visible. It is NOT stored; callers must save it immediately.
type APITokenResponse struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Token           string  `json:"token,omitempty"` // plaintext, creation only
	Role            string  `json:"role"`
	ServiceIdentity string  `json:"serviceIdentity"`
	CreatedAt       string  `json:"createdAt"`
	ExpiresAt       *string `json:"expiresAt,omitempty"`
}

// CreateAPITokenRequest is the body for POST /api/v1/api-tokens.
type CreateAPITokenRequest struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	ServiceIdentity string `json:"serviceIdentity"`
	// ExpiresInDays: 0 = never expires.
	ExpiresInDays int `json:"expiresInDays"`
}

// ListAPITokensResponse is the body for GET /api/v1/api-tokens.
type ListAPITokensResponse struct {
	Tokens []APITokenResponse `json:"tokens"`
}

var validTokenRoles = map[middleware.Role]bool{
	middleware.RoleSuperAdmin: true,
	middleware.RoleHelpdesk:   true,
	middleware.RoleAuditor:    true,
	middleware.RoleReadOnly:   true,
}

// CreateAPIToken handles POST /api/v1/api-tokens.
// Requires PermManageAPITokens (super-admin only).
func (h *Handler) CreateAPIToken(w http.ResponseWriter, r *http.Request) {
	var req CreateAPITokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var valErrs []ValidationError
	if req.Name == "" {
		valErrs = append(valErrs, ValidationError{Field: "name", Message: "name is required"})
	} else if len(req.Name) > 100 {
		valErrs = append(valErrs, ValidationError{Field: "name", Message: "name must be ≤ 100 characters"})
	}
	role, ok := middleware.RoleFromString(req.Role)
	if !ok || !validTokenRoles[role] {
		valErrs = append(valErrs, ValidationError{Field: "role", Message: "role must be super-admin, helpdesk, auditor, or read-only"})
	} else {
		req.Role = string(role)
	}
	if req.ServiceIdentity == "" {
		valErrs = append(valErrs, ValidationError{Field: "serviceIdentity", Message: "serviceIdentity is required"})
	} else if len(req.ServiceIdentity) > 100 {
		valErrs = append(valErrs, ValidationError{Field: "serviceIdentity", Message: "serviceIdentity must be ≤ 100 characters"})
	}
	if req.ExpiresInDays < 0 {
		valErrs = append(valErrs, ValidationError{Field: "expiresInDays", Message: "expiresInDays must be ≥ 0"})
	}
	if len(valErrs) > 0 {
		respondValidationErrors(w, valErrs)
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	// Generate a 32-byte random token with fc_ prefix.
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		h.logger.Error("failed to generate token entropy", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	plaintext := "fc_" + hex.EncodeToString(rawBytes)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(plaintext)))

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().UTC().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}

	var id string
	var createdAt time.Time
	err := h.db.QueryRow(ctx,
		`INSERT INTO api_tokens (name, token_hash, role, scopes, service_identity, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at`,
		req.Name, hash, req.Role, []string{}, req.ServiceIdentity, actorID, expiresAt,
	).Scan(&id, &createdAt)
	if err != nil {
		h.logger.Error("failed to insert api token", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	resp := APITokenResponse{
		ID:              id,
		Name:            req.Name,
		Token:           plaintext, // shown once
		Role:            req.Role,
		ServiceIdentity: req.ServiceIdentity,
		CreatedAt:       createdAt.Format(time.RFC3339),
	}
	if expiresAt != nil {
		s := expiresAt.Format(time.RFC3339)
		resp.ExpiresAt = &s
	}
	respondJSON(w, http.StatusCreated, resp)
}

// ListAPITokens handles GET /api/v1/api-tokens.
func (h *Handler) ListAPITokens(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, role, service_identity, created_at, expires_at
		 FROM api_tokens WHERE revoked_at IS NULL ORDER BY created_at DESC`,
	)
	if err != nil {
		h.logger.Error("failed to list api tokens", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	tokens := []APITokenResponse{}
	for rows.Next() {
		var t APITokenResponse
		var createdAt time.Time
		var expiresAt *time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Role, &t.ServiceIdentity, &createdAt, &expiresAt); err != nil {
			h.logger.Error("failed to scan api token row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		t.CreatedAt = createdAt.Format(time.RFC3339)
		if expiresAt != nil {
			s := expiresAt.Format(time.RFC3339)
			t.ExpiresAt = &s
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed to iterate api token rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, ListAPITokensResponse{Tokens: tokens})
}

// RevokeAPIToken handles DELETE /api/v1/api-tokens/{id}.
func (h *Handler) RevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondError(w, http.StatusBadRequest, "invalid token id")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	tag, err := h.db.Exec(r.Context(),
		`UPDATE api_tokens SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`,
		id,
	)
	if err != nil {
		h.logger.Error("failed to revoke api token", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		respondError(w, http.StatusNotFound, "token not found or already revoked")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}
