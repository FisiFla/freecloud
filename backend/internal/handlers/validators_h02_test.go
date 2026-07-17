package handlers

import "testing"

func TestH02_ValidateHostID(t *testing.T) {
	if err := ValidateHostID("host-1"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "a/b", "..x", ".hidden", string(make([]byte, 257))} {
		if err := ValidateHostID(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
