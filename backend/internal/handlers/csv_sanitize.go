package handlers

// Shared CSV formula-injection sanitizer (L3 lane). Every CSV writer in this
// package must pass attacker-influenceable cells through sanitizeCSVCell:
// encoding/csv quotes cells but does NOT stop a spreadsheet from evaluating
// a cell that starts with = + - @ (or a control character) as a formula.

import "strings"

// csvFormulaLead lists the first characters that make a spreadsheet treat a
// cell as a formula (or otherwise executable content).
const csvFormulaLead = "=+-@\t\r"

// csvLeadingWhitespace is skipped when looking for a formula lead: padding a
// payload with spaces must not bypass the check.
const csvLeadingWhitespace = " \n\v\f"

// sanitizeCSVCell prefixes a single quote to any cell whose first
// non-whitespace character is a formula lead (= + - @ tab carriage-return).
// The quote forces spreadsheet applications to treat the cell as text. Safe
// cells pass through unchanged.
func sanitizeCSVCell(cell string) string {
	trimmed := strings.TrimLeft(cell, csvLeadingWhitespace)
	if trimmed == "" {
		return cell
	}
	if strings.ContainsRune(csvFormulaLead, rune(trimmed[0])) {
		return "'" + cell
	}
	return cell
}

// sanitizeCSVRecord sanitizes every cell of a CSV record. It returns a new
// slice; the input is never mutated.
func sanitizeCSVRecord(record []string) []string {
	out := make([]string, len(record))
	for i, cell := range record {
		out[i] = sanitizeCSVCell(cell)
	}
	return out
}
