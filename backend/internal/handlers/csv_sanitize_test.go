package handlers

// L3 lane: unit tests for the shared CSV formula-injection sanitizer.

import (
	"reflect"
	"testing"
)

func TestSanitizeCSVCell(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "laptop-123", "laptop-123"},
		{"number", "42", "42"},
		{"equals", "=1+1", "'=1+1"},
		{"plus", "+2+2", "'+2+2"},
		{"minus", "-2+2", "'-2+2"},
		{"at", "@SUM(A1:A2)", "'@SUM(A1:A2)"},
		{"tab", "\tfoo", "'\tfoo"},
		{"carriage return", "\rfoo", "'\rfoo"},
		{"leading spaces then equals", "   =cmd", "'   =cmd"},
		{"leading space then plus", " +1", "' +1"},
		{"leading space then at", "  @x", "'  @x"},
		{"leading tab then equals", "\t=1", "'\t=1"},
		{"only spaces", "   ", "   "},
		{"single quote already", "'=1", "'=1"},
		{"middle equals", "a=b", "a=b"},
		{"trailing space", "=1 ", "'=1 "},
		{"unicode", "München 🖥️", "München 🖥️"},
		{"newline prefix then formula", "\n=1", "'\n=1"},
	}
	for _, tt := range tests {
		if got := sanitizeCSVCell(tt.in); got != tt.want {
			t.Errorf("%s: sanitizeCSVCell(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestSanitizeCSVRecord(t *testing.T) {
	in := []string{"ok", "=1+1", "  @x", ""}
	want := []string{"ok", "'=1+1", "'  @x", ""}
	if got := sanitizeCSVRecord(in); !reflect.DeepEqual(got, want) {
		t.Errorf("sanitizeCSVRecord(%q) = %q, want %q", in, got, want)
	}
	// Input slice must not be mutated.
	if !reflect.DeepEqual(in, []string{"ok", "=1+1", "  @x", ""}) {
		t.Errorf("sanitizeCSVRecord mutated its input: %q", in)
	}
}
