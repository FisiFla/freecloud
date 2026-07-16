package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// setChiURLParam injects a chi URL parameter into the request context.
func setChiURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestDryRunProvisioningNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"userId":"00000000-0000-0000-0000-000000000002"}`))
	req.Header.Set("Content-Type", "application/json")
	req = setChiURLParam(req, "appId", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.DryRunProvisioning(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunProvisioningInvalidAppID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"userId":"00000000-0000-0000-0000-000000000002"}`))
	req.Header.Set("Content-Type", "application/json")
	req = setChiURLParam(req, "appId", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.DryRunProvisioning(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunProvisioningInvalidBodyUserID(t *testing.T) {
	h := setupTestHandler(t)
	// DB is nil so the nil-DB check fires before userId validation — expect 500.
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"userId":"not-a-uuid"}`))
	req.Header.Set("Content-Type", "application/json")
	req = setChiURLParam(req, "appId", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.DryRunProvisioning(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (nil DB fires before userId check), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReconcileAllHandlerNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = setChiURLParam(req, "appId", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ReconcileAllHandler(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReconcileAllHandlerInvalidAppID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = setChiURLParam(req, "appId", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ReconcileAllHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoadProvisioningConnectorsRegistersEnabledSCIM(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	enc, err := encryptProvisioningToken("scim-token")
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}

	appID := "00000000-0000-0000-0000-000000000001"
	db := &fakeDB{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &fakeQueryRows{rows: [][]interface{}{
				{appID, "scim", "https://scim.example.com", &enc},
			}}, nil
		},
	}

	connectors, err := LoadProvisioningConnectors(context.Background(), db, zap.NewNop())
	if err != nil {
		t.Fatalf("LoadProvisioningConnectors: %v", err)
	}
	if len(connectors) != 1 {
		t.Fatalf("expected one connector, got %d", len(connectors))
	}
	if connectors[appID] == nil {
		t.Fatalf("expected connector for app %s", appID)
	}
}

func TestValidateOutboundEndpointURL(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	// Prefer literal IPs so the suite does not depend on outbound DNS.
	cases := []struct {
		url  string
		want bool
	}{
		{"https://8.8.8.8/v2", true}, // public IP, https
		{"http://8.8.8.8/v2", false}, // http blocked outside dev
		{"https://169.254.169.254/latest", false},
		{"https://127.0.0.1/v2", false},
		{"https://10.0.0.5/v2", false},
		{"not-a-url", false},
		{"", false},
	}
	for _, tc := range cases {
		err := validateOutboundEndpointURL(tc.url)
		ok := err == nil
		if ok != tc.want {
			t.Errorf("url %q: got ok=%v err=%v, want ok=%v", tc.url, ok, err, tc.want)
		}
	}
	// Dev allows localhost http
	t.Setenv("APP_ENV", "development")
	if err := validateOutboundEndpointURL("http://localhost:9999/scim"); err != nil {
		t.Errorf("dev localhost http should be allowed: %v", err)
	}
}
