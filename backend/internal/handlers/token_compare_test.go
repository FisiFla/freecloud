package handlers

import (
	"strings"
	"testing"
)

// The setup-token (and bearer-token) comparison hashes both sides before the
// constant-time compare, so mismatched lengths do not fail fast and leak the
// expected token's length through timing.
func TestConstantTimeStringEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"equal", "s3cret-setup-token", "s3cret-setup-token", true},
		{"empty equal", "", "", true},
		{"different same length", "aaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaab", false},
		{"different lengths", "s3cret-setup-token", "x", false},
		{"different lengths reversed", "x", "s3cret-setup-token", false},
		{"empty vs non-empty", "", "s3cret", false},
		{"prefix", "s3cret", "s3cret-setup-token", false},
		{"long equal", strings.Repeat("k", 4096), strings.Repeat("k", 4096), true},
		{"long differ in last byte", strings.Repeat("k", 4096), strings.Repeat("k", 4095) + "l", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := constantTimeStringEqual(c.a, c.b); got != c.want {
				t.Errorf("constantTimeStringEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
			// Symmetry: argument order must not matter.
			if got := constantTimeStringEqual(c.b, c.a); got != c.want {
				t.Errorf("constantTimeStringEqual(%q, %q) = %v, want %v (reversed)", c.b, c.a, got, c.want)
			}
		})
	}
}
