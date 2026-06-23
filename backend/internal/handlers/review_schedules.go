package handlers

// E3 — Review schedule management.
// Recurring access review schedules: create, list, update (enable/disable), delete.

import (
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
		`INSERT INTO review_schedules (name, cadence, day_of_month, next_run_at, created_by)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_by, created_at, next_run_at, enabled`,
		req.Name, req.Cadence, dayOfMonthPtr, nextRunAt, actorID,
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
	rows, err := h.db.Query(r.Context(),
		`SELECT id, name, cadence, COALESCE(day_of_month, 0), next_run_at, last_run_at, enabled, created_by, created_at
		 FROM review_schedules ORDER BY created_at DESC`,
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
