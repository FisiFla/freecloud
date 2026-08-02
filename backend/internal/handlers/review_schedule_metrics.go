package handlers

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Review-schedule runner metrics. Follows the repo convention
// `freecloud_<component>_<metric>` (see middleware/metrics.go, reconcile).
var (
	reviewScheduleTicks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "freecloud_review_schedule_ticks_total",
		Help: "Review schedule runner ticks by outcome (leader_ran, skipped_not_leader).",
	}, []string{"outcome"})

	reviewScheduleFired = promauto.NewCounter(prometheus.CounterOpts{
		Name: "freecloud_review_schedule_fired_total",
		Help: "Review schedules fired into access-review campaigns.",
	})
)

// IncReviewScheduleTick records a runner tick outcome.
func IncReviewScheduleTick(outcome string) {
	reviewScheduleTicks.WithLabelValues(outcome).Inc()
}

// IncReviewScheduleFired records a fired schedule (one campaign created).
func IncReviewScheduleFired() {
	reviewScheduleFired.Inc()
}
