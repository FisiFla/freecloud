package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// Security-fix lane L4, items 4-5: the provisioned check must mean "an admin
// exists" (local users row with the admin role, or a Keycloak "admin" role
// member — never "any user exists"), and the admin email must be normalised
// exactly like onboarding.

// A SCIM-provisioned or demo user created before first-run setup must not
// brick provisioning: with no local admin row and no Keycloak admin, setup
// stays open. (The Keycloak side — role membership instead of any-user —
// is covered in the keycloak package; here we pin the handler wiring: a
// local admin row provisions even when Keycloak reports no admin.)
func TestL4SetupStatusProvisionedByLocalAdminRow(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "role = 'admin'") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return errors.New("unexpected query") }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{
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
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", resp.Data)
	}
	if data["provisioned"] != true {
		t.Fatalf("expected provisioned=true from the local admin row, got %v", data["provisioned"])
	}
}

func TestL4SetupConflictWhenLocalAdminRowExists(t *testing.T) {
	created := false
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "role = 'admin'") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return errors.New("unexpected query") }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
		createAdminUserFn: func(_ context.Context, email, password, firstName, lastName string) (string, error) {
			created = true
			return "late-id", nil
		},
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if created {
		t.Fatal("no second admin may be created once a local admin row exists")
	}
}

// A local-DB error during the provisioned check must fail closed (500), never
// read as "un-provisioned" (which would reopen first-admin creation).
func TestL4SetupStatusLocalDBErrorFailsClosed(t *testing.T) {
	dbErr := errors.New("db unavailable")
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return dbErr }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on a provisioned-check DB error, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestL4SetupLocalDBErrorFailsClosed(t *testing.T) {
	created := false
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return errors.New("db unavailable") }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
		createAdminUserFn: func(_ context.Context, email, password, firstName, lastName string) (string, error) {
			created = true
			return "late-id", nil
		},
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on a provisioned-check DB error, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if created {
		t.Fatal("no admin may be created when the provisioned check errored")
	}
}

// Existing deployments whose first admin has no local users row must still
// count as provisioned via the Keycloak role-membership fallback.
func TestL4SetupStatusKeycloakFallbackWithoutLocalRow(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			// No local admin row (pgx.ErrNoRows equivalent for the default
			// fake path is covered by TestSetupStatusProvisioned; here the
			// fallback is pinned explicitly).
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{
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
		t.Fatalf("expected provisioned=true via the Keycloak fallback, got %v", data["provisioned"])
	}
}

// The admin email is normalised identically to onboarding
// (lowercase + trim) before validation, Keycloak creation and persistence.
func TestL4SetupNormalisesAdminEmail(t *testing.T) {
	var kcEmail string
	var persistedEmail any
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "INSERT INTO users") {
				persistedEmail = args[1]
			}
			return pgconn.CommandTag{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
		createAdminUserFn: func(_ context.Context, email, password, firstName, lastName string) (string, error) {
			kcEmail = email
			return "new-admin-id", nil
		},
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"  Admin@Example.COM  ","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if kcEmail != "admin@example.com" {
		t.Errorf("Keycloak admin email = %q, want %q", kcEmail, "admin@example.com")
	}
	if persistedEmail != "admin@example.com" {
		t.Errorf("persisted admin email = %v, want %q", persistedEmail, "admin@example.com")
	}
}

// The setup-token comparison must not leak the token length: a wrong token
// of a different length is still just a 403 (pins the hash-then-compare
// wiring; the helper itself is unit-tested in token_compare_test.go).
func TestL4SetupTokenWrongLengthForbidden(t *testing.T) {
	created := false
	r := setupTokenHandler("s3cret-setup-token", true, &created)
	rec := postSetup(t, r, `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme","adminFirstName":"Ada","adminLastName":"Lovelace","setupToken":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if created {
		t.Fatal("admin must not be created with a wrong setup token")
	}
}
