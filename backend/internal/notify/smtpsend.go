package notify

// Bounded, context-aware SMTP delivery.
//
// net/smtp's SendMail has no timeout and no context support: a hung relay
// keeps the connection (and the caller's goroutine) open forever. Every
// caller in this codebase wraps a send in a goroutine with a 10-second
// context, but that context never reached net/smtp, so those goroutines could
// live indefinitely.
//
// SendMail dials with a bounded net.Dialer and applies the context deadline
// to the whole SMTP conversation, so a stalled relay cannot outlive the
// caller's deadline. It also rejects addresses containing CR/LF: the raw
// message is assembled by string concatenation, so a CR/LF in an address
// would inject arbitrary SMTP headers (Bcc, Subject, ...).

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

const (
	// smtpDialTimeout bounds the TCP connect phase.
	smtpDialTimeout = 10 * time.Second
	// smtpConversationTimeout bounds the SMTP conversation when the caller
	// supplies a context without a deadline.
	smtpConversationTimeout = 30 * time.Second
)

// ErrInvalidMailAddress is returned when an address contains characters that
// would break out of the message headers.
var ErrInvalidMailAddress = errors.New("invalid mail address")

// ValidateMailAddress rejects an address that is empty or contains CR/LF.
// Header injection is the reason this exists: a "to" value of
// "victim@example.com\r\nBcc: attacker@example.com" would otherwise add
// headers to the outgoing message.
func ValidateMailAddress(addr string) error {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return fmt.Errorf("%w: address is empty", ErrInvalidMailAddress)
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return fmt.Errorf("%w: address contains a line break", ErrInvalidMailAddress)
	}
	return nil
}

// SendMail sends a message over SMTP, bounded by ctx.
//
// It is a drop-in replacement for smtp.SendMail with two differences: the
// dial and the conversation are bounded by the context deadline, and every
// envelope address is validated for CR/LF before it reaches the wire.
func SendMail(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateMailAddress(from); err != nil {
		return fmt.Errorf("from address: %w", err)
	}
	if len(to) == 0 {
		return fmt.Errorf("%w: no recipients", ErrInvalidMailAddress)
	}
	for _, rcpt := range to {
		if err := ValidateMailAddress(rcpt); err != nil {
			return fmt.Errorf("recipient address: %w", err)
		}
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	dialer := net.Dialer{Timeout: smtpDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	defer conn.Close()

	// Bound the whole conversation. A deadline (not a per-operation timeout)
	// is what makes a half-open relay release the goroutine.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(smtpConversationTimeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp: set deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp: greet %s: %w", addr, err)
	}
	defer client.Close()

	if auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp: server %s does not support AUTH", addr)
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp: RCPT TO: %w", err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: close body: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp: QUIT: %w", err)
	}
	return nil
}
