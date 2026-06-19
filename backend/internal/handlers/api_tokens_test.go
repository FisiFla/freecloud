package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestCreateAPITokenEmptyBody(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	// name="" → validation error → 400
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAPITokenInvalidRole(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]interface{}{"name": "tok", "role": "root", "serviceIdentity": "ci"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid role: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAPITokenMissingServiceIdentity(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]interface{}{"name": "tok", "role": "auditor"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing serviceIdentity: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAPITokenNilDB(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	b, _ := json.Marshal(map[string]interface{}{"name": "tok", "role": "auditor", "serviceIdentity": "ci"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAPITokensNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil)
	rec := httptest.NewRecorder()
	h.ListAPITokens(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeAPITokenInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/api-tokens/not-a-uuid", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"not-a-uuid"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.RevokeAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeAPITokenNilDB(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/api-tokens/"+validID, nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.RevokeAPIToken(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}
