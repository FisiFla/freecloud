package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ListPoliciesResponse wraps the policy list returned to the frontend.
type ListPoliciesResponse struct {
	Policies []fleet.Policy `json:"policies"`
}

// AssignPolicyRequest is the JSON request body for policy assignment.
type AssignPolicyRequest struct {
	PolicyID string `json:"policyId"`
}

// AssignPolicyResponse is the JSON response for policy assignment.
type AssignPolicyResponse struct {
	DeviceID string `json:"deviceId"`
	PolicyID string `json:"policyId"`
	Assigned bool   `json:"assigned"`
}

// ListPolicies returns all policies from FleetDM.
// Route: GET /api/v1/policies (admin-gated).
func (h *Handler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	policies, err := h.fleet.ListPolicies(ctx)
	if err != nil {
		h.logger.Error("failed to list fleet policies", zap.Error(err))
		respondError(w, http.StatusBadGateway, "failed to retrieve policies from Fleet")
		return
	}

	respondJSON(w, http.StatusOK, ListPoliciesResponse{Policies: policies})
}

// AssignDevicePolicy assigns a policy to a device via FleetDM.
// Route: POST /api/v1/devices/{id}/policies (admin-gated).
//
// Note: the FleetDM REST API does not have a direct host→policy assignment
// endpoint (policies are team-scoped); AssignPolicyToHost on the fleet client
// is marked e2e-pending against a live Fleet stack. The handler and client are
// complete; integration will be verified once a live Fleet environment is
// available.
func (h *Handler) AssignDevicePolicy(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	if deviceID == "" {
		respondError(w, http.StatusBadRequest, "device id is required")
		return
	}

	var req AssignPolicyRequest
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

	h.logger.Info("assigning policy to device",
		zap.String("device_id", deviceID),
		zap.String("policy_id", req.PolicyID),
		zap.String("actor_id", actorID),
	)

	if err := h.fleet.AssignPolicyToHost(ctx, deviceID, req.PolicyID); err != nil {
		h.logger.Error("failed to assign policy to device",
			zap.String("device_id", deviceID),
			zap.String("policy_id", req.PolicyID),
			zap.Error(err),
		)
		respondError(w, http.StatusBadGateway, "failed to assign policy via Fleet")
		return
	}

	// Detached audit write.
	if h.db != nil {
		details, _ := json.Marshal(map[string]interface{}{
			"device_id": deviceID,
			"policy_id": req.PolicyID,
		})
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := h.db.Exec(auditCtx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "device_policy_assign", "device", deviceID, details,
		); err != nil {
			h.logger.Warn("failed to write audit log for policy assignment", zap.Error(err))
		}
	}

	respondJSON(w, http.StatusOK, AssignPolicyResponse{
		DeviceID: deviceID,
		PolicyID: req.PolicyID,
		Assigned: true,
	})
}
