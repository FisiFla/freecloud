// Command import-fleet-teams validates a CSV of fleet_team_orgs mappings and
// either prints SQL INSERT statements (default) or applies them live against
// DATABASE_URL when -apply is set (P1 operator tooling).
//
// Usage:
//
//	go run ./cmd/import-fleet-teams mapping.csv
//	go run ./cmd/import-fleet-teams - < mapping.csv
//	DATABASE_URL=postgres://... go run ./cmd/import-fleet-teams -apply mapping.csv
//
// CSV lines: fleet_team_id,org_id,team_name  (# comments and blanks skipped)
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FisiFla/freecloud/backend/internal/httpx"
	"github.com/FisiFla/freecloud/backend/internal/ops/fleetteams"
)

func main() {
	apply := flag.Bool("apply", false, "apply mappings live to DATABASE_URL instead of printing SQL")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: %s [-apply] <file|->\n", os.Args[0])
		os.Exit(2)
	}
	var r io.Reader
	if flag.Arg(0) == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	}
	body, err := httpx.ReadAllBounded(r, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rows, err := fleetteams.ParseMappingCSV(string(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}

	if !*apply {
		for _, row := range rows {
			fmt.Println(fleetteams.SQLInsert(row))
		}
		fmt.Fprintf(os.Stderr, "ok: %d mapping row(s) (SQL printed; use -apply to execute)\n", len(rows))
		return
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "apply: DATABASE_URL is required")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply: connect:", err)
		os.Exit(1)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apply: begin:", err)
		os.Exit(1)
	}
	n, err := fleetteams.ApplyMappingsTx(ctx, tx, rows)
	if err != nil {
		tx.Rollback(ctx)
		fmt.Fprintln(os.Stderr, "apply:", err)
		os.Exit(1)
	}
	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "apply: commit:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "ok: applied %d mapping row(s)\n", n)
}
