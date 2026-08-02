package httpx

import (
	"strings"
	"testing"
)

func TestReadAllBounded_AcceptsSmallBody(t *testing.T) {
	body, err := ReadAllBounded(strings.NewReader(`{"ok":true}`), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestReadAllBounded_RejectsOversized(t *testing.T) {
	big := strings.Repeat("x", MaxResponseBytes+1)
	_, err := ReadAllBounded(strings.NewReader(big), 0)
	if err == nil {
		t.Fatal("expected error for oversized body")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected 'too large' error, got: %v", err)
	}
}

func TestReadAllBounded_RespectsCustomLimit(t *testing.T) {
	_, err := ReadAllBounded(strings.NewReader("1234567890"), 5)
	if err == nil {
		t.Fatal("expected error when body exceeds custom limit")
	}
	body, err := ReadAllBounded(strings.NewReader("1234567890"), 100)
	if err != nil || string(body) != "1234567890" {
		t.Fatalf("custom large limit: body=%q err=%v", body, err)
	}
}
