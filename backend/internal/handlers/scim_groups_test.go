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
)

// ---- helpers ----

func withGroupID(r *http.Request, id string) *http.Request {
	chiCtx := context.WithValue(r.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{id}},
	})
	return r.WithContext(chiCtx)
}

// ---- SCIMListGroups ----

func TestSCIMListGroups_Empty(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil)
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
	id1, name1 := "gid-1", "Engineering"
	id2, name2 := "gid-2", "Marketing"
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			return []*gocloak.Group{
				{ID: &id1, Name: &name1},
				{ID: &id2, Name: &name2},
			}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil)
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

func TestSCIMListGroups_FilterDisplayName(t *testing.T) {
	id1, name1 := "gid-1", "Engineering"
	id2, name2 := "gid-2", "Marketing"
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			return []*gocloak.Group{
				{ID: &id1, Name: &name1},
				{ID: &id2, Name: &name2},
			}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, `/scim/v2/Groups?filter=displayName+eq+"Engineering"`, nil)
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
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups", nil)
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// ---- SCIMGetGroup ----

func TestSCIMGetGroup_Success(t *testing.T) {
	gid, gname := "group-abc", "Sales"
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return &gocloak.Group{ID: &gid, Name: &gname}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups/group-abc", nil)
	req = withGroupID(req, "group-abc")
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

func TestSCIMGetGroup_NotFound(t *testing.T) {
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return nil, fmt.Errorf("404 not found")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Groups/missing", nil)
	req = withGroupID(req, "missing")
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

func TestSCIMCreateGroup_Success(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(SCIMGroup{
		Schemas:     []string{scimGroupSchema},
		DisplayName: "Finance",
	})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
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
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(SCIMGroup{
		Schemas:     []string{scimGroupSchema},
		DisplayName: "Engineering",
		Members:     []scimGroupMember{{Value: "user-1"}, {Value: "user-2"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if addCalled != 2 {
		t.Errorf("expected AddUserToGroup called 2 times, got %d", addCalled)
	}
}

func TestSCIMCreateGroup_KCError(t *testing.T) {
	kc := &fakeKeycloak{
		createGroupFn: func(ctx context.Context, name string) (string, error) {
			return "", fmt.Errorf("keycloak error")
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(SCIMGroup{Schemas: []string{scimGroupSchema}, DisplayName: "Ops"})
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMCreateGroup(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// ---- SCIMPatchGroup ----

func TestSCIMPatchGroup_RenameDisplayName(t *testing.T) {
	gid, gname := "g1", "OldName"
	renamed := ""
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return &gocloak.Group{ID: &gid, Name: &gname}, nil
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
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if renamed != "NewName" {
		t.Errorf("expected RenameGroup called with NewName, got %q", renamed)
	}
}

func TestSCIMPatchGroup_AddMembers(t *testing.T) {
	gid, gname := "g1", "Eng"
	addCalled := 0
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return &gocloak.Group{ID: &gid, Name: &gname}, nil
		},
		addUserToGroupFn: func(ctx context.Context, userID, groupID string) error {
			addCalled++
			return nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
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
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if addCalled != 2 {
		t.Errorf("expected 2 AddUserToGroup calls, got %d", addCalled)
	}
}

func TestSCIMPatchGroup_RemoveMembers(t *testing.T) {
	gid, gname := "g1", "Eng"
	removeCalled := 0
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return &gocloak.Group{ID: &gid, Name: &gname}, nil
		},
		removeUserFromGroupFn: func(ctx context.Context, userID, groupID string) error {
			removeCalled++
			return nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
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
	rec := httptest.NewRecorder()
	h.SCIMPatchGroup(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// ---- SCIMDeleteGroup ----

func TestSCIMDeleteGroup_Success(t *testing.T) {
	gid, gname := "del-grp", "ToDelete"
	deleted := false
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return &gocloak.Group{ID: &gid, Name: &gname}, nil
		},
		deleteGroupFn: func(ctx context.Context, groupID string) error {
			deleted = true
			return nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Groups/del-grp", nil)
	req = withGroupID(req, "del-grp")
	rec := httptest.NewRecorder()
	h.SCIMDeleteGroup(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if !deleted {
		t.Error("expected DeleteGroup to be called")
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
