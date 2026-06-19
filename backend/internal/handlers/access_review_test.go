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

func TestCreateCampaignNilDB(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]string{"name": "Q3 Review"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateCampaign(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateCampaignMissingName(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateCampaign(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing name: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListCampaignsNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	rec := httptest.NewRecorder()
	h.ListCampaigns(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListCampaignItemsInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns/bad/items", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"bad"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.ListCampaignItems(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecideCampaignItemInvalidDecision(t *testing.T) {
	h := setupTestHandler(t)
	const cID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const iID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	b, _ := json.Marshal(map[string]string{"decision": "maybe"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns/"+cID+"/items/"+iID+"/decide", bytes.NewReader(b))
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{
			Keys:   []string{"id", "itemId"},
			Values: []string{cID, iID},
		},
	})
	req = req.WithContext(chiCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.DecideCampaignItem(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid decision: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecideCampaignItemNilDB(t *testing.T) {
	h := setupTestHandler(t)
	const cID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const iID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	b, _ := json.Marshal(map[string]string{"decision": "confirm"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns/"+cID+"/items/"+iID+"/decide", bytes.NewReader(b))
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{
			Keys:   []string{"id", "itemId"},
			Values: []string{cID, iID},
		},
	})
	req = req.WithContext(chiCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.DecideCampaignItem(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCompleteCampaignInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns/bad/complete", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"bad"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.CompleteCampaign(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCompleteCampaignNilDB(t *testing.T) {
	h := setupTestHandler(t)
	const cID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns/"+cID+"/complete", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{cID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.CompleteCampaign(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}
