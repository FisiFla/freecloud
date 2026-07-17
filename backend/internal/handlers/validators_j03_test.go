package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func TestJ03_ValidateUserID(t *testing.T) {
	if err := ValidateUserID("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not-uuid", "a/b", "x\x00y", string(make([]byte, 65))} {
		if _, ok := errOrTrue(ValidateUserID(bad)); !ok {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func errOrTrue(err error) (error, bool) {
	return err, err != nil
}

func TestJ03_GetDeviceSoftware_RejectsPathUserID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/x/devices/software", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "../admin")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.GetDeviceSoftware(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
