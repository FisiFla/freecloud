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
	"fmt"
	"log/syslog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

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
}

// noopSink is a sink that discards every entry (used when the sink is not configured).
type noopSink struct{}

func (noopSink) Send(_ context.Context, _ AuditEntry) error { return nil }
func (noopSink) Name() string                               { return "noop" }

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

// poll reads the cursor, fetches new audit_log rows (by seq), sends them to
// the sink, and advances the cursor on success. FAIL-SOFT: a send error
// leaves the cursor unchanged so the next poll retries.
func (s *Streamer) poll(ctx context.Context) {
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
		LIMIT 100`, lastSeq)
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
// Returns a noopSink if no sink is configured.
func BuildSink(syslogNet, syslogAddr, httpURL, httpToken string, logger *zap.Logger) Sink {
	if syslogAddr != "" {
		net := syslogNet
		if net == "" {
			net = "udp"
		}
		sink, err := NewSyslogSink(net, syslogAddr)
		if err != nil {
			logger.Error("siem: failed to connect syslog sink; falling back to noop", zap.Error(err))
		} else {
			logger.Info("siem: syslog sink active", zap.String("addr", syslogAddr))
			return sink
		}
	}
	if httpURL != "" {
		logger.Info("siem: http sink active", zap.String("url", httpURL))
		return NewHTTPSink(httpURL, httpToken)
	}
	return noopSink{}
}
