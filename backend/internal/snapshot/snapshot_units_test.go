package snapshot

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap/zaptest"
)

// TestTakeSnapshot_RateFieldsAreRatios is the regression test for the metric
// unit inconsistency: complianceRate and mfaCoveragePct are both ratios in
// the range 0-1, so the dashboard's single pct() helper (which multiplies by
// 100) renders them correctly. mfaCoveragePct used to be stored as a 0-100
// percentage while complianceRate was a 0-1 ratio, which made the dashboard
// display MFA coverage as e.g. "8800%".
func TestTakeSnapshot_RateFieldsAreRatios(t *testing.T) {
	var gotComplianceRate, gotMFACoverage float64
	var insertCalled bool

	pool := &fakePool{
		queryFn: singleOrgQueryFn("11111111-1111-1111-1111-111111111111"),
		queryRowFn: queryRowCallSeq([]func(dest ...any) error{
			// COUNT(*) FROM devices WHERE org_id = $1
			func(dest ...any) error { *(dest[0].(*int)) = 5; return nil },
			// COUNT(*) FROM device_posture_cache JOIN devices (cacheTotal)
			func(dest ...any) error { *(dest[0].(*int)) = 4; return nil },
			// COUNT(*) ... WHERE compliant = TRUE (cacheCompliant)
			func(dest ...any) error { *(dest[0].(*int)) = 3; return nil },
			// COUNT(*) FROM users WHERE org_id = $1 (mfaTotal)
			func(dest ...any) error { *(dest[0].(*int)) = 5; return nil },
			// COUNT(*) FROM mfa_coverage_cache JOIN users WHERE has_mfa = TRUE (mfaEnrolled)
			func(dest ...any) error { *(dest[0].(*int)) = 3; return nil },
			// COUNT(*) FROM connected_apps WHERE org_id = $1
			func(dest ...any) error { *(dest[0].(*int)) = 3; return nil },
			// MAX(captured_at) FROM analytics_snapshots WHERE org_id = $1
			func(dest ...any) error { *(dest[0].(*time.Time)) = time.Time{}; return nil },
			// onboard count
			func(dest ...any) error { *(dest[0].(*int)) = 2; return nil },
			// offboard count
			func(dest ...any) error { *(dest[0].(*int)) = 1; return nil },
		}),
		execFn: func(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
			insertCalled = true
			if len(args) < 7 {
				t.Fatalf("INSERT called with %d args, want 7", len(args))
			}
			gotComplianceRate, _ = args[0].(float64)
			gotMFACoverage, _ = args[2].(float64)
			return pgconn.CommandTag{}, nil
		},
	}

	s := New(pool, zaptest.NewLogger(t))
	if err := s.TakeSnapshot(context.Background()); err != nil {
		t.Fatalf("TakeSnapshot returned error: %v", err)
	}
	if !insertCalled {
		t.Fatal("expected INSERT to be called")
	}

	// 3 compliant of 4 cached devices.
	if gotComplianceRate != 0.75 {
		t.Errorf("complianceRate = %v, want 0.75", gotComplianceRate)
	}
	// 3 users with MFA of 5 users — a 0-1 ratio, not 60.
	if gotMFACoverage != 0.6 {
		t.Errorf("mfaCoveragePct = %v, want 0.6 (ratio), not a 0-100 percentage", gotMFACoverage)
	}

	// Both rate-like fields must stay within 0-1 so the dashboard's
	// shared pct() helper cannot render an absurd percentage.
	for name, v := range map[string]float64{
		"complianceRate": gotComplianceRate,
		"mfaCoveragePct": gotMFACoverage,
	} {
		if v < 0 || v > 1 {
			t.Errorf("%s = %v, want a ratio in [0,1]", name, v)
		}
	}
}
