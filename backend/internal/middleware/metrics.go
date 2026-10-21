package middleware

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests by method, route, and status.",
	}, []string{"method", "route", "status"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds by method and route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})

	provisioningErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "freecloud_provisioning_errors_total",
		Help: "Total outbound provisioning errors by app_id and connector type.",
	}, []string{"app_id", "connector"})
)

// IncProvisioningError increments the provisioning error counter for the given app and connector.
func IncProvisioningError(appID, connector string) {
	provisioningErrors.WithLabelValues(appID, connector).Inc()
}

// statusRecorder captures the response status code for metrics labelling.
//
// It forwards the optional interfaces of the wrapped ResponseWriter
// (http.Flusher, http.Hijacker, io.ReaderFrom) so wrapping does not disable
// streaming or connection hijacking for the handlers behind it (issue #138).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		// The first WriteHeader wins; a second call is ignored, exactly
		// like net/http's own behaviour (it logs "superfluous WriteHeader
		// call" and keeps the first status).
		return
	}
	s.wroteHeader = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped ResponseWriter so http.ResponseController (and
// other helpers that look for Unwrap) can reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush forwards to the wrapped writer when it supports flushing, so
// streaming responses (SSE and friends) still work behind the metrics
// middleware.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the wrapped writer when it supports hijacking, so
// WebSocket-style upgrades still work behind the metrics middleware.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("middleware: underlying ResponseWriter does not implement http.Hijacker")
}

// ReadFrom forwards to the wrapped writer when it supports io.ReaderFrom, so
// efficient copies (io.Copy and friends) are not degraded by the wrapper.
func (s *statusRecorder) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := s.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	return io.Copy(struct{ io.Writer }{s.ResponseWriter}, r)
}

// Metrics records request count and latency to Prometheus, labelled by the chi
// route pattern (e.g. "/api/v1/users/{id}") rather than the raw path, so the
// metric cardinality stays bounded.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		httpRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		httpDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}
