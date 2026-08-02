// Package httputil provides HTTP response helpers shared by API handlers.
// It has zero domain dependencies so any package can reuse it (see
// docs/HANDLERS_OWNERSHIP.md).
package httputil

import (
	"encoding/json"
	"net/http"
)

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

// RespondJSON sends a success JSON response with the given status code and data.
func RespondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIResponse{Success: status < 400, Data: data})
}

// RespondError sends an error JSON response with the given status code and message.
func RespondError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIResponse{Success: false, Error: message})
}

// RespondValidationErrors sends a 400 response with field-level validation errors.
func RespondValidationErrors(w http.ResponseWriter, errors []ValidationError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(ValidationErrorsResponse{
		Success: false,
		Errors:  errors,
	})
}
