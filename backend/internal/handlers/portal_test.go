package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func TestPortalMyDevicesNoAuth(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/devices", nil)
	rec := httptest.NewRecorder()
	h.PortalMyDevices(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalMyDevicesNilDB(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/devices", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:  "user-123",
		Role: middleware.RoleEndUser,
	}))
	rec := httptest.NewRecorder()
	h.PortalMyDevices(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalMyAppsNoAuth(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/apps", nil)
	rec := httptest.NewRecorder()
	h.PortalMyApps(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalMyAppsNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/apps", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:  "user-123",
		Role: middleware.RoleEndUser,
	}))
	rec := httptest.NewRecorder()
	h.PortalMyApps(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalRequestAccessNoAuth(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/access-requests", nil)
	rec := httptest.NewRecorder()
	h.PortalRequestAccess(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPortalRequestAccessInvalidAppID(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]string{"appId": "not-a-uuid", "reason": "need it"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/access-requests", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:  "user-123",
		Role: middleware.RoleEndUser,
	}))
	rec := httptest.NewRecorder()
	h.PortalRequestAccess(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid appId: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminListAccessRequestsNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/access-requests", nil)
	rec := httptest.NewRecorder()
	h.AdminListAccessRequests(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminDecideAccessRequestInvalidDecision(t *testing.T) {
	h := setupTestHandler(t)
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	b, _ := json.Marshal(map[string]string{"decision": "maybe"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/portal/access-requests/"+validID, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.AdminDecideAccessRequest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid decision: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminDecideAccessRequestInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]string{"decision": "approved"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/portal/access-requests/bad", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"bad"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.AdminDecideAccessRequest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
