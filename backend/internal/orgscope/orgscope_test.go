package orgscope

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeRow struct{ found int }

func (r fakeRow) Scan(dest ...any) error {
	if r.found == 0 {
		return pgx.ErrNoRows
	}
	*(dest[0].(*int)) = r.found
	return nil
}

type fakeQueryer struct {
	row  pgx.Row
	last string
}

func (f *fakeQueryer) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	f.last = sql
	return f.row
}

func TestCheckResourceInOrg_Found(t *testing.T) {
	q := &fakeQueryer{row: fakeRow{found: 1}}
	ok, err := CheckResourceInOrg(context.Background(), q, "review_campaigns", "id", "c-1", "org-a")
	if err != nil || !ok {
		t.Fatalf("expected found, got ok=%v err=%v", ok, err)
	}
	if q.last == "" || !contains(q.last, "review_campaigns") {
		t.Fatalf("unexpected SQL: %s", q.last)
	}
}

func TestCheckResourceInOrg_MissingIsFalseNoError(t *testing.T) {
	// pgx.ErrNoRows → (false, nil): a missing resource and a foreign-org
	// resource are indistinguishable by design.
	q := &fakeQueryer{row: fakeRow{found: 0}}
	ok, err := CheckResourceInOrg(context.Background(), q, "devices", "fleet_host_id", "h-1", "org-a")
	if err != nil || ok {
		t.Fatalf("expected missing (false, nil), got ok=%v err=%v", ok, err)
	}
}

func TestCheckResourceInOrg_RejectsNonAllowlistedTable(t *testing.T) {
	// Table/column not in the allowlist must be rejected even though the
	// strings are only ever concatenated from the fixed map.
	for _, tc := range []struct{ table, col string }{
		{"audit_logs", "id"},   // not allowlisted
		{"users", "id"},        // allowlisted table, wrong column
		{"devices", "id"},      // allowlisted table, wrong column
	} {
		_, err := CheckResourceInOrg(context.Background(), &fakeQueryer{}, tc.table, tc.col, "x", "org-a")
		if err == nil {
			t.Errorf("expected error for %s/%s", tc.table, tc.col)
		}
	}
}

func TestCheckResourceInOrg_NilQueryer(t *testing.T) {
	_, err := CheckResourceInOrg(context.Background(), nil, "users", "keycloak_user_id", "u-1", "org-a")
	if err == nil {
		t.Fatal("expected error for nil queryer")
	}
}

func TestCheckResourceInOrg_ErrorPropagates(t *testing.T) {
	q := &fakeQueryer{row: errRow{}}
	_, err := CheckResourceInOrg(context.Background(), q, "users", "keycloak_user_id", "u-1", "org-a")
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected non-NoRows error, got %v", err)
	}
}

type errRow struct{}

func (errRow) Scan(dest ...any) error { return errors.New("connection refused") }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
