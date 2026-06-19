package handlers

// A3 — Keycloak group & role management.
//
// Admin-gated endpoints (auto-covered by isManagementEndpoint via /api/v1/groups/ prefix).
// All writes are audited via a detached context.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Nerzal/gocloak/v13"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

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

// ListGroups returns all realm groups.
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	groups, err := h.keycloak.ListGroups(ctx)
	if err != nil {
		h.logger.Error("failed to list groups", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to list groups")
		return
	}
	out := make([]GroupResponse, 0, len(groups))
	for _, g := range groups {
		if g.ID == nil || g.Name == nil {
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

// CreateGroup creates a new realm group.
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
	groupID, err := h.keycloak.CreateGroup(ctx, req.Name)
	if err != nil {
		h.logger.Error("failed to create group", zap.String("name", req.Name), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create group")
		return
	}

	actorID := middleware.GetActorID(ctx)
	if h.db != nil {
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.db.Exec(auditCtx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "group_create", "group", groupID,
			map[string]interface{}{"name": req.Name})
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
	if userID == "" || !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "valid user id is required")
		return
	}

	var req AssignUserToGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.GroupID = strings.TrimSpace(req.GroupID)
	if req.GroupID == "" {
		respondError(w, http.StatusBadRequest, "groupId is required")
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
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.db.Exec(auditCtx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "user_group_assign", "user", userID,
			map[string]interface{}{"group_id": req.GroupID})
	}

	respondJSON(w, http.StatusOK, map[string]bool{"assigned": true})
}

// UnassignUserFromGroup removes a user from a group.
func (h *Handler) UnassignUserFromGroup(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	groupID := chi.URLParam(r, "groupId")
	if userID == "" || !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "valid user id is required")
		return
	}
	if groupID == "" {
		respondError(w, http.StatusBadRequest, "groupId is required")
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
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.db.Exec(auditCtx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "user_group_unassign", "user", userID,
			map[string]interface{}{"group_id": groupID})
	}

	respondJSON(w, http.StatusOK, map[string]bool{"unassigned": true})
}

// ListRealmRoles returns all realm-level roles.
func (h *Handler) ListRealmRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	roles, err := h.keycloak.ListRealmRoles(ctx)
	if err != nil {
		h.logger.Error("failed to list realm roles", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to list realm roles")
		return
	}
	out := make([]RealmRoleResponse, 0, len(roles))
	for _, role := range roles {
		if role.ID == nil || role.Name == nil {
			continue
		}
		out = append(out, RealmRoleResponse{ID: *role.ID, Name: *role.Name})
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
	if userID == "" || !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "valid user id is required")
		return
	}

	var req AssignRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.RoleID = strings.TrimSpace(req.RoleID)
	req.RoleName = strings.TrimSpace(req.RoleName)
	if req.RoleID == "" || req.RoleName == "" {
		respondError(w, http.StatusBadRequest, "roleId and roleName are required")
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
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.db.Exec(auditCtx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "user_role_assign", "user", userID,
			map[string]interface{}{"role_id": req.RoleID, "role_name": req.RoleName})
	}

	respondJSON(w, http.StatusOK, map[string]bool{"assigned": true})
}
