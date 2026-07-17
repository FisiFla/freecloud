package handlers
import ("context"; "net/http"; "net/http/httptest"; "testing"; "github.com/go-chi/chi/v5"; "go.uber.org/zap")
func TestO07_SCIMGetUser_RejectsNonUUID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users/x", nil)
	rctx := chi.NewRouteContext(); rctx.URLParams.Add("id", "not-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder(); h.SCIMGetUser(rec, req)
	if rec.Code != http.StatusBadRequest { t.Fatalf("got %d %s", rec.Code, rec.Body.String()) }
}
