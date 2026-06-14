package handlers

import "testing"

func TestIsValidEmail(t *testing.T) {
	valid := []string{"a@b.com", "jane.doe@example.co.uk", "x+tag@sub.domain.org"}
	for _, e := range valid {
		if !isValidEmail(e) {
			t.Errorf("expected %q to be valid", e)
		}
	}
	invalid := []string{"", "@", "a@", "@b.com", "noatsign", "a@b", "a b@c.com", "a@b .com"}
	for _, e := range invalid {
		if isValidEmail(e) {
			t.Errorf("expected %q to be invalid", e)
		}
	}
}
