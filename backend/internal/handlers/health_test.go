package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func TestHealthzLiveness(t *testing.T) {
	h := setupTestHandler(t)
	rec := httptest.NewRecorder()
	h.Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from /healthz, got %d", rec.Code)
	}
}

func TestReadyzNotReadyWhenDBMissing(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when DB is not configured, got %d", rec.Code)
	}
}

func readyDB() *fakeDB {
	return &fakeDB{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
		return fakeRow{scanFn: func(dest ...any) error { return nil }}
	}}
}

func TestReadyzReadyWhenDepsOk(t *testing.T) {
	h := NewHandler(readyDB(), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when DB + Keycloak are reachable, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReadyzNotReadyWhenKeycloakDown(t *testing.T) {
	kc := &fakeKeycloak{pingFn: func(ctx context.Context) error { return fmt.Errorf("keycloak down") }}
	h := NewHandler(readyDB(), kc, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when Keycloak is unreachable, got %d", rec.Code)
	}
}
