package db

import (
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
