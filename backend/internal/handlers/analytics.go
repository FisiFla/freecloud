package handlers

import (
	"net/http"
	"strconv"

	"go.uber.org/zap"
)

// GetAnalyticsSnapshots returns the time-series analytics snapshot rows.
// Route: GET /api/v1/analytics/snapshots?limit=N (default 24, max 1000).
func (h *Handler) GetAnalyticsSnapshots(w http.ResponseWriter, r *http.Request) {
	if h.snapshotter == nil {
		respondError(w, http.StatusServiceUnavailable, "analytics snapshots not configured")
		return
	}

	limit := 24
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if v, err := strconv.Atoi(ls); err == nil && v > 0 {
			limit = v
		}
	}

	series, err := h.snapshotter.GetSeries(r.Context(), limit)
	if err != nil {
		h.logger.Error("failed to query analytics snapshots", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Normalise nil slice to empty array for consistent JSON.
	type snapshotResponse struct {
		ID              int64   `json:"id"`
		CapturedAt      string  `json:"capturedAt"`
		ComplianceRate  float64 `json:"complianceRate"`
		EnrolledDevices int     `json:"enrolledDevices"`
		MFACoveragePct  float64 `json:"mfaCoveragePct"`
		AppCount        int     `json:"appCount"`
		OnboardCount    int     `json:"onboardCount"`
		OffboardCount   int     `json:"offboardCount"`
	}

	out := make([]snapshotResponse, 0, len(series))
	for _, s := range series {
		out = append(out, snapshotResponse{
			ID:              s.ID,
			CapturedAt:      s.CapturedAt.Format("2006-01-02T15:04:05Z07:00"),
			ComplianceRate:  s.ComplianceRate,
			EnrolledDevices: s.EnrolledDevices,
			MFACoveragePct:  s.MFACoveragePct,
			AppCount:        s.AppCount,
			OnboardCount:    s.OnboardCount,
			OffboardCount:   s.OffboardCount,
		})
	}
	respondJSON(w, http.StatusOK, out)
}

