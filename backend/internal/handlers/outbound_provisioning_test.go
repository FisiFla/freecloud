// Failing-first tests for the provisioning handler hardening (security
// lane L2, issues 8, 10, 12): no unsalted secret hashes, per-connector-type
// endpoint validation, and bounded async provisioning concurrency.
package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/provisioning"
)

// outboundStubConnector is a no-op provisioning.Connector for handler tests.
type outboundStubConnector struct{}

func (outboundStubConnector) ProvisionUser(_ context.Context, _ provisioning.ProvisionableUser) (string, error) {
	return "remote-1", nil
}
func (outboundStubConnector) DeprovisionUser(_ context.Context, _ string) error { return nil }
func (outboundStubConnector) UpdateUser(_ context.Context, _ string, _ provisioning.ProvisionableUser) error {
	return nil
}
func (outboundStubConnector) SyncGroupMembership(_ context.Context, _ string, _ []string) error {
	return nil
}
func (outboundStubConnector) Name() string { return "stub" }

func outboundEngineWithConnector(appID string) *provisioning.Engine {
	e := provisioning.NewEngine(nil, zap.NewNop())
	e.RegisterConnector(appID, outboundStubConnector{})
	return e
}

const outboundTestAppID = "00000000-0000-0000-0000-000000000001"
const outboundTestUserID = "00000000-0000-0000-0000-000000000002"

// TestUpsertProvisioningConfig_WritesNoTokenHash pins issue 8: saving a
// bearer token must not persist an unsalted SHA-256 of the secret next to
// the ciphertext.
func TestUpsertProvisioningConfig_WritesNoTokenHash(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	const token = "test-token-123"
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))

	var execArgs [][]any
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
			execArgs = append(execArgs, args)
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
			// Saved-encrypted-token lookup: none stored yet.
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	body := `{"enabled":true,"connectorType":"scim","endpointUrl":"https://8.8.8.8/v2","bearerToken":"` + token + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/"+outboundTestAppID+"/provisioning", bytes.NewReader([]byte(body)))
	req = setChiURLParam(req, "appId", outboundTestAppID)
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertProvisioningConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, args := range execArgs {
		for _, arg := range args {
			if s, ok := arg.(*string); ok && s != nil && *s == wantHash {
				t.Fatalf("a write persisted the unsalted token hash %q", wantHash)
			}
			if s, ok := arg.(string); ok && s == wantHash {
				t.Fatalf("a write persisted the unsalted token hash %q", wantHash)
			}
		}
	}
	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, _ := json.Marshal(resp.Data)
	var cfg ProvisioningConfigResponse
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfg.BearerTokenConfigured {
		t.Error("BearerTokenConfigured must be true after saving a token")
	}
}

// TestGetProvisioningConfig_ConfiguredFromEncryptedValue pins issue 8's read
// side: "configured" is derived from the encrypted value's presence.
func TestGetProvisioningConfig_ConfiguredFromEncryptedValue(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	enc := "dm resort" // opaque ciphertext stand-in
	db := &fakeDB{
		queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*bool)) = true
				*(dest[1].(*string)) = "scim"
				*(dest[2].(*string)) = "https://8.8.8.8/v2"
				*(dest[3].(**string)) = &enc
				*(dest[4].(*[]byte)) = []byte(`{}`)
				return nil
			}}
		}),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+outboundTestAppID+"/provisioning", nil)
	req = setChiURLParam(req, "appId", outboundTestAppID)
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.GetProvisioningConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, _ := json.Marshal(resp.Data)
	var cfg ProvisioningConfigResponse
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfg.BearerTokenConfigured {
		t.Error("BearerTokenConfigured must be true when an encrypted token is stored")
	}
}

// TestUpsertProvisioningConfig_GitHubOrgValidation pins issue 10: the GitHub
// "endpoint" is an org name, not a URL, with a legacy-URL working path.
func TestUpsertProvisioningConfig_GitHubOrgValidation(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	cases := []struct {
		name       string
		endpoint   string
		wantCode   int
		wantStored string
	}{
		{"org name", "my-org", http.StatusOK, "my-org"},
		{"legacy github.com URL", "https://github.com/my-org", http.StatusOK, "my-org"},
		{"legacy api URL", "https://api.github.com/orgs/my-org", http.StatusOK, "my-org"},
		{"garbage rejected", "not an org!!", http.StatusBadRequest, ""},
		{"foreign URL rejected", "https://evil.example.com/my-org", http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var storedEndpoint string
			db := &fakeDB{
				execFn: func(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
					// endpoint is the 4th bind arg ($4) in both statements.
					if len(args) >= 4 {
						if s, ok := args[3].(string); ok {
							storedEndpoint = s
						}
					}
					return pgconn.CommandTag{}, nil
				},
				queryRowFn: ownershipFoundQueryRowFn(nil),
			}
			h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

			body := `{"enabled":true,"connectorType":"github","endpointUrl":"` + tc.endpoint + `"}`
			req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/"+outboundTestAppID+"/provisioning", bytes.NewReader([]byte(body)))
			req = setChiURLParam(req, "appId", outboundTestAppID)
			req = withOrgContext(req)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.UpsertProvisioningConfig(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
			}
			if tc.wantCode == http.StatusOK && storedEndpoint != tc.wantStored {
				t.Fatalf("stored endpoint = %q, want %q", storedEndpoint, tc.wantStored)
			}
		})
	}
}

// TestAsyncProvisioningSemaphore pins issue 12's mechanics: bounded
// concurrency with fail-fast acquisition.
func TestAsyncProvisioningSemaphore(t *testing.T) {
	acquired := 0
	for tryAcquireAsyncProvisioning() {
		acquired++
		if acquired > maxAsyncProvisioning+8 {
			t.Fatal("acquire never saturated")
		}
	}
	t.Cleanup(func() {
		for i := 0; i < acquired; i++ {
			releaseAsyncProvisioning()
		}
	})
	if acquired != maxAsyncProvisioning {
		t.Fatalf("acquired %d slots, want %d", acquired, maxAsyncProvisioning)
	}
	releaseAsyncProvisioning()
	acquired--
	if !tryAcquireAsyncProvisioning() {
		t.Fatal("acquire after release must succeed")
	}
	acquired++ // the re-acquired slot is released by the cleanup above
}

// TestResyncUser_SaturatedReturns429 pins issue 12: no unbounded goroutine
// per request — saturated handlers answer 429.
func TestResyncUser_SaturatedReturns429(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	for i := 0; i < maxAsyncProvisioning; i++ {
		if !tryAcquireAsyncProvisioning() {
			t.Fatal("could not saturate the semaphore")
		}
	}
	t.Cleanup(func() {
		for i := 0; i < maxAsyncProvisioning; i++ {
			releaseAsyncProvisioning()
		}
	})

	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "user@example.com"
				*(dest[1].(*string)) = "First"
				*(dest[2].(*string)) = "Last"
				*(dest[3].(*string)) = ""
				return nil
			}}
		}),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.provisionEngine = outboundEngineWithConnector(outboundTestAppID)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withOutboundChiParams(req, map[string]string{"appId": outboundTestAppID, "userId": outboundTestUserID})
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.ResyncUser(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestReconcileAllHandler_SaturatedReturns429 pins issue 12 for reconcile-all.
func TestReconcileAllHandler_SaturatedReturns429(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	for i := 0; i < maxAsyncProvisioning; i++ {
		if !tryAcquireAsyncProvisioning() {
			t.Fatal("could not saturate the semaphore")
		}
	}
	t.Cleanup(func() {
		for i := 0; i < maxAsyncProvisioning; i++ {
			releaseAsyncProvisioning()
		}
	})

	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: ownershipFoundQueryRowFn(nil),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.provisionEngine = outboundEngineWithConnector(outboundTestAppID)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = setChiURLParam(req, "appId", outboundTestAppID)
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.ReconcileAllHandler(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

// withOutboundChiParams injects several chi URL parameters at once (calling
// setChiURLParam twice would drop the first: it replaces the route context).
func withOutboundChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	// Stable order for deterministic tests.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rctx.URLParams.Add(k, params[k])
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
