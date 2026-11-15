// Failing-first tests for the SMTP config hardening (security lane L2,
// issues 5, 8): host changes require the password, host/port validation,
// generic test-send failures, and no password hash writes.
package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// smtpRowQueryFn serves one smtp_config row for any non-ownership query.
func smtpRowQueryFn(host, port, username, from string, pwEnc *string) func(context.Context, string, ...any) pgx.Row {
	return ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
		return fakeRow{scanFn: func(dest ...any) error {
			*(dest[0].(*string)) = host
			*(dest[1].(*string)) = port
			*(dest[2].(*string)) = username
			*(dest[3].(*string)) = from
			if len(dest) > 4 {
				*(dest[4].(**string)) = pwEnc
			}
			if len(dest) > 5 {
				// Legacy hash column position: always NULL now.
				*(dest[5].(**string)) = nil
			}
			return nil
		}}
	})
}

func TestUpsertSMTPConfig_HostChangeRequiresPassword(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	storedPw := "c3RvcmVkLXB3" // base64("stored-pw") stand-in

	newHandler := func() (*Handler, *fakeKeycloak) {
		db := &fakeDB{
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
			queryRowFn: smtpRowQueryFn("smtp-old.example.com", "587", "user", "from@example.com", &storedPw),
		}
		kc := &fakeKeycloak{}
		return NewHandler(db, kc, &fakeFleet{}, zap.NewNop()), kc
	}

	// Same host, no password: the stored password stays — allowed.
	h, _ := newHandler()
	body := `{"host":"smtp-old.example.com","port":"587","username":"user","fromAddress":"from@example.com"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertSMTPConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-host update without password: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Different host, no password, stored password present: refuse.
	h, _ = newHandler()
	body = `{"host":"smtp-new.example.com","port":"587","username":"user","fromAddress":"from@example.com"}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.UpsertSMTPConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("host change without password: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "password") {
		t.Fatalf("the 400 must explain the password is required: %s", rec.Body.String())
	}

	// Different host WITH the password: allowed.
	h, _ = newHandler()
	body = `{"host":"smtp-new.example.com","port":"587","username":"user","password":"fresh-pw","fromAddress":"from@example.com"}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.UpsertSMTPConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("host change with password: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertSMTPConfig_ValidatesHostAndPort(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	cases := []struct {
		name string
		host string
		port string
		want int
	}{
		{"valid relay", "smtp.example.com", "587", http.StatusOK},
		{"single label dev host", "mailhog", "1025", http.StatusOK},
		{"private relay", "10.0.0.5", "25", http.StatusOK},
		{"empty host clears", "", "587", http.StatusOK},
		{"port out of range", "smtp.example.com", "99999", http.StatusBadRequest},
		{"port zero", "smtp.example.com", "0", http.StatusBadRequest},
		{"port garbage", "smtp.example.com", "smtp", http.StatusBadRequest},
		{"host with userinfo", "user@smtp.example.com", "587", http.StatusBadRequest},
		{"host with path", "smtp.example.com/evil", "587", http.StatusBadRequest},
		{"host with spaces", "smtp exam ple.com", "587", http.StatusBadRequest},
		{"host with scheme", "smtp://smtp.example.com", "587", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeDB{
				execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
					return pgconn.CommandTag{}, nil
				},
				queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
					return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
				}),
			}
			h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
			body, _ := json.Marshal(map[string]string{
				"host": tc.host, "port": tc.port, "username": "", "fromAddress": "from@example.com",
			})
			req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", bytes.NewReader(body))
			req = withOrgContext(req)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.UpsertSMTPConfig(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("host=%q port=%q: expected %d, got %d: %s", tc.host, tc.port, tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUpsertSMTPConfig_BlockedHostInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	setMasterKeys(t, testMasterKeyB64(t, 20), "")
	for _, host := range []string{"127.0.0.1", "169.254.169.254", "[::1]"} {
		db := &fakeDB{
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
			queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}),
		}
		h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
		body := `{"host":"` + host + `","port":"587","username":"","fromAddress":"from@example.com"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", bytes.NewReader([]byte(body)))
		req = withOrgContext(req)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.UpsertSMTPConfig(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("host=%q: expected 400, got %d: %s", host, rec.Code, rec.Body.String())
		}
	}
	// A corporate relay on a private address stays allowed.
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body := `{"host":"10.0.0.5","port":"25","username":"","fromAddress":"from@example.com"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertSMTPConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("private relay: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTestSMTPEmail_GenericFailure(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	// Unroutable discard port: the send must fail, but the response must not
	// expose the transport error verbatim.
	db := &fakeDB{
		queryRowFn: smtpRowQueryFn("127.0.0.1", "1", "", "from@example.com", nil),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	body := `{"to":"to@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/smtp/test", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.TestSMTPEmail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with a sent:false payload, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "refused") || strings.Contains(rec.Body.String(), "127.0.0.1") {
		t.Fatalf("the failure must be generic, got: %s", rec.Body.String())
	}
	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, _ := json.Marshal(resp.Data)
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["sent"] != false {
		t.Fatalf("sent=%v want false", payload["sent"])
	}
}

func TestUpsertSMTPConfig_WritesNoPasswordHash(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	const password = "smtp-pw-123"
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(password)))
	var execArgs [][]any
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
			execArgs = append(execArgs, args)
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	body := `{"host":"smtp.example.com","port":"587","username":"user","password":"` + password + `","fromAddress":"from@example.com"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/smtp", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertSMTPConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, args := range execArgs {
		for _, arg := range args {
			if s, ok := arg.(*string); ok && s != nil && *s == wantHash {
				t.Fatalf("a write persisted the unsalted password hash %q", wantHash)
			}
			if s, ok := arg.(string); ok && s == wantHash {
				t.Fatalf("a write persisted the unsalted password hash %q", wantHash)
			}
		}
	}
}
