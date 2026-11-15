package handlers

// L3 lane: PreviewAppPolicy must not evaluate Fleet posture for body-supplied
// deviceId/userId from another org. Both IDs must belong to the caller's org
// (like the provisioning dry-run), and supplying only one of the two must
// fail closed instead of silently skipping the posture check.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// previewOrgDB answers org-ownership checks from a tiny in-memory org store:
// ownUser/ownDevice belong to the caller's org, everything else is foreign.
func previewOrgDB(t *testing.T, ownUser, ownDevice string) DBPool {
	t.Helper()
	return &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			switch {
			case strings.Contains(sql, "SELECT 1 FROM connected_apps"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			case strings.Contains(sql, "SELECT 1 FROM users"):
				if len(args) > 0 && args[0] == ownUser {
					return fakeRow{scanFn: func(dest ...any) error {
						*(dest[0].(*int)) = 1
						return nil
					}}
				}
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			case strings.Contains(sql, "SELECT 1 FROM devices"):
				if len(args) > 0 && args[0] == ownDevice {
					return fakeRow{scanFn: func(dest ...any) error {
						*(dest[0].(*int)) = 1
						return nil
					}}
				}
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			case strings.Contains(sql, "FROM users_devices_mapping"):
				// The mapping exists (e.g. the victim's real device-user
				// pair in their own org) — the org gates must stop the
				// evaluation before this is even consulted for foreign IDs.
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = ownDevice
					return nil
				}}
			default:
				// No stored policy row → zero-value policy.
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
	}
}

func previewRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/policy/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return withAppID(req, "app-1")
}

func TestPreviewAppPolicyRejectsForeignUser(t *testing.T) {
	h := NewHandler(previewOrgDB(t, "own-user", "own-device"), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.PreviewAppPolicy(rec, previewRequest(t, `{"deviceId":"own-device","userId":"foreign-user"}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign userId: expected 404, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPreviewAppPolicyRejectsForeignDevice(t *testing.T) {
	h := NewHandler(previewOrgDB(t, "own-user", "own-device"), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.PreviewAppPolicy(rec, previewRequest(t, `{"deviceId":"foreign-device","userId":"own-user"}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign deviceId: expected 404, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPreviewAppPolicyRejectsDeviceWithoutUser(t *testing.T) {
	h := NewHandler(previewOrgDB(t, "own-user", "own-device"), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.PreviewAppPolicy(rec, previewRequest(t, `{"deviceId":"own-device"}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("deviceId without userId: expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPreviewAppPolicyRejectsUserWithoutDevice(t *testing.T) {
	h := NewHandler(previewOrgDB(t, "own-user", "own-device"), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.PreviewAppPolicy(rec, previewRequest(t, `{"userId":"own-user"}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("userId without deviceId: expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPreviewAppPolicyAllowsOwnOrgPair(t *testing.T) {
	h := NewHandler(previewOrgDB(t, "own-user", "own-device"), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.PreviewAppPolicy(rec, previewRequest(t, `{"deviceId":"own-device","userId":"own-user"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("own-org pair: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPreviewAppPolicyAllowsNeitherID(t *testing.T) {
	h := NewHandler(previewOrgDB(t, "own-user", "own-device"), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	rec := httptest.NewRecorder()
	h.PreviewAppPolicy(rec, previewRequest(t, `{"clientIp":"10.0.0.1"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("no IDs: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
