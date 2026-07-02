// Package reconcile implements the Keycloak↔DB drift-detection job (FCEXP-21).
//
// The job runs on a configurable ticker, compares the set of users in Keycloak
// against the local PostgreSQL users table, and reports any discrepancies as:
//
//   - orphans_in_keycloak: users present in Keycloak but absent from the DB
//   - orphans_in_db: users present in the DB but absent from Keycloak
//
// Both counts are exposed as a Prometheus gauge so they alert on drift without
// auto-remediating (report-only by default).
package reconcile

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/notify"
)

// DBPool is the subset of *pgxpool.Pool the reconciler uses. *pgxpool.Pool
// satisfies it directly.
type DBPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	// Exec and QueryRow are required by the DBPool contract shared with handlers;
	// the reconciler does not use them but the interface must be satisfiable by
	// the same pool passed to handlers.
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	driftOrphansInKeycloak = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "freecloud_reconcile_orphans_in_keycloak",
		Help: "Users present in Keycloak but absent from the local DB (last reconcile run).",
	})
	driftOrphansInDB = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "freecloud_reconcile_orphans_in_db",
		Help: "Users present in the local DB but absent from Keycloak (last reconcile run).",
	})
	lastRunTimestamp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "freecloud_reconcile_last_run_timestamp_seconds",
		Help: "Unix timestamp of the last reconciliation run.",
	})
)

// DriftResult holds the outcome of a single reconciliation run.
type DriftResult struct {
	OrphansInKeycloak []string // Keycloak user IDs with no local DB row
	OrphansInDB       []string // DB keycloak_user_ids with no Keycloak counterpart
}

// Reconciler compares Keycloak users against the local DB.
type Reconciler struct {
	kc       keycloak.KeycloakClientInterface
	pool     DBPool
	logger   *zap.Logger
	notifier notify.Notifier
	// isLeader gates the ticker-driven run (B3, v1.7 HA): nil means "always
	// run" (single-instance / no leader election wired up), matching prior
	// behavior exactly. When set, only the leader's tick actually reconciles.
	isLeader func() bool
}

// New creates a Reconciler.
func New(kc keycloak.KeycloakClientInterface, pool DBPool, logger *zap.Logger) *Reconciler {
	return &Reconciler{kc: kc, pool: pool, logger: logger}
}

// SetNotifier wires the event notifier into the reconciler (D1 / FCEX2-17).
func (r *Reconciler) SetNotifier(n notify.Notifier) {
	r.notifier = n
}

// SetLeaderGate wires a leader-election check (B3, v1.7 HA) so the ticker in
// Start only performs a reconciliation pass on the instance that currently
// holds leadership. Manual calls to Run (e.g. the admin drift-report
// endpoint) are never gated — an operator explicitly asking for a live check
// should always get one, on any instance.
func (r *Reconciler) SetLeaderGate(isLeader func() bool) {
	r.isLeader = isLeader
}

// Run performs one reconciliation pass and returns the drift. It never mutates
// either system — detection only.
func (r *Reconciler) Run(ctx context.Context) (DriftResult, error) {
	// Fetch Keycloak users.
	kcUsers, err := r.kc.ListUsers(ctx)
	if err != nil {
		return DriftResult{}, err
	}
	kcByID := make(map[string]struct{}, len(kcUsers))
	for _, u := range kcUsers {
		if u.ID != nil && *u.ID != "" {
			kcByID[*u.ID] = struct{}{}
		}
	}

	// Fetch local DB user IDs.
	rows, err := r.pool.Query(ctx, `SELECT keycloak_user_id::TEXT FROM users`)
	if err != nil {
		return DriftResult{}, err
	}
	defer rows.Close()

	dbIDs := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			r.logger.Warn("reconcile: failed to scan user id", zap.Error(err))
			continue
		}
		dbIDs[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return DriftResult{}, err
	}

	var result DriftResult

	// Keycloak users absent from DB.
	for id := range kcByID {
		if _, ok := dbIDs[id]; !ok {
			result.OrphansInKeycloak = append(result.OrphansInKeycloak, id)
		}
	}

	// DB users absent from Keycloak.
	for id := range dbIDs {
		if _, ok := kcByID[id]; !ok {
			result.OrphansInDB = append(result.OrphansInDB, id)
		}
	}

	return result, nil
}

// Start launches the background reconciliation ticker. It returns immediately;
// the goroutine stops when ctx is cancelled.
func (r *Reconciler) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		r.logger.Info("reconciliation job disabled (RECONCILE_INTERVAL=0)")
		return
	}
	r.logger.Info("reconciliation job started", zap.Duration("interval", interval))
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				r.logger.Info("reconciliation job stopped")
				return
			case <-ticker.C:
				if r.isLeader != nil && !r.isLeader() {
					continue
				}
				r.runAndRecord(ctx)
			}
		}
	}()
}

// runAndRecord runs a reconciliation pass and updates Prometheus gauges.
func (r *Reconciler) runAndRecord(ctx context.Context) {
	result, err := r.Run(ctx)
	if err != nil {
		r.logger.Warn("reconciliation run failed", zap.Error(err))
		return
	}
	driftOrphansInKeycloak.Set(float64(len(result.OrphansInKeycloak)))
	driftOrphansInDB.Set(float64(len(result.OrphansInDB)))
	lastRunTimestamp.SetToCurrentTime()

	if len(result.OrphansInKeycloak) > 0 || len(result.OrphansInDB) > 0 {
		r.logger.Warn("reconciliation drift detected",
			zap.Int("orphans_in_keycloak", len(result.OrphansInKeycloak)),
			zap.Int("orphans_in_db", len(result.OrphansInDB)),
			zap.Strings("orphans_in_keycloak_ids", result.OrphansInKeycloak),
			zap.Strings("orphans_in_db_ids", result.OrphansInDB),
		)
		// Fire drift notification (fail-open: background goroutine).
		if r.notifier != nil {
			n := r.notifier
			go func() {
				notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = n.Notify(notifyCtx, notify.Event{
					Type: notify.EventReconcileDrift,
					Details: map[string]any{
						"orphans_in_keycloak": len(result.OrphansInKeycloak),
						"orphans_in_db":       len(result.OrphansInDB),
					},
				})
			}()
		}
	} else {
		r.logger.Info("reconciliation: no drift detected")
	}
}
