package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Nerzal/gocloak/v13"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"go.uber.org/zap"
)

func setupTestHandler(t *testing.T) *Handler {
	t.Helper()
	logger := zap.NewNop()
	kc := &fakeKeycloak{}
	fleet := &fakeFleet{}
	return NewHandler(nil, kc, fleet, logger)
}

func TestHealthEndpoint(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	h.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got %v", resp.Success)
	}
}

func TestListUsersNilDB(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	rec := httptest.NewRecorder()

	h.ListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got %v", resp.Success)
	}
}

func TestDeviceCheckNoDB(t *testing.T) {
	h := setupTestHandler(t)

	body := map[string]string{}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-check", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "test-user-123", IsAdmin: true}))
	rec := httptest.NewRecorder()

	h.DeviceCheck(rec, req)

	// Nil DB guard returns 500
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (nil DB), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeviceCheckMissingUserID(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-check", nil)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "test-user-123", IsAdmin: true}))
	rec := httptest.NewRecorder()

	h.DeviceCheck(rec, req)

	// With claims but no body needed (ID from claims), should hit nil-DB guard (500)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (nil DB), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeviceCheckUnauthenticated(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-check", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.DeviceCheck(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 (no JWT claims), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOnboardValidation(t *testing.T) {
	h := setupTestHandler(t)

	tests := []struct {
		name       string
		body       map[string]string
		wantStatus int
	}{
		{
			name:       "missing email",
			body:       map[string]string{"firstName": "Test", "lastName": "User", "department": "Engineering", "role": "Dev"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing firstName",
			body:       map[string]string{"lastName": "User", "email": "test@example.com", "department": "Engineering", "role": "Dev"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "valid request",
			body: map[string]string{
				"firstName": "Test", "lastName": "User", "email": "test@example.com",
				"department": "Engineering", "role": "Developer",
			},
			wantStatus: http.StatusOK, // fakes succeed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.Onboard(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOffboardEndpoint(t *testing.T) {
	h := setupTestHandler(t)

	const testUserID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/offboard/"+testUserID, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor-ID", "admin-test")

	// Inject chi URL param via route context
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{
			Keys:   []string{"userId"},
			Values: []string{testUserID},
		},
	})
	req = req.WithContext(chiCtx)

	rec := httptest.NewRecorder()
	h.Offboard(rec, req)

	// Fakes succeed — expects 200
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (fakes succeed), got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true on 200, got success=false: data=%v", resp.Data)
	}
}

func TestHealthKeycloakWithFakePingError(t *testing.T) {
	logger := zap.NewNop()
	kc := &fakeKeycloak{pingFn: func(ctx context.Context) error { return fmt.Errorf("keycloak down") }}
	fleet := &fakeFleet{}
	h := NewHandler(nil, kc, fleet, logger)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/keycloak", nil)
	rec := httptest.NewRecorder()
	h.HealthKeycloak(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (keycloak down), got %d", rec.Code)
	}
}


func TestOnboardKeycloakFailure(t *testing.T) {
	logger := zap.NewNop()
	kc := &fakeKeycloak{createUserFn: func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
		return nil, fmt.Errorf("keycloak unavailable")
	}}
	fleet := &fakeFleet{}
	h := NewHandler(nil, kc, fleet, logger)
	body := map[string]string{"firstName": "Test", "lastName": "User", "email": "test@test.com", "department": "Engineering", "role": "Dev"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Onboard(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (KC failure), got %d", rec.Code)
	}
}

func TestOnboardEmailFailure(t *testing.T) {
	logger := zap.NewNop()
	kc := &fakeKeycloak{createUserFn: func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
		uid := "kc-user-123"
		user := &gocloak.User{ID: &uid, FirstName: &firstName, LastName: &lastName, Email: &email}
		return &keycloak.CreateUserResult{User: user, PasswordSet: true, ResetEmailSent: false, SetupWarning: "email couldn't be sent"}, nil
	}}
	fleet := &fakeFleet{}
	h := NewHandler(nil, kc, fleet, logger)
	body := map[string]string{"firstName": "Test", "lastName": "User", "email": "test@test.com", "department": "Engineering", "role": "Dev"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Onboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (email fail is non-fatal), got %d", rec.Code)
	}
}

func TestOnboardFleetWarning(t *testing.T) {
	logger := zap.NewNop()
	kc := &fakeKeycloak{createUserFn: func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
		uid := "kc-user-789"
		user := &gocloak.User{ID: &uid, FirstName: &firstName, LastName: &lastName, Email: &email}
		return &keycloak.CreateUserResult{User: user, PasswordSet: true, ResetEmailSent: true}, nil
	}}
	fleet := &fakeFleet{createEnrollmentTokenFn: func(ctx context.Context) (string, error) {
		return "", fmt.Errorf("fleet unavailable")
	}}
	h := NewHandler(nil, kc, fleet, logger)
	body := map[string]string{"firstName": "Test", "lastName": "User", "email": "test@test.com", "department": "Engineering", "role": "Dev"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Onboard(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202 (Fleet failure), got %d", rec.Code)
	}
}

func TestOffboardMissingUserID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/offboard/", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Offboard(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (missing user ID), got %d", rec.Code)
	}
}

func TestOffboardContinuesOnDisableFailure(t *testing.T) {
	logger := zap.NewNop()
	kc := &fakeKeycloak{
		disableUserFn: func(ctx context.Context, userID string) error {
			return fmt.Errorf("keycloak unavailable")
		},
		logoutSessionsFn: func(ctx context.Context, userID string) error {
			return nil
		},
	}
	fleet := &fakeFleet{}
	h := NewHandler(nil, kc, fleet, logger)
	const testUserID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/offboard/"+testUserID, nil)
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"userId"}, Values: []string{testUserID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.Offboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (best-effort), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOnboardValidationWhitespaceEmailNormalized(t *testing.T) {
	logger := zap.NewNop()
	kc := &fakeKeycloak{createUserFn: func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
		if email != "test@example.com" {
			t.Errorf("expected normalized email 'test@example.com', got %q", email)
		}
		uid := "kc-user-email"
		user := &gocloak.User{ID: &uid}
		return &keycloak.CreateUserResult{User: user, PasswordSet: true, ResetEmailSent: true}, nil
	}}
	fleet := &fakeFleet{}
	h := NewHandler(nil, kc, fleet, logger)
	body := map[string]string{
		"firstName": " Test ", "lastName": "User", "email": " TEST@Example.COM ",
		"department": "Engineering", "role": "Dev",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Onboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestOnboardValidationWhitespaceOnly(t *testing.T) {
	h := setupTestHandler(t)
	body := map[string]string{
		"firstName": "   ", "lastName": "User", "email": "test@test.com",
		"department": "Engineering", "role": "Dev",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Onboard(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (whitespace-only firstName), got %d", rec.Code)
	}
}

func TestOnboardEmailWarningResponse(t *testing.T) {
	logger := zap.NewNop()
	kc := &fakeKeycloak{createUserFn: func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
		uid := "kc-user-warn"
		user := &gocloak.User{ID: &uid}
		return &keycloak.CreateUserResult{User: user, PasswordSet: true, ResetEmailSent: false, SetupWarning: "email delivery failed"}, nil
	}}
	fleet := &fakeFleet{}
	h := NewHandler(nil, kc, fleet, logger)
	body := map[string]string{"firstName": "Test", "lastName": "User", "email": "test@test.com", "department": "Engineering", "role": "Dev"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Onboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var resp APIResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "" {
		t.Errorf("expected no error, got %s", resp.Error)
	}
	if resp.Data != nil {
		b, _ := json.Marshal(resp.Data)
		var obResp OnboardResponse
		json.Unmarshal(b, &obResp)
		if !strings.Contains(obResp.Warning, "email delivery failed") {
			t.Fatalf("expected warning to contain 'email delivery failed', got %q", obResp.Warning)
		}
	}
}

func TestCreateAppValidation(t *testing.T) {
	h := setupTestHandler(t)

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "missing name and protocol",
			body:       map[string]interface{}{"redirectURIs": []string{"https://example.com/callback"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid protocol",
			body:       map[string]interface{}{"name": "MyApp", "protocol": "LDAP"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "name too long",
			body:       map[string]interface{}{"name": string(make([]byte, 256)), "protocol": "OIDC"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "OIDC without redirect URIs",
			body:       map[string]interface{}{"name": "MyApp", "protocol": "OIDC"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "too many redirect URIs",
			body:       map[string]interface{}{"name": "MyApp", "protocol": "OIDC", "redirectURIs": make([]string, 21)},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "redirect URI too long",
			body:       map[string]interface{}{"name": "MyApp", "protocol": "OIDC", "redirectURIs": []string{"https://example.com/" + string(make([]byte, 2000))}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid redirect URI scheme",
			body:       map[string]interface{}{"name": "MyApp", "protocol": "OIDC", "redirectURIs": []string{"ftp://example.com"}},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/apps", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.CreateApp(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}
