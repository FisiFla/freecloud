package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// chiCtxWithID injects a chi URL param into the request context.
func chiCtxWithID(r *http.Request, key, value string) *http.Request {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chiCtx))
}

// withAdminClaims injects fake admin JWT claims.
func withAdminClaims(r *http.Request) *http.Request {
	claims := &middleware.JWTClaims{Sub: "admin-id", PreferredUsername: "admin", IsAdmin: true}
	return r.WithContext(middleware.SetClaims(r.Context(), claims))
}

// ----- B1: Remote Lock -----

func TestRemoteLock_HappyPath(t *testing.T) {
	lockCalled := false
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		issueRemoteLockFn: func(_ context.Context, hostID string) error {
			lockCalled = true
			if hostID != "host-001" {
				t.Errorf("unexpected host ID: %s", hostID)
			}
			return nil
		},
	}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/lock", nil)
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteLock(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !lockCalled {
		t.Error("expected IssueRemoteLock to be called")
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}
}

func TestRemoteLock_MissingDeviceID(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices//lock", nil)
	req = chiCtxWithID(req, "id", "")
	req = withAdminClaims(req)
	rec := httptest.NewRecorder()

	h.RemoteLock(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestRemoteLock_FleetError(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		issueRemoteLockFn: func(_ context.Context, hostID string) error {
			return errors.New("fleet unreachable")
		},
	}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/lock", nil)
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteLock(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502 on fleet error, got %d", rec.Code)
	}
}

func TestRemoteLock_AuditWritten(t *testing.T) {
	auditWritten := false
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if len(args) >= 2 {
				if action, ok := args[1].(string); ok && action == "device_lock" {
					auditWritten = true
				}
			}
			return pgconn.CommandTag{}, nil
		},
	}
	db.beginFn = func(_ context.Context) (pgx.Tx, error) {
		return &fakeTx{
			execFn: db.execFn,
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			},
		}, nil
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/lock", nil)
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteLock(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !auditWritten {
		t.Error("expected audit log to be written for device_lock")
	}
}

// ----- B2: Software inventory -----

func TestGetDeviceSoftware_NilDB(t *testing.T) {
	h := setupTestHandler(t)

	const uid = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+uid+"/devices/software", nil)
	req = chiCtxWithID(req, "id", uid)
	rec := httptest.NewRecorder()

	h.GetDeviceSoftware(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (nil DB), got %d", rec.Code)
	}
}

func TestGetDeviceSoftware_InvalidUUID(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/not-a-uuid/devices/software", nil)
	req = chiCtxWithID(req, "id", "not-a-uuid")
	rec := httptest.NewRecorder()

	h.GetDeviceSoftware(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (bad UUID), got %d", rec.Code)
	}
}

func TestGetDeviceSoftware_FleetErrorReturnsEmpty(t *testing.T) {
	// When Fleet returns an error for a device, the handler returns an empty
	// software list for that device rather than failing the whole request.
	const uid = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	fakeRows := &fakeQueryRows{
		rows: [][]interface{}{
			{"host-001", "test-laptop"},
		},
	}
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			return fakeRows, nil
		},
	}
	fleetClient := &fakeFleet{
		getHostSoftwareFn: func(_ context.Context, hostID string) ([]fleet.Software, error) {
			return nil, errors.New("fleet down")
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fleetClient, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+uid+"/devices/software", nil)
	req = chiCtxWithID(req, "id", uid)
	rec := httptest.NewRecorder()

	h.GetDeviceSoftware(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 even with fleet error, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}
}

// ----- B3: Compliance -----

func TestGetUserCompliance_NilDB(t *testing.T) {
	h := setupTestHandler(t)

	const uid = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+uid+"/devices/compliance", nil)
	req = chiCtxWithID(req, "id", uid)
	rec := httptest.NewRecorder()

	h.GetUserCompliance(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (nil DB), got %d", rec.Code)
	}
}

func TestGetUserCompliance_InvalidUUID(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/bad-id/devices/compliance", nil)
	req = chiCtxWithID(req, "id", "bad-id")
	rec := httptest.NewRecorder()

	h.GetUserCompliance(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestGetOrgCompliance_NilDB(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/compliance", nil)
	rec := httptest.NewRecorder()

	h.GetOrgCompliance(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (nil DB), got %d", rec.Code)
	}
}

func TestGetOrgCompliance_WithDevices(t *testing.T) {
	fakeRows := &fakeQueryRows{
		rows: [][]interface{}{
			{"host-001", "test-laptop", "macOS 15"},
			{"host-002", "test-server", "Ubuntu 24.04"},
		},
	}
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			return fakeRows, nil
		},
	}
	// fakeFleet returns compliant state by default (disk+firewall enabled, no vulns).
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/compliance", nil)
	rec := httptest.NewRecorder()

	h.GetOrgCompliance(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Errorf("expected success=true")
	}
}

func TestBuildCompliancePostures_CompliantDevice(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		getHostSecurityStateFn: func(_ context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{
				FirewallEnabled: true,
				DiskEncrypted:   true,
				Vulnerabilities: nil,
				UnknownVulns:    false,
			}, nil
		},
	}, zap.NewNop())

	devices := []complianceDevice{{id: "host-001", hostname: "laptop", osVersion: "macOS"}}
	postures, summary := h.buildCompliancePostures(context.Background(), devices)

	if len(postures) != 1 {
		t.Fatalf("expected 1 posture, got %d", len(postures))
	}
	if !postures[0].Compliant {
		t.Error("expected device to be compliant")
	}
	if !postures[0].MDMEnrolled {
		t.Error("expected MDMEnrolled=true when fleet responds")
	}
	if summary.CompliantDevices != 1 {
		t.Errorf("expected 1 compliant device, got %d", summary.CompliantDevices)
	}
	if summary.TotalDevices != 1 {
		t.Errorf("expected 1 total device, got %d", summary.TotalDevices)
	}
}

func TestBuildCompliancePostures_NonCompliantDevice(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		getHostSecurityStateFn: func(_ context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{
				FirewallEnabled: false,
				DiskEncrypted:   false,
				Vulnerabilities: []string{"CVE-2024-1234"},
				UnknownVulns:    false,
			}, nil
		},
	}, zap.NewNop())

	devices := []complianceDevice{{id: "host-001", hostname: "laptop", osVersion: "macOS"}}
	postures, summary := h.buildCompliancePostures(context.Background(), devices)

	if postures[0].Compliant {
		t.Error("expected device to be non-compliant")
	}
	if summary.CompliantDevices != 0 {
		t.Errorf("expected 0 compliant devices")
	}
	if summary.DevicesWithVulns != 1 {
		t.Errorf("expected 1 device with vulns")
	}
}

func TestBuildCompliancePostures_FleetError(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		getHostSecurityStateFn: func(_ context.Context, hostID string) (*fleet.SecurityState, error) {
			return nil, errors.New("fleet error")
		},
	}, zap.NewNop())

	devices := []complianceDevice{{id: "host-001", hostname: "laptop", osVersion: "macOS"}}
	postures, summary := h.buildCompliancePostures(context.Background(), devices)

	if len(postures) != 1 {
		t.Fatalf("expected 1 posture even on error")
	}
	if postures[0].Compliant {
		t.Error("expected non-compliant on fleet error (fail closed)")
	}
	if !postures[0].UnknownVulns {
		t.Error("expected UnknownVulns=true on fleet error")
	}
	if summary.TotalDevices != 1 {
		t.Errorf("expected 1 total device")
	}
}

// ----- B4: Policies -----

func TestListPolicies_HappyPath(t *testing.T) {
	h := setupTestHandler(t) // fakeFleet returns 1 policy by default

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil)
	rec := httptest.NewRecorder()

	h.ListPolicies(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Errorf("expected success=true")
	}
}

func TestListPolicies_FleetError(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		listPoliciesFn: func(_ context.Context) ([]fleet.Policy, error) {
			return nil, errors.New("fleet down")
		},
	}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil)
	rec := httptest.NewRecorder()

	h.ListPolicies(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502 on fleet error, got %d", rec.Code)
	}
}

// B2: The host-scoped AssignDevicePolicy was replaced by the team-scoped
// AssignTeamPolicy / MoveHostToTeam flow. Coverage for those handlers lives in
// fleet_teams_test.go. These placeholder tests document the removal.

// ----- E1: RemoteRestart -----

func TestRemoteRestart_HappyPath(t *testing.T) {
	restartCalled := false
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		issueRestartFn: func(_ context.Context, hostID string) error {
			restartCalled = true
			if hostID != "host-001" {
				t.Errorf("unexpected host ID: %s", hostID)
			}
			return nil
		},
	}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/restart", nil)
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteRestart(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !restartCalled {
		t.Error("expected IssueRestart to be called")
	}
	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestRemoteRestart_MissingDeviceID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices//restart", nil)
	req = chiCtxWithID(req, "id", "")
	req = withAdminClaims(req)
	rec := httptest.NewRecorder()
	h.RemoteRestart(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestRemoteRestart_FleetError(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		issueRestartFn: func(_ context.Context, hostID string) error {
			return errors.New("fleet unreachable")
		},
	}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/restart", nil)
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteRestart(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// ----- E1: RemoteLockWithMessage -----

func TestRemoteLockWithMessage_HappyPath(t *testing.T) {
	var gotMessage string
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		issueLockWithMessageFn: func(_ context.Context, hostID string, message string) error {
			gotMessage = message
			return nil
		},
	}, zap.NewNop())

	body := `{"message":"Please return this device"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/lock-message",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteLockWithMessage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotMessage != "Please return this device" {
		t.Errorf("unexpected message: %q", gotMessage)
	}
}

func TestRemoteLockWithMessage_MissingDeviceID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices//lock-message",
		strings.NewReader(`{"message":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req = chiCtxWithID(req, "id", "")
	rec := httptest.NewRecorder()
	h.RemoteLockWithMessage(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestRemoteLockWithMessage_FleetError(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		issueLockWithMessageFn: func(_ context.Context, _ string, _ string) error {
			return errors.New("fleet down")
		},
	}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/lock-message",
		strings.NewReader(`{"message":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteLockWithMessage(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// ----- E1: RemoteClearPasscode -----

func TestRemoteClearPasscode_HappyPath(t *testing.T) {
	clearCalled := false
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		issueClearPasscodeFn: func(_ context.Context, hostID string) error {
			clearCalled = true
			return nil
		},
	}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/clear-passcode", nil)
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteClearPasscode(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !clearCalled {
		t.Error("expected IssueClearPasscode to be called")
	}
}

func TestRemoteClearPasscode_MissingDeviceID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices//clear-passcode", nil)
	req = chiCtxWithID(req, "id", "")
	rec := httptest.NewRecorder()
	h.RemoteClearPasscode(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestRemoteClearPasscode_FleetError(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		issueClearPasscodeFn: func(_ context.Context, hostID string) error {
			return errors.New("fleet down")
		},
	}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/host-001/clear-passcode", nil)
	req = chiCtxWithID(req, "id", "host-001")
	req = withAdminClaims(req)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ActorIDKey, "admin"))
	rec := httptest.NewRecorder()

	h.RemoteClearPasscode(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// ----- E2: GetDeviceCommandHistory -----

func TestGetDeviceCommandHistory_NilDB(t *testing.T) {
	h := setupTestHandler(t) // nil DB

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/host-001/commands", nil)
	req = chiCtxWithID(req, "id", "host-001")
	rec := httptest.NewRecorder()

	h.GetDeviceCommandHistory(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (nil DB), got %d", rec.Code)
	}
}

func TestGetDeviceCommandHistory_HappyPath(t *testing.T) {
	fakeRows := &fakeQueryRows{
		rows: [][]interface{}{
			// id, host_id, command_type, status, requested_by, requested_at, updated_at, fleet_command_uuid, result
			{"uuid-001", "host-001", "lock", "sent", "admin",
				"2024-01-01T10:00:00Z", "2024-01-01T10:00:01Z", "", ""},
			{"uuid-002", "host-001", "restart", "sent", "admin",
				"2024-01-02T10:00:00Z", "2024-01-02T10:00:01Z", "", ""},
		},
	}
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			return fakeRows, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/host-001/commands", nil)
	req = chiCtxWithID(req, "id", "host-001")
	rec := httptest.NewRecorder()

	h.GetDeviceCommandHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}
}

// ----- E3: NeedsUpdate in buildCompliancePostures -----

func TestBuildCompliancePostures_NeedsUpdate(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{
		getHostSecurityStateFn: func(_ context.Context, hostID string) (*fleet.SecurityState, error) {
			return &fleet.SecurityState{
				FirewallEnabled: true,
				DiskEncrypted:   true,
				Vulnerabilities: nil,
				UnknownVulns:    false,
			}, nil
		},
		getHostOSPostureFn: func(_ context.Context, hostID string) (*fleet.OSPosture, error) {
			return &fleet.OSPosture{OsVersion: "macOS 14", NeedsUpdate: true}, nil
		},
	}, zap.NewNop())

	devices := []complianceDevice{{id: "host-001", hostname: "laptop", osVersion: "macOS 14"}}
	postures, summary := h.buildCompliancePostures(context.Background(), devices)

	if len(postures) != 1 {
		t.Fatalf("expected 1 posture, got %d", len(postures))
	}
	if !postures[0].NeedsUpdate {
		t.Error("expected NeedsUpdate=true")
	}
	if summary.NeedsUpdateDevices != 1 {
		t.Errorf("expected NeedsUpdateDevices=1, got %d", summary.NeedsUpdateDevices)
	}
}
