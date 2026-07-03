package handlers

// Tests for Epic A (A2–A5) handler logic using the fake Keycloak/DB.
// DB-integrated paths run in CI (enrollment_integration_test.go pattern);
// these cover the handler routing, validation, and fake-propagation logic.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Nerzal/gocloak/v13"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ---- SCIM tests (A2) ----

func TestSCIMBearerMiddlewareRejectsEmpty(t *testing.T) {
	mw := SCIMBearerMiddleware("")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("empty token: expected 503, got %d", rec.Code)
	}
}

func TestSCIMBearerMiddlewareMissingHeader(t *testing.T) {
	mw := SCIMBearerMiddleware("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing header: expected 401, got %d", rec.Code)
	}
}

func TestSCIMBearerMiddlewareWrongToken(t *testing.T) {
	mw := SCIMBearerMiddleware("correct-secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: expected 401, got %d", rec.Code)
	}
}

func TestSCIMBearerMiddlewareAccepts(t *testing.T) {
	mw := SCIMBearerMiddleware("correct-secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	req.Header.Set("Authorization", "Bearer correct-secret")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("correct token: expected 200, got %d", rec.Code)
	}
}

func TestSCIMListUsersNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	rec := httptest.NewRecorder()
	h.SCIMListUsers(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil DB: expected 503, got %d", rec.Code)
	}
}

func TestSCIMGetUserNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users/some-id", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"some-id"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.SCIMGetUser(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil DB: expected 503, got %d", rec.Code)
	}
}

func TestSCIMCreateUserInvalidEmail(t *testing.T) {
	h := setupTestHandler(t)
	body := SCIMUser{
		Schemas:  []string{scimUserSchema},
		UserName: "not-an-email",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", bytes.NewReader(b))
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMCreateUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid email: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMCreateUserNilDB(t *testing.T) {
	h := setupTestHandler(t)
	body := SCIMUser{
		Schemas:  []string{scimUserSchema},
		UserName: "user@example.com",
		Name:     scimName{GivenName: "Test", FamilyName: "User"},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", bytes.NewReader(b))
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMCreateUser(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil DB: expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMGroupsListReplacedStub(t *testing.T) {
	// Verify that the Groups endpoint now returns a real response (200) rather than 501.
	// C1: SCIMListGroups is org-scoped, so the request needs a resolved
	// OrgContext (as the SCIM bearer middleware would set it) to reach 200.
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil).WithContext(
		middleware.SetOrgContext(context.Background(), &middleware.OrgContext{
			OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
		}))
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("groups list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestParseSCIMFilter(t *testing.T) {
	tests := []struct {
		raw       string
		wantAttr  string
		wantValue string
		wantNil   bool
	}{
		{`userName eq "alice@example.com"`, "username", "alice@example.com", false},
		{`emails.value eq "bob@example.com"`, "emails.value", "bob@example.com", false},
		{``, "", "", true},
		{`bad`, "", "", true},
	}
	for _, tt := range tests {
		f := parseSCIMFilter(tt.raw)
		if tt.wantNil {
			if f != nil {
				t.Errorf("filter(%q): expected nil, got %+v", tt.raw, f)
			}
			continue
		}
		if f == nil {
			t.Errorf("filter(%q): expected non-nil", tt.raw)
			continue
		}
		if f.attr != tt.wantAttr {
			t.Errorf("filter(%q): attr=%q want %q", tt.raw, f.attr, tt.wantAttr)
		}
		if f.value != tt.wantValue {
			t.Errorf("filter(%q): value=%q want %q", tt.raw, f.value, tt.wantValue)
		}
	}
}

// ---- Groups tests (A3) ----

func TestListGroupsSuccess(t *testing.T) {
	// M1: system-admin claims so the (legacy, unfiltered) happy path this
	// test exercises isn't affected by the new org-filter for non-admins —
	// that restriction is proven separately in org_isolation_test.go.
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil).WithContext(
		middleware.SetClaims(context.Background(), &middleware.JWTClaims{Sub: "admin", Role: middleware.RoleSuperAdmin}))
	rec := httptest.NewRecorder()
	h.ListGroups(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListGroupsKeycloakError(t *testing.T) {
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			return nil, fmt.Errorf("kc down")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil)
	rec := httptest.NewRecorder()
	h.ListGroups(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("kc error: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateGroupValidation(t *testing.T) {
	h := setupTestHandler(t)

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{"empty name", map[string]interface{}{"name": ""}, http.StatusBadRequest},
		{"missing name", map[string]interface{}{}, http.StatusBadRequest},
		{"name too long", map[string]interface{}{"name": string(make([]byte, 101))}, http.StatusBadRequest},
		{"valid", map[string]interface{}{"name": "Engineering"}, http.StatusCreated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/groups", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			// M1: CreateGroup now tags the group with the caller's org, so it
			// needs a resolved OrgContext. Harmless for the invalid-body
			// cases, which 400 on validation before that check is reached.
			req = req.WithContext(middleware.SetOrgContext(req.Context(), &middleware.OrgContext{
				OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
			}))
			rec := httptest.NewRecorder()
			h.CreateGroup(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAssignUserToGroupValidation(t *testing.T) {
	h := setupTestHandler(t)
	h.db = &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	const validUserID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	tests := []struct {
		name       string
		userID     string
		body       map[string]interface{}
		wantStatus int
	}{
		{"invalid user id", "not-a-uuid", map[string]interface{}{"groupId": "gid"}, http.StatusBadRequest},
		{"missing groupId", validUserID, map[string]interface{}{}, http.StatusBadRequest},
		{"valid", validUserID, map[string]interface{}{"groupId": "group-123"}, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+tt.userID+"/groups", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
				URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{tt.userID}},
			})
			req = req.WithContext(chiCtx)
			req = withOrgContext(req)
			rec := httptest.NewRecorder()
			h.AssignUserToGroup(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAssignRealmRoleValidation(t *testing.T) {
	h := setupTestHandler(t)
	h.db = &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	const validUserID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{"missing roleId and roleName", map[string]interface{}{}, http.StatusBadRequest},
		{"missing roleName", map[string]interface{}{"roleId": "rid"}, http.StatusBadRequest},
		{"valid", map[string]interface{}{"roleId": "rid", "roleName": "admin"}, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+validUserID+"/roles", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
				URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validUserID}},
			})
			req = req.WithContext(chiCtx)
			req = withOrgContext(req)
			rec := httptest.NewRecorder()
			h.AssignRealmRoleToUser(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("expected %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

// ---- PatchUser tests (A4) ----

func TestPatchUserInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/bad-id", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"bad-id"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.PatchUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestPatchUserNilDB(t *testing.T) {
	h := setupTestHandler(t)
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	firstName := "New"
	body, _ := json.Marshal(PatchUserRequest{FirstName: &firstName})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+validID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.PatchUser(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d", rec.Code)
	}
}

func TestPatchUserNoFields(t *testing.T) {
	h := setupTestHandler(t)
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+validID, bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.PatchUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no fields: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchUserValidationErrors(t *testing.T) {
	h := setupTestHandler(t)
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	emptyFirst := ""
	body, _ := json.Marshal(PatchUserRequest{FirstName: &emptyFirst})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+validID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.PatchUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty firstName: expected 400, got %d", rec.Code)
	}
}

// ---- ResetPassword tests (A5) ----

func TestResetPasswordInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/bad-id/reset-password", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"bad-id"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.ResetPassword(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestResetPasswordSentOK(t *testing.T) {
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	resetCalled := false
	kc := &fakeKeycloak{
		sendPasswordResetFn: func(ctx context.Context, userID string) error {
			resetCalled = true
			return nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+validID+"/reset-password", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.ResetPassword(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !resetCalled {
		t.Error("expected SendPasswordReset to be called")
	}
}

func TestResetPasswordKeycloakError(t *testing.T) {
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	kc := &fakeKeycloak{
		sendPasswordResetFn: func(ctx context.Context, userID string) error {
			return fmt.Errorf("keycloak unavailable")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+validID+"/reset-password", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.ResetPassword(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("kc error: expected 500, got %d", rec.Code)
	}
}
