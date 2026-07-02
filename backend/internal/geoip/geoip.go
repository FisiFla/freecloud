// Package geoip provides a real, MaxMind-GeoLite2-backed implementation of
// handlers.GeoIPLookup (A2). The no-op default in internal/handlers/geoip.go
// always returns "" (unknown), which makes geo-allowlist conditions fail
// closed. This package lets an operator opt into live geo resolution by
// supplying their own GeoLite2 (or commercial GeoIP2) City/Country mmdb file.
//
// FAIL-CLOSED: Open() returns an error (never a lookup that silently degrades
// to "unknown") when the configured path is set but unreadable or corrupt —
// callers (cmd/server/main.go) must refuse to start rather than boot with a
// geo gate that always denies. When GEOIP_MMDB_PATH is unset entirely, the
// caller should keep using the no-op default instead of calling Open.
package geoip

import (
	"fmt"
	"net/netip"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

// Resolver resolves client IPs to ISO 3166-1 alpha-2 country codes using a
// MaxMind mmdb file (GeoLite2-Country, GeoLite2-City, or the commercial
// GeoIP2 equivalents — all share the same "country.iso_code" record shape).
// Safe for concurrent use — the underlying reader is read-only after Open.
type Resolver struct {
	db *maxminddb.Reader
}

// countryRecord matches the subset of the GeoIP2/GeoLite2 City and Country
// database schemas this package needs. Both database types include this
// "country" block, so one struct works for either.
type countryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// Open loads the mmdb file at path. Returns an error if the file does not
// exist, cannot be read, or is not a valid mmdb — callers must treat any
// error as fatal-at-startup (fail closed), never fall back to a no-op lookup.
func Open(path string) (*Resolver, error) {
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: open mmdb %q: %w", path, err)
	}
	// Open() parses the mmdb metadata section as part of opening the file, but
	// a truncated/garbage file can still parse into zero-valued metadata
	// without erroring. Sanity-check the fields that must be present in any
	// real mmdb so a corrupt file fails at startup rather than degrading
	// every subsequent lookup to "unknown".
	if db.Metadata.DatabaseType == "" || db.Metadata.NodeCount == 0 {
		db.Close()
		return nil, fmt.Errorf("geoip: mmdb %q has empty/invalid metadata (not a valid MaxMind DB)", path)
	}
	return &Resolver{db: db}, nil
}

// Close releases the underlying memory-mapped file.
func (r *Resolver) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// Country resolves ip to an ISO 3166-1 alpha-2 country code. Returns "" for
// an unparseable IP, an IP absent from the database, or any decode error —
// callers (the access-eval fail-closed geo condition) treat "" as unknown.
func (r *Resolver) Country(ip string) string {
	if r == nil || r.db == nil {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	var rec countryRecord
	if err := r.db.Lookup(addr).Decode(&rec); err != nil {
		return ""
	}
	return rec.Country.ISOCode
}
