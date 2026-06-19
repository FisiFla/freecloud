package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestGetDriftNoReconciler(t *testing.T) {
	// Handler without a reconciler attached → 503.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	w := httptest.NewRecorder()
	h.GetDrift(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/drift", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when reconciler not configured, got %d", w.Code)
	}
}
