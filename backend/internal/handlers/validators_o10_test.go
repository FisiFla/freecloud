package handlers
import ("context"; "net/http"; "net/http/httptest"; "testing"; "github.com/go-chi/chi/v5"; "go.uber.org/zap")
func TestO10_SCIMGetGroup_RejectsPathID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups/x", nil)
	rctx := chi.NewRouteContext(); rctx.URLParams.Add("id", "a/../b")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder(); h.SCIMGetGroup(rec, req)
	if rec.Code != http.StatusBadRequest { t.Fatalf("got %d %s", rec.Code, rec.Body.String()) }
}
