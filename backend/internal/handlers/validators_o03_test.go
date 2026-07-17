package handlers
import ("context"; "net/http"; "net/http/httptest"; "testing"; "github.com/go-chi/chi/v5"; "go.uber.org/zap")
func TestO03_ResyncUser_RejectsNonUUIDUser(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/a/users/u/resync", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("appId", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rctx.URLParams.Add("userId", "not-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder(); h.ResyncUser(rec, req)
	if rec.Code != http.StatusBadRequest { t.Fatalf("got %d %s", rec.Code, rec.Body.String()) }
}
