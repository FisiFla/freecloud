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
	called := false
	h := NewHandler(&fakeDB{}, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return false, nil },
		createAdminUserFn: func(_ context.Context, email, password string) (string, error) {
			called = true
			return "new-admin-id", nil
		},
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme Corp"}`
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
}

func TestSetupRejectsSecondCall(t *testing.T) {
	h := NewHandler(&fakeDB{}, &fakeKeycloak{
		hasAdminUserFn: func(_ context.Context) (bool, error) { return true, nil },
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme Corp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", rec.Code, rec.Body.String())
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
			body: `{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":""}`,
			want: "orgName",
		},
		{
			name: "bad email",
			body: `{"adminEmail":"not-an-email","adminPassword":"securepass12","orgName":"Acme"}`,
			want: "adminEmail",
		},
		{
			name: "short password",
			body: `{"adminEmail":"admin@example.com","adminPassword":"short","orgName":"Acme"}`,
			want: "adminPassword",
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
