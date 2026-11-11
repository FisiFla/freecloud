package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
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
	h := NewHandler(&fakeDB{}, &fakeKeycloak{
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
