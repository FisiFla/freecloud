package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
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

// ListTeams returns Fleet teams visible to the caller.
// Route: GET /api/v1/teams (requires PermReadCompliance).
//
// System admins see every Fleet team. Other callers see only teams mapped to
// their active org in fleet_team_orgs (Migration046). Unmapped legacy teams
// remain invisible to non–system-admins (fail-closed).
func (h *Handler) ListTeams(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	teams, err := h.fleet.ListTeams(ctx)
	if err != nil {
		h.logger.Error("failed to list fleet teams", zap.Error(err))
		respondError(w, http.StatusBadGateway, "failed to retrieve teams from Fleet")
		return
	}
	if !isSystemAdminCaller(ctx) {
		oc := middleware.GetOrgContext(ctx)
		if oc == nil {
			// Fail closed: never return unscoped Fleet inventory without org.
			respondError(w, http.StatusForbidden, "forbidden: no organization context")
			return
		}
		if h.db == nil {
			respondError(w, http.StatusInternalServerError, "database not available")
			return
		}
		allowed, mapErr := h.fleetTeamIDsForOrg(ctx, oc.OrgID)
		if mapErr != nil {
			h.logger.Error("failed to load org fleet teams", zap.Error(mapErr))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		filtered := make([]fleet.Team, 0, len(teams))
		for _, tm := range teams {
			if allowed[tm.ID] {
				filtered = append(filtered, tm)
			}
		}
		teams = filtered
	}
	// Stable order for clients/tests regardless of Fleet API ordering.
	sort.Slice(teams, func(i, j int) bool { return teams[i].ID < teams[j].ID })

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
	if len(req.Name) > 120 {
		respondError(w, http.StatusBadRequest, "name must be ≤ 120 characters")
		return
	}
	// Reject path separators / control chars so clients cannot inject a fake
	// org UUID prefix or multi-segment Fleet names that break tenant namespacing.
	if err := ValidateFleetTeamDisplayName(req.Name); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	if err := ValidateTeamDescription(req.Description); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Description) > 500 {
		respondError(w, http.StatusBadRequest, "description must be ≤ 500 characters")
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	// Namespace the Fleet team name with the org id prefix so shared-Fleet
	// deployments do not collide across tenants on display name uniqueness.
	// Display name never contains '/'; prefix is always server-controlled.
	fleetName := oc.OrgID + "/" + req.Name

	team, err := h.fleet.CreateTeam(ctx, fleetName, req.Description)
	if err != nil {
		h.logger.Error("failed to create fleet team", zap.String("name", fleetName), zap.Error(err))
		respondError(w, http.StatusBadGateway, "failed to create team in Fleet")
		return
	}

	if h.db != nil {
		if _, err := h.db.Exec(ctx,
			`INSERT INTO fleet_team_orgs (fleet_team_id, org_id, team_name)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (fleet_team_id) DO UPDATE
			   SET org_id = EXCLUDED.org_id, team_name = EXCLUDED.team_name`,
			team.ID, oc.OrgID, team.Name,
		); err != nil {
			h.logger.Error("failed to record fleet team org mapping", zap.Error(err))
			// Best-effort: team exists in Fleet; surface error so caller retries
			// rather than leaving an unmapped team silently.
			respondError(w, http.StatusInternalServerError, "team created in Fleet but org mapping failed")
			return
		}
		if err := h.writeAuditEntryBestEffort(actorID, "fleet_team_create", "team", team.Name, map[string]interface{}{
			"team_name": team.Name,
			"team_id":   team.ID,
			"org_id":    oc.OrgID,
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
	if !h.requireFleetTeamInCallerOrg(w, r, teamID) {
		return
	}

	var req AssignTeamPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.PolicyID = strings.TrimSpace(req.PolicyID)
	if err := ValidatePolicyID(req.PolicyID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
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
	if !h.requireFleetTeamInCallerOrg(w, r, teamID) {
		return
	}

	var req MoveHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Trim empties so ["", "host-1"] cannot bypass emptiness checks.
	cleaned := make([]string, 0, len(req.HostIDs))
	for _, id := range req.HostIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if err := ValidateHostID(id); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		cleaned = append(cleaned, id)
	}
	req.HostIDs = cleaned
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

// fleetTeamIDsForOrg returns the set of Fleet team IDs owned by orgID.
func (h *Handler) fleetTeamIDsForOrg(ctx context.Context, orgID string) (map[int]bool, error) {
	out := map[int]bool{}
	if h.db == nil {
		return out, nil
	}
	rows, err := h.db.Query(ctx, `SELECT fleet_team_id FROM fleet_team_orgs WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// requireFleetTeamInCallerOrg gates team-scoped mutations. System admins may
// act on any team (including unmapped legacy teams). Everyone else must have a
// fleet_team_orgs row for the active org — 404 on miss (non-leakage).
func (h *Handler) requireFleetTeamInCallerOrg(w http.ResponseWriter, r *http.Request, teamID int) bool {
	if isSystemAdminCaller(r.Context()) {
		return true
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return false
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return false
	}
	var found int
	err := h.db.QueryRow(r.Context(),
		`SELECT 1 FROM fleet_team_orgs WHERE fleet_team_id = $1 AND org_id = $2`,
		teamID, oc.OrgID,
	).Scan(&found)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "team not found")
			return false
		}
		h.logger.Error("fleet team org check failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return false
	}
	return true
}

// ValidateFleetTeamDisplayName rejects separators that would break the
// server-controlled "{orgID}/" namespace or allow multi-segment injection.
func ValidateFleetTeamDisplayName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("name must not contain path separators")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("name must not contain '..'")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("name must not contain control characters")
		}
	}
	return nil
}
