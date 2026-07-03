package handlers

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

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ---- helpers ----

func withGroupID(r *http.Request, id string) *http.Request {
	chiCtx := context.WithValue(r.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{id}},
	})
	return r.WithContext(chiCtx)
}

// withScimOrg attaches a resolved OrgContext (as SCIMBearerMiddleware or
// SCIMOrgBearerMiddleware would have set it) to a request, without going
// through the middleware itself — these tests call the handlers directly.
func withScimOrg(r *http.Request, orgID string) *http.Request {
	return r.WithContext(middleware.SetOrgContext(r.Context(), &middleware.OrgContext{
		OrgID: orgID, Role: middleware.OrgMembershipRoleAdmin,
	}))
}

// groupWithOrg builds a *gocloak.Group tagged with the org_id attribute
// (C1), the shape every SCIM Groups handler now requires to resolve
// ownership.
func groupWithOrg(id, name, orgID string) *gocloak.Group {
	attrs := map[string][]string{keycloak.GroupOrgAttribute: {orgID}}
	return &gocloak.Group{ID: &id, Name: &name, Attributes: &attrs}
}

const scimTestOrgA = "10000000-0000-0000-0000-0000000000a1"
const scimTestOrgB = "20000000-0000-0000-0000-0000000000b2"

// ---- SCIMListGroups ----

func TestSCIMListGroups_RequiresOrgContext(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil)
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no org context: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMListGroups_Empty(t *testing.T) {
	h := setupTestHandler(t)
	req := withScimOrg(httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil), middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp scimGroupListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalResults != 0 {
		t.Errorf("expected 0 results, got %d", resp.TotalResults)
	}
}

func TestSCIMListGroups_WithGroups(t *testing.T) {
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			return []*gocloak.Group{
				groupWithOrg("gid-1", "Engineering", middleware.DefaultOrgID),
				groupWithOrg("gid-2", "Marketing", middleware.DefaultOrgID),
			}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := withScimOrg(httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil), middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp scimGroupListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalResults != 2 {
		t.Errorf("expected 2 groups, got %d", resp.TotalResults)
	}
}

// TestSCIMListGroups_OnlyReturnsCallerOrgGroups is the C1 load-bearing
// proof: an org-B SCIM caller must never see org A's groups, even though
// both live in the same shared Keycloak realm.
func TestSCIMListGroups_OnlyReturnsCallerOrgGroups(t *testing.T) {
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			return []*gocloak.Group{
				groupWithOrg("gid-a", "Org A Team", scimTestOrgA),
				groupWithOrg("gid-b", "Org B Team", scimTestOrgB),
			}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())

	reqA := withScimOrg(httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil), scimTestOrgA)
	recA := httptest.NewRecorder()
	h.SCIMListGroups(recA, reqA)
	var respA scimGroupListResponse
	if err := json.NewDecoder(recA.Body).Decode(&respA); err != nil {
		t.Fatalf("decode org A: %v", err)
	}
	if respA.TotalResults != 1 || len(respA.Resources) != 1 || respA.Resources[0].DisplayName != "Org A Team" {
		t.Fatalf("org A: expected only its own group, got: %s", recA.Body.String())
	}

	reqB := withScimOrg(httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil), scimTestOrgB)
	recB := httptest.NewRecorder()
	h.SCIMListGroups(recB, reqB)
	var respB scimGroupListResponse
	if err := json.NewDecoder(recB.Body).Decode(&respB); err != nil {
		t.Fatalf("decode org B: %v", err)
	}
	if respB.TotalResults != 1 || len(respB.Resources) != 1 || respB.Resources[0].DisplayName != "Org B Team" {
		t.Fatalf("org B: expected only its own group, got: %s", recB.Body.String())
	}
}

// TestSCIMListGroups_CountClamp proves M7: an out-of-range `count` is
// clamped rather than honored verbatim (and a huge legitimate value is
// capped at 1000), so a caller can no longer request an unbounded page.
func TestSCIMListGroups_CountClamp(t *testing.T) {
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			groups := make([]*gocloak.Group, 0, 3)
			for i := 0; i < 3; i++ {
				groups = append(groups, groupWithOrg(fmt.Sprintf("gid-%d", i), fmt.Sprintf("Group %d", i), middleware.DefaultOrgID))
			}
			return groups, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())

	req := withScimOrg(httptest.NewRequest(http.MethodGet, "/scim/v2/Groups?count=2", nil), middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)
	var resp scimGroupListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ItemsPerPage != 2 {
		t.Errorf("count=2: expected itemsPerPage=2, got %d", resp.ItemsPerPage)
	}
	if resp.TotalResults != 3 {
		t.Errorf("count=2: expected totalResults=3 (unclamped total), got %d", resp.TotalResults)
	}

	// An out-of-range count (over 1000) falls back to the default (100),
	// never an unbounded page — mirrors SCIMListUsers's existing clamp.
	reqHuge := withScimOrg(httptest.NewRequest(http.MethodGet, "/scim/v2/Groups?count=999999", nil), middleware.DefaultOrgID)
	recHuge := httptest.NewRecorder()
	h.SCIMListGroups(recHuge, reqHuge)
	var respHuge scimGroupListResponse
	if err := json.NewDecoder(recHuge.Body).Decode(&respHuge); err != nil {
		t.Fatalf("decode huge: %v", err)
	}
	if respHuge.ItemsPerPage != 3 {
		t.Errorf("out-of-range count: expected all 3 available groups (bounded by default, not the requested count), got itemsPerPage=%d", respHuge.ItemsPerPage)
	}
}

func TestSCIMListGroups_FilterDisplayName(t *testing.T) {
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			return []*gocloak.Group{
				groupWithOrg("gid-1", "Engineering", middleware.DefaultOrgID),
				groupWithOrg("gid-2", "Marketing", middleware.DefaultOrgID),
			}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := withScimOrg(httptest.NewRequest(http.MethodGet, `/scim/v2/Groups?filter=displayName+eq+"Engineering"`, nil), middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp scimGroupListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalResults != 1 {
		t.Errorf("expected 1 filtered group, got %d", resp.TotalResults)
	}
	if len(resp.Resources) > 0 && resp.Resources[0].DisplayName != "Engineering" {
		t.Errorf("unexpected group: %s", resp.Resources[0].DisplayName)
	}
}

func TestSCIMListGroups_KCError(t *testing.T) {
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			return nil, fmt.Errorf("keycloak down")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := withScimOrg(httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil), middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// ---- SCIMGetGroup ----

func TestSCIMGetGroup_RequiresOrgContext(t *testing.T) {
	h := setupTestHandler(t)
	req := withGroupID(httptest.NewRequest(http.MethodGet, "/scim/v2/Groups/group-abc", nil), "group-abc")
	rec := httptest.NewRecorder()
	h.SCIMGetGroup(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no org context: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMGetGroup_Success(t *testing.T) {
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("group-abc", "Sales", middleware.DefaultOrgID), nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups/group-abc", nil)
	req = withGroupID(req, "group-abc")
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMGetGroup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var grp SCIMGroup
	if err := json.NewDecoder(rec.Body).Decode(&grp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if grp.DisplayName != "Sales" {
		t.Errorf("expected displayName=Sales, got %s", grp.DisplayName)
	}
}

// TestSCIMGetGroup_CrossOrgReturns404 is the C1 load-bearing proof for the
// single-resource path: a group that exists but belongs to a DIFFERENT org
// than the caller's must 404, not leak its data.
func TestSCIMGetGroup_CrossOrgReturns404(t *testing.T) {
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("group-abc", "Org A Secrets", scimTestOrgA), nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups/group-abc", nil)
	req = withGroupID(req, "group-abc")
	req = withScimOrg(req, scimTestOrgB)
	rec := httptest.NewRecorder()
	h.SCIMGetGroup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org get: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if containsAll(rec.Body.String(), "Org A Secrets") {
		t.Fatalf("cross-org get LEAKED the other org's group data: %s", rec.Body.String())
	}
}

func TestSCIMGetGroup_NotFound(t *testing.T) {
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return nil, fmt.Errorf("404 not found")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups/missing", nil)
	req = withGroupID(req, "missing")
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMGetGroup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestSCIMGetGroup_MissingID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups/", nil)
	req = withGroupID(req, "")
	rec := httptest.NewRecorder()
	h.SCIMGetGroup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---- SCIMCreateGroup ----

func TestSCIMCreateGroup_RequiresOrgContext(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(SCIMGroup{Schemas: []string{scimGroupSchema}, DisplayName: "Finance"})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no org context: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMCreateGroup_Success(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(SCIMGroup{
		Schemas:     []string{scimGroupSchema},
		DisplayName: "Finance",
	})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var grp SCIMGroup
	if err := json.NewDecoder(rec.Body).Decode(&grp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if grp.DisplayName != "Finance" {
		t.Errorf("expected Finance, got %s", grp.DisplayName)
	}
}

// TestSCIMCreateGroup_TagsOrgID proves C1: the new group is tagged with the
// CALLER's org, via CreateGroupWithOrgID, not created org-less.
func TestSCIMCreateGroup_TagsOrgID(t *testing.T) {
	var gotOrgID string
	kc := &fakeKeycloak{
		createGroupWithOrgIDFn: func(ctx context.Context, name, orgID string) (string, error) {
			gotOrgID = orgID
			return "new-group-id", nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(SCIMGroup{Schemas: []string{scimGroupSchema}, DisplayName: "Engineering"})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, scimTestOrgA)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotOrgID != scimTestOrgA {
		t.Errorf("expected group tagged with org %s, got %q", scimTestOrgA, gotOrgID)
	}
}

func TestSCIMCreateGroup_MissingDisplayName(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(SCIMGroup{Schemas: []string{scimGroupSchema}})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestSCIMCreateGroup_WithInitialMembers(t *testing.T) {
	addCalled := 0
	kc := &fakeKeycloak{
		createGroupFn: func(ctx context.Context, name string) (string, error) {
			return "new-group-id", nil
		},
		addUserToGroupFn: func(ctx context.Context, userID, groupID string) error {
			addCalled++
			return nil
		},
	}
	// C1: initial members are only added once verified as belonging to the
	// caller's org — wire a permissive fakeDB so both seeded members pass.
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(SCIMGroup{
		Schemas:     []string{scimGroupSchema},
		DisplayName: "Engineering",
		Members:     []scimGroupMember{{Value: "user-1"}, {Value: "user-2"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if addCalled != 2 {
		t.Errorf("expected AddUserToGroup called 2 times, got %d", addCalled)
	}
}

// TestSCIMCreateGroup_SkipsMemberOutsideOrg proves C1: an initial member
// that does NOT belong to the caller's org is silently skipped, never
// bound into the new group.
func TestSCIMCreateGroup_SkipsMemberOutsideOrg(t *testing.T) {
	addCalled := 0
	kc := &fakeKeycloak{
		createGroupFn: func(ctx context.Context, name string) (string, error) {
			return "new-group-id", nil
		},
		addUserToGroupFn: func(ctx context.Context, userID, groupID string) error {
			addCalled++
			return nil
		},
	}
	// No queryRowFn override — fakeDB's default QueryRow returns ErrNoRows,
	// i.e. "not found in this org", for every ownership check.
	db := &fakeDB{}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(SCIMGroup{
		Schemas:     []string{scimGroupSchema},
		DisplayName: "Engineering",
		Members:     []scimGroupMember{{Value: "foreign-user"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if addCalled != 0 {
		t.Errorf("expected the foreign-org member to be skipped, but AddUserToGroup was called %d times", addCalled)
	}
	var grp SCIMGroup
	if err := json.NewDecoder(rec.Body).Decode(&grp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(grp.Members) != 0 {
		t.Errorf("expected 0 members in response, got %d", len(grp.Members))
	}
}

func TestSCIMCreateGroup_KCError(t *testing.T) {
	kc := &fakeKeycloak{
		createGroupWithOrgIDFn: func(ctx context.Context, name, orgID string) (string, error) {
			return "", fmt.Errorf("keycloak error")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(SCIMGroup{Schemas: []string{scimGroupSchema}, DisplayName: "Ops"})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// ---- SCIMPatchGroup ----

func TestSCIMPatchGroup_RequiresOrgContext(t *testing.T) {
	h := setupTestHandler(t)
	patch := scimPatchRequest{Schemas: []string{scimPatchSchema}}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Groups/g1", bytes.NewReader(body))
	req = withGroupID(req, "g1")
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no org context: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMPatchGroup_RenameDisplayName(t *testing.T) {
	renamed := ""
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("g1", "OldName", middleware.DefaultOrgID), nil
		},
		renameGroupFn: func(ctx context.Context, groupID, newName string) error {
			renamed = newName
			return nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	patch := scimPatchRequest{
		Schemas: []string{scimPatchSchema},
		Operations: []scimPatchOp{
			{Op: "replace", Path: "displayName", Value: "NewName"},
		},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Groups/g1", bytes.NewReader(body))
	req = withGroupID(req, "g1")
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if renamed != "NewName" {
		t.Errorf("expected RenameGroup called with NewName, got %q", renamed)
	}
}

// TestSCIMPatchGroup_CrossOrgReturns404 is the C1 load-bearing proof: an
// org-scoped SCIM token cannot PATCH a group belonging to a different org.
func TestSCIMPatchGroup_CrossOrgReturns404(t *testing.T) {
	renamed := false
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("g1", "Org A Group", scimTestOrgA), nil
		},
		renameGroupFn: func(ctx context.Context, groupID, newName string) error {
			renamed = true
			return nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	patch := scimPatchRequest{
		Schemas: []string{scimPatchSchema},
		Operations: []scimPatchOp{
			{Op: "replace", Path: "displayName", Value: "Pwned"},
		},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Groups/g1", bytes.NewReader(body))
	req = withGroupID(req, "g1")
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, scimTestOrgB)
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org patch: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if renamed {
		t.Fatal("cross-org patch must never reach Keycloak's RenameGroup")
	}
}

func TestSCIMPatchGroup_AddMembers(t *testing.T) {
	addCalled := 0
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("g1", "Eng", middleware.DefaultOrgID), nil
		},
		addUserToGroupFn: func(ctx context.Context, userID, groupID string) error {
			addCalled++
			return nil
		},
	}
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	patch := scimPatchRequest{
		Schemas: []string{scimPatchSchema},
		Operations: []scimPatchOp{
			{Op: "add", Path: "members", Value: []interface{}{
				map[string]interface{}{"value": "user-a"},
				map[string]interface{}{"value": "user-b"},
			}},
		},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Groups/g1", bytes.NewReader(body))
	req = withGroupID(req, "g1")
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if addCalled != 2 {
		t.Errorf("expected 2 AddUserToGroup calls, got %d", addCalled)
	}
}

// TestSCIMPatchGroup_SkipsAddMemberOutsideOrg proves C1: a PATCH add op
// targeting a user outside the caller's org is skipped, not applied.
func TestSCIMPatchGroup_SkipsAddMemberOutsideOrg(t *testing.T) {
	addCalled := 0
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("g1", "Eng", middleware.DefaultOrgID), nil
		},
		addUserToGroupFn: func(ctx context.Context, userID, groupID string) error {
			addCalled++
			return nil
		},
	}
	db := &fakeDB{} // default QueryRow -> ErrNoRows -> "not in org"
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	patch := scimPatchRequest{
		Schemas: []string{scimPatchSchema},
		Operations: []scimPatchOp{
			{Op: "add", Path: "members", Value: []interface{}{
				map[string]interface{}{"value": "foreign-user"},
			}},
		},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Groups/g1", bytes.NewReader(body))
	req = withGroupID(req, "g1")
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if addCalled != 0 {
		t.Errorf("expected the foreign-org member add to be skipped, got %d AddUserToGroup calls", addCalled)
	}
}

func TestSCIMPatchGroup_RemoveMembers(t *testing.T) {
	removeCalled := 0
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("g1", "Eng", middleware.DefaultOrgID), nil
		},
		removeUserFromGroupFn: func(ctx context.Context, userID, groupID string) error {
			removeCalled++
			return nil
		},
	}
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	patch := scimPatchRequest{
		Schemas: []string{scimPatchSchema},
		Operations: []scimPatchOp{
			{Op: "remove", Path: "members", Value: []interface{}{
				map[string]interface{}{"value": "user-x"},
			}},
		},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Groups/g1", bytes.NewReader(body))
	req = withGroupID(req, "g1")
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if removeCalled != 1 {
		t.Errorf("expected 1 RemoveUserFromGroup call, got %d", removeCalled)
	}
}

func TestSCIMPatchGroup_NotFound(t *testing.T) {
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return nil, fmt.Errorf("404 not found")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	patch := scimPatchRequest{Schemas: []string{scimPatchSchema}}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Groups/missing", bytes.NewReader(body))
	req = withGroupID(req, "missing")
	req.Header.Set("Content-Type", scimContentType)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// ---- SCIMDeleteGroup ----

func TestSCIMDeleteGroup_RequiresOrgContext(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Groups/del-grp", nil)
	req = withGroupID(req, "del-grp")
	rec := httptest.NewRecorder()
	h.SCIMDeleteGroup(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no org context: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSCIMDeleteGroup_Success(t *testing.T) {
	deleted := false
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("del-grp", "ToDelete", middleware.DefaultOrgID), nil
		},
		deleteGroupFn: func(ctx context.Context, groupID string) error {
			deleted = true
			return nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Groups/del-grp", nil)
	req = withGroupID(req, "del-grp")
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMDeleteGroup(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if !deleted {
		t.Error("expected DeleteGroup to be called")
	}
}

// TestSCIMDeleteGroup_CrossOrgReturns404 is the C1 load-bearing proof: an
// org-scoped SCIM token cannot DELETE a group belonging to a different org.
func TestSCIMDeleteGroup_CrossOrgReturns404(t *testing.T) {
	deleted := false
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("del-grp", "Org A Group", scimTestOrgA), nil
		},
		deleteGroupFn: func(ctx context.Context, groupID string) error {
			deleted = true
			return nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Groups/del-grp", nil)
	req = withGroupID(req, "del-grp")
	req = withScimOrg(req, scimTestOrgB)
	rec := httptest.NewRecorder()
	h.SCIMDeleteGroup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org delete: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if deleted {
		t.Fatal("cross-org delete must never reach Keycloak's DeleteGroup")
	}
}

func TestSCIMDeleteGroup_NotFound(t *testing.T) {
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return nil, fmt.Errorf("404 not found")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Groups/gone", nil)
	req = withGroupID(req, "gone")
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMDeleteGroup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// ---- extractMemberValues ----

func TestExtractMemberValues(t *testing.T) {
	vals := extractMemberValues([]interface{}{
		map[string]interface{}{"value": "uid-1"},
		map[string]interface{}{"value": "uid-2"},
	})
	if len(vals) != 2 || vals[0] != "uid-1" || vals[1] != "uid-2" {
		t.Errorf("unexpected: %v", vals)
	}

	single := extractMemberValues(map[string]interface{}{"value": "uid-3"})
	if len(single) != 1 || single[0] != "uid-3" {
		t.Errorf("unexpected single: %v", single)
	}

	empty := extractMemberValues(nil)
	if len(empty) != 0 {
		t.Errorf("expected empty, got %v", empty)
	}
}
