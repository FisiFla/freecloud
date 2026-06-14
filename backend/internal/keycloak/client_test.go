package keycloak

import (
	"errors"
	"testing"
)

// TestIsConflictErr confirms the helper correctly distinguishes a 409 Conflict
// (which AssignUserToClient treats as "role already exists") from real errors.
func TestIsConflictErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"409 conflict in message", errors.New("CreateClientRole: 409 Conflict, role already exists"), true},
		{"conflict lowercase", errors.New("create role: conflict detected"), true},
		{"404 not found", errors.New("GetClientRole: 404 Not Found"), false},
		{"500 server error", errors.New("unexpected 500"), false},
		{"network error", errors.New("dial tcp: connection refused"), false},
		{"empty message", errors.New(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isConflictErr(tt.err); got != tt.want {
				t.Errorf("isConflictErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
