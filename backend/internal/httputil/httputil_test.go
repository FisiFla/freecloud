package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRespondJSON_EnvelopeAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondJSON(rec, http.StatusOK, map[string]string{"id": "abc"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var out APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Success || out.Error != "" {
		t.Fatalf("unexpected envelope: %+v", out)
	}
}

func TestRespondJSON_SuccessFlagFollowsStatus(t *testing.T) {
	// 4xx must set Success=false even though RespondJSON is used.
	rec := httptest.NewRecorder()
	RespondJSON(rec, http.StatusNotFound, map[string]string{"id": "x"})
	var out APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Success {
		t.Fatal("Success must be false for a 4xx status")
	}
}

func TestRespondError(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondError(rec, http.StatusBadRequest, "bad input")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var out APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Success || out.Error != "bad input" {
		t.Fatalf("unexpected envelope: %+v", out)
	}
}

func TestRespondValidationErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondValidationErrors(rec, []ValidationError{
		{Field: "email", Message: "invalid email"},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var out ValidationErrorsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Success || len(out.Errors) != 1 || out.Errors[0].Field != "email" {
		t.Fatalf("unexpected response: %+v", out)
	}
}
