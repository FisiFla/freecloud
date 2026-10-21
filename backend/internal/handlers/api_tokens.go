package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
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

// MaxAPITokenTTLDays is the maximum lifetime an API token may be created
// with. Tokens are long-lived credentials, so an operator must opt in to a
// long lifetime explicitly and it can never be unbounded.
const MaxAPITokenTTLDays = 365

// DefaultAPITokenTTLDays is the lifetime applied when a caller asks for a
// token with no expiry (expiresInDays == 0). A non-super-admin token is
// always given a bounded lifetime — a never-expiring credential for a
// lower-privilege role has no legitimate use.
const DefaultAPITokenTTLDays = 90

// CreateAPITokenRequest is the body for POST /api/v1/api-tokens.
type CreateAPITokenRequest struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	ServiceIdentity string `json:"serviceIdentity"`
	// ExpiresInDays: 0 = the default bounded lifetime
	// (DefaultAPITokenTTLDays, or unlimited for a super-admin token).
	// Values above MaxAPITokenTTLDays are rejected.
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
	} else if req.ExpiresInDays > MaxAPITokenTTLDays {
		// A token is a long-lived credential: cap the lifetime so a
		// mistake (or a hostile value) cannot mint a permanent credential,
		// and so the expiry arithmetic below cannot overflow.
		valErrs = append(valErrs, ValidationError{Field: "expiresInDays",
			Message: fmt.Sprintf("expiresInDays must be ≤ %d", MaxAPITokenTTLDays)})
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

	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	// Compute the expiry.
	//
	// The expiry is computed with AddDate (calendar days) rather than
	// time.Duration(days) * 24 * time.Hour: the latter overflows int64 for
	// large day counts, which produced a token that was born expired or
	// expired at an arbitrary far-future date (issue #123). The day count is
	// bounded by MaxAPITokenTTLDays above.
	expiresInDays := req.ExpiresInDays
	if expiresInDays == 0 {
		// A super-admin token is the only one that may be created without an
		// expiry: it is the credential the backend itself uses to administer
		// the realm, and it is created deliberately. Every other role gets
		// the bounded default.
		if req.Role == string(middleware.RoleSuperAdmin) {
			expiresInDays = 0 // no expiry
		} else {
			expiresInDays = DefaultAPITokenTTLDays
		}
	}
	var expiresAt *time.Time
	if expiresInDays > 0 {
		t := time.Now().UTC().AddDate(0, 0, expiresInDays)
		expiresAt = &t
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin api token transaction", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create token")
		return
	}
	defer tx.Rollback(ctx)

	var id string
	var createdAt time.Time
	err = tx.QueryRow(ctx,
		`INSERT INTO api_tokens (name, token_hash, role, scopes, service_identity, created_by, expires_at, org_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, created_at`,
		req.Name, hash, req.Role, []string{}, req.ServiceIdentity, actorID, expiresAt, oc.OrgID,
	).Scan(&id, &createdAt)
	if err != nil {
		h.logger.Error("failed to insert api token", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create token")
		return
	}
	if err := writeAuditEntry(ctx, tx, actorID, "api_token.create", "api_token", id, map[string]interface{}{
		"name":             req.Name,
		"role":             req.Role,
		"service_identity": req.ServiceIdentity,
		"expires_at":       expiresAt,
	}); err != nil {
		h.logger.Error("failed to audit api token creation", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create token")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit api token creation", zap.Error(err))
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
	// C2: org-scoped read. Fail closed — no org context means no rows.
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, role, service_identity, created_at, expires_at
		 FROM api_tokens WHERE org_id = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW()) ORDER BY created_at DESC`,
		oc.OrgID,
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
	if err := ValidateUserID(id); err != nil {
		respondError(w, http.StatusBadRequest, "invalid token id")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin api token revocation", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var name, role, serviceIdentity string
	err = tx.QueryRow(ctx,
		`UPDATE api_tokens
		 SET revoked_at = NOW()
		 WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL
		 RETURNING name, role, service_identity`,
		id, oc.OrgID,
	).Scan(&name, &role, &serviceIdentity)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondError(w, http.StatusNotFound, "token not found or already revoked")
			return
		}
		h.logger.Error("failed to revoke api token", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := writeAuditEntry(ctx, tx, actorID, "api_token.revoke", "api_token", id, map[string]interface{}{
		"name":             name,
		"role":             role,
		"service_identity": serviceIdentity,
	}); err != nil {
		h.logger.Error("failed to audit api token revocation", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit api token revocation", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}
