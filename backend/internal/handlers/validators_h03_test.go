package handlers

import "testing"

func TestH03_ValidateTeamDescription(t *testing.T) {
	if err := ValidateTeamDescription("fine"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTeamDescription("bad\x01"); err == nil {
		t.Fatal("expected control char error")
	}
	if err := ValidateTeamDescription(string(make([]byte, 501))); err == nil {
		t.Fatal("expected length error")
	}
}
