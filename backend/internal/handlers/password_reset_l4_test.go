package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Security-fix lane L4, item 6: Keycloak must not be called synchronously
// only when the email exists — that response-time difference is a user
// enumeration oracle. The handler responds with the fixed generic body
// first and does the existence-dependent work afterwards, so response time
// is independent of existence.

// The response for an existing user returns while the Keycloak send is
// still in flight, and the send completes afterwards.
func TestL4ForgotPasswordRespondsBeforeKeycloakSend(t *testing.T) {
	var kcCalled atomic.Bool
	var kcDone atomic.Bool
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "kc-user-id"
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{
		sendPasswordResetEmailFn: func(ctx context.Context, kcUserID string) error {
			kcCalled.Store(true)
			time.Sleep(300 * time.Millisecond)
			kcDone.Store(true)
			return nil
		},
	}, &fakeFleet{}, zap.NewNop())
	r := newTestRouter(h)

	body := `{"email":"user@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	start := time.Now()
	r.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// The essence of the fix: with a 300ms Keycloak round trip, a
	// synchronous handler could not have responded this fast.
	if elapsed >= 300*time.Millisecond {
		t.Errorf("response took %v with a 300ms Keycloak send: the send must not block the response", elapsed)
	}
	if kcDone.Load() {
		t.Error("Keycloak send completed before the response returned: the send must happen after responding")
	}
	// ... but the legitimate send still happens.
	deadline := time.Now().Add(5 * time.Second)
	for !kcDone.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !kcDone.Load() {
		t.Error("expected the Keycloak password-reset send to complete after responding")
	}
}

// Existing and non-existing addresses get the same body in the same
// (non-Keycloak-bound) time: neither response waits on the user lookup.
func TestL4ForgotPasswordTimingIndependentOfExistence(t *testing.T) {
	serve := func(t *testing.T, emailExists bool) (time.Duration, string) {
		t.Helper()
		db := &fakeDB{
			queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error {
					if !emailExists {
						return pgx.ErrNoRows
					}
					*(dest[0].(*string)) = "kc-user-id"
					return nil
				}}
			},
		}
		h := NewHandler(db, &fakeKeycloak{
			sendPasswordResetEmailFn: func(ctx context.Context, kcUserID string) error {
				time.Sleep(300 * time.Millisecond)
				return nil
			},
		}, &fakeFleet{}, zap.NewNop())
		r := newTestRouter(h)
		body := `{"email":"user@example.com"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		start := time.Now()
		r.ServeHTTP(rec, req)
		return time.Since(start), rec.Body.String()
	}

	existsDur, existsBody := serve(t, true)
	missingDur, missingBody := serve(t, false)

	if existsBody != missingBody {
		t.Errorf("response bodies differ:\nexists:  %s\nmissing: %s", existsBody, missingBody)
	}
	var resp APIResponse
	if err := json.NewDecoder(strings.NewReader(existsBody)).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got false (%s)", existsBody)
	}
	// Both responses must return without waiting for the 300ms Keycloak
	// round trip (generous bound: CI slowness must not flake this).
	for name, d := range map[string]time.Duration{"exists": existsDur, "missing": missingDur} {
		if d >= 300*time.Millisecond {
			t.Errorf("%s response took %v: neither path may wait on Keycloak", name, d)
		}
	}
}
