// Command genfixturemmdb writes a tiny, non-MaxMind-licensed GeoLite2-Country-
// shaped mmdb file for the A2 e2e geo-condition enforcement test
// (TestE2E_Admin_GeoCondition_Enforcement). It is deliberately NOT baked into
// the production Docker image — the e2e workflow runs it once before
// `docker compose up` and bind-mounts the resulting file into backend-e2e via
// GEOIP_MMDB_PATH. See docs/DEPLOYMENT.md "GeoIP (MaxMind GeoLite2)" for how a
// real deployment supplies its own operator-downloaded GeoLite2 database
// instead — this tool exists solely so e2e doesn't need one.
//
// Usage: go run ./cmd/genfixturemmdb -out /path/to/fixture.mmdb
package main

import (
	"flag"
	"log"
	"net"
	"os"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

func main() {
	out := flag.String("out", "fixture.mmdb", "output path for the generated mmdb file")
	flag.Parse()

	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType:            "GeoLite2-Country",
		Languages:               []string{"en"},
		RecordSize:              24,
		IncludeReservedNetworks: true,
	})
	if err != nil {
		log.Fatalf("mmdbwriter.New: %v", err)
	}

	// POST /api/v1/access/evaluate accepts an explicit "clientIp" field in its
	// JSON body (used by the Keycloak SPI when forwarding the real client IP —
	// see A3), so the e2e test can simply send one of these documentation-range
	// IPs directly without needing any special container networking.
	entries := []struct {
		cidr    string
		country string
	}{
		{"203.0.113.0/24", "DE"},  // "allowed" country for the e2e policy
		{"198.51.100.0/24", "US"}, // "disallowed" country for the e2e policy
	}
	for _, e := range entries {
		_, network, err := net.ParseCIDR(e.cidr)
		if err != nil {
			log.Fatalf("parse CIDR %q: %v", e.cidr, err)
		}
		record := mmdbtype.Map{
			"country": mmdbtype.Map{
				"iso_code": mmdbtype.String(e.country),
			},
		}
		if err := tree.Insert(network, record); err != nil {
			log.Fatalf("insert %q: %v", e.cidr, err)
		}
	}

	f, err := os.Create(*out)
	if err != nil {
		log.Fatalf("create %q: %v", *out, err)
	}
	defer f.Close()

	if _, err := tree.WriteTo(f); err != nil {
		log.Fatalf("write mmdb: %v", err)
	}
	log.Printf("wrote fixture mmdb to %s", *out)
}
