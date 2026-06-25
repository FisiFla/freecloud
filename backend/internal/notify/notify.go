// Package notify implements pluggable event notifications for FreeCloud.
// A MultiNotifier fans out to one or more channel-specific notifiers (email,
// Slack, signed HTTP webhook). Notification failures are FAIL-OPEN: they are
// logged and counted in Prometheus but never block or fail the core action.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// Event type constants.
const (
	EventOffboardCompleted  = "offboard_completed"
	EventReconcileDrift     = "reconcile_drift"
	EventComplianceFailure  = "compliance_failure"
	EventAccessBlocked      = "access_blocked"      // fired by access_eval when posture denies (A4)
	EventProvisioningFailed = "provisioning_failed" // fired when outbound provisioning fails (A4)
	EventAccessReviewDue    = "access_review_due"   // fired when a review campaign is approaching due date
)

// notifyErrors counts notification failures by channel and event type.
var notifyErrors = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "freecloud_notify_errors_total",
	Help: "Total notification delivery failures by notifier channel and event type.",
}, []string{"notifier", "event_type"})

// Event carries information about a system event to be notified.
type Event struct {
	Type     string
	ActorID  string
	TargetID string
	Details  map[string]any
}

// Notifier is the interface implemented by each notification channel.
type Notifier interface {
	Notify(ctx context.Context, e Event) error
	// Name returns a short label used in metrics and logs.
	Name() string
}

// EventToggles controls which event types are delivered.
// All fields default to true (notify on all events). Setting a field to false
// suppresses delivery to all channels for that event type.
type EventToggles struct {
	Offboard      bool
	Drift         bool
	Compliance    bool
	AccessBlocked bool
	Provisioning  bool
	ReviewDue     bool
}

// MultiNotifier fans out to multiple Notifiers. A per-notifier failure is
// logged and counted but never propagated — Notify always returns nil.
type MultiNotifier struct {
	notifiers []Notifier
	toggles   EventToggles
	logger    *zap.Logger
}

// NewMultiNotifier creates a MultiNotifier wrapping the provided channels.
func NewMultiNotifier(toggles EventToggles, logger *zap.Logger, notifiers ...Notifier) *MultiNotifier {
	return &MultiNotifier{notifiers: notifiers, toggles: toggles, logger: logger}
}

// Notify delivers the event to all configured channels. Failures are fail-open.
func (m *MultiNotifier) Notify(ctx context.Context, e Event) error {
	if !m.eventEnabled(e.Type) {
		return nil
	}
	for _, n := range m.notifiers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					m.logger.Error("notifier panicked",
						zap.String("notifier", n.Name()),
						zap.String("event_type", e.Type),
						zap.Any("panic", r),
					)
					notifyErrors.WithLabelValues(n.Name(), e.Type).Inc()
				}
			}()
			if err := n.Notify(ctx, e); err != nil {
				m.logger.Warn("notification delivery failed",
					zap.String("notifier", n.Name()),
					zap.String("event_type", e.Type),
					zap.Error(err),
				)
				notifyErrors.WithLabelValues(n.Name(), e.Type).Inc()
			}
		}()
	}
	return nil
}

func (m *MultiNotifier) Name() string { return "multi" }

func (m *MultiNotifier) eventEnabled(eventType string) bool {
	switch eventType {
	case EventOffboardCompleted:
		return m.toggles.Offboard
	case EventReconcileDrift:
		return m.toggles.Drift
	case EventComplianceFailure:
		return m.toggles.Compliance
	case EventAccessBlocked:
		return m.toggles.AccessBlocked
	case EventProvisioningFailed:
		return m.toggles.Provisioning
	case EventAccessReviewDue:
		return m.toggles.ReviewDue
	}
	return true
}

// EmailConfig holds SMTP configuration for the email notifier.
type EmailConfig struct {
	Host     string
	Port     string
	Username string
	From     string
	To       []string
	Password string
}

// EmailNotifier sends event notifications via SMTP.
type EmailNotifier struct {
	cfg EmailConfig
}

// NewEmailNotifier creates an EmailNotifier.
func NewEmailNotifier(cfg EmailConfig) *EmailNotifier {
	return &EmailNotifier{cfg: cfg}
}

func (e *EmailNotifier) Name() string { return "email" }

// Notify formats and sends an email for the event.
func (e *EmailNotifier) Notify(_ context.Context, ev Event) error {
	if len(e.cfg.To) == 0 {
		return fmt.Errorf("email notifier: no recipients configured")
	}

	detailsJSON, _ := json.MarshalIndent(ev.Details, "", "  ")
	subject := fmt.Sprintf("[FreeCloud] Event: %s", ev.Type)
	body := fmt.Sprintf(
		"Event Type: %s\r\nActor:      %s\r\nTarget:     %s\r\nTime:       %s\r\n\r\nDetails:\r\n%s\r\n",
		ev.Type, ev.ActorID, ev.TargetID,
		time.Now().UTC().Format(time.RFC3339),
		string(detailsJSON),
	)

	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		e.cfg.From,
		strings.Join(e.cfg.To, ", "),
		subject,
		body,
	)

	addr := e.cfg.Host + ":" + e.cfg.Port
	var auth smtp.Auth
	if e.cfg.Password != "" {
		username := e.cfg.Username
		if username == "" {
			username = e.cfg.From
		}
		auth = smtp.PlainAuth("", username, e.cfg.Password, e.cfg.Host)
	}
	return smtp.SendMail(addr, auth, e.cfg.From, e.cfg.To, []byte(msg))
}

// SlackNotifier posts event notifications to a Slack incoming webhook.
type SlackNotifier struct {
	webhookURL string
	client     *http.Client
}

// NewSlackNotifier creates a SlackNotifier targeting the given webhook URL.
func NewSlackNotifier(webhookURL string) *SlackNotifier {
	return &SlackNotifier{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *SlackNotifier) Name() string { return "slack" }

// Notify posts a Slack message for the event.
func (s *SlackNotifier) Notify(ctx context.Context, ev Event) error {
	text := fmt.Sprintf("*[FreeCloud]* Event `%s` — actor: `%s`, target: `%s`",
		ev.Type, ev.ActorID, ev.TargetID)
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("slack notifier: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("slack notifier: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack notifier: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack notifier: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// WebhookNotifier sends HMAC-SHA256-signed HTTP POST notifications.
type WebhookNotifier struct {
	url    string
	secret string
	client *http.Client
}

// NewWebhookNotifier creates a WebhookNotifier. The secret is used to sign the
// request body with HMAC-SHA256; an empty secret disables signing.
func NewWebhookNotifier(url, secret string) *WebhookNotifier {
	return &WebhookNotifier{
		url:    url,
		secret: secret,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *WebhookNotifier) Name() string { return "webhook" }

// Notify POSTs the event as JSON to the webhook URL with an HMAC signature.
func (w *WebhookNotifier) Notify(ctx context.Context, ev Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("webhook notifier: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("webhook notifier: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if w.secret != "" {
		mac := hmac.New(sha256.New, []byte(w.secret))
		mac.Write(payload)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-FreeCloud-Signature", "sha256="+sig)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook notifier: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook notifier: unexpected status %d", resp.StatusCode)
	}
	return nil
}
