// Failing-first tests for SMTP transport security (security lane L2,
// issue 6): STARTTLS upgrade, implicit TLS on port 465, certificate
// verification, and refusal to send over plaintext outside development/test.
package notify

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// startTLSCapableSMTPServer starts a fake SMTP server that advertises
// STARTTLS and upgrades the connection with cert when the client asks.
// upgraded is closed once the server-side TLS handshake completes;
// received collects the DATA payload.
func startTLSCapableSMTPServer(t *testing.T, cert tls.Certificate, upgraded chan<- struct{}, received *strings.Builder) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSTARTTLSConn(conn, cert, upgraded, received)
		}
	}()
	return ln.Addr().String()
}

func serveSTARTTLSConn(conn net.Conn, cert tls.Certificate, upgraded chan<- struct{}, received *strings.Builder) {
	defer conn.Close()
	_, _ = conn.Write([]byte("220 fake ESMTP\r\n"))
	r := bufio.NewReader(conn)
	var once = false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		upper := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			_, _ = conn.Write([]byte("250-fake\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n"))
		case strings.HasPrefix(upper, "STARTTLS"):
			_, _ = conn.Write([]byte("220 Go ahead\r\n"))
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			if !once && upgraded != nil {
				once = true
				close(upgraded)
			}
			conn = tlsConn
			r = bufio.NewReader(conn)
		case strings.HasPrefix(upper, "DATA"):
			_, _ = conn.Write([]byte("354 End data\r\n"))
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				received.WriteString(dl)
			}
			_, _ = conn.Write([]byte("250 OK\r\n"))
		case strings.HasPrefix(upper, "QUIT"):
			_, _ = conn.Write([]byte("221 Bye\r\n"))
			return
		default:
			_, _ = conn.Write([]byte("250 OK\r\n"))
		}
	}
}

// testSMTPCert generates a self-signed certificate valid for 127.0.0.1.
func testSMTPCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake-smtp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if err != nil {
		t.Fatalf("key pair: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return cert, pool
}

func TestSendMail_RefusesPlaintextInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	var received strings.Builder
	addr := startFakeSMTPServer(t, &received)

	err := SendMail(context.Background(), addr, nil, "from@example.com", []string{"to@example.com"}, []byte("x"))
	if err == nil {
		t.Fatal("expected plaintext SMTP to be refused in production")
	}
	if received.Len() != 0 {
		t.Fatal("no mail must be transmitted when the upgrade is refused")
	}
}

func TestSendMail_UpgradesSTARTTLS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	cert, pool := testSMTPCert(t)
	upgraded := make(chan struct{})
	var received strings.Builder
	addr := startTLSCapableSMTPServer(t, cert, upgraded, &received)

	old := smtpTLSConfigForTest
	smtpTLSConfigForTest = func(host string) *tls.Config {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: pool}
	}
	t.Cleanup(func() { smtpTLSConfigForTest = old })

	msg := "From: from@example.com\r\nTo: to@example.com\r\nSubject: tls\r\n\r\nbody\r\n"
	if err := SendMail(context.Background(), addr, nil, "from@example.com", []string{"to@example.com"}, []byte(msg)); err != nil {
		t.Fatalf("STARTTLS send: %v", err)
	}
	select {
	case <-upgraded:
	default:
		t.Fatal("the server never saw a TLS handshake")
	}
	if !strings.Contains(received.String(), "Subject: tls") {
		t.Fatalf("message not delivered: %q", received.String())
	}
}

func TestSendMail_RejectsUntrustedCertificate(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	cert, _ := testSMTPCert(t)
	var received strings.Builder
	addr := startTLSCapableSMTPServer(t, cert, nil, &received)

	// No test seam: the self-signed cert is not in the system roots.
	err := SendMail(context.Background(), addr, nil, "from@example.com", []string{"to@example.com"}, []byte("x"))
	if err == nil {
		t.Fatal("expected an untrusted STARTTLS certificate to be rejected")
	}
	if received.Len() != 0 {
		t.Fatal("no mail must be transmitted when certificate verification fails")
	}
}

func TestSendMail_ImplicitTLS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	cert, pool := testSMTPCert(t)
	var received strings.Builder

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	sawTLS := make(chan struct{})
	var once = false
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				tlsConn := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
				if err := tlsConn.Handshake(); err != nil {
					return
				}
				if !once {
					once = true
					close(sawTLS)
				}
				_, _ = tlsConn.Write([]byte("220 fake ESMTPS\r\n"))
				r := bufio.NewReader(tlsConn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					upper := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(upper, "EHLO"):
						_, _ = tlsConn.Write([]byte("250-fake\r\n250 AUTH PLAIN\r\n"))
					case strings.HasPrefix(upper, "DATA"):
						_, _ = tlsConn.Write([]byte("354 End data\r\n"))
						for {
							dl, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if strings.TrimRight(dl, "\r\n") == "." {
								break
							}
							received.WriteString(dl)
						}
						_, _ = tlsConn.Write([]byte("250 OK\r\n"))
					case strings.HasPrefix(upper, "QUIT"):
						_, _ = tlsConn.Write([]byte("221 Bye\r\n"))
						return
					default:
						_, _ = tlsConn.Write([]byte("250 OK\r\n"))
					}
				}
			}(conn)
		}
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	oldPort := smtpExtraImplicitTLSPortForTest
	smtpExtraImplicitTLSPortForTest = port
	t.Cleanup(func() { smtpExtraImplicitTLSPortForTest = oldPort })
	old := smtpTLSConfigForTest
	smtpTLSConfigForTest = func(host string) *tls.Config {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: pool}
	}
	t.Cleanup(func() { smtpTLSConfigForTest = old })

	msg := "From: from@example.com\r\nTo: to@example.com\r\nSubject: implicit\r\n\r\nbody\r\n"
	if err := SendMail(context.Background(), ln.Addr().String(), nil, "from@example.com", []string{"to@example.com"}, []byte(msg)); err != nil {
		t.Fatalf("implicit-TLS send: %v", err)
	}
	select {
	case <-sawTLS:
	default:
		t.Fatal("the server never saw a TLS handshake")
	}
	if !strings.Contains(received.String(), "Subject: implicit") {
		t.Fatalf("message not delivered: %q", received.String())
	}
}
