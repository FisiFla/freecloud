package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// uuidPattern matches standard hyphenated UUID format.
var uuidPattern = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

// isValidUUID reports whether s is a well-formed UUID.
func isValidUUID(s string) bool {
	return uuidPattern.MatchString(s)
}

// emailPattern is a pragmatic email check: a local part, an @, a domain, and a
// dotted TLD, with no whitespace. It deliberately rejects "@", "a@", "@x" that a
// bare strings.Contains("@") would let through to Keycloak as a confusing error.
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// isValidEmail reports whether s is a plausibly-valid email address.
func isValidEmail(s string) bool {
	return len(s) <= 254 && emailPattern.MatchString(s)
}

// APIResponse is the standard JSON response envelope for all API endpoints.
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ValidationError represents a single field-level validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationErrorsResponse is the JSON body for field-level validation failures.
type ValidationErrorsResponse struct {
	Success bool              `json:"success"`
	Errors  []ValidationError `json:"errors"`
}

// respondJSON sends a success JSON response with the given status code and data.
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIResponse{Success: status < 400, Data: data})
}

// respondError sends an error JSON response with the given status code and message.
func respondError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIResponse{Success: false, Error: message})
}

// respondValidationErrors sends a 400 response with field-level validation errors.
func respondValidationErrors(w http.ResponseWriter, errors []ValidationError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(ValidationErrorsResponse{
		Success: false,
		Errors:  errors,
	})
}
