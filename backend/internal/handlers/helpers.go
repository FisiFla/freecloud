package handlers

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
