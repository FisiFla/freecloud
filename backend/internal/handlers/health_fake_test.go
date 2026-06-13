package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestHealthWithFakes(t *testing.T) {
	logger := zap.NewNop()
	kc := &fakeKeycloak{}
	fleet := &fakeFleet{}
	h := NewHandler(nil, kc, fleet, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	h.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}
