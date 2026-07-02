package handlers

import (
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/snapshot"
)

// GetAnalyticsSnapshots returns the time-series analytics snapshot rows.
// Route: GET /api/v1/analytics/snapshots?limit=N&from=RFC3339&to=RFC3339
// Default limit 24, max 1000. from/to filter by captured_at (inclusive).
func (h *Handler) GetAnalyticsSnapshots(w http.ResponseWriter, r *http.Request) {
	if h.snapshotter == nil {
		respondError(w, http.StatusServiceUnavailable, "analytics snapshots not configured")
		return
	}

	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	limit := 24
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if v, err := strconv.Atoi(ls); err == nil && v > 0 {
			limit = v
		}
	}

	var from, to time.Time
	if fs := r.URL.Query().Get("from"); fs != "" {
		if t, err := time.Parse(time.RFC3339, fs); err == nil {
			from = t
		} else {
			respondError(w, http.StatusBadRequest, "invalid 'from' timestamp; use RFC3339")
			return
		}
	}
	if ts := r.URL.Query().Get("to"); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			to = t
		} else {
			respondError(w, http.StatusBadRequest, "invalid 'to' timestamp; use RFC3339")
			return
		}
	}

	var (
		series []snapshotResponseRow
		err    error
	)

	if from.IsZero() && to.IsZero() {
		raw, e := h.snapshotter.GetSeries(r.Context(), oc.OrgID, limit)
		if e != nil {
			err = e
		} else {
			series = toSnapshotResponse(raw)
		}
	} else {
		raw, e := h.snapshotter.GetSeriesRange(r.Context(), oc.OrgID, from, to, limit)
		if e != nil {
			err = e
		} else {
			series = toSnapshotResponse(raw)
		}
	}

	if err != nil {
		h.logger.Error("failed to query analytics snapshots", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if series == nil {
		series = []snapshotResponseRow{}
	}
	respondJSON(w, http.StatusOK, series)
}

// snapshotResponseRow is the JSON shape for a single analytics snapshot.
type snapshotResponseRow struct {
	ID              int64   `json:"id"`
	CapturedAt      string  `json:"capturedAt"`
	ComplianceRate  float64 `json:"complianceRate"`
	EnrolledDevices int     `json:"enrolledDevices"`
	MFACoveragePct  float64 `json:"mfaCoveragePct"`
	AppCount        int     `json:"appCount"`
	OnboardCount    int     `json:"onboardCount"`
	OffboardCount   int     `json:"offboardCount"`
}

func toSnapshotResponse(raw []snapshot.SnapshotRow) []snapshotResponseRow {
	out := make([]snapshotResponseRow, 0, len(raw))
	for _, s := range raw {
		out = append(out, snapshotResponseRow{
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
	return out
}

