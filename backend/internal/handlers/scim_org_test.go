package handlers

// Tests for C4 (Epic C multi-tenant): org-scoped SCIM bearer authentication.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

const (
	testOrgAID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	testOrgBID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// scimTokenDB is a fakeDB preloaded with one token->org mapping, mirroring
// the scim_bearer_tokens table shape for SCIMOrgBearerMiddleware tests.
func scimTokenDB(tokenPlain, orgID string) *fakeDB {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(tokenPlain)))
	return &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if len(args) > 0 && args[0] == hash {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = orgID
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
}

func withOrgIDParam(req *http.Request, orgID string) *http.Request {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("orgID", orgID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
}

func TestSCIMOrgBearerMiddleware_ValidTokenMatchingOrg(t *testing.T) {
	h := setupTestHandler(t)
	db := scimTokenDB("org-a-token", testOrgAID)
	mw := h.SCIMOrgBearerMiddleware(db)

	var gotOrgID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if oc := middleware.GetOrgContext(r.Context()); oc != nil {
			gotOrgID = oc.OrgID
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/scim/v2/orgs/"+testOrgAID+"/Users", nil)
	req = withOrgIDParam(req, testOrgAID)
	req.Header.Set("Authorization", "Bearer org-a-token")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("valid token + matching org: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotOrgID != testOrgAID {
		t.Errorf("expected OrgContext.OrgID=%s, got %q", testOrgAID, gotOrgID)
	}
}

func TestSCIMOrgBearerMiddleware_ValidTokenWrongOrgPath(t *testing.T) {
	h := setupTestHandler(t)
	// The token is valid — but only for org A. Requesting it against org B's
	// path must NOT authenticate: this is exactly the cross-org SCIM leak
	// this middleware exists to prevent.
	db := scimTokenDB("org-a-token", testOrgAID)
	mw := h.SCIMOrgBearerMiddleware(db)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/scim/v2/orgs/"+testOrgBID+"/Users", nil)
	req = withOrgIDParam(req, testOrgBID)
	req.Header.Set("Authorization", "Bearer org-a-token")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("org A's token against org B's path: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMOrgBearerMiddleware_UnknownToken(t *testing.T) {
	h := setupTestHandler(t)
	db := scimTokenDB("org-a-token", testOrgAID)
	mw := h.SCIMOrgBearerMiddleware(db)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/scim/v2/orgs/"+testOrgAID+"/Users", nil)
	req = withOrgIDParam(req, testOrgAID)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMOrgBearerMiddleware_MalformedOrgPath(t *testing.T) {
	h := setupTestHandler(t)
	db := scimTokenDB("org-a-token", testOrgAID)
	mw := h.SCIMOrgBearerMiddleware(db)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/scim/v2/orgs/not-a-uuid/Users", nil)
	req = withOrgIDParam(req, "not-a-uuid")
	req.Header.Set("Authorization", "Bearer org-a-token")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("malformed org path: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMOrgBearerMiddleware_MissingBearer(t *testing.T) {
	h := setupTestHandler(t)
	db := scimTokenDB("org-a-token", testOrgAID)
	mw := h.SCIMOrgBearerMiddleware(db)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/scim/v2/orgs/"+testOrgAID+"/Users", nil)
	req = withOrgIDParam(req, testOrgAID)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer: expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}
