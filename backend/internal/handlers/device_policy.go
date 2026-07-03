package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ListPoliciesResponse wraps the policy list returned to the frontend.
type ListPoliciesResponse struct {
	Policies []fleet.Policy `json:"policies"`
}

// ListTeamsResponse wraps the team list returned to the frontend.
type ListTeamsResponse struct {
	Teams []fleet.Team `json:"teams"`
}

// CreateTeamRequest is the JSON body for team creation.
type CreateTeamRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AssignTeamPolicyRequest carries the policy ID to assign to a team.
type AssignTeamPolicyRequest struct {
	PolicyID string `json:"policyId"`
}

// MoveHostRequest carries the host IDs to move to a team.
type MoveHostRequest struct {
	HostIDs []string `json:"hostIds"`
}

// AssignPolicyResponse is the JSON response for policy assignment.
type AssignPolicyResponse struct {
	TeamID   int    `json:"teamId"`
	PolicyID string `json:"policyId"`
	Assigned bool   `json:"assigned"`
}

// ListPolicies returns all global policies from FleetDM.
// Route: GET /api/v1/policies (requires PermReadCompliance).
//
// M1: Fleet policies have no org concept (no per-org Fleet team scoping
// exists yet — see ListTeams below), so unlike org-scoped resources this
// isn't a per-org filter, it's a straight system-admin-only restriction.
// Every other caller holding PermReadCompliance (helpdesk, auditor,
// read-only) gets an empty list instead of every tenant's compliance
// policy inventory. Fleet is still queried first so an upstream failure
// surfaces the same way regardless of role.
func (h *Handler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	policies, err := h.fleet.ListPolicies(ctx)
	if err != nil {
		h.logger.Error("failed to list fleet policies", zap.Error(err))
		respondError(w, http.StatusBadGateway, "failed to retrieve policies from Fleet")
		return
	}
	if !isSystemAdminCaller(ctx) {
		policies = []fleet.Policy{}
	}

	respondJSON(w, http.StatusOK, ListPoliciesResponse{Policies: policies})
}

// ListTeams returns all Fleet teams.
// Route: GET /api/v1/teams (requires PermReadCompliance).
//
// M1: Fleet teams have no per-org scoping today (future work — see the
// package-level TODO note near MoveHostToTeam), so this is a
// system-admin-only restriction, same rationale as ListPolicies above.
func (h *Handler) ListTeams(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	teams, err := h.fleet.ListTeams(ctx)
	if err != nil {
		h.logger.Error("failed to list fleet teams", zap.Error(err))
		respondError(w, http.StatusBadGateway, "failed to retrieve teams from Fleet")
		return
	}
	if !isSystemAdminCaller(ctx) {
		teams = []fleet.Team{}
	}

	respondJSON(w, http.StatusOK, ListTeamsResponse{Teams: teams})
}

// CreateTeam creates a new Fleet team.
// Route: POST /api/v1/teams (requires PermManagePolicies, audited).
func (h *Handler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	var req CreateTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	team, err := h.fleet.CreateTeam(ctx, req.Name, req.Description)
	if err != nil {
		h.logger.Error("failed to create fleet team", zap.String("name", req.Name), zap.Error(err))
		respondError(w, http.StatusBadGateway, "failed to create team in Fleet")
		return
	}

	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "fleet_team_create", "team", team.Name, map[string]interface{}{
			"team_name": team.Name,
			"team_id":   team.ID,
		}); err != nil {
			h.logger.Warn("failed to write audit log for team creation", zap.Error(err))
		}
	}

	respondJSON(w, http.StatusCreated, team)
}

// AssignTeamPolicy assigns a global policy to a Fleet team.
// Route: POST /api/v1/teams/{id}/policies (requires PermManagePolicies, audited).
func (h *Handler) AssignTeamPolicy(w http.ResponseWriter, r *http.Request) {
	teamIDStr := chi.URLParam(r, "id")
	if teamIDStr == "" {
		respondError(w, http.StatusBadRequest, "team id is required")
		return
	}

	var teamID int
	if _, err := parseIntParam(teamIDStr, &teamID); err != nil {
		respondError(w, http.StatusBadRequest, "team id must be a positive integer")
		return
	}

	var req AssignTeamPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.PolicyID = strings.TrimSpace(req.PolicyID)
	if req.PolicyID == "" {
		respondError(w, http.StatusBadRequest, "policyId is required")
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	if err := h.fleet.AssignPolicyToTeam(ctx, teamID, req.PolicyID); err != nil {
		h.logger.Error("failed to assign policy to team",
			zap.Int("team_id", teamID),
			zap.String("policy_id", req.PolicyID),
			zap.Error(err),
		)
		respondError(w, http.StatusBadGateway, "failed to assign policy via Fleet")
		return
	}

	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "fleet_team_policy_assign", "team", teamIDStr, map[string]interface{}{
			"team_id":   teamID,
			"policy_id": req.PolicyID,
		}); err != nil {
			h.logger.Warn("failed to write audit log for team policy assignment", zap.Error(err))
		}
	}

	respondJSON(w, http.StatusOK, AssignPolicyResponse{
		TeamID:   teamID,
		PolicyID: req.PolicyID,
		Assigned: true,
	})
}

// MoveHostToTeam moves one or more hosts to a Fleet team (host→team→policy chain).
// Route: POST /api/v1/teams/{id}/hosts (requires PermManageDevices, audited).
func (h *Handler) MoveHostToTeam(w http.ResponseWriter, r *http.Request) {
	teamIDStr := chi.URLParam(r, "id")
	if teamIDStr == "" {
		respondError(w, http.StatusBadRequest, "team id is required")
		return
	}

	var teamID int
	if _, err := parseIntParam(teamIDStr, &teamID); err != nil {
		respondError(w, http.StatusBadRequest, "team id must be a positive integer")
		return
	}

	var req MoveHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.HostIDs) == 0 {
		respondError(w, http.StatusBadRequest, "hostIds must not be empty")
		return
	}
	if len(req.HostIDs) > 500 {
		respondError(w, http.StatusBadRequest, "too many host IDs (max 500)")
		return
	}

	// H4: every other device-scoped handler in this codebase verifies
	// ownership via requireDeviceInCallerOrg before acting; this one moved
	// hosts to a Fleet team with zero check, so an org-B caller who merely
	// knew (or guessed/enumerated) an org-A host ID could reassign its
	// policy. Reject the whole batch on the first foreign/unknown host —
	// requireDeviceInCallerOrg has already written the 403/404 response.
	for _, id := range req.HostIDs {
		if !h.requireDeviceInCallerOrg(w, r, id) {
			return
		}
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	if err := h.fleet.MoveHostToTeam(ctx, teamID, req.HostIDs); err != nil {
		h.logger.Error("failed to move hosts to team",
			zap.Int("team_id", teamID),
			zap.Strings("host_ids", req.HostIDs),
			zap.Error(err),
		)
		respondError(w, http.StatusBadGateway, "failed to move hosts via Fleet")
		return
	}

	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "fleet_host_move_team", "team", teamIDStr, map[string]interface{}{
			"team_id":  teamID,
			"host_ids": req.HostIDs,
		}); err != nil {
			h.logger.Warn("failed to write audit log for host move", zap.Error(err))
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"teamId": teamID,
		"moved":  len(req.HostIDs),
	})
}

// parseIntParam parses a string to a positive int. Returns an error if invalid.
func parseIntParam(s string, out *int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a positive integer")
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	*out = n
	return n, nil
}
