package notify

// Regression tests for the bounded, context-aware SMTP send:
//   - #148 net/smtp's SendMail has neither a timeout nor context support, so
//     a hung relay pinned the caller's goroutine forever. The notifier also
//     discarded the context it was given.
//   - #148 a CR/LF in an address injected arbitrary SMTP headers (Bcc, ...),
//     because the message is assembled by string concatenation.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

// startFakeSMTPServer starts a minimal SMTP server that records the message
// body it receives. It returns the "host:port" address.
func startFakeSMTPServer(t *testing.T, received *strings.Builder) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = fmt.Fprint(conn, "220 fake ESMTP\r\n")
				scanner := bufio.NewScanner(conn)
				inData := false
				for scanner.Scan() {
					line := scanner.Text()
					if inData {
						if line == "." {
							inData = false
							_, _ = fmt.Fprint(conn, "250 OK\r\n")
							continue
						}
						received.WriteString(line + "\n")
						continue
					}
					upper := strings.ToUpper(line)
					switch {
					case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
						_, _ = fmt.Fprint(conn, "250 fake\r\n")
					case strings.HasPrefix(upper, "DATA"):
						inData = true
						_, _ = fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
					case strings.HasPrefix(upper, "QUIT"):
						_, _ = fmt.Fprint(conn, "221 Bye\r\n")
						return
					default:
						_, _ = fmt.Fprint(conn, "250 OK\r\n")
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestSendMail_DeliversTheMessage(t *testing.T) {
	var received strings.Builder
	addr := startFakeSMTPServer(t, &received)

	msg := "From: from@example.com\r\nTo: to@example.com\r\nSubject: hi\r\n\r\nbody\r\n"
	if err := SendMail(context.Background(), addr, nil, "from@example.com", []string{"to@example.com"}, []byte(msg)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	if !strings.Contains(received.String(), "Subject: hi") {
		t.Errorf("the message was not delivered: %q", received.String())
	}
}

func TestSendMail_RejectsCRLFInAddresses(t *testing.T) {
	err := SendMail(context.Background(), "127.0.0.1:1", nil,
		"from@example.com", []string{"victim@example.com\r\nBcc: attacker@example.com"}, []byte("x"))
	if err == nil {
		t.Fatal("expected a CRLF address to be rejected")
	}
	if !errors.Is(err, ErrInvalidMailAddress) {
		t.Errorf("expected ErrInvalidMailAddress, got %v", err)
	}
}

func TestSendMail_HonoursTheContextDeadline(t *testing.T) {
	// A listener that accepts the connection but never answers: without a
	// deadline the send would block forever.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open without sending a greeting.
			go func() {
				time.Sleep(2 * time.Second)
				_ = conn.Close()
			}()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = SendMail(ctx, ln.Addr().String(), nil, "from@example.com", []string{"to@example.com"}, []byte("x"))
	if err == nil {
		t.Fatal("expected the send to fail when the context expires")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the send took %v: the context deadline was not honoured", elapsed)
	}
}

func TestEmailNotifier_RejectsCRLFRecipient(t *testing.T) {
	n := NewEmailNotifier(EmailConfig{
		Host: "127.0.0.1:1", Port: "1", From: "from@example.com",
		To: []string{"ops@example.com\r\nBcc: attacker@example.com"},
	})
	err := n.Notify(context.Background(), Event{Type: EventOffboardCompleted, TargetID: "u1"})
	if err == nil {
		t.Fatal("expected the notifier to reject a CRLF recipient")
	}
	if !errors.Is(err, ErrInvalidMailAddress) {
		t.Errorf("expected ErrInvalidMailAddress, got %v", err)
	}
}

func TestEmailNotifier_PassesTheContextToTheSend(t *testing.T) {
	// The notifier used to discard the context (`_ context.Context`), so
	// the caller's timeout never applied to the SMTP conversation.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				time.Sleep(2 * time.Second)
				_ = conn.Close()
			}()
		}
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	n := NewEmailNotifier(EmailConfig{
		Host: host, Port: port, From: "from@example.com", To: []string{"ops@example.com"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := n.Notify(ctx, Event{Type: EventOffboardCompleted, TargetID: "u1"}); err == nil {
		t.Fatal("expected the send to fail when the context expires")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the send took %v: the context was not honoured", elapsed)
	}
}

// compile-time assertion: the notifier still uses net/smtp's Auth type.
var _ smtp.Auth = smtp.PlainAuth("", "u", "p", "h")
