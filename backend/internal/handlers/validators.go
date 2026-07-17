package handlers

import (
	"fmt"
	"strings"
)

// ValidatePolicyID rejects empty, overlong, or path-like Fleet policy IDs.
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
	return nil
}
