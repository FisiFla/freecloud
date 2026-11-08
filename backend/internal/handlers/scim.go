package handlers

// SCIM 2.0 Users/Groups shims. The implementation lives in
// internal/scim (extracted per docs/HANDLERS_OWNERSHIP.md step 3b);
// these aliases, the scimHost adapter and the delegating Handler
// methods keep routes.go and the handler tests on the names they have
// always used. New code should call internal/scim directly.
//
// The one dependency that could not move with the endpoints is the
// onboarding saga write (persistOnboard): it stays here and reaches
// internal/scim through the scim.Host seam implemented by scimHost
// below — see internal/scim/service.go.

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/FisiFla/freecloud/backend/internal/scim"
)

// SCIMUser is the SCIM User resource representation (re-exported from
// internal/scim).
type SCIMUser = scim.User

// SCIMGroup is the SCIM Group resource (re-exported from internal/scim).
type SCIMGroup = scim.Group

type scimName = scim.Name
type scimGroupListResponse = scim.GroupListResponse
type scimGroupMember = scim.GroupMember
type scimError = scim.ErrorResponse
type scimPatchOp = scim.PatchOp
type scimPatchRequest = scim.PatchRequest

// scimOrgTokenDB is the per-org SCIM token lookup surface (re-exported
// from internal/scim).
type scimOrgTokenDB = scim.OrgTokenDB

const (
	scimUserSchema  = scim.SchemaUser
	scimGroupSchema = scim.SchemaGroup
	scimPatchSchema = scim.SchemaPatchOp
	scimErrorSchema = scim.SchemaError
	scimContentType = scim.ContentType
)

// scimHost adapts Handler to scim.Host: the onboarding saga, audit
// attribution and the shared input validators stay in this package
// and are injected into the SCIM service at wiring time.
type scimHost struct{ h *Handler }

func (a scimHost) PersistOnboard(ctx context.Context, kcUserID string, req scim.OnboardRequest, actorID string, auditDetails any, enrollmentToken string, orgID string) error {
	return a.h.persistOnboard(ctx, kcUserID, OnboardRequest{
		FirstName:  req.FirstName,
		LastName:   req.LastName,
		Email:      req.Email,
		Department: req.Department,
		Role:       req.Role,
	}, actorID, auditDetails, enrollmentToken, orgID)
}

func (a scimHost) WriteAuditEntryBestEffort(ctx context.Context, actorID, action, targetType, targetID string, details any) error {
	return a.h.writeAuditEntryBestEffort(ctx, actorID, action, targetType, targetID, details)
}

func (scimHost) ValidateUserID(id string) error { return ValidateUserID(id) }
func (scimHost) ValidateOpaqueID(id, field string) error {
	return ValidateOpaqueID(id, field)
}
func (scimHost) IsValidEmail(s string) bool { return isValidEmail(s) }

// scimService builds the internal/scim service from this Handler's
// dependencies. The service is stateless, so constructing it per call
// is just bundling the same db/keycloak/logger the endpoints always
// used, plus the Host seam above.
func (h *Handler) scimService() *scim.Service {
	return scim.NewService(h.db, h.keycloak, h.logger, scimHost{h: h})
}

// SCIMBearerMiddleware returns middleware that enforces the legacy static
// SCIM Bearer [REDACTED] (see scim.BearerMiddleware).
func SCIMBearerMiddleware(token string) func(http.Handler) http.Handler {
	return scim.BearerMiddleware(token)
}

// SCIMOrgBearerMiddleware authenticates the org-scoped SCIM base path
// against the scim_bearer_tokens table (see scim.Service.OrgBearerMiddleware).
func (h *Handler) SCIMOrgBearerMiddleware(db scimOrgTokenDB) func(http.Handler) http.Handler {
	return h.scimService().OrgBearerMiddleware(db)
}

// SCIMListUsers handles GET /scim/v2/Users.
func (h *Handler) SCIMListUsers(w http.ResponseWriter, r *http.Request) {
	h.scimService().ListUsers(w, r)
}

// SCIMGetUser handles GET /scim/v2/Users/{id}.
func (h *Handler) SCIMGetUser(w http.ResponseWriter, r *http.Request) {
	h.scimService().GetUser(w, r)
}

// SCIMCreateUser handles POST /scim/v2/Users — maps to the onboard flow.
func (h *Handler) SCIMCreateUser(w http.ResponseWriter, r *http.Request) {
	h.scimService().CreateUser(w, r)
}

// SCIMPatchUser handles PATCH /scim/v2/Users/{id}.
func (h *Handler) SCIMPatchUser(w http.ResponseWriter, r *http.Request) {
	h.scimService().PatchUser(w, r)
}

// SCIMDeleteUser handles DELETE /scim/v2/Users/{id} — maps to the offboard flow.
func (h *Handler) SCIMDeleteUser(w http.ResponseWriter, r *http.Request) {
	h.scimService().DeleteUser(w, r)
}

// SCIMListGroups handles GET /scim/v2/Groups.
func (h *Handler) SCIMListGroups(w http.ResponseWriter, r *http.Request) {
	h.scimService().ListGroups(w, r)
}

// SCIMGetGroup handles GET /scim/v2/Groups/{id}.
func (h *Handler) SCIMGetGroup(w http.ResponseWriter, r *http.Request) {
	h.scimService().GetGroup(w, r)
}

// SCIMCreateGroup handles POST /scim/v2/Groups.
func (h *Handler) SCIMCreateGroup(w http.ResponseWriter, r *http.Request) {
	h.scimService().CreateGroup(w, r)
}

// SCIMPatchGroup handles PATCH /scim/v2/Groups/{id}.
func (h *Handler) SCIMPatchGroup(w http.ResponseWriter, r *http.Request) {
	h.scimService().PatchGroup(w, r)
}

// SCIMDeleteGroup handles DELETE /scim/v2/Groups/{id}.
func (h *Handler) SCIMDeleteGroup(w http.ResponseWriter, r *http.Request) {
	h.scimService().DeleteGroup(w, r)
}

// extractMemberValues extracts user IDs from a SCIM members PATCH value
// (re-exported from internal/scim for the group tests).
func extractMemberValues(v interface{}) []string {
	return scim.ExtractMemberValues(v)
}

// isNotFound checks if a pgx error is a not-found (ErrNoRows) condition.
// Kept alongside the SCIM shims for the handler tests; internal/scim has
// its own copy now that the endpoints live there.
//
// The check uses errors.Is against the sentinel error rather than matching
// on the error text: a substring match on "no rows" breaks on driver or
// locale changes and can misclassify an unrelated error as "not found".
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, pgx.ErrNoRows)
}
