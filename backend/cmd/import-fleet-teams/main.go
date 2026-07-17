// Command import-fleet-teams validates a CSV of fleet_team_orgs mappings and
// prints SQL INSERT statements for operator backfill (P1 operator tooling).
//
// Usage:
//
//	go run ./cmd/import-fleet-teams mapping.csv
//	go run ./cmd/import-fleet-teams - < mapping.csv
//
// CSV lines: fleet_team_id,org_id,team_name  (# comments and blanks skipped)
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/FisiFla/freecloud/backend/internal/ops/fleetteams"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <file|->\n", os.Args[0])
		os.Exit(2)
	}
	var r io.Reader
	if os.Args[1] == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	}
	body, err := io.ReadAll(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rows, err := fleetteams.ParseMappingCSV(string(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
	for _, row := range rows {
		fmt.Println(fleetteams.SQLInsert(row))
	}
	fmt.Fprintf(os.Stderr, "ok: %d mapping row(s)\n", len(rows))
}
