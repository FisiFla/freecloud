package handlers

import (
	"context"
	"encoding/json"
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
		if isCanonicalQuery(sql) {
			return fakeCanonicalRow(args)
		}
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

func readyzChecks(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var envelope struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("readyz body is not the expected JSON envelope: %v (%s)", err, rec.Body.String())
	}
	return envelope.Data
}

// TestReadyzNoRedisKeyWhenCheckUnwired pins the pre-Redis behaviour: with no
// redisCheck set (in-memory limiter), the response has no "redis" entry.
func TestReadyzNoRedisKeyWhenCheckUnwired(t *testing.T) {
	h := NewHandler(readyDB(), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := readyzChecks(t, rec)["redis"]; ok {
		t.Errorf("expected no redis key when no redis check is wired, got %s", rec.Body.String())
	}
}

func TestReadyzRedisOk(t *testing.T) {
	h := NewHandler(readyDB(), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetRedisCheck(func(ctx context.Context) error { return nil })
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when Redis is reachable, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readyzChecks(t, rec)["redis"]; got != "ok" {
		t.Errorf("expected redis=ok, got %q (%s)", got, rec.Body.String())
	}
}

func TestReadyzNotReadyWhenRedisDown(t *testing.T) {
	h := NewHandler(readyDB(), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetRedisCheck(func(ctx context.Context) error { return fmt.Errorf("redis down") })
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Redis is unreachable, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readyzChecks(t, rec)["redis"]; got != "unreachable" {
		t.Errorf("expected redis=unreachable, got %q (%s)", got, rec.Body.String())
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
