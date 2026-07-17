package handlers

import "testing"

func TestH01_ValidatePolicyID(t *testing.T) {
	if err := ValidatePolicyID("ok-policy"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "a/b", "x..y", string(make([]byte, 129))} {
		if err := ValidatePolicyID(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
