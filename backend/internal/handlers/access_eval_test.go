package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// ---- Bearer middleware tests ----

func TestAccessEvalBearerMiddlewareRejectsEmpty(t *testing.T) {
	mw := accessEvalBearerMiddleware("")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", nil)
	req.Header.Set("Authorization", "Bearer something")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("empty token: expected 503, got %d", rec.Code)
	}
}

func TestAccessEvalBearerMiddlewareMissingHeader(t *testing.T) {
	mw := accessEvalBearerMiddleware("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing header: expected 401, got %d", rec.Code)
	}
}

func TestAccessEvalBearerMiddlewareWrongToken(t *testing.T) {
	mw := accessEvalBearerMiddleware("correct")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: expected 401, got %d", rec.Code)
	}
}

func TestAccessEvalBearerMiddlewareAccepts(t *testing.T) {
	mw := accessEvalBearerMiddleware("correct")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", nil)
	req.Header.Set("Authorization", "Bearer correct")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("correct token: expected 200, got %d", rec.Code)
	}
}

// ---- EvaluateAccess handler tests ----

// evalResponse decodes an AccessEvalResponse from the recorder.
func evalResponse(t *testing.T, rec *httptest.ResponseRecorder) AccessEvalResponse {
	t.Helper()
	var env struct {
		Success bool               `json:"success"`
		Data    AccessEvalResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("failed to decode response: %v (body: %s)", err, rec.Body.String())
	}
	return env.Data
}

func TestAccessEvalMissingUserID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false for empty userId")
	}
	if len(resp.Reasons) == 0 {
		t.Error("expected at least one reason")
	}
}

func TestAccessEvalNilDB(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false when DB is nil")
	}
}

func TestAccessEvalUserNotFound(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			// user lookup returns no rows
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false for missing user")
	}
	if len(resp.Reasons) == 0 {
		t.Error("expected deny reason for missing user")
	}
}

func TestAccessEvalNoDevices(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	callCount := 0
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			// Bootstrap window probe: a mapping exists system-wide, so the
			// window is closed for this non-admin user.
			if strings.Contains(sql, "NOT EXISTS") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*bool)) = false
					return nil
				}}
			}
			// user exists (non-admin employee)
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = userID
				}
				if len(dest) > 1 {
					if r, ok := dest[1].(*string); ok {
						*r = "employee"
					}
				}
				return nil
			}}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			callCount++
			// device mapping query returns zero rows
			return &fakeQueryRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false when user has no devices")
	}
	found := false
	for _, r := range resp.Reasons {
		if r == "no enrolled device found for user" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'no enrolled device' reason, got: %v", resp.Reasons)
	}
}

func TestAccessEvalExplicitDeviceMustBeMappedToUser(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const devID = "dev-spoofed"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			switch {
			case strings.Contains(sql, "FROM users "):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = userID
					return nil
				}}
			case strings.Contains(sql, "FROM users_devices_mapping"):
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			t.Fatalf("Fleet must not be called for an unmapped explicit device ID")
			return nil, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, DeviceID: devID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false for unmapped explicit device")
	}
	if len(resp.Reasons) != 1 || resp.Reasons[0] != "device is not enrolled for user" {
		t.Fatalf("unexpected reasons: %v", resp.Reasons)
	}
}

func TestAccessEvalExplicitMappedDeviceAllowsCleanPosture(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const devID = "dev-clean"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			switch {
			case strings.Contains(sql, "FROM users "):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = userID
					return nil
				}}
			case strings.Contains(sql, "FROM users_devices_mapping"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = devID
					return nil
				}}
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			if hostID != devID {
				t.Fatalf("got hostID %q, want %q", hostID, devID)
			}
			return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, DeviceID: "  " + devID + "  "})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if !resp.Allow {
		t.Fatalf("expected allow=true for mapped clean explicit device, got reasons: %v", resp.Reasons)
	}
}

func TestAccessEvalFleetError(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const devID = "dev-001"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = userID
				}
				return nil
			}}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeQueryRows{rows: [][]interface{}{{devID}}}, nil
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			return nil, errors.New("fleet unreachable")
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false when fleet returns error")
	}
	if len(resp.Reasons) == 0 {
		t.Error("expected deny reason for fleet error")
	}
}

func TestAccessEvalAllow(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const devID = "dev-clean"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = userID
				}
				return nil
			}}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeQueryRows{rows: [][]interface{}{{devID}}}, nil
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if !resp.Allow {
		t.Errorf("expected allow=true for clean device, got reasons: %v", resp.Reasons)
	}
}

func TestAccessEvalNoPolicyRowAllowsCleanDevice(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const appID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	const devID = "dev-clean"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			switch {
			case strings.Contains(sql, "FROM users"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = userID
					return nil
				}}
			case strings.Contains(sql, "FROM app_access_policies"):
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeQueryRows{rows: [][]interface{}{{devID}}}, nil
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, AppID: appID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if !resp.Allow {
		t.Errorf("expected allow=true when app has no policy row and device is clean, got reasons: %v", resp.Reasons)
	}
}

func TestAccessEvalPolicyLookupErrorDenies(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const appID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	const devID = "dev-clean"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			switch {
			case strings.Contains(sql, "FROM users"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = userID
					return nil
				}}
			case strings.Contains(sql, "FROM app_access_policies"):
				return fakeRow{scanFn: func(dest ...any) error { return errors.New("policy db unavailable") }}
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeQueryRows{rows: [][]interface{}{{devID}}}, nil
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, AppID: appID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false when app policy lookup fails")
	}
	if len(resp.Reasons) != 1 || resp.Reasons[0] != "app policy lookup failed" {
		t.Errorf("expected app policy lookup failure reason, got: %v", resp.Reasons)
	}
}

func TestAccessEvalLoadsPolicyByKeycloakClientID(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const keycloakClientID = "kc-client-uuid"
	const devID = "dev-noenc"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			switch {
			case strings.Contains(sql, "FROM users "):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = userID
					return nil
				}}
			case strings.Contains(sql, "FROM users_devices_mapping"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = devID
					return nil
				}}
			case strings.Contains(sql, "FROM app_access_policies"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*bool)) = false
					*(dest[1].(*bool)) = true
					*(dest[2].(*bool)) = false
					*(dest[3].(**int)) = nil
					// D1 new columns: dest[4]=allowedTimeStart, dest[5]=allowedTimeEnd,
					// dest[6]=networkAllowlist, dest[7]=geoCountryAllowlist — zero values.
					if len(dest) > 4 {
						*(dest[4].(**string)) = nil
					}
					if len(dest) > 5 {
						*(dest[5].(**string)) = nil
					}
					if len(dest) > 6 {
						*(dest[6].(*[]string)) = nil
					}
					if len(dest) > 7 {
						*(dest[7].(*[]string)) = nil
					}
					return nil
				}}
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: false}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, AppID: keycloakClientID, DeviceID: devID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	foundPolicyReason := false
	for _, reason := range resp.Reasons {
		if reason == "app policy requires disk encryption on device "+devID {
			foundPolicyReason = true
			break
		}
	}
	if !foundPolicyReason {
		t.Fatalf("expected app-policy disk encryption reason, got: %v", resp.Reasons)
	}
}

func TestAccessEvalLegacyMaxOSAgePolicyFailsClosed(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const appID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	const devID = "dev-clean"
	maxAge := 90
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			switch {
			case strings.Contains(sql, "FROM users "):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = userID
					return nil
				}}
			case strings.Contains(sql, "FROM users_devices_mapping"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = devID
					return nil
				}}
			case strings.Contains(sql, "FROM app_access_policies"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*bool)) = false
					*(dest[1].(*bool)) = false
					*(dest[2].(*bool)) = false
					*(dest[3].(**int)) = &maxAge
					// D1 new columns: dest[4]=allowedTimeStart, dest[5]=allowedTimeEnd,
					// dest[6]=networkAllowlist, dest[7]=geoCountryAllowlist — zero values.
					if len(dest) > 4 {
						*(dest[4].(**string)) = nil
					}
					if len(dest) > 5 {
						*(dest[5].(**string)) = nil
					}
					if len(dest) > 6 {
						*(dest[6].(*[]string)) = nil
					}
					if len(dest) > 7 {
						*(dest[7].(*[]string)) = nil
					}
					return nil
				}}
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, AppID: appID, DeviceID: devID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	// max_os_age_days is rejected at write time and ignored at eval so legacy
	// rows cannot permanently lock out compliant devices.
	if !resp.Allow {
		t.Fatalf("expected allow=true when max OS age is set but unsupported; reasons: %v", resp.Reasons)
	}
}

func TestAccessEvalDenyDiskEncryption(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const devID = "dev-noenc"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = userID
				}
				return nil
			}}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeQueryRows{rows: [][]interface{}{{devID}}}, nil
		},
	}
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: false}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false when disk not encrypted")
	}
	if len(resp.Reasons) == 0 {
		t.Error("expected deny reason for disk encryption failure")
	}
}

// ---- Bootstrap window tests ----
//
// While ZERO device mappings exist system-wide, an admin-role user with no
// enrolled device is allowed (first-admin bootstrap). The window closes
// permanently once the first device mapping exists. Steady state stays
// fail-closed for everyone.

// bootstrapWindowDB builds a fakeDB for window tests: the user exists with
// the given role, the user has no enrolled devices, and the system-wide
// mapping probe answers mappingsExist (or windowErr on failure).
func bootstrapWindowDB(userID, role string, mappingsExist bool, windowErr error) *fakeDB {
	return &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			// System-wide mapping probe. Checked before the per-user
			// mapping branch: both queries mention users_devices_mapping.
			if strings.Contains(sql, "NOT EXISTS") {
				return fakeRow{scanFn: func(dest ...any) error {
					if windowErr != nil {
						return windowErr
					}
					*(dest[0].(*bool)) = !mappingsExist
					return nil
				}}
			}
			if strings.Contains(sql, "FROM users_devices_mapping") {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
			// User lookup: id + role. The len(dest) guard keeps the fake
			// working against both the 1-column and 2-column SELECT.
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = userID
				}
				if len(dest) > 1 {
					if r, ok := dest[1].(*string); ok {
						*r = role
					}
				}
				return nil
			}}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			// The user has no enrolled devices.
			return &fakeQueryRows{}, nil
		},
	}
}

func TestAccessEvalBootstrapWindowAdminNoDevicesAllows(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	db := bootstrapWindowDB(userID, "admin", false, nil)
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			t.Fatalf("Fleet must not be called: no devices exist to posture-check")
			return nil, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if !resp.Allow {
		t.Fatalf("expected allow=true for admin with zero system mappings, got reasons: %v", resp.Reasons)
	}
	if len(resp.Reasons) != 0 {
		t.Errorf("window allow must keep response reasons empty like the normal allow path, got: %v", resp.Reasons)
	}
}

func TestAccessEvalBootstrapWindowClosedAfterFirstEnrollment(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	// Another user's mapping exists: the window is closed even for admins.
	db := bootstrapWindowDB(userID, "admin", true, nil)
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false once a device mapping exists system-wide")
	}
	if len(resp.Reasons) != 1 || resp.Reasons[0] != "no enrolled device found for user" {
		t.Errorf("expected unchanged no-device deny reason, got: %v", resp.Reasons)
	}
}

func TestAccessEvalBootstrapWindowNonAdminDenied(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	db := bootstrapWindowDB(userID, "employee", false, nil)
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false: the window is admins-only")
	}
	if len(resp.Reasons) != 1 || resp.Reasons[0] != "no enrolled device found for user" {
		t.Errorf("expected unchanged no-device deny reason, got: %v", resp.Reasons)
	}
}

func TestAccessEvalBootstrapWindowCheckErrorDenies(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	db := bootstrapWindowDB(userID, "admin", false, errors.New("mapping probe unavailable"))
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false when the window probe fails (fail closed)")
	}
	if len(resp.Reasons) != 1 || resp.Reasons[0] != "no enrolled device found for user" {
		t.Errorf("expected unchanged no-device deny reason, got: %v", resp.Reasons)
	}
}

func TestAccessEvalBootstrapWindowExplicitDeviceStillDenied(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	// Even an admin inside an open window is denied when they present an
	// explicit device id that is not mapped to them (anti-spoof path).
	db := bootstrapWindowDB(userID, "admin", false, nil)
	fl := &fakeFleet{
		getHostSecurityStateFn: func(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
			t.Fatalf("Fleet must not be called for an unmapped explicit device ID")
			return nil, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fl, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, DeviceID: "dev-spoofed"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false for unmapped explicit device inside the window")
	}
	if len(resp.Reasons) != 1 || resp.Reasons[0] != "device is not enrolled for user" {
		t.Fatalf("unexpected reasons: %v", resp.Reasons)
	}
}

func TestAccessEvalBootstrapWindowAppConditionsStillBind(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const appID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	// The window waives ONLY the device requirement: a connected-app policy
	// with network restrictions still binds during the window.
	db := bootstrapWindowDB(userID, "admin", false, nil)
	inner := db.queryRowFn
	db.queryRowFn = func(ctx context.Context, sql string, args ...any) pgx.Row {
		if strings.Contains(sql, "FROM app_access_policies") {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*bool)) = false
				*(dest[1].(*bool)) = false
				*(dest[2].(*bool)) = false
				*(dest[3].(**int)) = nil
				*(dest[4].(**string)) = nil
				*(dest[5].(**string)) = nil
				*(dest[6].(*[]string)) = []string{"10.99.0.0/16"}
				*(dest[7].(*[]string)) = nil
				return nil
			}}
		}
		return inner(ctx, sql, args...)
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, AppID: appID, ClientIP: "192.0.2.1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if resp.Allow {
		t.Error("expected allow=false: app network conditions still bind inside the window")
	}
	found := false
	for _, r := range resp.Reasons {
		if strings.Contains(r, "network allowlist") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected network-allowlist deny reason, got: %v", resp.Reasons)
	}
}

func TestAccessEvalBootstrapWindowNoPolicyRowAllows(t *testing.T) {
	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const appID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	// No policy row means no requirements: an admin dashboard login with no
	// app conditions is allowed inside the window.
	db := bootstrapWindowDB(userID, "admin", false, nil)
	inner := db.queryRowFn
	db.queryRowFn = func(ctx context.Context, sql string, args ...any) pgx.Row {
		if strings.Contains(sql, "FROM app_access_policies") {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}
		return inner(ctx, sql, args...)
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: userID, AppID: appID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)

	resp := evalResponse(t, rec)
	if !resp.Allow {
		t.Fatalf("expected allow=true for admin with no-policy app inside the window, got: %v", resp.Reasons)
	}
}
