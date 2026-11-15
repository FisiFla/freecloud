package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// keyedRows serves chain rows plus the row_mac column (dest[10]).
func keyedRows(entries []ChainEntry, macs []string) pgx.Rows {
	base := chainRows(entries).(*fakeRows)
	for i := range base.scanFns {
		inner, mac := base.scanFns[i], macs[i]
		base.scanFns[i] = func(dest ...any) error {
			*dest[10].(*string) = mac
			return inner(dest...)
		}
	}
	return base
}

func TestVerifyChainKeyed(t *testing.T) {
	key := DeriveChainKey([]byte("0123456789abcdef0123456789abcdef"))
	oldKey := DeriveChainKey([]byte("ffffffffffffffffffffffffffffffff"))
	SetChainKey(key, oldKey)
	defer SetChainKey(nil, nil)

	chain := buildChain([]ChainEntry{
		{Seq: 1, ID: "a", ActorID: "alice", Action: "onboard", Details: "{}"},
		{Seq: 2, ID: "b", ActorID: "bob", Action: "offboard", Details: "{}"},
		{Seq: 3, ID: "c", ActorID: "alice", Action: "login", Details: "{}"},
	})
	good := []string{"", macHex(oldKey, chain[1].RowHash), macHex(key, chain[2].RowHash)}

	cases := []struct {
		name    string
		macs    []string
		wantOK  bool
		wantErr string
	}{
		{"legacy prefix, previous and current key", good, true, ""},
		{"forged mac", []string{"", good[1], macHex([]byte("attacker"), chain[2].RowHash)}, false, "row_mac mismatch"},
		{"mac stripped after keyed rows", []string{"", good[1], ""}, false, "missing row_mac"},
		{"all macs stripped", []string{"", "", ""}, false, "chain head carries no row_mac"},
	}
	for _, tc := range cases {
		db := &fakeDB{queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return keyedRows(chain, tc.macs), nil
		}}
		res, err := VerifyChain(context.Background(), db)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if res.OK != tc.wantOK || !strings.Contains(res.Error, tc.wantErr) {
			t.Errorf("%s: got OK=%v error=%q", tc.name, res.OK, res.Error)
		}
	}
}
