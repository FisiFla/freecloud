package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func TestSetupStatusUnprovisioned(t *testing.T) {
	h := NewHandler(&fakeDB{}, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got false")
	}
	// Data is map[string]bool{"provisioned": false}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", resp.Data)
	}
	if data["provisioned"] != false {
		t.Fatalf("expected provisioned=false, got %v", data["provisioned"])
	}
}

func TestSetupStatusProvisioned(t *testing.T) {
	h := NewHandler(&fakeDB{}, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return true, nil },
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", resp.Data)
	}
	if data["provisioned"] != true {
		t.Fatalf("expected provisioned=true, got %v", data["provisioned"])
	}
}

func TestSetupCreatesFirstAdmin(t *testing.T) {
	var gotFirst, gotLast string
	called := false
	type execCall struct {
		sql  string
		args []any
	}
	var execs []execCall
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			execs = append(execs, execCall{sql: sql, args: args})
			return pgconn.CommandTag{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
		createAdminUserFn: func(_ context.Context, email, password, firstName, lastName string) (string, error) {
			called = true
			gotFirst, gotLast = firstName, lastName
			return "new-admin-id", nil
		},
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme Corp","adminFirstName":"Ada","adminLastName":"Lovelace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("expected createAdminUserFn to be called")
	}
	if gotFirst != "Ada" || gotLast != "Lovelace" {
		t.Fatalf("expected CreateAdminUser to receive first/last name (Ada, Lovelace), got (%q, %q)", gotFirst, gotLast)
	}
	// The first admin needs a local users row or access-eval step 1 denies
	// every posture/access decision with "user not found or disabled".
	var usersInsert *execCall
	for i := range execs {
		if strings.Contains(execs[i].sql, "INSERT INTO users") {
			usersInsert = &execs[i]
			break
		}
	}
	if usersInsert == nil {
		t.Fatalf("expected an INSERT INTO users exec, got %d execs", len(execs))
	}
	// VALUES ($1,$2,$3,$4,'admin',$5): keycloak_user_id, email, first, last, org_id.
	if len(usersInsert.args) != 5 {
		t.Fatalf("expected 5 args to users INSERT, got %d (%v)", len(usersInsert.args), usersInsert.args)
	}
	if usersInsert.args[0] != "new-admin-id" {
		t.Errorf("arg keycloak_user_id = %v, want new-admin-id", usersInsert.args[0])
	}
	if usersInsert.args[1] != "admin@example.com" {
		t.Errorf("arg email = %v, want admin@example.com", usersInsert.args[1])
	}
	if usersInsert.args[2] != "Ada" || usersInsert.args[3] != "Lovelace" {
		t.Errorf("arg first/last name = %v/%v, want Ada/Lovelace", usersInsert.args[2], usersInsert.args[3])
	}
	if usersInsert.args[4] != middleware.DefaultOrgID {
		t.Errorf("arg org_id = %v, want %s", usersInsert.args[4], middleware.DefaultOrgID)
	}
	if !strings.Contains(usersInsert.sql, "'admin'") {
		t.Errorf("users INSERT must set role 'admin', got SQL: %s", usersInsert.sql)
	}
}

func TestSetupUsersInsertFailureRollsBackKeycloakUser(t *testing.T) {
	// If the local users INSERT fails, Setup must delete the just-created
	// Keycloak admin and return 500 (onboarding.go compensation precedent):
	// a bare 500 would brick provisioning (retry sees HasAdminUser → 409),
	// and a best-effort 201 would leave a silently-broken admin.
	const kcID = "22222222-2222-2222-2222-222222222222"
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "INSERT INTO users") {
				return pgconn.CommandTag{}, errors.New("db down")
			}
			return pgconn.CommandTag{}, nil
		},
	}
	var deleted []string
	kc := &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
		createAdminUserFn: func(_ context.Context, email, password, firstName, lastName string) (string, error) {
			return kcID, nil
		},
		deleteUserFn: func(_ context.Context, userID string) error {
			deleted = append(deleted, userID)
			return nil
		},
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme Corp","adminFirstName":"Ada","adminLastName":"Lovelace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if len(deleted) != 1 || deleted[0] != kcID {
		t.Fatalf("expected the orphaned Keycloak admin %s to be rolled back, got %v", kcID, deleted)
	}
}

func TestSetupRejectsEmptyKeycloakUserID(t *testing.T) {
	// Defensive guard mirroring onboarding.go: a Keycloak create that reports
	// success without a user ID must fail loudly before any DB write, since
	// there is no ID to persist (or roll back).
	execs := 0
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			execs++
			return pgconn.CommandTag{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
		createAdminUserFn: func(_ context.Context, email, password, firstName, lastName string) (string, error) {
			return "", nil
		},
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme Corp","adminFirstName":"Ada","adminLastName":"Lovelace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if execs != 0 {
		t.Fatalf("expected no DB writes without a Keycloak user ID, got %d execs", execs)
	}
}

func TestSetupRejectsSecondCall(t *testing.T) {
	h := NewHandler(&fakeDB{}, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return true, nil },
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme Corp","adminFirstName":"Ada","adminLastName":"Lovelace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// setupTokenHandler builds a Handler whose setup-token gate is configured
// with the given token/required pair (see SetSetupToken).
func setupTokenHandler(token string, required bool, created *bool) http.Handler {
	h := NewHandler(&fakeDB{}, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
		createAdminUserFn: func(_ context.Context, email, password, firstName, lastName string) (string, error) {
			*created = true
			return "new-admin-id", nil
		},
	}, &fakeFleet{}, zap.NewNop())
	h.SetSetupToken(token, required)
	return newTestRouter(h)
}

func postSetup(t *testing.T, r http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSetupTokenConfiguredMissingTokenForbidden(t *testing.T) {
	created := false
	r := setupTokenHandler("s3cret-setup-token", true, &created)
	rec := postSetup(t, r, `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if created {
		t.Fatal("admin must not be created without the setup token")
	}
}

func TestSetupTokenConfiguredWrongTokenForbidden(t *testing.T) {
	created := false
	r := setupTokenHandler("s3cret-setup-token", true, &created)
	rec := postSetup(t, r, `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace","setupToken":"wrong"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if created {
		t.Fatal("admin must not be created with a wrong setup token")
	}
}

func TestSetupTokenConfiguredCorrectTokenProvisions(t *testing.T) {
	created := false
	r := setupTokenHandler("s3cret-setup-token", true, &created)
	rec := postSetup(t, r, `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace","setupToken":"s3cret-setup-token"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !created {
		t.Fatal("expected admin creation with the correct setup token")
	}
}

func TestSetupTokenRequiredButUnconfiguredFailsClosed(t *testing.T) {
	created := false
	r := setupTokenHandler("", true, &created)
	rec := postSetup(t, r, `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace","setupToken":"anything"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if created {
		t.Fatal("admin must not be created when the server has no setup token configured")
	}
}

func TestSetupStatusReportsSetupTokenRequired(t *testing.T) {
	cases := []struct {
		name     string
		token    string
		required bool
		want     bool
	}{
		{name: "token configured", token: "s3cret", required: true, want: true},
		{name: "required but unconfigured", token: "", required: true, want: true},
		{name: "dev stack", token: "", required: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(&fakeDB{}, &fakeKeycloak{
				hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
			}, &fakeFleet{}, zap.NewNop())
			h.SetSetupToken(tc.token, tc.required)
			r := newTestRouter(h)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
			}
			var resp APIResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			data, ok := resp.Data.(map[string]interface{})
			if !ok {
				t.Fatalf("expected data to be a map, got %T", resp.Data)
			}
			if data["setupTokenRequired"] != tc.want {
				t.Fatalf("expected setupTokenRequired=%v, got %v", tc.want, data["setupTokenRequired"])
			}
			if data["provisioned"] != false {
				t.Fatalf("expected provisioned=false, got %v", data["provisioned"])
			}
		})
	}
}

func TestSetupValidation(t *testing.T) {
	h := NewHandler(&fakeDB{}, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	cases := []struct {
		name string
		body string
		want string // expected field in errors
	}{
		{
			name: "missing orgName",
			body: `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"","adminFirstName":"Ada","adminLastName":"Lovelace"}`,
			want: "orgName",
		},
		{
			name: "bad email",
			body: `{"adminEmail":"not-an-email","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace"}`,
			want: "adminEmail",
		},
		{
			name: "short password",
			body: `{"adminEmail":"admin@example.com","adminPassword":"short","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace"}`,
			want: "adminPassword",
		},
		{
			name: "missing adminFirstName",
			body: `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminLastName":"Lovelace"}`,
			want: "adminFirstName",
		},
		{
			name: "blank adminLastName",
			body: `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"   "}`,
			want: "adminLastName",
		},
		{
			name: "adminFirstName too long",
			body: `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"` + strings.Repeat("a", 101) + `","adminLastName":"Lovelace"}`,
			want: "adminFirstName",
		},
		{
			name: "adminLastName too long",
			body: `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"` + strings.Repeat("b", 101) + `"}`,
			want: "adminLastName",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body: %s)", rec.Code, rec.Body.String())
			}
			var resp ValidationErrorsResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Success {
				t.Fatal("expected success=false")
			}
			found := false
			for _, e := range resp.Errors {
				if e.Field == tc.want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected validation error for field %q, got: %+v", tc.want, resp.Errors)
			}
		})
	}
}
