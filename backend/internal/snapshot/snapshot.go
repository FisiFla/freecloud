// Package snapshot implements the periodic analytics snapshot job (FCEX2-18).
// It writes time-series rows to the analytics_snapshots table capturing key
// health metrics: compliance rate, enrolled devices, MFA coverage, app count,
// and onboard/offboard activity since the previous snapshot.
package snapshot

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// DBPool is the subset of *pgxpool.Pool the snapshotter uses.
type DBPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// SnapshotRow is one time-series data point returned by GetSeries.
type SnapshotRow struct {
	ID              int64     `json:"id"`
	CapturedAt      time.Time `json:"capturedAt"`
	ComplianceRate  float64   `json:"complianceRate"`
	EnrolledDevices int       `json:"enrolledDevices"`
	MFACoveragePct  float64   `json:"mfaCoveragePct"`
	AppCount        int       `json:"appCount"`
	OnboardCount    int       `json:"onboardCount"`
	OffboardCount   int       `json:"offboardCount"`
}

// Snapshotter periodically writes analytics snapshots to the DB.
type Snapshotter struct {
	pool   DBPool
	logger *zap.Logger
}

// New creates a Snapshotter.
func New(pool DBPool, logger *zap.Logger) *Snapshotter {
	return &Snapshotter{pool: pool, logger: logger}
}

// TakeSnapshot computes current metrics and inserts one row.
func (s *Snapshotter) TakeSnapshot(ctx context.Context) error {
	// Compliance rate: compliant devices / total enrolled devices.
	// A device is compliant when disk_encrypted AND firewall_enabled (mirroring
	// the compliance handler). We approximate here from the devices table count
	// since full posture requires a Fleet round-trip; store enrolled count + use
	// 0 as the denominator guard.
	var enrolledDevices int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM devices`).Scan(&enrolledDevices); err != nil {
		return err
	}

	// compliance_rate: placeholder 0 until posture data is stored in DB.
	// TODO: compute from a posture cache table when it exists.
	var complianceRate float64

	// MFA coverage: TODO — MFA state is in Keycloak, not in our DB yet.
	// Track as 0.0 until a local mfa_state cache is introduced.
	var mfaCoveragePct float64

	// App count.
	var appCount int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM connected_apps`).Scan(&appCount); err != nil {
		return err
	}

	// Onboard/offboard counts since the last snapshot.
	var lastCaptured time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(captured_at), '1970-01-01') FROM analytics_snapshots`,
	).Scan(&lastCaptured)
	if err != nil {
		return err
	}

	var onboardCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action = 'onboard' AND created_at > $1`,
		lastCaptured,
	).Scan(&onboardCount); err != nil {
		return err
	}

	var offboardCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action = 'offboard' AND created_at > $1`,
		lastCaptured,
	).Scan(&offboardCount); err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO analytics_snapshots
		    (compliance_rate, enrolled_devices, mfa_coverage_pct, app_count, onboard_count, offboard_count)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		complianceRate, enrolledDevices, mfaCoveragePct, appCount, onboardCount, offboardCount,
	)
	return err
}

// GetSeries returns the most recent limit snapshot rows, oldest first.
func (s *Snapshotter) GetSeries(ctx context.Context, limit int) ([]SnapshotRow, error) {
	if limit <= 0 {
		limit = 24
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, captured_at, compliance_rate, enrolled_devices, mfa_coverage_pct,
		       app_count, onboard_count, offboard_count
		FROM analytics_snapshots
		ORDER BY captured_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var series []SnapshotRow
	for rows.Next() {
		var r SnapshotRow
		if err := rows.Scan(
			&r.ID, &r.CapturedAt,
			&r.ComplianceRate, &r.EnrolledDevices,
			&r.MFACoveragePct, &r.AppCount,
			&r.OnboardCount, &r.OffboardCount,
		); err != nil {
			return nil, err
		}
		series = append(series, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Return oldest-first for time-series charts.
	for i, j := 0, len(series)-1; i < j; i, j = i+1, j-1 {
		series[i], series[j] = series[j], series[i]
	}
	return series, nil
}

// Start launches the periodic snapshot ticker. It returns immediately; the
// goroutine stops when ctx is cancelled.
func (s *Snapshotter) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		s.logger.Info("analytics snapshot job disabled (SNAPSHOT_INTERVAL=0)")
		return
	}
	s.logger.Info("analytics snapshot job started", zap.Duration("interval", interval))
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.logger.Info("analytics snapshot job stopped")
				return
			case <-ticker.C:
				if err := s.TakeSnapshot(ctx); err != nil {
					s.logger.Warn("analytics snapshot failed", zap.Error(err))
				} else {
					s.logger.Info("analytics snapshot captured")
				}
			}
		}
	}()
}
