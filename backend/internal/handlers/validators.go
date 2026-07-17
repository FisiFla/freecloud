package handlers

import (
	"fmt"
	"strings"
)

// ValidatePolicyID rejects empty, overlong, path-like, or control-bearing Fleet policy IDs.
func ValidatePolicyID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("policyId is required")
	}
	if len(id) > 128 {
		return fmt.Errorf("policyId must be ≤ 128 characters")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("policyId must not contain path separators or '..'")
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("policyId must not contain control characters")
		}
	}
	return nil
}

// ValidateHostID rejects empty, overlong, path-like, hidden, or control-bearing host IDs.
func ValidateHostID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("host id is required")
	}
	if len(id) > 256 {
		return fmt.Errorf("host id too long")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("host id must not contain path separators or '..'")
	}
	if strings.HasPrefix(id, ".") {
		return fmt.Errorf("host id must not start with '.'")
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("host id must not contain control characters")
		}
	}
	return nil
}

// ValidateTeamDescription enforces length and printable text.
func ValidateTeamDescription(desc string) error {
	if len(desc) > 500 {
		return fmt.Errorf("description must be ≤ 500 characters")
	}
	for _, r := range desc {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("description must not contain control characters")
		}
	}
	return nil
}

// ValidateOpaqueID rejects empty, overlong, path-like, or control-bearing opaque IDs
// (Keycloak group/role ids and similar path/body identifiers).
func ValidateOpaqueID(id, field string) error {
	id = strings.TrimSpace(id)
	if field == "" {
		field = "id"
	}
	if id == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(id) > 128 {
		return fmt.Errorf("%s too long", field)
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("%s must not contain path separators or '..'", field)
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

// ValidateUserID rejects empty, overlong, path-like, or non-UUID user path ids.
func ValidateUserID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("user id is required")
	}
	if len(id) > 64 {
		return fmt.Errorf("user id too long")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("user id must not contain path separators or '..'")
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("user id must not contain control characters")
		}
	}
	if !isValidUUID(id) {
		return fmt.Errorf("user id must be a valid UUID")
	}
	return nil
}

// ParsePositiveTeamID parses a Fleet team path segment as a positive int.
// Rejects empty, non-digit, zero, and overlong digit strings (overflow-safe).
func ParsePositiveTeamID(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("team id is required")
	}
	// Cap length before multiply to avoid int wrap on long digit runs.
	if len(s) > 9 {
		return 0, fmt.Errorf("team id must be a positive integer")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("team id must be a positive integer")
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("team id must be a positive integer")
	}
	return n, nil
}
