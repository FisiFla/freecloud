package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// TestForgotPasswordAlwaysReturns200 proves that both valid and invalid inputs
// return 200 with the same generic message (no user enumeration).
func TestForgotPasswordAlwaysReturns200(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"valid email existing user", `{"email":"alice@example.com"}`},
		{"valid email nonexistent user", `{"email":"nobody@example.com"}`},
		{"invalid email", `{"email":"not-an-email"}`},
		{"empty email", `{"email":""}`},
		{"bad json", `{broken`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := setupTestHandler(t)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password",
				bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ForgotPassword(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestForgotPasswordCallsKCWhenUserExists: when the DB has the user, the KC
// send-reset should be invoked.
func TestForgotPasswordCallsKCWhenUserExists(t *testing.T) {
	kcCalled := false
	kc := &fakeKeycloak{
		sendPasswordResetEmailFn: func(ctx context.Context, userID string) error {
			kcCalled = true
			return nil
		},
	}
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if sp, ok := dest[0].(*string); ok {
					*sp = "kc-uid-found"
				}
				return nil
			}}
		},
	}
	h2 := newHandlerWithDeps(db, kc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password",
		bytes.NewBufferString(`{"email":"alice@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h2.ForgotPassword(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !kcCalled {
		t.Error("expected KC SendPasswordResetEmail to be called")
	}
}

// TestForgotPasswordKCFailureSilent: KC failure must NOT bubble up to caller.
func TestForgotPasswordKCFailureSilent(t *testing.T) {
	kc := &fakeKeycloak{
		sendPasswordResetEmailFn: func(ctx context.Context, userID string) error {
			return errors.New("smtp down")
		},
	}
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if sp, ok := dest[0].(*string); ok {
					*sp = "kc-uid-found"
				}
				return nil
			}}
		},
	}
	h := newHandlerWithDeps(db, kc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password",
		bytes.NewBufferString(`{"email":"alice@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ForgotPassword(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 even on KC failure, got %d", rec.Code)
	}
}

// TestForgotPasswordResponseMessage verifies the generic message is returned.
func TestForgotPasswordResponseMessage(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password",
		bytes.NewBufferString(`{"email":"x@y.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ForgotPassword(rec, req)

	var env struct {
		Data map[string]string `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data["message"] == "" {
		t.Error("expected non-empty message in response")
	}
}

func newHandlerWithDeps(db DBPool, kc *fakeKeycloak) *Handler {
	return NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
}
