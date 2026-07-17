package handlers
import ("bytes"; "context"; "encoding/json"; "net/http"; "net/http/httptest"; "testing"; "github.com/go-chi/chi/v5"; "go.uber.org/zap")
func TestO04_DryRunProvisioning_RejectsNonUUIDUser(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body,_ := json.Marshal(map[string]string{"userId":"not-uuid"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/a/provisioning/dry-run", bytes.NewReader(body))
	rctx := chi.NewRouteContext(); rctx.URLParams.Add("appId", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req); req.Header.Set("Content-Type","application/json")
	rec := httptest.NewRecorder(); h.DryRunProvisioning(rec, req)
	if rec.Code != http.StatusBadRequest { t.Fatalf("got %d %s", rec.Code, rec.Body.String()) }
}
