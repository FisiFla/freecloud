package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ReviewCampaign represents an access review campaign.
type ReviewCampaign struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	CreatedBy string  `json:"createdBy"`
	CreatedAt string  `json:"createdAt"`
	ClosedAt  *string `json:"closedAt,omitempty"`
}

// ReviewItem represents a single user-resource access record in a campaign.
type ReviewItem struct {
	ID           string  `json:"id"`
	CampaignID   string  `json:"campaignId"`
	UserID       string  `json:"userId"`
	ResourceType string  `json:"resourceType"`
	ResourceID   string  `json:"resourceId"`
	ResourceName string  `json:"resourceName"`
	Decision     *string `json:"decision,omitempty"`
	DecidedBy    *string `json:"decidedBy,omitempty"`
	DecidedAt    *string `json:"decidedAt,omitempty"`
	CreatedAt    string  `json:"createdAt"`
}

// CreateCampaignRequest is the body for POST /api/v1/campaigns.
type CreateCampaignRequest struct {
	Name string `json:"name"`
}

// DecideRequest is the body for POST /api/v1/campaigns/{id}/items/{itemId}/decide.
type DecideRequest struct {
	Decision string `json:"decision"` // "confirm" or "revoke"
}

// CreateCampaign creates a new access review campaign and snapshots current app assignments.
// Route: POST /api/v1/campaigns (requires PermManageCampaigns via middleware)
func (h *Handler) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	var req CreateCampaignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 200 {
		respondError(w, http.StatusBadRequest, "name must be ≤ 200 characters")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin campaign transaction", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create campaign")
		return
	}
	defer tx.Rollback(ctx)

	var campaignID string
	var createdAt time.Time
	err = tx.QueryRow(ctx,
		`INSERT INTO review_campaigns (name, created_by) VALUES ($1, $2) RETURNING id, created_at`,
		req.Name, actorID,
	).Scan(&campaignID, &createdAt)
	if err != nil {
		h.logger.Error("failed to create campaign", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create campaign")
		return
	}

	// Snapshot current app assignments into review items.
	_, snapErr := tx.Exec(ctx,
		`INSERT INTO review_items (campaign_id, user_id, resource_type, resource_id, resource_name)
		 SELECT $1, aa.user_id, 'app', aa.app_id::text, ca.name
		 FROM app_assignments aa
		 JOIN connected_apps ca ON ca.id = aa.app_id`,
		campaignID,
	)
	if snapErr != nil {
		h.logger.Error("campaign snapshot failed", zap.Error(snapErr))
		respondError(w, http.StatusInternalServerError, "failed to create campaign snapshot")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit campaign transaction", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create campaign")
		return
	}

	respondJSON(w, http.StatusCreated, ReviewCampaign{
		ID:        campaignID,
		Name:      req.Name,
		Status:    "open",
		CreatedBy: actorID,
		CreatedAt: createdAt.Format(time.RFC3339),
	})
}

// ListCampaigns lists all campaigns.
// Route: GET /api/v1/campaigns (requires PermReviewCampaigns via middleware)
func (h *Handler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, status, created_by, created_at, closed_at
		 FROM review_campaigns ORDER BY created_at DESC LIMIT 100`,
	)
	if err != nil {
		h.logger.Error("failed to list campaigns", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	campaigns := []ReviewCampaign{}
	for rows.Next() {
		var c ReviewCampaign
		var createdAt time.Time
		var closedAt *time.Time
		if err := rows.Scan(&c.ID, &c.Name, &c.Status, &c.CreatedBy, &createdAt, &closedAt); err != nil {
			h.logger.Error("failed to scan campaign row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		c.CreatedAt = createdAt.Format(time.RFC3339)
		if closedAt != nil {
			s := closedAt.Format(time.RFC3339)
			c.ClosedAt = &s
		}
		campaigns = append(campaigns, c)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed to iterate campaign rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, campaigns)
}

// ListCampaignItems returns the review items for a campaign.
// Route: GET /api/v1/campaigns/{id}/items (requires PermReviewCampaigns via middleware)
func (h *Handler) ListCampaignItems(w http.ResponseWriter, r *http.Request) {
	campaignID := chi.URLParam(r, "id")
	if !isValidUUID(campaignID) {
		respondError(w, http.StatusBadRequest, "invalid campaign id")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT id, campaign_id, user_id, resource_type, resource_id, resource_name,
		        decision, decided_by, decided_at, created_at
		 FROM review_items WHERE campaign_id = $1 ORDER BY created_at`,
		campaignID,
	)
	if err != nil {
		h.logger.Error("failed to list campaign items", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	items := []ReviewItem{}
	for rows.Next() {
		var item ReviewItem
		var createdAt time.Time
		var decidedAt *time.Time
		if err := rows.Scan(
			&item.ID, &item.CampaignID, &item.UserID,
			&item.ResourceType, &item.ResourceID, &item.ResourceName,
			&item.Decision, &item.DecidedBy, &decidedAt, &createdAt,
		); err != nil {
			h.logger.Error("failed to scan campaign item row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		if decidedAt != nil {
			s := decidedAt.Format(time.RFC3339)
			item.DecidedAt = &s
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed to iterate campaign item rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, items)
}

// DecideCampaignItem records a confirm/revoke decision on a review item.
// Route: POST /api/v1/campaigns/{id}/items/{itemId}/decide (requires PermReviewCampaigns)
func (h *Handler) DecideCampaignItem(w http.ResponseWriter, r *http.Request) {
	campaignID := chi.URLParam(r, "id")
	itemID := chi.URLParam(r, "itemId")
	if !isValidUUID(campaignID) || !isValidUUID(itemID) {
		respondError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req DecideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Decision != "confirm" && req.Decision != "revoke" {
		respondError(w, http.StatusBadRequest, "decision must be 'confirm' or 'revoke'")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	actorID := middleware.GetActorID(r.Context())
	tag, err := h.db.Exec(r.Context(),
		`UPDATE review_items SET decision = $1, decided_by = $2, decided_at = NOW()
		 WHERE id = $3 AND campaign_id = $4 AND decision IS NULL`,
		req.Decision, actorID, itemID, campaignID,
	)
	if err != nil {
		h.logger.Error("failed to record decision", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		respondError(w, http.StatusNotFound, "item not found, already decided, or wrong campaign")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"decision": req.Decision})
}

// CompleteCampaign closes a campaign and applies revoke decisions (removes app assignments).
// Route: POST /api/v1/campaigns/{id}/complete (requires PermManageCampaigns)
func (h *Handler) CompleteCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID := chi.URLParam(r, "id")
	if !isValidUUID(campaignID) {
		respondError(w, http.StatusBadRequest, "invalid campaign id")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin campaign completion transaction", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	// Apply revocations for app resources in the same transaction as campaign
	// completion so the campaign cannot close with unapplied revoke decisions.
	_, revokeErr := tx.Exec(ctx,
		`DELETE FROM app_assignments aa
		 USING review_items ri
		 WHERE ri.campaign_id = $1
		   AND ri.decision = 'revoke'
		   AND ri.resource_type = 'app'
		   AND aa.app_id::text = ri.resource_id
		   AND aa.user_id = ri.user_id`,
		campaignID,
	)
	if revokeErr != nil {
		h.logger.Error("revocation failed during campaign completion", zap.Error(revokeErr))
		respondError(w, http.StatusInternalServerError, "failed to apply revocations")
		return
	}

	// Mark campaign as completed.
	tag, err := tx.Exec(ctx,
		`UPDATE review_campaigns SET status = 'completed', closed_at = NOW()
		 WHERE id = $1 AND status = 'open'`,
		campaignID,
	)
	if err != nil {
		h.logger.Error("failed to complete campaign", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		respondError(w, http.StatusNotFound, "campaign not found or already closed")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit campaign completion", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.writeAuditEntryBestEffort(actorID, "complete_campaign", "campaign", campaignID, map[string]interface{}{}); err != nil {
		h.logger.Warn("failed to write campaign completion audit log", zap.Error(err))
	}

	respondJSON(w, http.StatusOK, map[string]bool{"completed": true})
}
