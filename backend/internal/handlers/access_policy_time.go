package handlers

import (
	"fmt"
	"strings"
	"time"
)

// Time-window helpers for the D1 conditional-access policy.
//
// The policy columns allowed_time_start / allowed_time_end are Postgres TIME
// columns. pgx scans a TIME column in its preferred (binary) wire format and
// converts it to text with microsecond precision, so the value that reaches Go
// is "09:00:00.000000" — not the "09:00" that the API contract documents.
// Parsing that with time.Parse("15:04", …) always fails, which used to make
// every policy carrying a time window deny access unconditionally.
//
// These helpers accept every rendering the value can take and normalise it to
// the documented "HH:MM" form.

// policyTimeLayouts lists the accepted renderings of a TIME value, most
// specific first.
var policyTimeLayouts = []string{
	"15:04:05.999999",
	"15:04:05",
	"15:04",
}

// parsePolicyTime parses a TIME column value into minutes since midnight.
// It reports false when the value is empty or unparsable, so callers can fail
// closed rather than silently ignore a misconfigured window.
func parsePolicyTime(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	for _, layout := range policyTimeLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Hour()*60 + t.Minute(), true
		}
	}
	return 0, false
}

// formatPolicyTime renders minutes since midnight back as the "HH:MM" form
// used by the API.
func formatPolicyTime(minutes int) string {
	minutes = ((minutes % 1440) + 1440) % 1440
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// normalisePolicyTime converts a scanned TIME value to the documented "HH:MM"
// form. It returns nil for a nil pointer, and leaves an unparsable value
// untouched so the caller can surface it rather than hiding it.
func normalisePolicyTime(v *string) *string {
	if v == nil {
		return nil
	}
	minutes, ok := parsePolicyTime(*v)
	if !ok {
		return v
	}
	out := formatPolicyTime(minutes)
	return &out
}

// validatePolicyTimeWindow checks a start/end time-window pair. Both bounds
// must be present (a half-specified window used to be silently ignored, which
// failed open) and each must be a parsable "HH:MM" value. The returned strings
// are normalised to "HH:MM" for storage. The returned field names the offending
// input so the API can report which one was wrong.
func validatePolicyTimeWindow(start, end *string) (string, string, string, string) {
	startVal, endVal := "", ""
	if start != nil {
		startVal = strings.TrimSpace(*start)
	}
	if end != nil {
		endVal = strings.TrimSpace(*end)
	}

	if startVal == "" && endVal == "" {
		return "", "", "", ""
	}
	if startVal == "" {
		return "", "", "allowedTimeStart", "allowedTimeStart and allowedTimeEnd must both be set to define a time window"
	}
	if endVal == "" {
		return "", "", "allowedTimeEnd", "allowedTimeStart and allowedTimeEnd must both be set to define a time window"
	}

	startMins, ok := parsePolicyTime(startVal)
	if !ok {
		return "", "", "allowedTimeStart", "allowedTimeStart must be a time of day in HH:MM (24h) format"
	}
	endMins, ok := parsePolicyTime(endVal)
	if !ok {
		return "", "", "allowedTimeEnd", "allowedTimeEnd must be a time of day in HH:MM (24h) format"
	}

	return formatPolicyTime(startMins), formatPolicyTime(endMins), "", ""
}
