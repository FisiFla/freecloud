package handlers
import ("context"; "net/http"; "net/http/httptest"; "testing"; "github.com/go-chi/chi/v5"; "go.uber.org/zap")
func TestO01_AddOrgMember_RejectsNonUUIDOrg(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/x/members", nil)
	rctx := chi.NewRouteContext(); rctx.URLParams.Add("orgId", "not-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder(); h.AddOrgMember(rec, req)
	if rec.Code != http.StatusBadRequest { t.Fatalf("got %d %s", rec.Code, rec.Body.String()) }
}
