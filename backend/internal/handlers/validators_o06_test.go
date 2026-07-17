package handlers
import ("context"; "net/http"; "net/http/httptest"; "testing"; "github.com/go-chi/chi/v5")
func TestO06_SCIMOrgBearer_RejectsNonUUIDOrg(t *testing.T) {
	h := setupTestHandler(t)
	mw := h.SCIMOrgBearerMiddleware(nil)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/orgs/x/Users", nil)
	rctx := chi.NewRouteContext(); rctx.URLParams.Add("orgID", "not-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound { t.Fatalf("got %d %s", rec.Code, rec.Body.String()) }
}
