package handlers

// Regression tests for the backend handler fixes:
//   - #144 the approval-executed onboarding had no Keycloak rollback.
//   - #145 the approval-executed offboarding did not wipe the devices.
//   - #146 PatchUser left the IdP and the DB split-brain on a DB failure.
//   - #148 the SMTP test endpoint accepted a CRLF address; the send had no
//     timeout.
//   - #149 updating the SMTP config without a password wiped Keycloak's
//     realm SMTP password.
//   - #151 the list/export endpoints had no LIMIT.
//   - #153 / #179 the bulk onboarding leaked multipart temp files and
//     enforced the row cap only after the whole file was parsed.
//   - #155 a failed Keycloak federation update/delete still changed the DB.
//   - #156 the federation sync action was not validated.
//   - #158 the password-policy message disagreed with the enforced minimum.
//   - #159 a database outage was answered with 404.
//   - #161 raw upstream errors were returned to the client.
//   - #172 the analytics limit was never clamped.
//   - #174 MoveHostToTeam ran one query per host.
//   - #175 a failed fleet_team_orgs insert orphaned the Fleet team.
//   - #176 a stored provisioning bearer token could not be cleared.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Nerzal/gocloak/v13"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/snapshot"
)

// fixTestUserID is a UUID-shaped id: the handlers validate ids with
// ValidateUserID, which requires a UUID.
const fixTestUserID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// ---- #152: a failed audit write is logged, not silently discarded ----

func TestAuditWriteFailureIsLogged(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	// The default fakeDB cannot begin a transaction, so every audit write
	// fails — the handlers must log that instead of dropping the error.
	db := &fakeDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, logger)

	body := `{"alias":"google","providerType":"google","displayName":"Google"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/identity-providers", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateIdentityProvider(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "audit") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("the audit write failure must be logged, logs: %v", logs.All())
	}
}

// ---- #144: the approval-executed onboarding rolls back the Keycloak user ----

func TestExecuteApprovedAction_OnboardRollsBackKeycloakUser(t *testing.T) {
	deleted := ""
	kc := &fakeKeycloak{
		createUserFn: func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
			uid := "kc-user-1"
			return &keycloak.CreateUserResult{User: &gocloak.User{ID: &uid}, PasswordSet: true}, nil
		},
		deleteUserFn: func(ctx context.Context, userID string) error {
			deleted = userID
			return nil
		},
	}
	// persistOnboard fails: the transaction cannot be started.
	db := &fakeDB{
		beginFn: func(ctx context.Context) (pgx.Tx, error) {
			return nil, errors.New("db down")
		},
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())

	ctx := middleware.SetOrgContext(context.Background(), &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
	err := h.executeApprovedAction(ctx, "approver", "onboard", "approval-1", map[string]interface{}{
		"email": "a@b.com", "firstName": "A", "lastName": "B",
	})
	if err == nil {
		t.Fatal("expected the persist failure to be reported")
	}
	if deleted != "kc-user-1" {
		t.Errorf("expected the orphaned Keycloak user to be deleted, deleted=%q", deleted)
	}
}

// ---- #145: the approval-executed offboarding wipes the devices ----

func TestExecuteApprovedAction_OffboardWipesDevices(t *testing.T) {
	wiped := []string{}
	fc := &fakeFleet{
		issueRemoteWipeFn: func(ctx context.Context, hostID string) error {
			wiped = append(wiped, hostID)
			return nil
		},
	}
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = fixTestUserID
				return nil
			}}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeQueryRows{rows: [][]interface{}{{"device-1"}, {"device-2"}}}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, fc, zap.NewNop())

	ctx := middleware.SetOrgContext(context.Background(), &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
	err := h.executeApprovedAction(ctx, "approver", "offboard", "approval-1", map[string]interface{}{
		"userId": fixTestUserID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wiped) != 2 || wiped[0] != "device-1" || wiped[1] != "device-2" {
		t.Errorf("expected both devices to be wiped, got %v", wiped)
	}
}

// ---- #146: PatchUser compensates the Keycloak write ----

func TestPatchUser_DBFailureRestoresKeycloak(t *testing.T) {
	var updates [][]string
	kc := &fakeKeycloak{
		updateUserFn: func(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error {
			updates = append(updates, []string{firstName, lastName, department, fmt.Sprintf("%t", enabled)})
			return nil
		},
	}
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			return fakeRow{scanFn: func(dest ...any) error {
				now := time.Now()
				if len(dest) == 1 {
					// The org-ownership check scans a single int.
					*(dest[0].(*int)) = 1
					return nil
				}
				*(dest[0].(*string)) = "Old"
				*(dest[1].(*string)) = "Name"
				*(dest[2].(*string)) = "Ops"
				*(dest[3].(*string)) = "admin"
				*(dest[4].(*bool)) = false
				*(dest[5].(*time.Time)) = now
				*(dest[6].(*time.Time)) = now
				return nil
			}}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, errors.New("db down")
		},
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())

	body := `{"firstName":"New"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+fixTestUserID, bytes.NewReader([]byte(body)))
	req = withChiIDParam(req, fixTestUserID)
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.PatchUser(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on the DB failure, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(updates) != 2 {
		t.Fatalf("expected the Keycloak update and its compensating restore, got %d calls", len(updates))
	}
	if updates[0][0] != "New" {
		t.Errorf("first Keycloak write=%v, want the new value", updates[0])
	}
	if updates[1][0] != "Old" || updates[1][1] != "Name" || updates[1][2] != "Ops" {
		t.Errorf("the compensating write must restore the previous values, got %v", updates[1])
	}
}

// ---- #148: the SMTP test endpoint rejects a CRLF address ----

func TestTestSMTPEmail_RejectsCRLFInjection(t *testing.T) {
	h := NewHandler(&fakeDB{}, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body := `{"to":"victim@example.com\r\nBcc: attacker@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/smtp/test", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.TestSMTPEmail(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a CRLF address, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---- #149: an SMTP update without a password keeps Keycloak's password ----

func TestUpsertSMTPConfig_WithoutPasswordKeepsKeycloakPassword(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	var got keycloak.SMTPConfig
	kc := &fakeKeycloak{
		updateRealmSMTPFn: func(ctx context.Context, cfg keycloak.SMTPConfig) error {
			got = cfg
			return nil
		},
	}
	enc, err := encryptProvisioningToken("stored-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "smtp.example.com"
				*(dest[1].(*string)) = "587"
				*(dest[2].(*string)) = "user"
				*(dest[3].(*string)) = "from@example.com"
				*(dest[4].(**string)) = &enc
				*(dest[5].(**string)) = nil
				return nil
			}}
		},
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())

	body := `{"host":"smtp.example.com","port":"587","username":"user","fromAddress":"from@example.com"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertSMTPConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got.Password == "" {
		t.Error("Keycloak's realm SMTP password must not be wiped by an update without a password")
	}
	if got.Password != "stored-secret" {
		t.Errorf("Keycloak password=%q want the stored one", got.Password)
	}
}

// ---- #151: the list endpoints are bounded ----

func TestListUsers_QueryIsBounded(t *testing.T) {
	var sql string
	db := &fakeDB{
		queryFn: func(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
			sql = q
			return &fakeQueryRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withOrgContext(httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	rec := httptest.NewRecorder()
	h.ListUsers(rec, req)

	if !strings.Contains(sql, "LIMIT") {
		t.Errorf("the users query must be bounded, got: %s", sql)
	}
}

func TestListProvisioningState_QueryIsBounded(t *testing.T) {
	var sql string
	db := &fakeDB{
		queryRowFn: ownershipFoundQueryRowFn(nil),
		queryFn: func(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
			sql = q
			return &fakeQueryRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withAppID(withOrgContext(httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+fixTestUserID+"/provisioning/state", nil)), fixTestUserID)
	rec := httptest.NewRecorder()
	h.ListProvisioningState(rec, req)

	if !strings.Contains(sql, "LIMIT") {
		t.Errorf("the provisioning state query must be bounded, got: %s", sql)
	}
}

func TestExportCampaign_QueryIsBounded(t *testing.T) {
	var sql string
	db := &fakeDB{
		queryRowFn: ownershipFoundQueryRowFn(nil),
		queryFn: func(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
			sql = q
			return &fakeQueryRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withChiIDParam(withOrgContext(httptest.NewRequest(http.MethodGet, "/api/v1/campaigns/"+fixTestUserID+"/export?format=csv", nil)), fixTestUserID)
	rec := httptest.NewRecorder()
	h.ExportCampaign(rec, req)

	if !strings.Contains(sql, "LIMIT") {
		t.Errorf("the campaign export query must be bounded, got: %s", sql)
	}
}

func TestExportAuditLogs_QueryIsBounded(t *testing.T) {
	var sql string
	db := &fakeDBWithQuery{
		queryFn: func(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
			sql = q
			return &fakeAuditRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withDefaultOrg(httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export?format=csv", nil))
	rec := httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)

	if !strings.Contains(sql, "LIMIT") {
		t.Errorf("the audit export query must be bounded, got: %s", sql)
	}
}

// ---- #153 / #179: bulk onboarding temp files + row cap ----

func TestBulkOnboard_RowCapIsEnforcedWhileParsing(t *testing.T) {
	// A CSV with more rows than the cap must be rejected while it is read,
	// not after the whole file was parsed into memory.
	var sb strings.Builder
	sb.WriteString("firstName,lastName,email\n")
	for i := 0; i < maxBulkRows+10; i++ {
		fmt.Fprintf(&sb, "F%d,L%d,u%d@example.com\n", i, i, i)
	}
	if _, err := parseCSVLimited(strings.NewReader(sb.String()), maxBulkRows); err == nil {
		t.Fatal("expected the oversized CSV to be rejected")
	} else if !strings.Contains(err.Error(), "too many rows") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBulkOnboard_JsonRowCapIsEnforcedWhileParsing(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < maxBulkRows+10; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"firstName":"F","lastName":"L","email":"u%d@example.com"}`, i)
	}
	sb.WriteString("]")
	if _, err := parseJSONRows(strings.NewReader(sb.String()), maxBulkRows); err == nil {
		t.Fatal("expected the oversized JSON array to be rejected")
	}
}

func TestBulkOnboard_MultipartTempFilesAreRemoved(t *testing.T) {
	before := countMultipartTempFiles(t)

	// A file larger than the 8 MB in-memory limit makes ParseMultipartForm
	// spill a temporary file to disk — the handler must remove it again.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "users.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	part.Write([]byte("firstName,lastName,email\n"))
	chunk := bytes.Repeat([]byte("x"), 1024*1024)
	for i := 0; i < 9; i++ {
		part.Write(chunk)
	}
	writer.Close()

	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard/bulk", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	h.BulkOnboard(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for the oversized upload, got %d: %s", rec.Code, rec.Body.String())
	}
	if after := countMultipartTempFiles(t); after > before {
		t.Errorf("multipart temp files leaked: %d before, %d after", before, after)
	}
}

// countMultipartTempFiles counts the multipart spill files Go's mime
// multipart reader leaves in the temp directory.
func countMultipartTempFiles(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "multipart-") {
			count++
		}
	}
	return count
}

// ---- #155 / #156 / #159 / #161: the federation handlers ----

// federationDB answers the federation source lookups with a single row.
func federationDB(execErr error, queryErr error) *fakeDB {
	return &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			if strings.Contains(sql, "SELECT 1 FROM") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			if queryErr != nil {
				return fakeRow{scanFn: func(dest ...any) error { return queryErr }}
			}
			return fakeRow{scanFn: func(dest ...any) error {
				now := time.Now()
				switch len(dest) {
				case 1:
					// The delete path scans only the component id.
					*(dest[0].(*string)) = "kc-comp-1"
					return nil
				case 3:
					// The connection-test path scans id, component id, config.
					*(dest[0].(*string)) = "fed-1"
					*(dest[1].(*string)) = "kc-comp-1"
					*(dest[2].(*string)) = `{"connectionUrl":"ldap://dc.example.com","bindDn":"cn=svc","usersDn":"ou=users"}`
					return nil
				}
				*(dest[0].(*string)) = "fed-1"
				*(dest[1].(*string)) = "Corp AD"
				*(dest[2].(*string)) = "ldap"
				*(dest[3].(*string)) = "ad"
				*(dest[4].(*string)) = `{"connectionUrl":"ldap://dc.example.com","bindDn":"cn=svc","usersDn":"ou=users"}`
				*(dest[5].(*string)) = "kc-comp-1"
				*(dest[6].(*string)) = ""
				*(dest[7].(*string)) = ""
				*(dest[8].(*time.Time)) = now
				*(dest[9].(*time.Time)) = now
				return nil
			}}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if execErr != nil {
				return pgconn.CommandTag{}, execErr
			}
			return pgconn.CommandTag{}, nil
		},
	}
}

func TestUpdateFederationSource_KeycloakFailureDoesNotUpdateTheDB(t *testing.T) {
	dbUpdated := false
	kc := &fakeKeycloak{
		updateFederationComponentFn: func(ctx context.Context, componentID, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) error {
			return errors.New("keycloak down")
		},
	}
	db := federationDB(nil, nil)
	innerExec := db.execFn
	db.execFn = func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
		if strings.Contains(sql, "UPDATE federation_sources") {
			dbUpdated = true
		}
		return innerExec(ctx, sql, args...)
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	h.SetLDAPBindPassword("bind-secret")

	body := `{"name":"New name"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/federation/sources/fed-1", bytes.NewReader([]byte(body)))
	req = withChiIDParam(req, "fed-1")
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateFederationSource(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when Keycloak fails, got %d: %s", rec.Code, rec.Body.String())
	}
	if dbUpdated {
		t.Error("the DB row must not be updated when the Keycloak component update failed")
	}
}

func TestDeleteFederationSource_KeycloakFailureKeepsTheDBRow(t *testing.T) {
	dbDeleted := false
	kc := &fakeKeycloak{
		deleteFederationComponentFn: func(ctx context.Context, componentID string) error {
			return errors.New("keycloak down")
		},
	}
	db := federationDB(nil, nil)
	innerExec := db.execFn
	db.execFn = func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
		if strings.Contains(sql, "DELETE FROM federation_sources") {
			dbDeleted = true
		}
		return innerExec(ctx, sql, args...)
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/federation/sources/fed-1", nil)
	req = withChiIDParam(req, "fed-1")
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.DeleteFederationSource(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when Keycloak fails, got %d: %s", rec.Code, rec.Body.String())
	}
	if dbDeleted {
		t.Error("the DB row must survive: deleting it would orphan the Keycloak component")
	}
}

func TestTriggerFederationSync_RejectsUnknownAction(t *testing.T) {
	called := false
	kc := &fakeKeycloak{
		triggerFederationSyncFn: func(ctx context.Context, componentID, action string) error {
			called = true
			return nil
		},
	}
	h := NewHandler(federationDB(nil, nil), kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources/fed-1/sync?action=triggerFullSync%26foo=bar", nil)
	req = withChiIDParam(req, "fed-1")
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.TriggerFederationSync(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown action, got %d: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("Keycloak must not be called with an unvalidated action")
	}
}

func TestGetFederationSource_DBErrorIsNotAnotFound(t *testing.T) {
	h := NewHandler(federationDB(nil, errors.New("connection reset")), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/sources/fed-1", nil)
	req = withChiIDParam(req, "fed-1")
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.GetFederationSource(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for a DB outage, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTestFederationConnection_DoesNotLeakTheUpstreamError(t *testing.T) {
	kc := &fakeKeycloak{
		testLDAPConnectionFn: func(ctx context.Context, componentID, connectionURL, bindDN, bindPassword string) error {
			return errors.New("dial ldap://dc.example.com:389 failed for cn=svc,password=hunter2")
		},
	}
	h := NewHandler(federationDB(nil, nil), kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources/fed-1/test", nil)
	req = withChiIDParam(req, "fed-1")
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.TestFederationConnection(rec, req)

	body := rec.Body.String()
	for _, secret := range []string{"cn=svc", "hunter2", "dc.example.com"} {
		if strings.Contains(body, secret) {
			t.Errorf("the response leaks %q: %s", secret, body)
		}
	}
}

// withChiIDParam attaches an "id" chi route parameter (the handlers read it
// with chi.URLParam, and these tests call the handlers directly).
func withChiIDParam(r *http.Request, id string) *http.Request {
	chiCtx := context.WithValue(r.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{id}},
	})
	return r.WithContext(chiCtx)
}

// ---- #158: the password policy message matches the enforced minimum ----

func TestSetup_PasswordPolicyMessageMatchesEnforcedMinimum(t *testing.T) {
	h := setupTestHandler(t)
	body := `{"adminEmail":"admin@example.com","adminPassword":"short1A","orgName":"Acme"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Setup(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf("at least %d characters", minAdminPasswordLength)
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("the message must state the enforced minimum (%q), got: %s", want, rec.Body.String())
	}
	// The enforced minimum must be the one the message advertises.
	tooShort := strings.Repeat("a", minAdminPasswordLength-2) + "1"
	justLongEnough := strings.Repeat("a", minAdminPasswordLength-1) + "1"
	if !isStrongEnough(justLongEnough) {
		t.Errorf("a password of %d characters must be accepted", minAdminPasswordLength)
	}
	if isStrongEnough(tooShort) {
		t.Errorf("a password shorter than %d characters must be rejected", minAdminPasswordLength)
	}
}

// ---- #172: the analytics limit is clamped at the handler boundary ----

// analyticsFakePool records the limit the snapshotter was asked for.
type analyticsFakePool struct {
	snapshot.DBPool
	limit int
}

func (p *analyticsFakePool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (p *analyticsFakePool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "FROM analytics_snapshots") && len(args) > 1 {
		if l, ok := args[1].(int); ok {
			p.limit = l
		}
	}
	return &fakeQueryRows{}, nil
}

func (p *analyticsFakePool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
}

func TestGetAnalyticsSnapshots_LimitIsClamped(t *testing.T) {
	pool := &analyticsFakePool{}
	h := NewHandler(&fakeDB{}, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetSnapshotter(snapshot.New(pool, zap.NewNop()))

	req := withOrgContext(httptest.NewRequest(http.MethodGet, "/api/v1/analytics/snapshots?limit=100000", nil))
	rec := httptest.NewRecorder()
	h.GetAnalyticsSnapshots(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if pool.limit != maxAnalyticsLimit {
		t.Errorf("limit=%d want the documented maximum %d", pool.limit, maxAnalyticsLimit)
	}
}

// ---- #174: MoveHostToTeam issues ONE query for the whole batch ----

func TestMoveHostToTeam_OwnershipCheckIsBatched(t *testing.T) {
	queries := 0
	db := &fakeDB{
		queryRowFn: ownershipFoundQueryRowFn(nil),
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			queries++
			return deviceOwnershipQueryFn(nil)(ctx, sql, args...)
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	hostIDs := []string{"host-1", "host-2", "host-3", "host-4", "host-5"}
	body, _ := json.Marshal(MoveHostRequest{HostIDs: hostIDs})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/hosts", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if queries != 1 {
		t.Errorf("expected exactly 1 ownership query for %d hosts, got %d", len(hostIDs), queries)
	}
}

// ---- #175: a failed mapping insert deletes the Fleet team ----

func TestCreateTeam_DeletesTheFleetTeamWhenTheMappingFails(t *testing.T) {
	deletedPath := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("FLEET_URL", srv.URL)

	db := &fakeDB{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, errors.New("mapping insert failed")
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	body := `{"name":"Security"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the mapping insert fails, got %d: %s", rec.Code, rec.Body.String())
	}
	if deletedPath == "" {
		t.Error("the orphaned Fleet team must be deleted")
	}
}

// ---- #176: a stored bearer token can be cleared ----

func TestUpsertProvisioningConfig_ClearBearerToken(t *testing.T) {
	var sql string
	db := &fakeDB{
		execFn: func(ctx context.Context, q string, args ...any) (pgconn.CommandTag, error) {
			sql = q
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: ownershipFoundQueryRowFn(nil),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	body := `{"enabled":true,"connectorType":"scim","clearBearerToken":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/"+fixTestUserID+"/provisioning", bytes.NewReader([]byte(body)))
	req = withAppID(req, fixTestUserID)
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertProvisioningConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(sql, "bearer_token_hash = NULL") || !strings.Contains(sql, "bearer_token_enc  = NULL") {
		t.Errorf("the token columns must be cleared, got: %s", sql)
	}
}

// withAppID attaches the {appId} chi route parameter (see access_policy_test.go).
