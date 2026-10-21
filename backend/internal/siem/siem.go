//go:build !windows

// Package siem implements near-real-time audit log streaming to external SIEM
// sinks (syslog RFC5424 and HTTP NDJSON). A durable cursor persisted in the
// siem_cursor table ensures no events are dropped across restarts.
//
// Multi-instance: wire SetLeaderGate so only one replica advances the cursor
// and streams events. Without a gate, every replica would re-deliver the same
// audit rows (at-least-once fan-out).
package siem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/syslog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// sinkTypeGauge exposes the ACTIVE sink type so a deployment that silently
// streams nothing is visible in Prometheus. The value is the number of the
// active sink: 0 = none (no-op), 1 = syslog, 2 = http. A value of 0 with a
// non-zero SIEM_INTERVAL means audit events are being generated but NOT
// delivered (issue #128).
var sinkTypeGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "freecloud_siem_sink_type",
	Help: "Active SIEM sink type: 0 = none (no-op, events are not delivered), 1 = syslog, 2 = http.",
}, []string{"type"})

// SinkTypeNone is the gauge value for "no sink configured".
const SinkTypeNone = 0

// SinkTypeSyslog is the gauge value for the syslog sink.
const SinkTypeSyslog = 1

// SinkTypeHTTP is the gauge value for the HTTP NDJSON sink.
const SinkTypeHTTP = 2

// setSinkTypeGauge records the active sink type.
func setSinkTypeGauge(kind string, value float64) {
	sinkTypeGauge.WithLabelValues(kind).Set(value)
}

// DBPool is the subset of *pgxpool.Pool the streamer uses.
type DBPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// AuditEntry mirrors audit_logs columns sent to external sinks.
type AuditEntry struct {
	ID         string `json:"id"`
	Seq        int64  `json:"seq"`
	ActorID    string `json:"actorId"`
	Action     string `json:"action"`
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	Details    string `json:"details"`
	CreatedAt  string `json:"createdAt"`
}

// Sink is the interface implemented by each SIEM output.
type Sink interface {
	Send(ctx context.Context, entry AuditEntry) error
	// Name returns a short label for logging.
	Name() string
	// IsNoop reports whether the sink DISCARDS every entry instead of
	// delivering it. The streamer refuses to advance its cursor for a no-op
	// sink: advancing it would drop the audit events permanently while
	// logging a healthy-looking "streamed N entries" line (issue #128).
	IsNoop() bool
}

// noopSink is a sink that discards every entry (used when the sink is not
// configured).
type noopSink struct{}

func (noopSink) Send(_ context.Context, _ AuditEntry) error { return nil }
func (noopSink) Name() string                               { return "noop" }
func (noopSink) IsNoop() bool                               { return true }

// SyslogSink writes RFC5424-formatted messages to a remote syslog server.
type SyslogSink struct {
	writer *syslog.Writer
}

// NewSyslogSink connects to the given network address (e.g. "localhost:514")
// over the given network ("udp" or "tcp").
func NewSyslogSink(network, addr string) (*SyslogSink, error) {
	w, err := syslog.Dial(network, addr, syslog.LOG_INFO|syslog.LOG_DAEMON, "freecloud-audit")
	if err != nil {
		return nil, fmt.Errorf("siem: syslog dial %s %s: %w", network, addr, err)
	}
	return &SyslogSink{writer: w}, nil
}

func (s *SyslogSink) Name() string { return "syslog" }

// IsNoop reports false: the syslog sink delivers every entry.
func (s *SyslogSink) IsNoop() bool { return false }

// Send formats the entry as a structured syslog message.
func (s *SyslogSink) Send(_ context.Context, entry AuditEntry) error {
	msg := fmt.Sprintf("audit seq=%d id=%s actor=%s action=%s target_type=%s target_id=%s details=%s ts=%s",
		entry.Seq, entry.ID, entry.ActorID, entry.Action,
		entry.TargetType, entry.TargetID, entry.Details, entry.CreatedAt,
	)
	return s.writer.Info(msg)
}

// HTTPSink POSTs NDJSON audit entries to an HTTP endpoint.
type HTTPSink struct {
	url    string
	token  string
	client *http.Client
}

// NewHTTPSink creates a HTTPSink posting to the given URL with an optional Bearer token.
func NewHTTPSink(url, token string) *HTTPSink {
	return &HTTPSink{
		url:    url,
		token:  token,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (h *HTTPSink) Name() string { return "http" }

// IsNoop reports false: the HTTP sink delivers every entry.
func (h *HTTPSink) IsNoop() bool { return false }

// Send POSTs a single audit entry as JSON.
func (h *HTTPSink) Send(ctx context.Context, entry AuditEntry) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("siem: http sink marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("siem: http sink build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("siem: http sink send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("siem: http sink unexpected status %d", resp.StatusCode)
	}
	return nil
}

// Streamer polls audit_logs and streams new entries to a Sink.
type Streamer struct {
	pool     DBPool
	sink     Sink
	logger   *zap.Logger
	isLeader func() bool
}

// New creates a Streamer.
func New(pool DBPool, sink Sink, logger *zap.Logger) *Streamer {
	return &Streamer{pool: pool, sink: sink, logger: logger}
}

// SetLeaderGate wires a leader-election check so only one replica streams.
func (s *Streamer) SetLeaderGate(isLeader func() bool) {
	s.isLeader = isLeader
}

// pollBatchSize caps audit rows fetched per SIEM poll tick.
const pollBatchSize = 100

// poll reads the cursor, fetches new audit_log rows (by seq), sends them to
// the sink, and advances the cursor on success. FAIL-SOFT: a send error
// leaves the cursor unchanged so the next poll retries.
//
// A NO-OP sink (no SIEM configured, or the configured sink failed to
// initialise) must NEVER advance the cursor: the events would be dropped
// permanently while the log claimed they were streamed (issue #128).
func (s *Streamer) poll(ctx context.Context) {
	if s.sink.IsNoop() {
		s.logger.Warn("siem: sink is a no-op (no SIEM sink configured or the configured sink failed to initialise); " +
			"audit events are NOT being delivered and the SIEM cursor is NOT advanced")
		return
	}

	var lastSeq int64
	if err := s.pool.QueryRow(ctx, `SELECT last_seq FROM siem_cursor WHERE id = 1`).Scan(&lastSeq); err != nil {
		s.logger.Warn("siem: failed to read cursor", zap.Error(err))
		return
	}

	rows, err := s.pool.Query(ctx, `
		SELECT seq, id::TEXT, actor_id, action,
		       COALESCE(target_type, ''), COALESCE(target_id, ''),
		       details::TEXT, created_at
		FROM audit_logs
		WHERE seq > $1
		ORDER BY seq ASC
		LIMIT $2`, lastSeq, pollBatchSize)
	if err != nil {
		s.logger.Warn("siem: failed to query audit_logs", zap.Error(err))
		return
	}
	defer rows.Close()

	var entries []AuditEntry
	var maxSeq int64 = lastSeq
	for rows.Next() {
		var e AuditEntry
		var createdAt time.Time
		if err := rows.Scan(
			&e.Seq, &e.ID, &e.ActorID, &e.Action,
			&e.TargetType, &e.TargetID, &e.Details, &createdAt,
		); err != nil {
			s.logger.Warn("siem: failed to scan audit row", zap.Error(err))
			continue
		}
		e.CreatedAt = createdAt.Format(time.RFC3339)
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		s.logger.Warn("siem: error iterating audit rows", zap.Error(err))
		return
	}

	if len(entries) == 0 {
		return
	}

	// Send entries to sink; fail-soft on first error (don't advance cursor).
	for _, entry := range entries {
		if err := s.sink.Send(ctx, entry); err != nil {
			s.logger.Warn("siem: sink send failed, will retry next poll",
				zap.String("sink", s.sink.Name()),
				zap.Int64("seq", entry.Seq),
				zap.Error(err),
			)
			return // cursor not advanced
		}
	}

	// Advance cursor only after all entries succeeded.
	if _, err := s.pool.Exec(ctx,
		`UPDATE siem_cursor SET last_seq = $1, updated_at = NOW() WHERE id = 1`,
		maxSeq,
	); err != nil {
		s.logger.Warn("siem: failed to advance cursor", zap.Error(err))
	} else {
		s.logger.Info("siem: streamed audit entries",
			zap.Int("count", len(entries)),
			zap.Int64("last_seq", maxSeq),
		)
	}
}

// Start launches the polling ticker. Returns immediately; stops on ctx cancellation.
func (s *Streamer) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		s.logger.Info("siem streamer disabled (SIEM_INTERVAL=0)")
		return
	}
	if s.sink.IsNoop() {
		// Loud startup warning: without a real sink the audit events are
		// generated but never delivered, and the SIEM cursor is deliberately
		// NOT advanced (issue #128).
		s.logger.Warn("siem: no SIEM sink configured — audit events will NOT be delivered to a SIEM. " +
			"Set SIEM_SYSLOG_ADDR or SIEM_HTTP_URL to enable streaming.")
		return
	}
	s.logger.Info("siem streamer started",
		zap.String("sink", s.sink.Name()),
		zap.Duration("interval", interval),
	)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.logger.Info("siem streamer stopped")
				return
			case <-ticker.C:
				if s.isLeader != nil && !s.isLeader() {
					s.logger.Debug("siem: skipping poll, not leader")
					continue
				}
				s.poll(ctx)
			}
		}
	}()
}

// BuildSink constructs the active Sink from config parameters.
//
// It returns an ERROR when no sink could be built — either because nothing
// is configured, or because the configured sink failed to initialise (for
// example a syslog dial failure). The previous behaviour silently fell back
// to a no-op sink, so the streamer looked healthy ("siem: streamed audit
// entries count=100") while every event was discarded (issue #128).
//
// The caller decides what to do with the error: the server must still start
// when SIEM is simply not configured, so main.go logs it loudly and starts
// the streamer with a no-op sink, which never advances the cursor.
func BuildSink(syslogNet, syslogAddr, httpURL, httpToken string, logger *zap.Logger) (Sink, error) {
	if syslogAddr != "" {
		net := syslogNet
		if net == "" {
			net = "udp"
		}
		sink, err := NewSyslogSink(net, syslogAddr)
		if err != nil {
			return nil, fmt.Errorf("siem: syslog sink at %s could not be initialised: %w", syslogAddr, err)
		}
		logger.Info("siem: syslog sink active", zap.String("addr", syslogAddr))
		setSinkTypeGauge("syslog", SinkTypeSyslog)
		setSinkTypeGauge("http", 0)
		setSinkTypeGauge("none", 0)
		return sink, nil
	}
	if httpURL != "" {
		logger.Info("siem: http sink active", zap.String("url", httpURL))
		setSinkTypeGauge("http", SinkTypeHTTP)
		setSinkTypeGauge("syslog", 0)
		setSinkTypeGauge("none", 0)
		return NewHTTPSink(httpURL, httpToken), nil
	}
	setSinkTypeGauge("none", SinkTypeNone)
	setSinkTypeGauge("syslog", 0)
	setSinkTypeGauge("http", 0)
	return nil, errors.New("siem: no sink configured (set SIEM_SYSLOG_ADDR or SIEM_HTTP_URL)")
}

// NewNoopSink returns the sink that discards every entry. The streamer
// refuses to advance its cursor for it, so no audit event is silently
// dropped while the logs claim a successful stream.
func NewNoopSink() Sink {
	setSinkTypeGauge("none", SinkTypeNone)
	setSinkTypeGauge("syslog", 0)
	setSinkTypeGauge("http", 0)
	return noopSink{}
}
