// Hardened outbound HTTP client for admin-configured destinations.
//
// A save-time URL check alone cannot stop SSRF: the endpoint may redirect to
// a blocked address afterwards, or its DNS may re-resolve to one (TOCTOU).
// NewOutboundClient therefore enforces the same policy at three layers:
//
//  1. a RoundTripper that re-validates every request URL (scheme, blocked
//     hostnames, literal IPs, DNS answers) before it is sent,
//  2. a CheckRedirect that re-validates each hop, forbids https->http
//     downgrade and caps the chain length,
//  3. a dial Control hook that rejects blocked IPs on the ACTUAL address
//     being connected, closing the resolve-to-connect race.
//
// The IP block list lives here in one place (BlockedIPReason) and is shared
// with the save-time validators in the handlers package.
package httpx

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// MaxOutboundRedirects caps how many redirects the hardened outbound client
// follows. Legitimate API endpoints redirect at most once or twice; anything
// longer is treated as hostile or broken.
const MaxOutboundRedirects = 5

// MaxErrorBodyBytes bounds how much of a remote response body may be embedded
// in an error string. Full bodies end up in provisioning last_error rows and
// audit details, turning an SSRF into a full-read primitive.
const MaxErrorBodyBytes = 512

// IsDevOrTestEnv reports whether the process runs in development or test
// mode. Only there may outbound clients reach loopback/RFC1918 compose mocks
// over plain HTTP. Anything else (including an unset APP_ENV) is production.
func IsDevOrTestEnv() bool {
	env := os.Getenv("APP_ENV")
	return env == "development" || env == "test"
}

// BlockedIPReason reports why ip must never be dialled by an outbound client
// under the production policy, or "" when the address is allowed. This is the
// single shared block list: loopback, link-local (including the 169.254.0.0/16
// cloud-metadata range and fe80::/10), RFC1918, CGNAT 100.64.0.0/10,
// unique-local fc00::/7, unspecified and multicast addresses, with IPv4-mapped
// IPv6 forms normalised first so ::ffff:10.0.0.1 cannot smuggle a blocked
// IPv4 address past the check.
func BlockedIPReason(ip net.IP) string {
	if ip == nil {
		return "missing IP"
	}
	// Normalise IPv4-mapped IPv6 (::ffff:a.b.c.d) to plain IPv4 so the
	// checks below see the address that is actually connected.
	check := ip
	if ip4 := ip.To4(); ip4 != nil {
		check = ip4
	}
	switch {
	case check.IsUnspecified():
		return "unspecified address"
	case check.IsLoopback():
		return "loopback address"
	case check.IsMulticast():
		return "multicast address"
	case check.IsLinkLocalUnicast():
		return "link-local address"
	case check.IsLinkLocalMulticast():
		return "link-local multicast address"
	case check.IsPrivate():
		return "private address"
	}
	if ip4 := check.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return "carrier-grade NAT address"
		}
	}
	return ""
}

// IsBlockedOutboundIP reports whether ip is blocked under the production
// policy (see BlockedIPReason). It is environment-independent; use
// IsAllowedOutboundIP for the dev/test-aware check.
func IsBlockedOutboundIP(ip net.IP) bool {
	return BlockedIPReason(ip) != ""
}

// IsAllowedOutboundIP is the environment-aware dial check. In
// development/test it additionally allows loopback and private addresses so
// compose mocks stay reachable; link-local/metadata, unspecified, multicast
// and CGNAT addresses stay blocked everywhere.
func IsAllowedOutboundIP(ip net.IP) bool {
	if BlockedIPReason(ip) == "" {
		return true
	}
	if IsDevOrTestEnv() && (ip.IsLoopback() || ip.IsPrivate()) {
		return true
	}
	return false
}

// IsBlockedLoopbackOrLinkLocal reports whether ip is a loopback,
// link-local/metadata or unspecified address. LDAP and SMTP validators use
// this narrower policy because corporate directory and mail relays normally
// live on private (RFC1918/ULA) networks, which must stay reachable.
func IsBlockedLoopbackOrLinkLocal(ip net.IP) bool {
	if ip == nil {
		return true
	}
	check := ip
	if ip4 := ip.To4(); ip4 != nil {
		check = ip4
	}
	return check.IsUnspecified() || check.IsLoopback() ||
		check.IsLinkLocalUnicast() || check.IsLinkLocalMulticast()
}

// ValidateHostAddresses rejects host when it is a literal blocked IP or when
// any of its DNS answers is blocked. A resolution failure fails closed: an
// unverifiable destination must not receive secrets. isBlocked selects the
// policy (IsBlockedOutboundIP for HTTP(S) endpoints, a loopback/link-local
// predicate for LDAP/SMTP hosts). In development/test the check is skipped
// entirely so unit tests and compose mocks with fake hostnames keep working.
func ValidateHostAddresses(field, host string, isBlocked func(net.IP) bool) error {
	if IsDevOrTestEnv() {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlocked(ip) {
			return fmt.Errorf("%s must not target a blocked address", field)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("%s host could not be resolved", field)
	}
	for _, ip := range ips {
		if isBlocked(ip) {
			return fmt.Errorf("%s must not target a blocked address", field)
		}
	}
	return nil
}

// ValidateOutboundURL is the shared save-time SSRF guard for
// operator-supplied outbound HTTP(S) URLs. field names the request field so
// the error points at the offending input. Only https is accepted outside
// development/test; known metadata hostnames, blocked literal IPs and DNS
// answers resolving to blocked addresses are rejected.
func ValidateOutboundURL(field, raw string) error {
	return validateOutboundURLWithLookup(field, raw, net.LookupIP)
}

func validateOutboundURLWithLookup(field, raw string, lookup func(string) ([]net.IP, error)) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s must be a valid absolute URL", field)
	}
	scheme := strings.ToLower(u.Scheme)
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%s must include a host", field)
	}
	devOrTest := IsDevOrTestEnv()
	switch scheme {
	case "https":
		// always ok
	case "http":
		// HTTP only in development/test (compose mocks rarely terminate TLS).
		if !devOrTest {
			return fmt.Errorf("%s must use https", field)
		}
	default:
		return fmt.Errorf("%s scheme must be https", field)
	}

	// Block obvious metadata / internal hostnames without DNS.
	lowerHost := strings.ToLower(host)
	if lowerHost == "metadata.google.internal" ||
		strings.HasSuffix(lowerHost, ".internal") ||
		lowerHost == "metadata" {
		return fmt.Errorf("%s host is not allowed", field)
	}

	// A literal IP is checked directly against the environment-aware policy.
	if ip := net.ParseIP(host); ip != nil {
		if !IsAllowedOutboundIP(ip) {
			return fmt.Errorf("%s must not target a private or link-local address", field)
		}
		return nil
	}

	// Resolve DNS and reject when any answer is blocked. Failures fail
	// closed: an unverifiable destination must not receive secrets.
	ips, err := lookup(host)
	if err != nil {
		return fmt.Errorf("%s host could not be resolved", field)
	}
	for _, ip := range ips {
		if !IsAllowedOutboundIP(ip) {
			return fmt.Errorf("%s must not target a private or link-local address", field)
		}
	}
	return nil
}

// OutboundDialControl is the net.Dialer Control hook used by the hardened
// outbound client. It sees the ACTUAL address being connected (Go resolves
// first, then calls Control per candidate), so a hostname that re-resolves
// to a blocked IP mid-flight is still refused. Unparseable addresses fail
// closed. Exported so the policy is unit-testable.
func OutboundDialControl(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("outbound: unparseable dial address %q", address)
	}
	// Zone-scoped literals (fe80::1%eth0) must not bypass the check.
	if i := strings.LastIndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return fmt.Errorf("outbound: unparseable dial address %q", address)
	}
	if !IsAllowedOutboundIP(ip) {
		return fmt.Errorf("outbound: dial to %s blocked (%s)", host, BlockedIPReason(ip))
	}
	return nil
}

// outboundRoundTripper re-validates the request URL against the outbound
// policy before it is sent, so a stale or legacy stored URL that would no
// longer pass the save-time check is still refused at use time.
type outboundRoundTripper struct {
	base http.RoundTripper
}

func (rt outboundRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	lookup := func(host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(req.Context(), "ip", host)
	}
	if err := validateOutboundURLWithLookup("url", req.URL.String(), lookup); err != nil {
		return nil, err
	}
	return rt.base.RoundTrip(req)
}

// CheckOutboundRedirect re-validates every redirect hop: it forbids
// https->http downgrade, caps the chain and applies the full outbound URL
// policy to the new location. Exported so the policy is unit-testable; wired
// into the client by NewOutboundClient.
func CheckOutboundRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxOutboundRedirects {
		return fmt.Errorf("outbound: too many redirects (> %d)", MaxOutboundRedirects)
	}
	if len(via) > 0 {
		prevScheme := strings.ToLower(via[len(via)-1].URL.Scheme)
		if prevScheme == "https" && strings.ToLower(req.URL.Scheme) != "https" {
			return fmt.Errorf("outbound: refusing https->http redirect downgrade")
		}
	}
	lookup := func(host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(req.Context(), "ip", host)
	}
	if err := validateOutboundURLWithLookup("redirect", req.URL.String(), lookup); err != nil {
		return err
	}
	return nil
}

// NewOutboundClient builds the hardened HTTP client for admin-configured
// destinations (provisioning connectors, DB-configured Fleet URL). It
// re-validates every request and redirect hop and refuses to connect to
// blocked IPs at dial time. timeout bounds the whole request; <=0 selects a
// 15s default.
func NewOutboundClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   OutboundDialControl,
	}).DialContext
	return &http.Client{
		Transport:     outboundRoundTripper{base: transport},
		CheckRedirect: CheckOutboundRedirect,
		Timeout:       timeout,
	}
}

// TruncateErrorBody renders a remote response body safe for error strings:
// short bodies pass through, longer ones are cut to MaxErrorBodyBytes with an
// explicit marker. Use it everywhere a remote body is embedded in an error so
// last_error rows and audit details cannot become full-read exfil channels.
func TruncateErrorBody(body []byte) string {
	if len(body) <= MaxErrorBodyBytes {
		return string(body)
	}
	return fmt.Sprintf("%s...[truncated, %d bytes total]", string(body[:MaxErrorBodyBytes]), len(body))
}

// DecodeBoundedJSON reads r with ReadAllBounded and unmarshals the result
// into v. Use it instead of json.NewDecoder(resp.Body).Decode on every
// success path that buffers a remote body. limit <= 0 selects MaxResponseBytes.
func DecodeBoundedJSON(r io.Reader, limit int64, v any) error {
	body, err := ReadAllBounded(r, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
