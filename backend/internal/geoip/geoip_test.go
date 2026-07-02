package geoip

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

// buildFixtureMMDB generates a tiny GeoLite2-Country-shaped mmdb at test
// runtime (via mmdbwriter) mapping a couple of known CIDRs to country codes.
// No binary fixture is committed — this is deliberate per the A2 ticket, so
// the test never depends on redistributing (or accidentally committing)
// MaxMind's licensed data.
func buildFixtureMMDB(t *testing.T) string {
	t.Helper()

	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: "GeoLite2-Country",
		Languages:    []string{"en"},
		RecordSize:   24,
		// The fixture uses documentation-range IPs (RFC 5737 203.0.113.0/24),
		// which mmdbwriter treats as "reserved" and excludes by default.
		IncludeReservedNetworks: true,
	})
	if err != nil {
		t.Fatalf("mmdbwriter.New: %v", err)
	}

	entries := []struct {
		cidr    string
		country string
	}{
		{"81.2.69.0/24", "GB"},
		{"203.0.113.0/24", "US"},
	}
	for _, e := range entries {
		_, network, err := net.ParseCIDR(e.cidr)
		if err != nil {
			t.Fatalf("parse CIDR %q: %v", e.cidr, err)
		}
		record := mmdbtype.Map{
			"country": mmdbtype.Map{
				"iso_code": mmdbtype.String(e.country),
			},
		}
		if err := tree.Insert(network, record); err != nil {
			t.Fatalf("insert %q: %v", e.cidr, err)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.mmdb")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture file: %v", err)
	}
	defer f.Close()

	if _, err := tree.WriteTo(f); err != nil {
		t.Fatalf("write fixture mmdb: %v", err)
	}
	return path
}

func TestOpen_KnownIPResolvesCountry(t *testing.T) {
	path := buildFixtureMMDB(t)

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	if got := r.Country("81.2.69.142"); got != "GB" {
		t.Errorf("Country(81.2.69.142) = %q, want %q", got, "GB")
	}
	if got := r.Country("203.0.113.55"); got != "US" {
		t.Errorf("Country(203.0.113.55) = %q, want %q", got, "US")
	}
}

func TestOpen_UnknownIPReturnsEmpty(t *testing.T) {
	path := buildFixtureMMDB(t)

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	// An IP not covered by any inserted network — the fail-closed contract in
	// evalConditions() treats "" as unknown and denies when a geo allowlist exists.
	if got := r.Country("8.8.8.8"); got != "" {
		t.Errorf("Country(8.8.8.8) = %q, want empty (unknown)", got)
	}
}

func TestOpen_UnparseableIPReturnsEmpty(t *testing.T) {
	path := buildFixtureMMDB(t)

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	if got := r.Country("not-an-ip"); got != "" {
		t.Errorf("Country(not-an-ip) = %q, want empty", got)
	}
	if got := r.Country(""); got != "" {
		t.Errorf("Country(\"\") = %q, want empty", got)
	}
}

func TestOpen_MissingFileFailsClosed(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "does-not-exist.mmdb"))
	if err == nil {
		t.Fatal("expected an error opening a nonexistent mmdb path, got nil")
	}
}

func TestOpen_CorruptFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.mmdb")
	if err := os.WriteFile(path, []byte("this is not a valid mmdb file"), 0o644); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	_, err := Open(path)
	if err == nil {
		t.Fatal("expected an error opening a corrupt mmdb file, got nil")
	}
}

func TestResolver_NilSafe(t *testing.T) {
	var r *Resolver
	if got := r.Country("1.2.3.4"); got != "" {
		t.Errorf("nil *Resolver.Country() = %q, want empty", got)
	}
	if err := r.Close(); err != nil {
		t.Errorf("nil *Resolver.Close() = %v, want nil", err)
	}
}
