package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func chiCtxWithIDParam(r *http.Request, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestCreateReviewScheduleNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"name":"Q1 Review","cadence":"quarterly"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateReviewSchedule(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateReviewScheduleInvalidCadence(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"name":"Test","cadence":"daily"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateReviewSchedule(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid cadence: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateReviewScheduleMissingName(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"name":"","cadence":"monthly"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateReviewSchedule(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing name: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListReviewSchedulesNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ListReviewSchedules(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestExportCampaignInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/?format=json", nil)
	req = chiCtxWithIDParam(req, "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ExportCampaign(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestExportCampaignInvalidFormat(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/?format=xml", nil)
	req = chiCtxWithIDParam(req, "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ExportCampaign(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid format: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
