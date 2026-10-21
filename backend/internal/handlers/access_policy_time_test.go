package handlers

// Regression tests for the conditional-access time window.
//
// A TIME column read through pgx is rendered as "09:00:00.000000" (pgx's
// TimeCodec encodes binary time as "%02d:%02d:%02d.%06d"). The original
// implementation parsed that string with time.Parse("15:04", ...), which
// always failed, so every policy carrying a time window denied every request
// regardless of the actual time. These tests pin the tolerant parsing, the
// "HH:MM" wire format, and the rejection of half-specified windows.

import (
	"strings"
	"testing"
	"time"
)

func TestParsePolicyTimeAcceptsDatabaseShape(t *testing.T) {
	// The exact string pgx produces for a TIME column.
	cases := []struct {
		in   string
		want int
	}{
		{"09:00:00.000000", 9 * 60},
		{"00:00:00.000000", 0},
		{"17:30:00.000000", 17*60 + 30},
		{"23:59:00.000000", 23*60 + 59},
		{"09:00", 9 * 60},
		{"09:00:00", 9 * 60},
		{"9:00", 9 * 60},
	}
	for _, tc := range cases {
		got, ok := parsePolicyTime(tc.in)
		if !ok {
			t.Errorf("parsePolicyTime(%q) failed to parse", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("parsePolicyTime(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParsePolicyTimeRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "notaTime", "25:00", "09:61", "abc:de"} {
		if _, ok := parsePolicyTime(in); ok {
			t.Errorf("parsePolicyTime(%q) unexpectedly succeeded", in)
		}
	}
}

func TestFormatPolicyTime(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "00:00"},
		{9 * 60, "09:00"},
		{17*60 + 30, "17:30"},
		{23*60 + 59, "23:59"},
	}
	for _, tc := range cases {
		if got := formatPolicyTime(tc.in); got != tc.want {
			t.Errorf("formatPolicyTime(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalisePolicyTime(t *testing.T) {
	// A value straight out of the database becomes the documented "HH:MM".
	got := normalisePolicyTime(strPtr("09:00:00.000000"))
	if got == nil || *got != "09:00" {
		t.Fatalf("normalisePolicyTime = %v, want 09:00", got)
	}
	// nil stays nil (no window configured).
	if normalisePolicyTime(nil) != nil {
		t.Error("normalisePolicyTime(nil) should stay nil")
	}
	// An unparsable value is left untouched rather than silently blanked.
	bad := normalisePolicyTime(strPtr("notaTime"))
	if bad == nil || *bad != "notaTime" {
		t.Errorf("normalisePolicyTime(notaTime) = %v, want the input unchanged", bad)
	}
}

// TestTimeWindowAllowsInWindowDatabaseShape is the regression test for the
// always-deny defect: the values here are exactly what pgx hands back for a
// TIME column, and access inside the window must be allowed.
func TestTimeWindowAllowsInWindowDatabaseShape(t *testing.T) {
	p := appPolicy{
		AllowedTimeStart: strPtr("09:00:00.000000"),
		AllowedTimeEnd:   strPtr("17:00:00.000000"),
	}
	evalTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	reasons := evalConditions(p, "1.2.3.4", evalTime, noopGeoIP{})
	if len(reasons) != 0 {
		t.Fatalf("expected no reasons for in-window access, got: %v", reasons)
	}
}

func TestTimeWindowDeniesOutsideWindowDatabaseShape(t *testing.T) {
	p := appPolicy{
		AllowedTimeStart: strPtr("09:00:00.000000"),
		AllowedTimeEnd:   strPtr("17:00:00.000000"),
	}
	evalTime := time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC)
	reasons := evalConditions(p, "1.2.3.4", evalTime, noopGeoIP{})
	if len(reasons) != 1 {
		t.Fatalf("expected exactly 1 reason, got: %v", reasons)
	}
	if !strings.Contains(reasons[0], "09:00") || !strings.Contains(reasons[0], "17:00") {
		t.Errorf("expected the window bounds in the reason, got: %s", reasons[0])
	}
}

func TestValidatePolicyTimeWindow(t *testing.T) {
	// No window at all is valid.
	if _, _, _, msg := validatePolicyTimeWindow(nil, nil); msg != "" {
		t.Errorf("expected no error for an absent window, got %q", msg)
	}

	// Both bounds present and parsable is valid, and they are normalised.
	start, end, _, msg := validatePolicyTimeWindow(strPtr("09:00"), strPtr("17:00"))
	if msg != "" {
		t.Fatalf("unexpected error: %q", msg)
	}
	if start != "09:00" || end != "17:00" {
		t.Errorf("got start=%q end=%q, want 09:00 / 17:00", start, end)
	}

	// A half-specified window must be rejected rather than silently ignored.
	if _, _, field, msg := validatePolicyTimeWindow(strPtr("09:00"), nil); msg == "" {
		t.Error("expected an error for a window with only a start bound")
	} else if field != "allowedTimeEnd" {
		t.Errorf("expected field allowedTimeEnd, got %q", field)
	}
	if _, _, field, msg := validatePolicyTimeWindow(nil, strPtr("17:00")); msg == "" {
		t.Error("expected an error for a window with only an end bound")
	} else if field != "allowedTimeStart" {
		t.Errorf("expected field allowedTimeStart, got %q", field)
	}

	// An unparsable bound is rejected.
	if _, _, _, msg := validatePolicyTimeWindow(strPtr("notaTime"), strPtr("17:00")); msg == "" {
		t.Error("expected an error for an unparsable start bound")
	}
	if _, _, _, msg := validatePolicyTimeWindow(strPtr("09:00"), strPtr("notaTime")); msg == "" {
		t.Error("expected an error for an unparsable end bound")
	}
}
