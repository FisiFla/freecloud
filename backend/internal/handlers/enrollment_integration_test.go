//go:build integration

package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/db"
)

func enrollTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	trunc := `TRUNCATE enrollment_tokens, users_devices_mapping, app_assignments, connected_apps, audit_logs, devices, users RESTART IDENTITY CASCADE`
	_, _ = pool.Exec(ctx, trunc)
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(c, trunc)
		pool.Close()
	})
	return pool
}

// TestEnrollmentLoopEndToEnd proves the full device loop: an enrollment token
// linked to a user, a Fleet callback that records the device + mapping and
// consumes the token, and an offboard that then wipes exactly that device.
func TestEnrollmentLoopEndToEnd(t *testing.T) {
	pool := enrollTestPool(t)
	ctx := context.Background()

	const secret = "webhook-secret"
	const token = "enrolltoken-abc"
	userID := uuid.New().String()

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (keycloak_user_id, email, first_name, last_name) VALUES ($1,$2,$3,$4)`,
		userID, "e2e@example.com", "E2E", "User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO enrollment_tokens (token_hash, user_id, expires_at) VALUES ($1,$2, NOW() + INTERVAL '1 hour')`,
		enrollmentTokenHash(token), userID); err != nil {
		t.Fatalf("insert enrollment token: %v", err)
	}

	var wiped []string
	fleet := &fakeFleet{issueRemoteWipeFn: func(ctx context.Context, hostID string) error {
		wiped = append(wiped, hostID)
		return nil
	}}
	h := NewHandler(pool, &fakeKeycloak{}, fleet, zap.NewNop())
	h.SetFleetWebhookSecret(secret)

	// 1) FleetDM calls the enrollment callback for host "host-e2e".
	body := []byte(`{"enrollment_token":"enrolltoken-abc","host_id":"host-e2e","hostname":"e2e.local","os_version":"macOS 15"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users_devices_mapping WHERE user_id=$1 AND device_id=$2`,
		userID, "host-e2e").Scan(&n); err != nil {
		t.Fatalf("count mapping: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 device mapping row, got %d", n)
	}
	var used *time.Time
	if err := pool.QueryRow(ctx, `SELECT used_at FROM enrollment_tokens WHERE token_hash=$1`, enrollmentTokenHash(token)).Scan(&used); err != nil {
		t.Fatalf("lookup token used_at: %v", err)
	}
	if used == nil {
		t.Error("expected enrollment token to be marked used")
	}

	// Replaying the same token must now be rejected (already used).
	replay := httptest.NewRecorder()
	rreq := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	rreq.Header.Set("X-Fleet-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	h.FleetEnrollmentCallback(replay, rreq)
	if replay.Code != http.StatusConflict {
		t.Errorf("expected 409 on token replay, got %d", replay.Code)
	}

	// 2) Offboard the user -> the enrolled device must be wiped.
	offReq := httptest.NewRequest(http.MethodPost, "/api/v1/offboard/"+userID, nil)
	chiCtx := context.WithValue(offReq.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"userId"}, Values: []string{userID}},
	})
	offReq = offReq.WithContext(chiCtx)
	// The user row above was inserted directly via SQL with no org_id, so it
	// took Migration043's Default Organization default -- resolve the same
	// org here so Offboard's org-ownership guard (Epic C) doesn't 403 it.
	offReq = withOrgContext(offReq)
	offRec := httptest.NewRecorder()
	h.Offboard(offRec, offReq)
	if offRec.Code != http.StatusOK {
		t.Fatalf("offboard: expected 200, got %d: %s", offRec.Code, offRec.Body.String())
	}
	if len(wiped) != 1 || wiped[0] != "host-e2e" {
		t.Errorf("expected host-e2e to be wiped exactly once, got %v", wiped)
	}
}

func TestEnrollmentTokenConcurrentCallbacksSingleWinner(t *testing.T) {
	pool := enrollTestPool(t)
	ctx := context.Background()

	const secret = "webhook-secret"
	const token = "race-token"
	userID := uuid.New().String()

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (keycloak_user_id, email, first_name, last_name) VALUES ($1,$2,$3,$4)`,
		userID, "race@example.com", "Race", "User"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO enrollment_tokens (token_hash, user_id, expires_at) VALUES ($1,$2, NOW() + INTERVAL '1 hour')`,
		enrollmentTokenHash(token), userID); err != nil {
		t.Fatalf("insert enrollment token: %v", err)
	}

	h := NewHandler(pool, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"race-token","host_id":"host-race","hostname":"race.local","os_version":"macOS 15"}`)
	sig := "sha256=" + hex.EncodeToString(func() []byte {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return mac.Sum(nil)
	}())

	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
			req.Header.Set("X-Fleet-Signature", sig)
			rec := httptest.NewRecorder()
			h.FleetEnrollmentCallback(rec, req)
			statuses <- rec.Code
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)

	ok, conflict := 0, 0
	for code := range statuses {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("unexpected callback status %d", code)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("expected exactly one 200 and one 409, got ok=%d conflict=%d", ok, conflict)
	}
}
