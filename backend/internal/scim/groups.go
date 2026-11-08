package scim

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/orgscope"
)

// ---- SCIM Group types ----

// GroupMember is a SCIM Group member reference (RFC 7643 §4.2).
type GroupMember struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
	Ref     string `json:"$ref,omitempty"`
}

// Group is the SCIM Group resource (RFC 7643 §4.2).
type Group struct {
	Schemas     []string      `json:"schemas"`
	ID          string        `json:"id,omitempty"`
	ExternalID  string        `json:"externalId,omitempty"`
	DisplayName string        `json:"displayName"`
	Members     []GroupMember `json:"members"`
	Meta        Meta          `json:"meta,omitempty"`
}

// GroupListResponse is the SCIM ListResponse for Groups.
type GroupListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []Group  `json:"Resources"`
}

// groupFromKC converts a Keycloak group (with optional members) to a Group.
func groupFromKC(id, name string, members []GroupMember) Group {
	return Group{
		Schemas:     []string{SchemaGroup},
		ID:          id,
		DisplayName: name,
		Members:     members,
		Meta:        Meta{ResourceType: "Group"},
	}
}

// ListGroups handles GET /scim/v2/Groups
func (s *Service) ListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// C1: org-scoped read. Fail closed — no org context means no rows,
	// mirroring the User handlers (ListUsers) — never fall back to
	// listing the whole realm's groups.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	startIndex := 1
	if v := r.URL.Query().Get("startIndex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			startIndex = n
		}
	}
	// M7: honor + clamp count (max 1000), same bound as ListUsers —
	// previously this was ignored entirely and the whole realm was fetched.
	count := 100
	if v := r.URL.Query().Get("count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			count = n
		}
	}

	filterRaw := r.URL.Query().Get("filter")
	var displayNameFilter string
	if filterRaw != "" {
		// Logical expressions are rejected rather than silently ignored —
		// see ListUsers.
		f, err := ParseFlatFilterStrict(filterRaw)
		if err != nil {
			respondError(w, http.StatusBadRequest, "filter: logical expressions (and/or/not) are not supported", "invalidFilter")
			return
		}
		if f != nil && f.Op == "eq" && strings.EqualFold(f.Attr, "displayname") {
			displayNameFilter = f.Value
		}
	}

	// M7/C1: fetch only THIS org's groups — never the whole realm. The org
	// filter is pushed into the Keycloak query itself (ListGroupsByOrg,
	// with a defensive re-check on the way back). startIndex/count paging
	// and the optional displayName filter are then applied in-process
	// against that already-org-scoped set, exactly as before this fix, just
	// no longer over every tenant's groups. maxOrgGroups bounds the single
	// upstream call; a truly huge single org would need real Keycloak-side
	// cursoring, but that's a pagination-completeness concern, not the
	// security fix this endpoint needed.
	const maxOrgGroups = 5000
	kcGroups, err := s.keycloak.ListGroupsByOrg(ctx, oc.OrgID, 0, maxOrgGroups)
	if err != nil {
		s.logger.Error("scim list groups: keycloak failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	var groups []Group
	for _, g := range kcGroups {
		if g.ID == nil || g.Name == nil {
			continue
		}
		if displayNameFilter != "" && !strings.EqualFold(*g.Name, displayNameFilter) {
			continue
		}
		groups = append(groups, groupFromKC(*g.ID, *g.Name, []GroupMember{}))
	}
	if groups == nil {
		groups = []Group{}
	}

	// Pagination (SCIM uses 1-based startIndex).
	offset := startIndex - 1
	if offset < 0 {
		offset = 0
	}
	total := len(groups)
	if offset < len(groups) {
		groups = groups[offset:]
	} else {
		groups = []Group{}
	}
	if len(groups) > count {
		groups = groups[:count]
	}

	Respond(w, http.StatusOK, GroupListResponse{
		Schemas:      []string{SchemaListResponse},
		TotalResults: total,
		StartIndex:   startIndex,
		ItemsPerPage: len(groups),
		Resources:    groups,
	})
}

// GetGroup handles GET /scim/v2/Groups/{id}
func (s *Service) GetGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if err := s.host.ValidateOpaqueID(groupID, "id"); err != nil {
		respondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	ctx := r.Context()

	// C1: org-scoped read. Fail closed — no org context means "not found".
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	g, err := s.keycloak.GetGroupByID(ctx, groupID)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "group not found", "")
			return
		}
		s.logger.Error("scim get group: keycloak failed", zap.String("group_id", groupID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	if g.ID == nil || g.Name == nil {
		respondError(w, http.StatusInternalServerError, "incomplete group data", "")
		return
	}
	// C1: verify the group belongs to the caller's org — fail closed with
	// the SAME 404 as "doesn't exist" so a foreign-org group ID never
	// distinguishably leaks its existence.
	if keycloak.GroupOrgID(g) != oc.OrgID {
		respondError(w, http.StatusNotFound, "group not found", "")
		return
	}

	members, err := s.keycloak.ListGroupMembers(ctx, groupID)
	if err != nil {
		s.logger.Warn("scim get group: failed to list members", zap.String("group_id", groupID), zap.Error(err))
		members = nil
	}

	scimMembers := make([]GroupMember, 0, len(members))
	for _, m := range members {
		if m.ID == nil {
			continue
		}
		display := ""
		if m.Email != nil {
			display = *m.Email
		}
		scimMembers = append(scimMembers, GroupMember{Value: *m.ID, Display: display})
	}

	grp := groupFromKC(*g.ID, *g.Name, scimMembers)
	Respond(w, http.StatusOK, grp)
}

// scimUserInOrg reports whether userID belongs to orgID (per the users
// table), used by the SCIM Groups handlers (C1) to stop an org-scoped SCIM
// token from binding another tenant's user into one of its groups via a
// member add/replace/initial-members list. Fails closed: any error
// (including s.db being nil) is treated as "not verified" i.e. not in org.
func (s *Service) userInOrg(ctx context.Context, userID, orgID string) bool {
	ok, err := orgscope.CheckResourceInOrg(ctx, s.db, "users", "keycloak_user_id", userID, orgID)
	if err != nil {
		s.logger.Warn("scim: failed to verify member org membership; treating as not-in-org",
			zap.String("user_id", userID), zap.Error(err))
		return false
	}
	return ok
}

// CreateGroup handles POST /scim/v2/Groups
func (s *Service) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var req Group
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON", "invalidValue")
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		respondError(w, http.StatusBadRequest, "displayName is required", "invalidValue")
		return
	}
	ctx := r.Context()

	// C1: org-scoped write. Fail closed — no org context means no group.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	groupID, err := s.keycloak.CreateGroupWithOrgID(ctx, req.DisplayName, oc.OrgID)
	if err != nil {
		s.logger.Error("scim create group: keycloak failed", zap.String("name", req.DisplayName), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create group", "")
		return
	}

	// Add initial members if provided — but only ones that actually belong
	// to the caller's org (C1): otherwise a per-org SCIM token could bind
	// another tenant's user into a group it just created in this org.
	var addedMembers []GroupMember
	for _, m := range req.Members {
		if m.Value == "" {
			continue
		}
		if !s.userInOrg(ctx, m.Value, oc.OrgID) {
			s.logger.Warn("scim create group: skipped initial member outside caller's org",
				zap.String("group_id", groupID), zap.String("user_id", m.Value))
			continue
		}
		if err := s.keycloak.AddUserToGroup(ctx, m.Value, groupID); err != nil {
			s.logger.Warn("scim create group: failed to add initial member",
				zap.String("group_id", groupID), zap.String("user_id", m.Value), zap.Error(err))
			continue
		}
		addedMembers = append(addedMembers, m)
	}

	grp := groupFromKC(groupID, req.DisplayName, addedMembers)
	if grp.Members == nil {
		grp.Members = []GroupMember{}
	}
	Respond(w, http.StatusCreated, grp)
}

// scimFilterUserIDsInOrg returns the subset of uids that belong to orgID,
// logging (and dropping) any that don't. Used by every SCIM Groups PATCH
// path that adds/removes/replaces members (C1) so an org-scoped SCIM token
// can never bind — or unbind — a foreign tenant's user via group membership.
func (s *Service) filterUserIDsInOrg(ctx context.Context, groupID, orgID string, uids []string) []string {
	out := make([]string, 0, len(uids))
	for _, uid := range uids {
		if s.userInOrg(ctx, uid, orgID) {
			out = append(out, uid)
			continue
		}
		s.logger.Warn("scim patch group: skipped member outside caller's org",
			zap.String("group_id", groupID), zap.String("user_id", uid))
	}
	return out
}

// PatchGroup handles PATCH /scim/v2/Groups/{id}
// Supports: displayName replace, members add/remove.
func (s *Service) PatchGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if err := s.host.ValidateOpaqueID(groupID, "id"); err != nil {
		respondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}

	var patch PatchRequest
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON", "invalidValue")
		return
	}
	ctx := r.Context()

	// C1: org-scoped write. Fail closed — no org context means "not found".
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Verify group exists
	g, err := s.keycloak.GetGroupByID(ctx, groupID)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "group not found", "")
			return
		}
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	if g.ID == nil || g.Name == nil {
		respondError(w, http.StatusInternalServerError, "incomplete group data", "")
		return
	}
	// C1: verify the group belongs to the caller's org — fail closed with
	// the SAME 404 as "doesn't exist" so a foreign-org group ID never
	// distinguishably leaks its existence.
	if keycloak.GroupOrgID(g) != oc.OrgID {
		respondError(w, http.StatusNotFound, "group not found", "")
		return
	}

	displayName := *g.Name

	for _, op := range patch.Operations {
		opLow := strings.ToLower(op.Op)
		pathLow := strings.ToLower(op.Path)

		// Detect filter-qualified paths like "members[value eq \"user-id\"]".
		// If present, extract the target user ID from the inner filter.
		var filterQualifiedUserID string
		if strings.Contains(pathLow, "[") {
			base, inner := ParseFilterPath(op.Path)
			if base == "members" && inner != nil {
				// The inner filter should be something like: value eq "user-id"
				// We extract the value directly from the node.
				if inner.Op == OpEq && inner.Attr == "value" {
					filterQualifiedUserID = inner.Value
				}
				// Normalise the path so the switch below matches "members".
				pathLow = base
			}
		}

		switch {
		case (opLow == "replace" || opLow == "add") && (pathLow == "displayname" || pathLow == ""):
			// Replace displayName (direct or object)
			if pathLow == "displayname" {
				if s, ok := op.Value.(string); ok {
					displayName = strings.TrimSpace(s)
				}
			} else if m, ok := op.Value.(map[string]interface{}); ok {
				if v, ok := m["displayName"].(string); ok {
					displayName = strings.TrimSpace(v)
				}
			}

		case (opLow == "add") && pathLow == "members":
			if filterQualifiedUserID != "" {
				// Filter-qualified add: add a specific member by value filter.
				// C1: verify org membership before ever calling Keycloak.
				for _, uid := range s.filterUserIDsInOrg(ctx, groupID, oc.OrgID, []string{filterQualifiedUserID}) {
					if err := s.keycloak.AddUserToGroup(ctx, uid, groupID); err != nil {
						s.logger.Warn("scim patch group: filter-qualified add member failed",
							zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
					}
				}
			} else {
				// Add members: value is []{"value": userID}. C1: only add
				// members that belong to the caller's org.
				userIDs := s.filterUserIDsInOrg(ctx, groupID, oc.OrgID, ExtractMemberValues(op.Value))
				for _, uid := range userIDs {
					if err := s.keycloak.AddUserToGroup(ctx, uid, groupID); err != nil {
						s.logger.Warn("scim patch group: add member failed",
							zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
					}
				}
			}

		case (opLow == "remove") && pathLow == "members":
			if filterQualifiedUserID != "" {
				// Filter-qualified remove: remove a specific member. C1: only
				// act on a user that belongs to the caller's org.
				for _, uid := range s.filterUserIDsInOrg(ctx, groupID, oc.OrgID, []string{filterQualifiedUserID}) {
					if err := s.keycloak.RemoveUserFromGroup(ctx, uid, groupID); err != nil {
						s.logger.Warn("scim patch group: filter-qualified remove member failed",
							zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
					}
				}
			} else {
				// Remove members: value may be []{"value": userID} or empty
				// (remove all, not supported). C1: org-verify each target.
				userIDs := s.filterUserIDsInOrg(ctx, groupID, oc.OrgID, ExtractMemberValues(op.Value))
				for _, uid := range userIDs {
					if err := s.keycloak.RemoveUserFromGroup(ctx, uid, groupID); err != nil {
						s.logger.Warn("scim patch group: remove member failed",
							zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
					}
				}
			}

		case (opLow == "replace") && pathLow == "members":
			// Replace all members: first remove all, then add new set. The
			// removal side doesn't need an org check (it's un-binding
			// whatever is already there); the new set does (C1), same as add.
			//
			// The current member list MUST be read successfully: if the read
			// fails and the error is ignored, `existing` is empty, the
			// removal loop removes nothing and the new set is added on top —
			// the group ends up with the UNION of the old and new members
			// instead of the requested replacement.
			existing, listErr := s.keycloak.ListGroupMembers(ctx, groupID)
			if listErr != nil {
				s.logger.Error("scim patch group: failed to list current members; refusing to replace",
					zap.String("group_id", groupID), zap.Error(listErr))
				respondError(w, http.StatusInternalServerError, "failed to list current group members", "")
				return
			}
			for _, m := range existing {
				if m.ID == nil {
					continue
				}
				if err := s.keycloak.RemoveUserFromGroup(ctx, *m.ID, groupID); err != nil {
					s.logger.Warn("scim patch group: remove existing member failed",
						zap.String("group_id", groupID), zap.String("user_id", *m.ID), zap.Error(err))
				}
			}
			userIDs := s.filterUserIDsInOrg(ctx, groupID, oc.OrgID, ExtractMemberValues(op.Value))
			for _, uid := range userIDs {
				if err := s.keycloak.AddUserToGroup(ctx, uid, groupID); err != nil {
					s.logger.Warn("scim patch group: add replacement member failed",
						zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
				}
			}
		}
	}

	// Rename if displayName changed
	if displayName != *g.Name && displayName != "" {
		if err := s.keycloak.RenameGroup(ctx, groupID, displayName); err != nil {
			s.logger.Error("scim patch group: rename failed",
				zap.String("group_id", groupID), zap.String("name", displayName), zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to rename group", "")
			return
		}
	}

	// Return current state
	members, membersErr := s.keycloak.ListGroupMembers(ctx, groupID)
	if membersErr != nil {
		// Reporting an empty member list would tell the client the group
		// has no members when in fact the membership is unknown.
		s.logger.Error("scim patch group: failed to list members for the response",
			zap.String("group_id", groupID), zap.Error(membersErr))
		respondError(w, http.StatusInternalServerError, "failed to list group members", "")
		return
	}
	scimMembers := make([]GroupMember, 0, len(members))
	for _, m := range members {
		if m.ID == nil {
			continue
		}
		display := ""
		if m.Email != nil {
			display = *m.Email
		}
		scimMembers = append(scimMembers, GroupMember{Value: *m.ID, Display: display})
	}

	grp := groupFromKC(groupID, displayName, scimMembers)
	Respond(w, http.StatusOK, grp)
}

// DeleteGroup handles DELETE /scim/v2/Groups/{id}
func (s *Service) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if err := s.host.ValidateOpaqueID(groupID, "id"); err != nil {
		respondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	ctx := r.Context()

	// C1: org-scoped write. Fail closed — no org context means "not found".
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Verify existence and ownership
	g, err := s.keycloak.GetGroupByID(ctx, groupID)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "group not found", "")
			return
		}
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	// C1: verify the group belongs to the caller's org — fail closed with
	// the SAME 404 as "doesn't exist" so a foreign-org group ID never
	// distinguishably leaks its existence.
	if keycloak.GroupOrgID(g) != oc.OrgID {
		respondError(w, http.StatusNotFound, "group not found", "")
		return
	}

	if err := s.keycloak.DeleteGroup(ctx, groupID); err != nil {
		s.logger.Error("scim delete group: keycloak failed", zap.String("group_id", groupID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to delete group", "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ExtractMemberValues extracts user IDs from a SCIM members PATCH value.
// Handles: []interface{}{map{"value": uid}}, or a single map{"value": uid}.
func ExtractMemberValues(v interface{}) []string {
	var out []string
	switch arr := v.(type) {
	case []interface{}:
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if uid, ok := m["value"].(string); ok && uid != "" {
					out = append(out, uid)
				}
			}
		}
	case map[string]interface{}:
		if uid, ok := arr["value"].(string); ok && uid != "" {
			out = append(out, uid)
		}
	}
	return out
}
