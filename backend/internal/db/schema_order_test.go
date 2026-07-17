package db

import (
	"strings"
	"testing"
)

// TestMigrationsAreStrictlyAscending asserts that the migrations slice is
// ordered by strictly increasing id values. A mis-ordered append would cause
// subtle bugs (migrations might be skipped or re-run in the wrong sequence),
// so we catch it early here.
func TestMigrationsAreStrictlyAscending(t *testing.T) {
	if len(migrations) == 0 {
		t.Fatal("migrations slice is empty")
	}
	for i := 1; i < len(migrations); i++ {
		if migrations[i].id <= migrations[i-1].id {
			t.Errorf("migration at index %d (id=%d) is not strictly greater than the previous (id=%d); migrations must be in ascending id order",
				i, migrations[i].id, migrations[i-1].id)
		}
	}
}

func TestMigration046FleetTeamOrgsRegistered(t *testing.T) {
	found := false
	for _, m := range migrations {
		if m.id == 46 && m.name == "fleet_team_orgs" {
			found = true
			if !strings.Contains(m.statement, "CREATE TABLE IF NOT EXISTS fleet_team_orgs") {
				t.Fatal("Migration046 statement missing fleet_team_orgs table")
			}
			break
		}
	}
	if !found {
		t.Fatal("Migration046 fleet_team_orgs not registered in migrations slice")
	}
	if LatestMigrationID() < 46 {
		t.Fatalf("LatestMigrationID()=%d want >= 46", LatestMigrationID())
	}
}
