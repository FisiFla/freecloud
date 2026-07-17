package handlers

// A3 — Keycloak group & role management.
//
// Group and role endpoints are permission-gated in routes.go.
// All writes are audited via a detached context.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Nerzal/gocloak/v13"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ---- response types ----

// GroupResponse is the JSON representation of a Keycloak group.
type GroupResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RealmRoleResponse is the JSON representation of a Keycloak realm role.
type RealmRoleResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ---- handlers ----

// ListGroups returns realm groups. M1: a system-admin sees every group
// (unchanged legacy behavior); anyone else — helpdesk/auditor/read-only,
// who all hold PermReadGroups too — only sees groups tagged (via the org_id
// attribute, see keycloak.GroupOrgAttribute / C1) as belonging to their own
// org. A group with no org_id attribute (e.g. one created before this fix,
// or a bootstrap system group) is fail-closed: invisible to non-admins.
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	groups, err := h.keycloak.ListGroups(ctx)
	if err != nil {
		h.logger.Error("failed to list groups", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to list groups")
		return
	}

	isAdmin := isSystemAdminCaller(ctx)
	var callerOrgID string
	if !isAdmin {
		if oc := middleware.GetOrgContext(ctx); oc != nil {
			callerOrgID = oc.OrgID
		}
	}

	out := make([]GroupResponse, 0, len(groups))
	for _, g := range groups {
		if g.ID == nil || g.Name == nil {
			continue
		}
		if !isAdmin && (callerOrgID == "" || keycloak.GroupOrgID(g) != callerOrgID) {
			continue
		}
		out = append(out, GroupResponse{ID: *g.ID, Name: *g.Name})
	}
	respondJSON(w, http.StatusOK, out)
}

// CreateGroupRequest is the JSON request body for creating a group.
type CreateGroupRequest struct {
	Name string `json:"name"`
}

// CreateGroup creates a new realm group, tagged (M1/C1) with the caller's
// active org so it is visible to that org's non-system-admin roles under
// ListGroups' filter above. Fail closed: no resolvable org context means no
// group, same as every other org-tagged write in this codebase.
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var req CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 100 {
		respondError(w, http.StatusBadRequest, "name must be ≤ 100 characters")
		return
	}

	ctx := r.Context()
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	groupID, err := h.keycloak.CreateGroupWithOrgID(ctx, req.Name, oc.OrgID)
	if err != nil {
		h.logger.Error("failed to create group", zap.String("name", req.Name), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create group")
		return
	}

	actorID := middleware.GetActorID(ctx)
	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "group_create", "group", groupID, map[string]interface{}{
			"name": req.Name,
		}); err != nil {
			h.logger.Warn("failed to write group create audit log", zap.Error(err))
		}
	}

	respondJSON(w, http.StatusCreated, GroupResponse{ID: groupID, Name: req.Name})
}

// AssignUserToGroupRequest is the JSON body for group assignment.
type AssignUserToGroupRequest struct {
	GroupID string `json:"groupId"`
}

// AssignUserToGroup adds a user to a group.
func (h *Handler) AssignUserToGroup(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if err := ValidateUserID(userID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireUserInCallerOrg(w, r, userID) {
		return
	}

	var req AssignUserToGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.GroupID = strings.TrimSpace(req.GroupID)
	if err := ValidateOpaqueID(req.GroupID, "groupId"); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	if err := h.keycloak.AddUserToGroup(ctx, userID, req.GroupID); err != nil {
		h.logger.Error("failed to add user to group", zap.String("user_id", userID), zap.String("group_id", req.GroupID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to assign user to group")
		return
	}

	actorID := middleware.GetActorID(ctx)
	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "user_group_assign", "user", userID, map[string]interface{}{
			"group_id": req.GroupID,
		}); err != nil {
			h.logger.Warn("failed to write group assignment audit log", zap.Error(err))
		}
	}

	// A3: Sync group membership to provisioning-enabled apps (best-effort).
	if h.provisionEngine != nil {
		capturedUserID := userID
		capturedCtx := ctx
		go h.triggerGroupSyncForUser(capturedCtx, capturedUserID)
	}

	respondJSON(w, http.StatusOK, map[string]bool{"assigned": true})
}

// UnassignUserFromGroup removes a user from a group.
func (h *Handler) UnassignUserFromGroup(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	groupID := chi.URLParam(r, "groupId")
	if err := ValidateUserID(userID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateOpaqueID(groupID, "groupId"); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireUserInCallerOrg(w, r, userID) {
		return
	}

	ctx := r.Context()
	if err := h.keycloak.RemoveUserFromGroup(ctx, userID, groupID); err != nil {
		h.logger.Error("failed to remove user from group", zap.String("user_id", userID), zap.String("group_id", groupID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to remove user from group")
		return
	}

	actorID := middleware.GetActorID(ctx)
	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "user_group_unassign", "user", userID, map[string]interface{}{
			"group_id": groupID,
		}); err != nil {
			h.logger.Warn("failed to write group unassignment audit log", zap.Error(err))
		}
	}

	// A3: Sync group membership to provisioning-enabled apps (best-effort).
	if h.provisionEngine != nil {
		capturedUserID := userID
		capturedCtx := ctx
		go h.triggerGroupSyncForUser(capturedCtx, capturedUserID)
	}

	respondJSON(w, http.StatusOK, map[string]bool{"unassigned": true})
}

// triggerGroupSyncForUser fetches the user's current Keycloak groups and pushes
// membership to all provisioning-enabled apps for that user.
func (h *Handler) triggerGroupSyncForUser(ctx context.Context, userID string) {
	kcGroups, err := h.keycloak.GetUserGroups(ctx, userID)
	if err != nil {
		h.logger.Warn("triggerGroupSync: failed to get keycloak groups",
			zap.String("user_id", userID), zap.Error(err))
		return
	}

	var groupNames []string
	for _, g := range kcGroups {
		if g.Name != nil {
			groupNames = append(groupNames, *g.Name)
		}
	}

	if h.db == nil {
		return
	}
	rows, err := h.db.Query(ctx,
		`SELECT app_id::TEXT FROM provisioning_state WHERE user_id = $1 AND status = 'provisioned'`,
		userID,
	)
	if err != nil {
		h.logger.Warn("triggerGroupSync: query provisioned apps failed", zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var appID string
		if err := rows.Scan(&appID); err != nil {
			continue
		}
		if err := h.provisionEngine.SyncGroupMembership(ctx, appID, userID, groupNames); err != nil {
			h.logger.Warn("triggerGroupSync: sync failed",
				zap.String("app_id", appID), zap.String("user_id", userID), zap.Error(err))
		}
	}
}

// ListRealmRoles returns all realm-level roles. M1: realm roles have no org
// concept (they're global RBAC definitions, not tenant data), so unlike
// Groups this isn't a per-org filter — it's a straight system-admin-only
// restriction. Every other caller holding PermReadGroups (helpdesk,
// auditor, read-only) gets an empty list rather than every tenant's role
// inventory.
func (h *Handler) ListRealmRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	roles, err := h.keycloak.ListRealmRoles(ctx)
	if err != nil {
		h.logger.Error("failed to list realm roles", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to list realm roles")
		return
	}
	out := make([]RealmRoleResponse, 0, len(roles))
	if isSystemAdminCaller(ctx) {
		for _, role := range roles {
			if role.ID == nil || role.Name == nil {
				continue
			}
			out = append(out, RealmRoleResponse{ID: *role.ID, Name: *role.Name})
		}
	}
	respondJSON(w, http.StatusOK, out)
}

// AssignRoleRequest is the JSON body for role assignment.
type AssignRoleRequest struct {
	RoleID   string `json:"roleId"`
	RoleName string `json:"roleName"`
}

// AssignRealmRoleToUser assigns a realm role to a user.
func (h *Handler) AssignRealmRoleToUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if err := ValidateUserID(userID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req AssignRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.RoleID = strings.TrimSpace(req.RoleID)
	req.RoleName = strings.TrimSpace(req.RoleName)
	if err := ValidateOpaqueID(req.RoleID, "roleId"); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateOpaqueID(req.RoleName, "roleName"); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireUserInCallerOrg(w, r, userID) {
		return
	}

	ctx := r.Context()
	roles := []gocloak.Role{{ID: &req.RoleID, Name: &req.RoleName}}
	if err := h.keycloak.AssignRealmRoleToUser(ctx, userID, roles); err != nil {
		h.logger.Error("failed to assign realm role to user",
			zap.String("user_id", userID), zap.String("role_id", req.RoleID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to assign role to user")
		return
	}

	actorID := middleware.GetActorID(ctx)
	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "user_role_assign", "user", userID, map[string]interface{}{
			"role_id":   req.RoleID,
			"role_name": req.RoleName,
		}); err != nil {
			h.logger.Warn("failed to write role assignment audit log", zap.Error(err))
		}
	}

	respondJSON(w, http.StatusOK, map[string]bool{"assigned": true})
}
