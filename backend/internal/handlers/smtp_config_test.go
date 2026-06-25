package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// TestGetSMTPConfig_NoDB verifies 500 when db is nil.
func TestGetSMTPConfig_NoDB(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/smtp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestGetSMTPConfig_NoRow returns default response when row is absent.
func TestGetSMTPConfig_NoRow(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/smtp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool               `json:"success"`
		Data    SMTPConfigResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Port != "587" {
		t.Errorf("default port: want 587, got %q", resp.Data.Port)
	}
}

// TestGetSMTPConfig_Row verifies that GetSMTPConfig returns stored values
// without revealing the password.
func TestGetSMTPConfig_Row(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	hash := "somehash"
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "smtp.example.com"
				*(dest[1].(*string)) = "587"
				*(dest[2].(*string)) = "user@example.com"
				*(dest[3].(*string)) = "from@example.com"
				*(dest[4].(**string)) = &hash
				*(dest[5].(*time.Time)) = now
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/smtp", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool               `json:"success"`
		Data    SMTPConfigResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Host != "smtp.example.com" {
		t.Errorf("host: want smtp.example.com, got %q", resp.Data.Host)
	}
	if !resp.Data.PasswordConfigured {
		t.Error("expected passwordConfigured=true")
	}
	// Password must never appear in the response.
	if strings.Contains(rec.Body.String(), "password") && strings.Contains(rec.Body.String(), "plain") {
		t.Error("password may be leaking in response body")
	}
}

// TestUpsertSMTPConfig_SavesConfig verifies persistence and Keycloak sync.
func TestUpsertSMTPConfig_SavesConfig(t *testing.T) {
	var execSQL string
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			execSQL = sql
			return pgconn.CommandTag{}, nil
		},
	}
	var kcSMTP keycloak.SMTPConfig
	kc := &fakeKeycloak{
		updateRealmSMTPFn: func(_ context.Context, cfg keycloak.SMTPConfig) error {
			kcSMTP = cfg
			return nil
		},
	}
	h := NewHandler(db, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	body := `{"host":"smtp.example.com","port":"587","username":"user@example.com","fromAddress":"from@example.com"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(execSQL, "smtp_config") {
		t.Errorf("expected SQL to touch smtp_config, got: %s", execSQL)
	}
	if kcSMTP.Host != "smtp.example.com" {
		t.Errorf("keycloak smtp host: want smtp.example.com, got %q", kcSMTP.Host)
	}
	if kcSMTP.From != "from@example.com" {
		t.Errorf("keycloak smtp from: want from@example.com, got %q", kcSMTP.From)
	}
}

// TestUpsertSMTPConfig_WithPassword verifies that a password is encrypted.
func TestUpsertSMTPConfig_WithPassword(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	var encryptedPW bool
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			// If password_enc is in the SQL, we know an encryption path was used.
			if strings.Contains(sql, "password_enc") {
				encryptedPW = true
			}
			return pgconn.CommandTag{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	body := `{"host":"smtp.example.com","port":"587","username":"u","password":"s3cr3t","fromAddress":"f@example.com"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !encryptedPW {
		t.Error("expected encrypted password path to be taken")
	}
}

// TestUpsertSMTPConfig_BadJSON verifies 400 on malformed body.
func TestUpsertSMTPConfig_BadJSON(t *testing.T) {
	db := &fakeDB{}
	h := NewHandler(db, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestTestSMTPEmail_NoTo verifies 400 when the 'to' field is missing.
func TestTestSMTPEmail_NoTo(t *testing.T) {
	db := &fakeDB{}
	h := NewHandler(db, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/smtp/test", strings.NewReader(`{"to":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTestSMTPEmail_NotConfigured verifies that missing SMTP config returns sent=false.
func TestTestSMTPEmail_NotConfigured(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/smtp/test", strings.NewReader(`{"to":"user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Sent  bool   `json:"sent"`
			Error string `json:"error,omitempty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Sent {
		t.Error("expected sent=false when SMTP is not configured")
	}
}

// TestTestSMTPEmail_NoHost verifies that an empty host returns sent=false.
func TestTestSMTPEmail_NoHost(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "" // empty host
				*(dest[1].(*string)) = "587"
				*(dest[2].(*string)) = ""
				*(dest[3].(*string)) = ""
				*(dest[4].(**string)) = nil
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/smtp/test", strings.NewReader(`{"to":"user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Sent bool `json:"sent"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Sent {
		t.Error("expected sent=false when host is not configured")
	}
}

// TestSMTPConfig_PermissionGated verifies that non-super-admin gets 403.
func TestSMTPConfig_PermissionGated(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, nil, zap.NewNop())
	r := newRoleTestRouter(h, middleware.RoleEndUser)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/settings/smtp"},
		{http.MethodPut, "/api/v1/settings/smtp"},
		{http.MethodPost, "/api/v1/settings/smtp/test"},
	}
	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: expected 403, got %d", rt.method, rt.path, rec.Code)
		}
	}
}
