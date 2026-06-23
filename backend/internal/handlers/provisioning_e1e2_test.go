package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// setChiURLParam injects a chi URL parameter into the request context.
func setChiURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestDryRunProvisioningNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"userId":"00000000-0000-0000-0000-000000000002"}`))
	req.Header.Set("Content-Type", "application/json")
	req = setChiURLParam(req, "appId", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.DryRunProvisioning(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunProvisioningInvalidAppID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"userId":"00000000-0000-0000-0000-000000000002"}`))
	req.Header.Set("Content-Type", "application/json")
	req = setChiURLParam(req, "appId", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.DryRunProvisioning(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDryRunProvisioningInvalidBodyUserID(t *testing.T) {
	h := setupTestHandler(t)
	// DB is nil so the nil-DB check fires before userId validation — expect 500.
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"userId":"not-a-uuid"}`))
	req.Header.Set("Content-Type", "application/json")
	req = setChiURLParam(req, "appId", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.DryRunProvisioning(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (nil DB fires before userId check), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReconcileAllHandlerNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = setChiURLParam(req, "appId", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ReconcileAllHandler(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReconcileAllHandlerInvalidAppID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = setChiURLParam(req, "appId", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ReconcileAllHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
