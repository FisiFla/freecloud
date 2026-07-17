package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ApprovalRequest is a pending privileged action (onboard / offboard) waiting
// for super-admin sign-off before the actual Keycloak / Fleet work executes.
type ApprovalRequest struct {
	ID          string                 `json:"id"`
	ActionType  string                 `json:"actionType"`
	RequesterID string                 `json:"requesterId"`
	Payload     map[string]interface{} `json:"payload"`
	Status      string                 `json:"status"`
	DecidedBy   string                 `json:"decidedBy,omitempty"`
	DecidedAt   string                 `json:"decidedAt,omitempty"`
	CreatedAt   string                 `json:"createdAt,omitempty"`
}

// SubmitApprovalRequest is the JSON body for POST /api/v1/approval-requests.
type SubmitApprovalRequest struct {
	ActionType string                 `json:"actionType"`
	Payload    map[string]interface{} `json:"payload"`
}

// SubmitApproval creates a pending approval request. Called by helpdesk.
//
// Route: POST /api/v1/approval-requests
// Permission-gated via PermSubmitApprovals.
func (h *Handler) SubmitApproval(w http.ResponseWriter, r *http.Request) {
	actorID := middleware.GetActorID(r.Context())

	var req SubmitApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.ActionType = strings.TrimSpace(req.ActionType)
	if req.ActionType != "onboard" && req.ActionType != "offboard" {
		respondError(w, http.StatusBadRequest, "actionType must be 'onboard' or 'offboard'")
		return
	}
	if req.Payload == nil {
		respondError(w, http.StatusBadRequest, "payload is required")
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	var id string
	err = h.db.QueryRow(r.Context(),
		`INSERT INTO approval_requests (action_type, requester_id, payload, org_id)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		req.ActionType, actorID, payloadBytes, oc.OrgID,
	).Scan(&id)
	if err != nil {
		h.logger.Error("failed to insert approval request", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.writeAuditEntryBestEffort(actorID, "approval.submitted", "approval_request", id, map[string]interface{}{
		"action_type": req.ActionType,
	}); err != nil {
		h.logger.Warn("failed to write approval submission audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusCreated, map[string]string{"id": id, "status": "pending"})
}

// ListApprovalRequests returns pending (or all) approval requests for admin review.
//
// Route: GET /api/v1/approval-requests[?status=pending|approved|rejected]
// Permission-gated via PermApproveRequests.
func (h *Handler) ListApprovalRequests(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondJSON(w, http.StatusOK, []ApprovalRequest{})
		return
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	statusFilter := r.URL.Query().Get("status")
	if statusFilter == "" {
		statusFilter = "pending"
	}

	query := `SELECT id, action_type, requester_id, payload, status,
	                 COALESCE(decided_by, ''), COALESCE(decided_at::text, ''), created_at
	          FROM approval_requests WHERE org_id = $1`
	args := []interface{}{oc.OrgID}
	if statusFilter != "all" {
		query += ` AND status = $2`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY created_at DESC LIMIT 200`

	rows, err := h.db.Query(r.Context(), query, args...)
	if err != nil {
		h.logger.Error("failed to query approval requests", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var out []ApprovalRequest
	for rows.Next() {
		var a ApprovalRequest
		var payloadBytes []byte
		var createdAt time.Time
		if err := rows.Scan(&a.ID, &a.ActionType, &a.RequesterID, &payloadBytes,
			&a.Status, &a.DecidedBy, &a.DecidedAt, &createdAt); err != nil {
			h.logger.Error("scan approval request", zap.Error(err))
			continue
		}
		if len(payloadBytes) > 0 {
			_ = json.Unmarshal(payloadBytes, &a.Payload)
		}
		a.CreatedAt = createdAt.Format(time.RFC3339)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("iterate approval requests", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if out == nil {
		out = []ApprovalRequest{}
	}
	respondJSON(w, http.StatusOK, out)
}

// DecideApproval approves or rejects a pending request. On approval, the
// underlying onboard/offboard action is executed synchronously.
//
// Route: PATCH /api/v1/approval-requests/{id}
// Body: {"decision":"approved"|"rejected"}
// Permission-gated via PermApproveRequests.
func (h *Handler) DecideApproval(w http.ResponseWriter, r *http.Request) {
	approvalID := chi.URLParam(r, "id")
	if err := ValidateUserID(approvalID); err != nil {
		respondError(w, http.StatusBadRequest, "invalid id")
		return
	}

	actorID := middleware.GetActorID(r.Context())

	var body struct {
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Decision != "approved" && body.Decision != "rejected" {
		respondError(w, http.StatusBadRequest, "decision must be 'approved' or 'rejected'")
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	// Fetch and lock the approval request.
	var actionType, status, requesterID string
	var payloadBytes []byte
	err := h.db.QueryRow(r.Context(),
		`SELECT action_type, status, payload, requester_id FROM approval_requests WHERE id = $1 AND org_id = $2`,
		approvalID, oc.OrgID,
	).Scan(&actionType, &status, &payloadBytes, &requesterID)
	if err != nil {
		respondError(w, http.StatusNotFound, "approval request not found")
		return
	}
	if status != "pending" {
		respondError(w, http.StatusConflict, "approval request already decided")
		return
	}
	if actorID == requesterID {
		respondError(w, http.StatusForbidden, "cannot approve or reject your own request")
		return
	}

	if body.Decision == "rejected" {
		tag, err := h.db.Exec(r.Context(),
			`UPDATE approval_requests
			 SET status='rejected', decided_by=$1, decided_at=NOW()
			 WHERE id=$2 AND status='pending'`,
			actorID, approvalID,
		)
		if err != nil {
			h.logger.Error("failed to reject approval request", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if tag.RowsAffected() == 0 {
			respondError(w, http.StatusConflict, "approval request already decided")
			return
		}
		if err := h.writeAuditEntryBestEffort(actorID, "approval.rejected", "approval_request", approvalID, map[string]interface{}{
			"action_type": actionType,
		}); err != nil {
			h.logger.Warn("failed to write approval rejection audit log", zap.Error(err))
		}
		respondJSON(w, http.StatusOK, map[string]string{"id": approvalID, "status": "rejected"})
		return
	}

	tag, err := h.db.Exec(r.Context(),
		`UPDATE approval_requests
		 SET status='executing', decided_by=$1, decided_at=NOW()
		 WHERE id=$2 AND status='pending'`,
		actorID, approvalID,
	)
	if err != nil {
		h.logger.Error("failed to claim approval request", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		respondError(w, http.StatusConflict, "approval request already decided")
		return
	}

	// Execute the underlying action.
	var payload map[string]interface{}
	if len(payloadBytes) > 0 {
		_ = json.Unmarshal(payloadBytes, &payload)
	}

	if err := h.executeApprovedAction(r.Context(), actorID, actionType, approvalID, payload); err != nil {
		h.logger.Error("approved action execution failed",
			zap.String("approval_id", approvalID),
			zap.String("action_type", actionType),
			zap.Error(err),
		)
		resetCtx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
		defer cancel()
		if _, resetErr := h.db.Exec(resetCtx,
			`UPDATE approval_requests
			 SET status='pending', decided_by=NULL, decided_at=NULL
			 WHERE id=$1 AND status='executing'`,
			approvalID,
		); resetErr != nil {
			h.logger.Error("failed to reset approval request after execution failure",
				zap.String("approval_id", approvalID),
				zap.Error(resetErr),
			)
		}
		if auditErr := h.writeAuditEntryBestEffort(actorID, "approval.execution_failed", "approval_request", approvalID, map[string]interface{}{
			"action_type": actionType,
			"error":       err.Error(),
		}); auditErr != nil {
			h.logger.Warn("failed to write approval execution failure audit log", zap.Error(auditErr))
		}
		respondError(w, http.StatusInternalServerError, "action execution failed")
		return
	}

	tag, err = h.db.Exec(r.Context(),
		`UPDATE approval_requests
		 SET status='approved', decided_by=$1, decided_at=NOW()
		 WHERE id=$2 AND status='executing'`,
		actorID, approvalID,
	)
	if err != nil {
		h.logger.Error("failed to mark approval request approved", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		h.logger.Error("approval request changed state during execution", zap.String("approval_id", approvalID))
		respondError(w, http.StatusInternalServerError, "approval request changed state during execution")
		return
	}
	if err := h.writeAuditEntryBestEffort(actorID, "approval.approved", "approval_request", approvalID, map[string]interface{}{
		"action_type": actionType,
	}); err != nil {
		h.logger.Warn("failed to write approval approval audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]string{"id": approvalID, "status": "approved"})
}

// executeApprovedAction runs the underlying Keycloak/Fleet action for an
// approved request. actorID is the approver; the original requester is in
// the audit payload.
func (h *Handler) executeApprovedAction(ctx context.Context, approverID, actionType, approvalID string, payload map[string]interface{}) error {
	str := func(key string) string {
		if v, ok := payload[key].(string); ok {
			return v
		}
		return ""
	}

	switch actionType {
	case "onboard":
		req := OnboardRequest{
			FirstName:  str("firstName"),
			LastName:   str("lastName"),
			Email:      str("email"),
			Department: str("department"),
			Role:       str("role"),
		}
		if req.Email == "" {
			return fmt.Errorf("missing email in onboard payload")
		}

		kcResult, err := h.keycloak.CreateUser(ctx, req.FirstName, req.LastName, req.Email, req.Department)
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if kcResult.User == nil || kcResult.User.ID == nil {
			return fmt.Errorf("keycloak returned no user id")
		}
		kcUserID := *kcResult.User.ID

		enrollmentToken := ""
		if tok, err := h.fleet.CreateEnrollmentToken(ctx); err == nil {
			enrollmentToken = tok
		}

		if h.db != nil {
			auditDetails := map[string]interface{}{
				"email": req.Email, "approval_id": approvalID,
			}
			// C2: the approver's currently-resolved org owns the onboarded user.
			// Fail closed rather than defaulting silently.
			oc := middleware.GetOrgContext(ctx)
			if oc == nil {
				return fmt.Errorf("no organization context for approval execution")
			}
			if err := h.persistOnboard(ctx, kcUserID, req, approverID, auditDetails, enrollmentToken, oc.OrgID); err != nil {
				return fmt.Errorf("persist onboard: %w", err)
			}
		}
		return nil

	case "offboard":
		userID := str("userId")
		if err := ValidateUserID(userID); err != nil {
			return fmt.Errorf("invalid userId in offboard payload: %w", err)
		}
		// C2: the approval-request payload carries a caller-supplied userId —
		// verify it belongs to the approver's own org before touching Keycloak
		// or the DB. Without this, an org-B approver could offboard an org-A
		// user by submitting a crafted approval request naming their ID.
		oc := middleware.GetOrgContext(ctx)
		if oc == nil {
			return fmt.Errorf("no organization context for approval execution")
		}
		if h.db == nil {
			return fmt.Errorf("database not available")
		}
		var found string
		if err := h.db.QueryRow(ctx,
			`SELECT keycloak_user_id::TEXT FROM users WHERE keycloak_user_id = $1 AND org_id = $2`,
			userID, oc.OrgID,
		).Scan(&found); err != nil {
			return fmt.Errorf("offboard target not found in caller's organization")
		}
		if err := h.keycloak.DisableUser(ctx, userID); err != nil {
			return fmt.Errorf("disable user: %w", err)
		}
		_ = h.keycloak.LogoutAllSessions(ctx, userID)
		_, _ = h.db.Exec(ctx,
			`UPDATE users SET disabled = true, updated_at = NOW() WHERE keycloak_user_id = $1`,
			userID,
		)
		if err := h.writeAuditEntryBestEffort(approverID, "offboard", "user", userID, map[string]interface{}{
			"approval_id": approvalID,
		}); err != nil {
			h.logger.Warn("failed to write approved offboard audit log", zap.Error(err))
		}
		return nil

	default:
		return fmt.Errorf("unknown action_type %q", actionType)
	}
}
