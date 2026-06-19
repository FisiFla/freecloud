package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// fakeNotifier counts successful calls and optionally returns an error.
type fakeNotifier struct {
	name    string
	calls   atomic.Int32
	failErr error
}

func (f *fakeNotifier) Name() string { return f.name }
func (f *fakeNotifier) Notify(_ context.Context, _ Event) error {
	f.calls.Add(1)
	return f.failErr
}

// panicNotifier panics on every call.
type panicNotifier struct{}

func (p *panicNotifier) Name() string { return "panic" }
func (p *panicNotifier) Notify(_ context.Context, _ Event) error {
	panic("deliberate panic in notifier")
}

func TestMultiNotifier_FailOpen(t *testing.T) {
	logger := zaptest.NewLogger(t)
	good := &fakeNotifier{name: "good"}
	bad := &fakeNotifier{name: "bad", failErr: errors.New("send error")}

	m := NewMultiNotifier(EventToggles{Offboard: true, Drift: true, Compliance: true}, logger, good, bad)
	err := m.Notify(context.Background(), Event{Type: EventOffboardCompleted, ActorID: "actor1"})
	if err != nil {
		t.Fatalf("MultiNotifier.Notify returned unexpected error: %v", err)
	}
	if good.calls.Load() != 1 {
		t.Errorf("good notifier expected 1 call, got %d", good.calls.Load())
	}
	// bad returned error — should not stop good
	if bad.calls.Load() != 1 {
		t.Errorf("bad notifier expected 1 call, got %d", bad.calls.Load())
	}
}

func TestMultiNotifier_PanicRecovery(t *testing.T) {
	logger := zaptest.NewLogger(t)
	good := &fakeNotifier{name: "good"}

	m := NewMultiNotifier(EventToggles{Offboard: true, Drift: true, Compliance: true}, logger, &panicNotifier{}, good)
	err := m.Notify(context.Background(), Event{Type: EventReconcileDrift})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if good.calls.Load() != 1 {
		t.Errorf("good notifier expected 1 call after panic, got %d", good.calls.Load())
	}
}

func TestMultiNotifier_EventToggle_Suppressed(t *testing.T) {
	logger := zaptest.NewLogger(t)
	good := &fakeNotifier{name: "good"}

	// Drift suppressed
	m := NewMultiNotifier(EventToggles{Offboard: true, Drift: false, Compliance: true}, logger, good)
	_ = m.Notify(context.Background(), Event{Type: EventReconcileDrift})
	if good.calls.Load() != 0 {
		t.Errorf("expected 0 calls when drift toggled off, got %d", good.calls.Load())
	}
}

func TestMultiNotifier_EventToggle_Enabled(t *testing.T) {
	logger := zaptest.NewLogger(t)
	good := &fakeNotifier{name: "good"}

	m := NewMultiNotifier(EventToggles{Offboard: true, Drift: true, Compliance: true}, logger, good)
	_ = m.Notify(context.Background(), Event{Type: EventOffboardCompleted})
	if good.calls.Load() != 1 {
		t.Errorf("expected 1 call, got %d", good.calls.Load())
	}
}

func TestWebhookNotifier_SignsCorrectly(t *testing.T) {
	const secret = "test-secret-key"
	var gotSig string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-FreeCloud-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.URL, secret)
	ev := Event{Type: EventOffboardCompleted, ActorID: "actor1", TargetID: "target1"}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}

	// Compute expected signature over the body we received
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if gotSig != expected {
		t.Errorf("signature mismatch:\n  got:  %s\n  want: %s", gotSig, expected)
	}
}

func TestWebhookNotifier_ErrorOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.URL, "")
	err := n.Notify(context.Background(), Event{Type: EventReconcileDrift})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestSlackNotifier_PostsCorrectJSON(t *testing.T) {
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewSlackNotifier(srv.URL)
	ev := Event{Type: EventComplianceFailure, ActorID: "actor2", TargetID: "device3"}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	text, ok := payload["text"]
	if !ok {
		t.Fatal("payload missing 'text' field")
	}
	if !strings.Contains(text, "compliance_failure") {
		t.Errorf("Slack text missing event type: %q", text)
	}
}

func TestEmailNotifier_NoRecipients(t *testing.T) {
	n := NewEmailNotifier(EmailConfig{
		Host: "localhost", Port: "25", From: "from@example.com", To: nil,
	})
	err := n.Notify(context.Background(), Event{Type: EventOffboardCompleted})
	if err == nil {
		t.Fatal("expected error when no recipients, got nil")
	}
}

func TestEmailNotifier_Name(t *testing.T) {
	n := NewEmailNotifier(EmailConfig{})
	if n.Name() != "email" {
		t.Errorf("expected 'email', got %q", n.Name())
	}
}

// Verify zap.Logger satisfies the compilation check (not a runtime test).
var _ = zap.NewNop()
