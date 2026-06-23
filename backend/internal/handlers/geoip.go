package handlers

// GeoIPLookup resolves an IP address to an ISO 3166-1 alpha-2 country code.
// An empty string return means "unknown".
// The no-op default always returns "" — live geo is an operator-supplied plugin.
//
// FAIL-CLOSED: when GeoCountryAllowlist is non-empty and the lookup returns ""
// (unknown), access is denied. Operators that want geo-gating must supply a
// real GeoIPLookup implementation via Handler.SetGeoIPLookup.
type GeoIPLookup interface {
	Country(ip string) string
}

// noopGeoIP is the default implementation: always returns "" (unknown).
type noopGeoIP struct{}

func (noopGeoIP) Country(_ string) string { return "" }
