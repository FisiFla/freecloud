package middleware

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// ipv6KeyPrefixLen is the prefix length used to bucket IPv6 clients.
//
// An IPv6 client is normally assigned a /64 prefix, so keying on the full
// 128-bit address lets a single client mint an unbounded number of buckets by
// rotating addresses within its own prefix. Bucketing on the /64 keeps every
// address of one client in the same bucket (issue #137).
const ipv6KeyPrefixLen = 64

// ClientKeyer derives the rate-limit bucket key for a request.
//
// The key is the CLIENT address. When the request arrives through a reverse
// proxy that is listed in the trusted-proxy set, the address the proxy
// forwarded is used; otherwise the peer address is used and the
// X-Forwarded-For header is IGNORED, because that header is trivially
// spoofable by the client.
//
// Behind a single reverse proxy every request otherwise arrives from the
// proxy's own address, so all tenants share one bucket and one attacker can
// rate-limit every other tenant (issue #129).
//
// The bucket key deliberately does NOT include the proxy's identity: keying
// on (proxy, client) would hand a client a fresh budget every time it moved
// between proxies, which is a rate-limit bypass. Keying on the client alone
// keeps one budget per client, whichever proxy forwarded it.
type ClientKeyer struct {
	// trustedProxies are the proxy prefixes whose X-Forwarded-For header may
	// be trusted. Empty means no proxy is trusted (the safe default).
	trustedProxies []netip.Prefix
}

// NewClientKeyer builds a ClientKeyer from a list of trusted proxy CIDRs or
// bare IP addresses. An unparsable entry is an error: silently ignoring it
// would leave the limiter keying on the proxy address (the bug this fixes)
// while appearing configured.
func NewClientKeyer(trustedProxyCIDRs []string) (*ClientKeyer, error) {
	k := &ClientKeyer{}
	for _, raw := range trustedProxyCIDRs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			p, err := netip.ParsePrefix(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", raw, err)
			}
			k.trustedProxies = append(k.trustedProxies, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy address %q: %w", raw, err)
		}
		k.trustedProxies = append(k.trustedProxies, netip.PrefixFrom(a, a.BitLen()))
	}
	return k, nil
}

// ClientKey returns the rate-limit bucket key for the request.
//
// A nil ClientKeyer behaves like a keyer with no trusted proxies: only the
// peer address is used.
func (k *ClientKeyer) ClientKey(r *http.Request) string {
	peer := clientIP(r)
	if k == nil || len(k.trustedProxies) == 0 {
		return bucketKey(peer)
	}
	peerAddr, err := netip.ParseAddr(peer)
	if err != nil || !k.isTrusted(peerAddr) {
		// The request did not come through a trusted proxy: the forwarded
		// header is client-controlled, so it is ignored entirely.
		return bucketKey(peer)
	}
	// The peer is a trusted proxy, so the X-Forwarded-For header it set is
	// meaningful. Walk it from the RIGHT: the rightmost entries were added
	// by proxies (which are trusted infrastructure), the leftmost ones were
	// supplied by the client. The first address from the right that is NOT a
	// trusted proxy is the real client.
	for _, field := range reverseForwardedFor(r.Header.Values("X-Forwarded-For")) {
		addr, err := netip.ParseAddr(field)
		if err != nil {
			// Unparsable entry: stop, everything to the left is
			// client-controlled and therefore not trustworthy.
			return bucketKey(peer)
		}
		if k.isTrusted(addr) {
			continue
		}
		return bucketKey(addr.String())
	}
	return bucketKey(peer)
}

// isTrusted reports whether addr falls inside one of the trusted proxy
// prefixes.
func (k *ClientKeyer) isTrusted(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, p := range k.trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// bucketKey normalises a client address into a rate-limit bucket key.
// IPv6 addresses are truncated to their /64 prefix so one client cannot mint
// unbounded buckets by rotating addresses within its own prefix (issue #137).
func bucketKey(addr string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		// Not an IP (e.g. a unix socket path): use the raw value.
		return addr
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(ipv6KeyPrefixLen)
	if err != nil {
		return a.String()
	}
	return p.Masked().String()
}

// reverseForwardedFor flattens the X-Forwarded-For header values into the
// individual hop addresses and returns them in REVERSE order.
//
// A proxy appends the address it saw to any existing header, so the
// right-most hop is the most trustworthy one: it was written by
// infrastructure the operator controls, while everything to its left was
// supplied by the client and can be forged.
func reverseForwardedFor(values []string) []string {
	var fields []string
	for _, v := range values {
		for _, f := range strings.Split(v, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				fields = append(fields, f)
			}
		}
	}
	out := make([]string, 0, len(fields))
	for i := len(fields) - 1; i >= 0; i-- {
		out = append(out, fields[i])
	}
	return out
}
