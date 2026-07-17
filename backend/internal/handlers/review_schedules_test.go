package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
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

func TestRunDueReviewSchedulesNilDB(t *testing.T) {
	h := setupTestHandler(t)
	if n := h.RunDueReviewSchedules(context.Background()); n != 0 {
		t.Fatalf("nil DB: expected 0 schedules fired, got %d", n)
	}
}

func TestNextRunFromCadence(t *testing.T) {
	now := time.Now().UTC()
	for _, c := range []string{"weekly", "monthly", "quarterly", "unknown"} {
		got := nextRunFromCadence(c)
		if !got.After(now) {
			t.Errorf("cadence %q: next run %v not after now", c, got)
		}
	}
}

func TestFireReviewScheduleCreatesCampaign(t *testing.T) {
	// Production path: fireReviewSchedule advances schedule + inserts campaign in one tx.
	var campaignInserted bool
	var scheduleUpdated bool
	tx := &fakeTx{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "UPDATE review_schedules") {
				scheduleUpdated = true
				return pgconn.NewCommandTag("UPDATE 1"), nil
			}
			if strings.Contains(sql, "INSERT INTO review_items") {
				return pgconn.NewCommandTag("INSERT 0 0"), nil
			}
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		},
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "INSERT INTO review_campaigns") {
				campaignInserted = true
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "cccccccc-cccc-cccc-cccc-cccccccccccc"
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	db := &fakeDB{
		beginFn: func(ctx context.Context) (pgx.Tx, error) { return tx, nil },
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	err := h.fireReviewSchedule(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "Q1", "monthly", middleware.DefaultOrgID, "actor-1")
	if err != nil {
		t.Fatalf("fireReviewSchedule: %v", err)
	}
	if !scheduleUpdated || !campaignInserted {
		t.Fatalf("scheduleUpdated=%v campaignInserted=%v", scheduleUpdated, campaignInserted)
	}
}

func TestFireReviewSchedule_RaceLostNoError(t *testing.T) {
	// UPDATE affects 0 rows → lost race → nil error, no campaign insert.
	tx := &fakeTx{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "UPDATE review_schedules") {
				return pgconn.NewCommandTag("UPDATE 0"), nil
			}
			t.Fatal("should not insert campaign when race lost")
			return pgconn.CommandTag{}, nil
		},
	}
	db := &fakeDB{beginFn: func(ctx context.Context) (pgx.Tx, error) { return tx, nil }}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	if err := h.fireReviewSchedule(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "Q", "weekly", middleware.DefaultOrgID, "a"); err != nil {
		t.Fatalf("lost race should return nil, got %v", err)
	}
}

func TestJ09_MaxDueReviewSchedulesConstant(t *testing.T) {
	if maxDueReviewSchedules != 50 {
		t.Fatalf("maxDueReviewSchedules=%d", maxDueReviewSchedules)
	}
}
