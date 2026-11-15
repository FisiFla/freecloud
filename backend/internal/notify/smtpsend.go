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
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/FisiFla/freecloud/backend/internal/httpx"
)

const (
	// smtpDialTimeout bounds the TCP connect phase.
	smtpDialTimeout = 10 * time.Second
	// smtpConversationTimeout bounds the SMTP conversation when the caller
	// supplies a context without a deadline.
	smtpConversationTimeout = 30 * time.Second
	// smtpImplicitTLSPort is the well-known port for implicit TLS (SMTPS).
	smtpImplicitTLSPort = "465"
)

// smtpTLSConfigForTest overrides the TLS configuration in tests (to trust a
// test certificate). It is nil in production, where the system roots and
// mandatory verification apply. Certificate verification is never disabled.
var smtpTLSConfigForTest func(host string) *tls.Config

// smtpExtraImplicitTLSPortForTest makes one additional port use implicit TLS
// in tests, so the SMTPS path is exercisable without binding port 465. Empty
// in production.
var smtpExtraImplicitTLSPortForTest string

// smtpTLSConfig returns the TLS configuration for host: verified certificates
// against the system roots, TLS 1.2 minimum, no InsecureSkipVerify anywhere.
func smtpTLSConfig(host string) *tls.Config {
	if smtpTLSConfigForTest != nil {
		return smtpTLSConfigForTest(host)
	}
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func isImplicitTLSPort(port string) bool {
	return port == smtpImplicitTLSPort ||
		(port != "" && port == smtpExtraImplicitTLSPortForTest)
}

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

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	dialer := net.Dialer{Timeout: smtpDialTimeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	defer rawConn.Close()

	conn := rawConn
	// Port 465 uses implicit TLS: the TLS handshake happens immediately,
	// before any SMTP bytes are exchanged.
	if isImplicitTLSPort(port) {
		tlsConn := tls.Client(rawConn, smtpTLSConfig(host))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("smtp: TLS handshake with %s: %w", addr, err)
		}
		conn = tlsConn
		// client.Close below closes the TLS session (and with it the raw
		// connection); the deferred rawConn.Close is then a harmless no-op.
	}

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

	if !isImplicitTLSPort(port) {
		// Opportunistic STARTTLS on every other port. Outside
		// development/test a relay that cannot be upgraded is refused:
		// credentials and mail must never travel in cleartext.
		if ok, _ := client.Extension("STARTTLS"); ok {
			// StartTLS upgrades the session and re-issues EHLO itself, so
			// the extension state afterwards reflects the encrypted session.
			if err := client.StartTLS(smtpTLSConfig(host)); err != nil {
				return fmt.Errorf("smtp: STARTTLS with %s: %w", addr, err)
			}
		} else if !httpx.IsDevOrTestEnv() {
			return fmt.Errorf("smtp: server %s does not support STARTTLS: refusing to send credentials or mail in cleartext", addr)
		}
	}

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
