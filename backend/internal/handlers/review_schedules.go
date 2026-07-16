package handlers

// E3 — Review schedule management.
// Recurring access review schedules: create, list, update (enable/disable), delete.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ReviewSchedule represents a recurring access review schedule.
type ReviewSchedule struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Cadence    string  `json:"cadence"`
	DayOfMonth int     `json:"dayOfMonth,omitempty"`
	NextRunAt  string  `json:"nextRunAt"`
	LastRunAt  *string `json:"lastRunAt,omitempty"`
	Enabled    bool    `json:"enabled"`
	CreatedBy  string  `json:"createdBy"`
	CreatedAt  string  `json:"createdAt"`
}

// CreateScheduleRequest is the body for POST /api/v1/review-schedules.
type CreateScheduleRequest struct {
	Name       string `json:"name"`
	Cadence    string `json:"cadence"`
	DayOfMonth int    `json:"dayOfMonth,omitempty"`
}

// UpdateScheduleRequest is the body for PATCH /api/v1/review-schedules/{id}.
type UpdateScheduleRequest struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Name    *string `json:"name,omitempty"`
}

// nextRunFromCadence computes the next_run_at time from a cadence string.
func nextRunFromCadence(cadence string) time.Time {
	now := time.Now().UTC()
	switch cadence {
	case "weekly":
		return now.Add(7 * 24 * time.Hour)
	case "monthly":
		return now.Add(30 * 24 * time.Hour)
	case "quarterly":
		return now.Add(90 * 24 * time.Hour)
	default:
		return now.Add(30 * 24 * time.Hour)
	}
}

// CreateReviewSchedule creates a new recurring access review schedule.
// Route: POST /api/v1/review-schedules
func (h *Handler) CreateReviewSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 200 {
		respondError(w, http.StatusBadRequest, "name must be ≤ 200 characters")
		return
	}
	validCadences := map[string]bool{"weekly": true, "monthly": true, "quarterly": true}
	if !validCadences[req.Cadence] {
		respondError(w, http.StatusBadRequest, "cadence must be weekly, monthly, or quarterly")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}
	actorID := middleware.GetActorID(ctx)
	nextRunAt := nextRunFromCadence(req.Cadence)

	var dayOfMonthPtr *int
	if req.DayOfMonth > 0 {
		dayOfMonthPtr = &req.DayOfMonth
	}

	var id, createdBy, createdAt string
	var nextRunAtDB time.Time
	var dbEnabled bool
	err := h.db.QueryRow(ctx,
		`INSERT INTO review_schedules (name, cadence, day_of_month, next_run_at, created_by, org_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_by, created_at, next_run_at, enabled`,
		req.Name, req.Cadence, dayOfMonthPtr, nextRunAt, actorID, oc.OrgID,
	).Scan(&id, &createdBy, &createdAt, &nextRunAtDB, &dbEnabled)
	if err != nil {
		h.logger.Error("create review schedule: insert failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create schedule")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "review_schedule.create", "schedule", id,
		map[string]interface{}{"name": req.Name, "cadence": req.Cadence})

	resp := ReviewSchedule{
		ID:        id,
		Name:      req.Name,
		Cadence:   req.Cadence,
		NextRunAt: nextRunAtDB.Format(time.RFC3339),
		Enabled:   dbEnabled,
		CreatedBy: createdBy,
		CreatedAt: createdAt,
	}
	if req.DayOfMonth > 0 {
		resp.DayOfMonth = req.DayOfMonth
	}
	respondJSON(w, http.StatusCreated, resp)
}

// ListReviewSchedules lists all review schedules ordered by created_at DESC.
// Route: GET /api/v1/review-schedules
func (h *Handler) ListReviewSchedules(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, cadence, COALESCE(day_of_month, 0), next_run_at, last_run_at, enabled, created_by, created_at
		 FROM review_schedules WHERE org_id = $1 ORDER BY created_at DESC`,
		oc.OrgID,
	)
	if err != nil {
		h.logger.Error("list review schedules: query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	schedules := []ReviewSchedule{}
	for rows.Next() {
		var s ReviewSchedule
		var nextRunAt, createdAt time.Time
		var lastRunAt *time.Time
		if err := rows.Scan(&s.ID, &s.Name, &s.Cadence, &s.DayOfMonth, &nextRunAt, &lastRunAt, &s.Enabled, &s.CreatedBy, &createdAt); err != nil {
			h.logger.Error("list review schedules: scan failed", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		s.NextRunAt = nextRunAt.Format(time.RFC3339)
		s.CreatedAt = createdAt.Format(time.RFC3339)
		if lastRunAt != nil {
			str := lastRunAt.Format(time.RFC3339)
			s.LastRunAt = &str
		}
		schedules = append(schedules, s)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("list review schedules: iterate failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, schedules)
}

// UpdateReviewSchedule updates a review schedule's name and/or enabled state.
// Route: PATCH /api/v1/review-schedules/{id}
func (h *Handler) UpdateReviewSchedule(w http.ResponseWriter, r *http.Request) {
	schedID := chi.URLParam(r, "id")
	if !isValidUUID(schedID) {
		respondError(w, http.StatusBadRequest, "invalid schedule id")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireReviewScheduleInCallerOrg(w, r, schedID) {
		return
	}

	var req UpdateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	_, err := h.db.Exec(ctx,
		`UPDATE review_schedules SET
		   name    = COALESCE($2, name),
		   enabled = COALESCE($3, enabled),
		   updated_at = NOW()
		 WHERE id = $1`,
		schedID, req.Name, req.Enabled,
	)
	if err != nil {
		h.logger.Error("update review schedule: exec failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Re-fetch updated row.
	var s ReviewSchedule
	var nextRunAt, createdAt time.Time
	var lastRunAt *time.Time
	err = h.db.QueryRow(ctx,
		`SELECT id, name, cadence, COALESCE(day_of_month, 0), next_run_at, last_run_at, enabled, created_by, created_at
		 FROM review_schedules WHERE id = $1`,
		schedID,
	).Scan(&s.ID, &s.Name, &s.Cadence, &s.DayOfMonth, &nextRunAt, &lastRunAt, &s.Enabled, &s.CreatedBy, &createdAt)
	if err != nil {
		h.logger.Error("update review schedule: refetch failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.NextRunAt = nextRunAt.Format(time.RFC3339)
	s.CreatedAt = createdAt.Format(time.RFC3339)
	if lastRunAt != nil {
		str := lastRunAt.Format(time.RFC3339)
		s.LastRunAt = &str
	}

	_ = h.writeAuditEntryBestEffort(actorID, "review_schedule.update", "schedule", schedID,
		map[string]interface{}{})

	respondJSON(w, http.StatusOK, s)
}

// DeleteReviewSchedule deletes a review schedule.
// Route: DELETE /api/v1/review-schedules/{id}
func (h *Handler) DeleteReviewSchedule(w http.ResponseWriter, r *http.Request) {
	schedID := chi.URLParam(r, "id")
	if !isValidUUID(schedID) {
		respondError(w, http.StatusBadRequest, "invalid schedule id")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireReviewScheduleInCallerOrg(w, r, schedID) {
		return
	}

	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	_, err := h.db.Exec(ctx, `DELETE FROM review_schedules WHERE id = $1`, schedID)
	if err != nil {
		h.logger.Error("delete review schedule: exec failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	_ = h.writeAuditEntryBestEffort(actorID, "review_schedule.delete", "schedule", schedID,
		map[string]interface{}{})

	respondJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// RunDueReviewSchedules creates access-review campaigns for every enabled
// schedule whose next_run_at is due. Intended to be called from a leader-gated
// ticker so only one replica fires schedules in multi-instance deploys.
// Returns the number of schedules that successfully created a campaign.
func (h *Handler) RunDueReviewSchedules(ctx context.Context) int {
	if h.db == nil {
		return 0
	}
	rows, err := h.db.Query(ctx, `
		SELECT id, name, cadence, org_id::text, created_by
		FROM review_schedules
		WHERE enabled = true AND next_run_at <= NOW()
		ORDER BY next_run_at ASC
		LIMIT 50`)
	if err != nil {
		h.logger.Error("review schedules: list due failed", zap.Error(err))
		return 0
	}
	defer rows.Close()

	type due struct {
		id, name, cadence, orgID, createdBy string
	}
	var dueList []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.name, &d.cadence, &d.orgID, &d.createdBy); err != nil {
			h.logger.Error("review schedules: scan due failed", zap.Error(err))
			return 0
		}
		dueList = append(dueList, d)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("review schedules: iterate due failed", zap.Error(err))
		return 0
	}

	created := 0
	for _, d := range dueList {
		if err := h.fireReviewSchedule(ctx, d.id, d.name, d.cadence, d.orgID, d.createdBy); err != nil {
			h.logger.Error("review schedules: fire failed",
				zap.String("schedule_id", d.id),
				zap.Error(err),
			)
			continue
		}
		created++
	}
	return created
}

func (h *Handler) fireReviewSchedule(ctx context.Context, scheduleID, name, cadence, orgID, createdBy string) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Re-check due under lock via UPDATE ... WHERE next_run_at <= NOW() so two
	// leaders cannot double-fire the same schedule.
	nextRun := nextRunFromCadence(cadence)
	tag, err := tx.Exec(ctx, `
		UPDATE review_schedules
		SET last_run_at = NOW(), next_run_at = $1
		WHERE id = $2 AND enabled = true AND next_run_at <= NOW()`,
		nextRun, scheduleID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // lost the race; another replica already advanced it
	}

	campaignName := name + " (" + time.Now().UTC().Format("2006-01-02") + ")"
	var campaignID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO review_campaigns (name, created_by, org_id) VALUES ($1, $2, $3) RETURNING id`,
		campaignName, createdBy, orgID,
	).Scan(&campaignID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO review_items (campaign_id, user_id, resource_type, resource_id, resource_name)
		SELECT $1, aa.user_id, 'app', aa.app_id::text, ca.name
		FROM app_assignments aa
		JOIN connected_apps ca ON ca.id = aa.app_id
		WHERE ca.org_id = $2`,
		campaignID, orgID,
	); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	h.logger.Info("review schedules: created campaign",
		zap.String("schedule_id", scheduleID),
		zap.String("campaign_id", campaignID),
		zap.String("org_id", orgID),
	)
	return nil
}

// StartReviewScheduleRunner runs due schedules on interval when isLeader (if
// non-nil) reports true. interval <= 0 disables the runner.
func (h *Handler) StartReviewScheduleRunner(ctx context.Context, interval time.Duration, isLeader func() bool) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if isLeader != nil && !isLeader() {
					continue
				}
				n := h.RunDueReviewSchedules(ctx)
				if n > 0 {
					h.logger.Info("review schedules: fired due schedules", zap.Int("count", n))
				}
			}
		}
	}()
}
