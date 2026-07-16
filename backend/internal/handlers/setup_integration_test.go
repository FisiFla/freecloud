//go:build integration

package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/db"
)

// setupRaceTestPool returns a fresh, independent *pgxpool.Pool against
// TEST_DATABASE_URL, migrated and ready. Each call simulates a SEPARATE
// backend replica's own connection pool — all instances point at the same
// physical Postgres, which is exactly the topology H3 protects (two
// processes, each with its own in-process sync.Mutex, coordinating only
// through the shared database).
func setupRaceTestPool(t *testing.T) *pgxpool.Pool {
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
	t.Cleanup(pool.Close)
	return pool
}

// raceKeycloak simulates the ONE real Keycloak that both replicas talk to:
// HasAdminUser/CreateAdminUser share state guarded by its own mutex (a real
// Keycloak is a single external service both replicas call — its own
// internal consistency isn't what's under test here). CreateAdminUser sleeps
// briefly to widen the window between the check and the write, so the race
// would be very likely to reproduce if Setup's locking were removed.
type raceKeycloak struct {
	fakeKeycloak
	mu          sync.Mutex
	provisioned bool
	createCalls int32
	createDelay time.Duration
}

func (k *raceKeycloak) HasAdminUser(ctx context.Context) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.provisioned, nil
}

func (k *raceKeycloak) CreateAdminUser(ctx context.Context, email, password string) (string, error) {
	time.Sleep(k.createDelay)
	k.mu.Lock()
	defer k.mu.Unlock()
	k.provisioned = true
	atomic.AddInt32(&k.createCalls, 1)
	return "admin-id", nil
}

// TestSetupConcurrentReplicasSingleWinner is the H3 regression guard: two
// Handler instances, each with its OWN *pgxpool.Pool (so each has its own
// independent in-process setupMu — a stand-in sync.Mutex would NOT serialize
// them) but pointed at the same Postgres, both call Setup for the very first
// admin at the same time. Only one may create the admin; the other must see
// 409. Before the H3 fix (in-process mutex only), this test reproduces the
// double-provisioning bug: both replicas would call CreateAdminUser.
func TestSetupConcurrentReplicasSingleWinner(t *testing.T) {
	pool1 := setupRaceTestPool(t)
	pool2 := setupRaceTestPool(t)

	kc := &raceKeycloak{createDelay: 300 * time.Millisecond}

	h1 := NewHandler(pool1, kc, &fakeFleet{}, zap.NewNop())
	h2 := NewHandler(pool2, kc, &fakeFleet{}, zap.NewNop())

	body := []byte(`{"adminEmail":"admin@example.com","adminPassword":"securepass12","orgName":"Acme Corp"}`)

	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for _, h := range []*Handler{h1, h2} {
		wg.Add(1)
		h := h
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.Setup(rec, req)
			statuses <- rec.Code
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)

	created, conflict := 0, 0
	for code := range statuses {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("unexpected setup status %d", code)
		}
	}
	if created != 1 || conflict != 1 {
		t.Fatalf("expected exactly one 201 and one 409 across replicas, got created=%d conflict=%d (createCalls=%d)",
			created, conflict, atomic.LoadInt32(&kc.createCalls))
	}
	if got := atomic.LoadInt32(&kc.createCalls); got != 1 {
		t.Fatalf("expected CreateAdminUser called exactly once across both replicas, got %d", got)
	}
}
