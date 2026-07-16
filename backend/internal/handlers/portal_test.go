package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func TestPortalMyDevicesNoAuth(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/devices", nil)
	rec := httptest.NewRecorder()
	h.PortalMyDevices(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalMyDevicesNilDB(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/devices", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:  "user-123",
		Role: middleware.RoleEndUser,
	}))
	rec := httptest.NewRecorder()
	h.PortalMyDevices(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalMyAppsNoAuth(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/apps", nil)
	rec := httptest.NewRecorder()
	h.PortalMyApps(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalMyAppsNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/apps", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:  "user-123",
		Role: middleware.RoleEndUser,
	}))
	rec := httptest.NewRecorder()
	h.PortalMyApps(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalRequestAccessNoAuth(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/access-requests", nil)
	rec := httptest.NewRecorder()
	h.PortalRequestAccess(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalRequestAccessInvalidAppID(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]string{"appId": "not-a-uuid", "reason": "need it"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/access-requests", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:  "user-123",
		Role: middleware.RoleEndUser,
	}))
	rec := httptest.NewRecorder()
	h.PortalRequestAccess(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid appId: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminListAccessRequestsNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/access-requests", nil)
	rec := httptest.NewRecorder()
	h.AdminListAccessRequests(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminDecideAccessRequestInvalidDecision(t *testing.T) {
	h := setupTestHandler(t)
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	b, _ := json.Marshal(map[string]string{"decision": "maybe"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/portal/access-requests/"+validID, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.AdminDecideAccessRequest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid decision: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminDecideAccessRequestInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]string{"decision": "approved"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/portal/access-requests/bad", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"bad"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.AdminDecideAccessRequest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalRequestAccessForeignAppRejected(t *testing.T) {
	// Cross-tenant: app UUID that does not belong to the caller's org must 404
	// before any access_requests INSERT (requireAppInCallerOrg).
	const userID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	const foreignApp = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	const callerOrg = "00000000-0000-0000-0000-000000000001"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			// requireAppInCallerOrg → resourceInOrg on connected_apps: no row.
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			t.Fatalf("must not INSERT access_requests for a foreign app; SQL=%s args=%v", sql, args)
			return pgconn.CommandTag{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	b, _ := json.Marshal(map[string]string{"appId": foreignApp, "reason": "please"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/access-requests", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:  userID,
		Role: middleware.RoleEndUser,
	})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: callerOrg, Role: "member"})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.PortalRequestAccess(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign app: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMintAndVerifyDeviceCookieRoundTrip(t *testing.T) {
	const secret = "round-trip-secret"
	const host = "fleet-host-42"
	val, err := MintDeviceCookieValue(host, secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ParseAndVerifyDeviceCookie(val, secret, time.Now().UTC())
	if !ok || got != host {
		t.Fatalf("round-trip failed: ok=%v got=%q", ok, got)
	}
	if _, ok := ParseAndVerifyDeviceCookie(val, "wrong-secret", time.Now().UTC()); ok {
		t.Fatal("wrong secret must fail verification")
	}
	// Tamper host segment
	if _, ok := ParseAndVerifyDeviceCookie(val+"x", secret, time.Now().UTC()); ok {
		t.Fatal("tampered cookie must fail")
	}
	// Expired
	past := time.Now().UTC().Add(-2 * deviceCookieTTL)
	old, err := MintDeviceCookieValue(host, secret, past)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ParseAndVerifyDeviceCookie(old, secret, time.Now().UTC()); ok {
		t.Fatal("expired cookie must fail")
	}
}

func TestAccessEvalRejectsUnsignedDeviceIDWhenSecretSet(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const hostID = "host-plain"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = userID
				}
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret("signing-secret")
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, DeviceID: hostID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)
	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Fatal("unsigned deviceId must be denied when signing secret is configured")
	}
	if len(resp.Reasons) == 0 || resp.Reasons[0] != "invalid or expired device identity" {
		t.Fatalf("unexpected reasons: %v", resp.Reasons)
	}
}

func TestAccessEvalAcceptsSignedDeviceCookie(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const hostID = "host-signed"
	const secret = "signing-secret"
	signed, err := MintDeviceCookieValue(hostID, secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM users "):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = userID
					return nil
				}}
			case strings.Contains(sql, "FROM users_devices_mapping"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = hostID
					return nil
				}}
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, id string) (*fleet.SecurityState, error) {
			if id != hostID {
				t.Fatalf("fleet host id = %q, want %q", id, hostID)
			}
			return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, DeviceID: signed})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)
	resp := evalResponse(t, rec)
	if !resp.Allow {
		t.Fatalf("signed device cookie should allow; reasons: %v", resp.Reasons)
	}
}
