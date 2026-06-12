package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"go.uber.org/zap"
)

func setupTestHandler(t *testing.T) *Handler {
	t.Helper()
	logger := zap.NewNop()
	kcClient := keycloak.NewClient(
		os.Getenv("KEYCLOAK_URL"),
		os.Getenv("KEYCLOAK_CLIENT_ID"),
		os.Getenv("KEYCLOAK_CLIENT_SECRET"),
		os.Getenv("KEYCLOAK_REALM"),
	)
	fleetClient := fleet.NewClient(
		os.Getenv("FLEET_URL"),
		os.Getenv("FLEET_API_TOKEN"),
	)
	return NewHandler(nil, kcClient, fleetClient, logger)
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

func TestDeviceCheckEndpoint(t *testing.T) {
	h := setupTestHandler(t)

	body := map[string]string{"keycloakUserId": "test-user-123"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/device-check", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.DeviceCheck(rec, req)

	if rec.Code != http.StatusOK && rec.Code != http.StatusForbidden {
		t.Errorf("expected 200 or 403, got %d: %s", rec.Code, rec.Body.String())
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
			wantStatus: http.StatusOK,
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/offboard/test-user-456", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor-ID", "admin-test")
	rec := httptest.NewRecorder()

	h.Offboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got %v: error=%v", resp.Success, resp.Error)
	}
}
