package handlers

// Epic C (v1.7) — multi-tenant org management.
//
// One shared Keycloak realm; org isolation lives entirely in FreeCloud's own
// data model (organizations + org_memberships, see backend/internal/db/schema.go
// Migration043 and docs/adr/0005-multi-tenant-shared-realm.md).
//
// Two admin tiers:
//   - system-admin (RoleSuperAdmin, global JWT role): creates orgs, may act on
//     any org.
//   - org-admin (org_memberships.role = "org-admin", scoped to one org):
//     manages membership within their own org only.

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// OrgResponse is the JSON representation of an organization.
type OrgResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"createdAt"`
}

// CreateOrgRequest is the body for POST /api/v1/orgs.
type CreateOrgRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// CreateOrg handles POST /api/v1/orgs (system-admin only via PermManageOrgs).
func (h *Handler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	var req CreateOrgRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))

	var valErrs []ValidationError
	if req.Name == "" {
		valErrs = append(valErrs, ValidationError{Field: "name", Message: "name is required"})
	} else if len(req.Name) > 200 {
		valErrs = append(valErrs, ValidationError{Field: "name", Message: "name must be ≤ 200 characters"})
	}
	if req.Slug == "" {
		valErrs = append(valErrs, ValidationError{Field: "slug", Message: "slug is required"})
	} else if len(req.Slug) > 63 || !slugPattern.MatchString(req.Slug) {
		valErrs = append(valErrs, ValidationError{Field: "slug", Message: "slug must be lowercase alphanumeric with hyphens (e.g. acme-corp)"})
	}
	if len(valErrs) > 0 {
		respondValidationErrors(w, valErrs)
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	var id string
	var createdAt time.Time
	err := h.db.QueryRow(ctx,
		`INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING id, created_at`,
		req.Name, req.Slug,
	).Scan(&id, &createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			respondValidationErrors(w, []ValidationError{{Field: "slug", Message: "slug is already in use"}})
			return
		}
		h.logger.Error("failed to create organization", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create organization")
		return
	}

	if err := h.writeAuditEntryBestEffort(actorID, "org.create", "organization", id, map[string]interface{}{
		"name": req.Name,
		"slug": req.Slug,
	}); err != nil {
		h.logger.Warn("failed to write org create audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusCreated, OrgResponse{
		ID: id, Name: req.Name, Slug: req.Slug, CreatedAt: createdAt.Format(time.RFC3339),
	})
}

// ListOrgs handles GET /api/v1/orgs (system-admin only).
func (h *Handler) ListOrgs(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondJSON(w, http.StatusOK, []OrgResponse{})
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, slug, created_at FROM organizations ORDER BY created_at ASC`,
	)
	if err != nil {
		h.logger.Error("failed to list organizations", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := []OrgResponse{}
	for rows.Next() {
		var o OrgResponse
		var createdAt time.Time
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &createdAt); err != nil {
			h.logger.Error("failed to scan organization row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		o.CreatedAt = createdAt.Format(time.RFC3339)
		out = append(out, o)
	}
	respondJSON(w, http.StatusOK, out)
}

// MeOrgMembership describes one organization the caller belongs to.
type MeOrgMembership struct {
	OrgID   string `json:"orgId"`
	OrgName string `json:"orgName"`
	OrgSlug string `json:"orgSlug"`
	Role    string `json:"role"`
}

// MeResponse is the body for GET /api/v1/me — the caller's identity, global
// RBAC role, resolved active org, and every org they belong to (for the
// frontend org switcher).
type MeResponse struct {
	Sub          string            `json:"sub"`
	Email        string            `json:"email"`
	GlobalRole   string            `json:"globalRole"`
	ActiveOrgID  string            `json:"activeOrgId"`
	ActiveRole   string            `json:"activeRole"`
	Orgs         []MeOrgMembership `json:"orgs"`
	IsSystemAdmin bool             `json:"isSystemAdmin"`
}

// Me handles GET /api/v1/me.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	oc := middleware.GetOrgContext(ctx)

	resp := MeResponse{
		Sub:           claims.Sub,
		Email:         claims.Email,
		GlobalRole:    string(claims.Role),
		IsSystemAdmin: claims.Role == middleware.RoleSuperAdmin,
		Orgs:          []MeOrgMembership{},
	}
	if oc != nil {
		resp.ActiveOrgID = oc.OrgID
		resp.ActiveRole = oc.Role
	}

	if h.db != nil {
		rows, err := h.db.Query(ctx,
			`SELECT o.id, o.name, o.slug, m.role
			 FROM org_memberships m
			 JOIN organizations o ON o.id = m.org_id
			 WHERE m.user_id = $1
			 ORDER BY o.name ASC`,
			claims.Sub,
		)
		if err != nil {
			h.logger.Warn("failed to list caller's org memberships", zap.Error(err))
		} else {
			defer rows.Close()
			for rows.Next() {
				var m MeOrgMembership
				if err := rows.Scan(&m.OrgID, &m.OrgName, &m.OrgSlug, &m.Role); err != nil {
					continue
				}
				resp.Orgs = append(resp.Orgs, m)
			}
		}
	}

	respondJSON(w, http.StatusOK, resp)
}

// OrgMemberResponse is the JSON representation of one org membership row.
type OrgMemberResponse struct {
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Role      string `json:"role"`
}

// requireOwnOrgOrSystemAdmin verifies the caller (already known to be either a
// system-admin or SOME org's org-admin, per RequireOrgAdminOrSystemAdmin) is
// authorized for the specific :orgId path param. System-admin passes for any
// org; an org-admin passes only when orgId matches their resolved OrgContext.
// Returns false (and has already written the response) when denied.
func (h *Handler) requireOwnOrgOrSystemAdmin(w http.ResponseWriter, r *http.Request, orgID string) bool {
	claims := middleware.GetClaims(r.Context())
	if claims != nil && claims.Role == middleware.RoleSuperAdmin {
		return true
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc != nil && oc.Role == middleware.OrgMembershipRoleAdmin && oc.OrgID == orgID {
		return true
	}
	respondError(w, http.StatusForbidden, "forbidden: not an admin of this organization")
	return false
}

// ListOrgMembers handles GET /api/v1/orgs/{orgId}/members.
func (h *Handler) ListOrgMembers(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if !isValidUUID(orgID) {
		respondError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	if !h.requireOwnOrgOrSystemAdmin(w, r, orgID) {
		return
	}
	if h.db == nil {
		respondJSON(w, http.StatusOK, []OrgMemberResponse{})
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT u.keycloak_user_id, u.email, u.first_name, u.last_name, m.role
		 FROM org_memberships m
		 JOIN users u ON u.keycloak_user_id = m.user_id
		 WHERE m.org_id = $1
		 ORDER BY u.email ASC`,
		orgID,
	)
	if err != nil {
		h.logger.Error("failed to list org members", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := []OrgMemberResponse{}
	for rows.Next() {
		var m OrgMemberResponse
		if err := rows.Scan(&m.UserID, &m.Email, &m.FirstName, &m.LastName, &m.Role); err != nil {
			h.logger.Error("failed to scan org member row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, m)
	}
	respondJSON(w, http.StatusOK, out)
}

// AddOrgMemberRequest is the body for POST /api/v1/orgs/{orgId}/members.
type AddOrgMemberRequest struct {
	UserID string `json:"userId"`
	Role   string `json:"role"` // "org-admin" or "member"
}

// AddOrgMember handles POST /api/v1/orgs/{orgId}/members.
func (h *Handler) AddOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if !isValidUUID(orgID) {
		respondError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	if !h.requireOwnOrgOrSystemAdmin(w, r, orgID) {
		return
	}

	var req AddOrgMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Role = strings.TrimSpace(req.Role)
	if req.Role == "" {
		req.Role = "member"
	}

	var valErrs []ValidationError
	if !isValidUUID(req.UserID) {
		valErrs = append(valErrs, ValidationError{Field: "userId", Message: "userId must be a valid UUID"})
	}
	if req.Role != "org-admin" && req.Role != "member" {
		valErrs = append(valErrs, ValidationError{Field: "role", Message: "role must be org-admin or member"})
	}
	if len(valErrs) > 0 {
		respondValidationErrors(w, valErrs)
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	// H1: verify the target user's OWN org (users.org_id) is the org being
	// joined — not merely that the user exists anywhere. Without this, an
	// org-admin (who passes requireOwnOrgOrSystemAdmin for their own org)
	// could bind ANY other tenant's user into their org with a caller-chosen
	// role by ID alone. A user who exists but belongs to a different org is
	// indistinguishable here from one that doesn't exist at all — both 404,
	// never leaking which case it was.
	var foundUID string
	if err := h.db.QueryRow(ctx,
		`SELECT keycloak_user_id FROM users WHERE keycloak_user_id = $1 AND org_id = $2`,
		req.UserID, orgID,
	).Scan(&foundUID); err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("failed to verify user for org membership", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err := h.db.Exec(ctx,
		`INSERT INTO org_memberships (org_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		orgID, req.UserID, req.Role,
	)
	if err != nil {
		h.logger.Error("failed to add org member", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to add member")
		return
	}

	if err := h.writeAuditEntryBestEffort(actorID, "org.member_add", "organization", orgID, map[string]interface{}{
		"user_id": req.UserID,
		"role":    req.Role,
	}); err != nil {
		h.logger.Warn("failed to write org member add audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"userId": req.UserID, "role": req.Role})
}

// RemoveOrgMember handles DELETE /api/v1/orgs/{orgId}/members/{userId}.
func (h *Handler) RemoveOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	userID := chi.URLParam(r, "userId")
	if !isValidUUID(orgID) || !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if !h.requireOwnOrgOrSystemAdmin(w, r, orgID) {
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	tag, err := h.db.Exec(ctx, `DELETE FROM org_memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID)
	if err != nil {
		h.logger.Error("failed to remove org member", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		respondError(w, http.StatusNotFound, "membership not found")
		return
	}

	if err := h.writeAuditEntryBestEffort(actorID, "org.member_remove", "organization", orgID, map[string]interface{}{
		"user_id": userID,
	}); err != nil {
		h.logger.Warn("failed to write org member remove audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505), used to turn a slug collision into a clean
// 400 validation error instead of a 500.
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "duplicate key")
}
