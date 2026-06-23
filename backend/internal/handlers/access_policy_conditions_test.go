package handlers

// D3 unit tests for the evalConditions helper introduced in D1.
// Each test calls evalConditions directly with a crafted appPolicy, clientIP,
// evalTime, and GeoIPLookup, then asserts whether reasons are returned.

import (
	"strings"
	"testing"
	"time"
)

// fakeGeoIP is a test-only GeoIPLookup that returns a fixed country code.
type fakeGeoIP struct {
	country string
}

func (f fakeGeoIP) Country(_ string) string { return f.country }

// ptr returns a pointer to a string literal — small helper for test cases.
func strPtr(s string) *string { return &s }

// ---- Time-window tests ----

func TestTimeWindowAllowsInWindow(t *testing.T) {
	// Window 08:00–18:00; eval time is 12:00 → inside window.
	p := appPolicy{
		AllowedTimeStart: strPtr("08:00"),
		AllowedTimeEnd:   strPtr("18:00"),
	}
	evalTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	reasons := evalConditions(p, "1.2.3.4", evalTime, noopGeoIP{})
	if len(reasons) != 0 {
		t.Errorf("expected no reasons for in-window access, got: %v", reasons)
	}
}

func TestTimeWindowDeniesOutsideWindow(t *testing.T) {
	// Window 09:00–17:00; eval time is 02:00 → outside window.
	p := appPolicy{
		AllowedTimeStart: strPtr("09:00"),
		AllowedTimeEnd:   strPtr("17:00"),
	}
	evalTime := time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC)
	reasons := evalConditions(p, "1.2.3.4", evalTime, noopGeoIP{})
	if len(reasons) != 1 {
		t.Fatalf("expected exactly 1 reason, got: %v", reasons)
	}
	if !strings.Contains(reasons[0], "time") {
		t.Errorf("expected time-related reason, got: %s", reasons[0])
	}
}

func TestTimeWindowBadConfig(t *testing.T) {
	// Unparseable start time → misconfigured deny.
	p := appPolicy{
		AllowedTimeStart: strPtr("notaTime"),
		AllowedTimeEnd:   strPtr("18:00"),
	}
	evalTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	reasons := evalConditions(p, "1.2.3.4", evalTime, noopGeoIP{})
	if len(reasons) != 1 {
		t.Fatalf("expected exactly 1 reason for bad config, got: %v", reasons)
	}
	if !strings.Contains(reasons[0], "misconfigured") {
		t.Errorf("expected misconfigured reason, got: %s", reasons[0])
	}
}

// ---- Network allowlist tests ----

func TestNetworkAllowlistAllowsMatchingCIDR(t *testing.T) {
	p := appPolicy{
		NetworkAllowlist: []string{"192.168.1.0/24"},
	}
	reasons := evalConditions(p, "192.168.1.42", time.Now(), noopGeoIP{})
	if len(reasons) != 0 {
		t.Errorf("expected no reasons for matching CIDR, got: %v", reasons)
	}
}

func TestNetworkAllowlistDeniesNonMatching(t *testing.T) {
	p := appPolicy{
		NetworkAllowlist: []string{"10.0.0.0/8"},
	}
	reasons := evalConditions(p, "172.16.0.1", time.Now(), noopGeoIP{})
	if len(reasons) != 1 {
		t.Fatalf("expected exactly 1 reason, got: %v", reasons)
	}
	if !strings.Contains(reasons[0], "network allowlist") {
		t.Errorf("expected network allowlist reason, got: %s", reasons[0])
	}
}

func TestNetworkAllowlistEmptyAllowsAll(t *testing.T) {
	p := appPolicy{
		NetworkAllowlist: []string{},
	}
	reasons := evalConditions(p, "8.8.8.8", time.Now(), noopGeoIP{})
	if len(reasons) != 0 {
		t.Errorf("expected no reasons for empty allowlist, got: %v", reasons)
	}
}

// ---- Geo allowlist tests ----

func TestGeoAllowlistUnknownCountryDenies(t *testing.T) {
	// noopGeoIP returns "" → fail closed.
	p := appPolicy{
		GeoCountryAllowlist: []string{"DE", "AT"},
	}
	reasons := evalConditions(p, "1.2.3.4", time.Now(), noopGeoIP{})
	if len(reasons) != 1 {
		t.Fatalf("expected exactly 1 reason, got: %v", reasons)
	}
	if !strings.Contains(reasons[0], "country unknown") {
		t.Errorf("expected country-unknown reason, got: %s", reasons[0])
	}
}

func TestGeoAllowlistMatchingCountryAllows(t *testing.T) {
	p := appPolicy{
		GeoCountryAllowlist: []string{"DE", "AT"},
	}
	reasons := evalConditions(p, "1.2.3.4", time.Now(), fakeGeoIP{country: "DE"})
	if len(reasons) != 0 {
		t.Errorf("expected no reasons for matching country, got: %v", reasons)
	}
}

func TestGeoAllowlistNonMatchingDenies(t *testing.T) {
	p := appPolicy{
		GeoCountryAllowlist: []string{"DE"},
	}
	reasons := evalConditions(p, "1.2.3.4", time.Now(), fakeGeoIP{country: "CN"})
	if len(reasons) != 1 {
		t.Fatalf("expected exactly 1 reason, got: %v", reasons)
	}
	if !strings.Contains(reasons[0], "CN") {
		t.Errorf("expected country CN in reason, got: %s", reasons[0])
	}
	if !strings.Contains(reasons[0], "geo allowlist") {
		t.Errorf("expected geo allowlist in reason, got: %s", reasons[0])
	}
}
