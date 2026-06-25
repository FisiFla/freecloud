package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// TestGetFleetConfig_NoDB verifies that GetFleetConfig returns 500 when the db is nil.
func TestGetFleetConfig_NoDB(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/fleet", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestGetFleetConfig_NoRow verifies that GetFleetConfig returns an empty response
// when the singleton row is absent (ErrNoRows path).
func TestGetFleetConfig_NoRow(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/fleet", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool                `json:"success"`
		Data    FleetConfigResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

// TestGetFleetConfig_Row verifies that GetFleetConfig returns the stored values.
func TestGetFleetConfig_Row(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	hash := "abc123hash"
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "https://fleet.example.com"
				*(dest[1].(**string)) = &hash
				*(dest[2].(*time.Time)) = now
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/fleet", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool                `json:"success"`
		Data    FleetConfigResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.ServerURL != "https://fleet.example.com" {
		t.Errorf("serverUrl: want https://fleet.example.com, got %q", resp.Data.ServerURL)
	}
	if !resp.Data.APITokenConfigured {
		t.Error("expected apiTokenConfigured=true when hash is non-empty")
	}
}

// TestUpsertFleetConfig_SavesURL verifies that UpsertFleetConfig persists the URL.
func TestUpsertFleetConfig_SavesURL(t *testing.T) {
	var execSQL string
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			execSQL = sql
			return pgconn.CommandTag{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"serverUrl":"https://fleet.example.com"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/fleet", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(execSQL, "fleet_config") {
		t.Errorf("expected SQL to touch fleet_config, got: %s", execSQL)
	}
}

// TestUpsertFleetConfig_BadJSON verifies 400 on malformed body.
func TestUpsertFleetConfig_BadJSON(t *testing.T) {
	db := &fakeDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/fleet", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestDynamicFleetClientUsesStoredConfig(t *testing.T) {
	t.Setenv("APP_ENV", "test")

	authCh := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fleet/status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		authCh <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	enc, err := encryptProvisioningToken("stored-token")
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = srv.URL
				*(dest[1].(**string)) = &enc
				return nil
			}}
		},
	}

	client := NewDynamicFleetClient(db, "http://fallback.invalid", "fallback-token", zap.NewNop())
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	seenAuth := <-authCh
	if seenAuth != "Bearer stored-token" {
		t.Fatalf("Authorization: want stored token, got %q", seenAuth)
	}
}

// TestTestFleetConfig_NoServerURL verifies that TestFleetConfig returns ok=false
// when no server URL is configured.
func TestTestFleetConfig_NoServerURL(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "" // empty server URL
				*(dest[1].(**string)) = nil
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/fleet/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Ok    bool   `json:"ok"`
			Error string `json:"error,omitempty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Ok {
		t.Error("expected ok=false when no server URL configured")
	}
}

// TestFleetConfig_PermissionGated verifies that an end-user (non-super-admin)
// gets 403 on all fleet config endpoints.
func TestFleetConfig_PermissionGated(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newRoleTestRouter(h, middleware.RoleEndUser)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/settings/fleet"},
		{http.MethodPut, "/api/v1/settings/fleet"},
		{http.MethodPost, "/api/v1/settings/fleet/test"},
	}
	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: expected 403, got %d", rt.method, rt.path, rec.Code)
		}
	}
}
