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

// PostureEntry is one device's compliance posture as reported by Fleet.
// Callers (handlers/device_actions.go) populate it and pass a slice to
// SyncPostureCache so TakeSnapshot can compute real compliance_rate.
type PostureEntry struct {
	HostID          string
	Compliant       bool
	DiskEncrypted   bool
	OsUpToDate      bool // true when NOT needs_update
	NeedsUpdate     bool
	FirewallEnabled bool
}

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
	// isLeader gates the ticker-driven snapshot (B3, v1.7 HA): nil means
	// "always run" (single-instance / no leader election wired up), matching
	// prior behavior exactly.
	isLeader func() bool
}

// New creates a Snapshotter.
func New(pool DBPool, logger *zap.Logger) *Snapshotter {
	return &Snapshotter{pool: pool, logger: logger}
}

// SetLeaderGate wires a leader-election check (B3, v1.7 HA) so the ticker in
// Start only takes a snapshot on the instance that currently holds leadership.
func (s *Snapshotter) SetLeaderGate(isLeader func() bool) {
	s.isLeader = isLeader
}

// SyncPostureCache upserts a batch of posture entries into device_posture_cache.
// Called by the compliance handler (or any path that has live Fleet data) so
// TakeSnapshot can compute compliance_rate from DB without a Fleet round-trip.
func (s *Snapshotter) SyncPostureCache(ctx context.Context, entries []PostureEntry) error {
	for _, e := range entries {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO device_posture_cache
			    (host_id, compliant, disk_encrypted, os_up_to_date, needs_update, firewall_enabled, checked_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW())
			ON CONFLICT (host_id) DO UPDATE SET
			    compliant        = EXCLUDED.compliant,
			    disk_encrypted   = EXCLUDED.disk_encrypted,
			    os_up_to_date    = EXCLUDED.os_up_to_date,
			    needs_update     = EXCLUDED.needs_update,
			    firewall_enabled = EXCLUDED.firewall_enabled,
			    checked_at       = NOW()`,
			e.HostID, e.Compliant, e.DiskEncrypted, e.OsUpToDate, e.NeedsUpdate, e.FirewallEnabled,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// TakeSnapshot computes current metrics for every organization and inserts
// one row per org (Epic C / v1.7 multi-tenant). Each org's metrics are
// computed from that org's own rows only — an org's dashboard must never
// reflect another org's device/user/app counts.
func (s *Snapshotter) TakeSnapshot(ctx context.Context) error {
	orgIDs, err := s.listOrgIDs(ctx)
	if err != nil {
		return err
	}
	for _, orgID := range orgIDs {
		if err := s.takeSnapshotForOrg(ctx, orgID); err != nil {
			return err
		}
	}
	return nil
}

// listOrgIDs returns every organization id known to the deployment.
func (s *Snapshotter) listOrgIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::TEXT FROM organizations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// takeSnapshotForOrg computes current metrics for one org and inserts a row.
func (s *Snapshotter) takeSnapshotForOrg(ctx context.Context, orgID string) error {
	// Enrolled devices count.
	var enrolledDevices int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM devices WHERE org_id = $1`, orgID).Scan(&enrolledDevices); err != nil {
		return err
	}

	// Compliance rate: computed from the device_posture_cache table populated
	// by SyncPostureCache (called whenever the compliance handler fetches live
	// Fleet posture), joined through devices to scope it to this org. Falls
	// back to 0 when the cache has no entries for this org's devices.
	var complianceRate float64
	var cacheTotal, cacheCompliant int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM device_posture_cache pc
		 JOIN devices d ON d.fleet_host_id = pc.host_id
		 WHERE d.org_id = $1`, orgID,
	).Scan(&cacheTotal); err != nil {
		return err
	}
	if cacheTotal > 0 {
		if err := s.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM device_posture_cache pc
			 JOIN devices d ON d.fleet_host_id = pc.host_id
			 WHERE d.org_id = $1 AND pc.compliant = TRUE`, orgID,
		).Scan(&cacheCompliant); err != nil {
			return err
		}
		complianceRate = float64(cacheCompliant) / float64(cacheTotal)
	}

	// MFA coverage: computed from the mfa_coverage_cache table which is kept
	// up-to-date by the self-service MFA enrollment endpoints (B1), joined
	// through users to scope it to this org. Users not yet in the cache are
	// treated as not enrolled (conservative).
	var mfaCoveragePct float64
	var mfaTotal, mfaEnrolled int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE org_id = $1 AND disabled IS NOT TRUE`, orgID,
	).Scan(&mfaTotal); err != nil {
		return err
	}
	if mfaTotal > 0 {
		if err := s.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM mfa_coverage_cache mc
			 JOIN users u ON u.keycloak_user_id::TEXT = mc.user_id
			 WHERE u.org_id = $1 AND mc.has_mfa = TRUE`, orgID,
		).Scan(&mfaEnrolled); err != nil {
			return err
		}
		mfaCoveragePct = float64(mfaEnrolled) / float64(mfaTotal) * 100.0
	}

	// App count.
	var appCount int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM connected_apps WHERE org_id = $1`, orgID).Scan(&appCount); err != nil {
		return err
	}

	// Onboard/offboard counts since this org's last snapshot.
	var lastCaptured time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(captured_at), '1970-01-01') FROM analytics_snapshots WHERE org_id = $1`, orgID,
	).Scan(&lastCaptured)
	if err != nil {
		return err
	}

	// audit_logs keeps one global hash chain, but org_id is a valid filter
	// column on it (see docs/adr/0005) — onboard/offboard counts are scoped
	// to this org's own audit entries.
	var onboardCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE org_id = $1 AND action = 'onboard' AND created_at > $2`,
		orgID, lastCaptured,
	).Scan(&onboardCount); err != nil {
		return err
	}

	var offboardCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE org_id = $1 AND action = 'offboard' AND created_at > $2`,
		orgID, lastCaptured,
	).Scan(&offboardCount); err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO analytics_snapshots
		    (compliance_rate, enrolled_devices, mfa_coverage_pct, app_count, onboard_count, offboard_count, org_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		complianceRate, enrolledDevices, mfaCoveragePct, appCount, onboardCount, offboardCount, orgID,
	)
	return err
}

// GetSeries returns the most recent limit snapshot rows for orgID, oldest first.
func (s *Snapshotter) GetSeries(ctx context.Context, orgID string, limit int) ([]SnapshotRow, error) {
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
		WHERE org_id = $1
		ORDER BY captured_at DESC
		LIMIT $2`, orgID, limit)
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

// GetSeriesRange returns snapshot rows for orgID between from and to
// (inclusive), oldest first, up to limit rows. Zero values of from/to mean
// no lower/upper bound.
func (s *Snapshotter) GetSeriesRange(ctx context.Context, orgID string, from, to time.Time, limit int) ([]SnapshotRow, error) {
	if limit <= 0 {
		limit = 90
	}
	if limit > 1000 {
		limit = 1000
	}

	var (
		sqlStr string
		args   []any
	)
	switch {
	case !from.IsZero() && !to.IsZero():
		sqlStr = `SELECT id, captured_at, compliance_rate, enrolled_devices, mfa_coverage_pct,
		       app_count, onboard_count, offboard_count
		FROM analytics_snapshots
		WHERE org_id = $1 AND captured_at >= $2 AND captured_at <= $3
		ORDER BY captured_at ASC
		LIMIT $4`
		args = []any{orgID, from, to, limit}
	case !from.IsZero():
		sqlStr = `SELECT id, captured_at, compliance_rate, enrolled_devices, mfa_coverage_pct,
		       app_count, onboard_count, offboard_count
		FROM analytics_snapshots
		WHERE org_id = $1 AND captured_at >= $2
		ORDER BY captured_at ASC
		LIMIT $3`
		args = []any{orgID, from, limit}
	case !to.IsZero():
		sqlStr = `SELECT id, captured_at, compliance_rate, enrolled_devices, mfa_coverage_pct,
		       app_count, onboard_count, offboard_count
		FROM analytics_snapshots
		WHERE org_id = $1 AND captured_at <= $2
		ORDER BY captured_at ASC
		LIMIT $3`
		args = []any{orgID, to, limit}
	default:
		// No bounds — return most recent, then reverse.
		return s.GetSeries(ctx, orgID, limit)
	}

	rows, err := s.pool.Query(ctx, sqlStr, args...)
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
				if s.isLeader != nil && !s.isLeader() {
					continue
				}
				if err := s.TakeSnapshot(ctx); err != nil {
					s.logger.Warn("analytics snapshot failed", zap.Error(err))
				} else {
					s.logger.Info("analytics snapshot captured")
				}
			}
		}
	}()
}
