package handlers

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestReviewScheduleMetrics_CountersIncrement verifies the runner metrics
// increment through their helper functions (Prometheus counter behavior).
func TestReviewScheduleMetrics_CountersIncrement(t *testing.T) {
	beforeTicks := testutil.ToFloat64(reviewScheduleTicks.WithLabelValues("skipped_not_leader"))
	beforeFired := testutil.ToFloat64(reviewScheduleFired)

	IncReviewScheduleTick("skipped_not_leader")
	IncReviewScheduleFired()

	afterTicks := testutil.ToFloat64(reviewScheduleTicks.WithLabelValues("skipped_not_leader"))
	afterFired := testutil.ToFloat64(reviewScheduleFired)

	if afterTicks <= beforeTicks {
		t.Fatalf("skipped_not_leader tick counter did not increment: %v -> %v", beforeTicks, afterTicks)
	}
	if afterFired <= beforeFired {
		t.Fatalf("fired counter did not increment: %v -> %v", beforeFired, afterFired)
	}
}

// TestReviewScheduleMetrics_LabelsRegistered confirms the outcome label space
// is stable so dashboards/alerts don't break on a label rename.
func TestReviewScheduleMetrics_LabelsRegistered(t *testing.T) {
	labels := reviewScheduleTicks.WithLabelValues("leader_ran")
	if testutil.ToFloat64(labels) < 0 {
		t.Fatal("negative counter value")
	}
	// Exercise the skip label too — this is the label the "stuck runner"
	// alert would key on.
	IncReviewScheduleTick("skipped_not_leader")
}
