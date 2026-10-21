package handlers

// Regression tests for the SCIM handler fixes:
//   - #141 a PATCH reported success when the local DB write failed, and the
//     version bump had no lost-update protection.
//   - #142 a group "replace members" silently became "add members" when the
//     current member list could not be read.
//   - #143 the user-create idempotency check treated ANY database error as
//     "not found".
//   - #160 not-found detection matched on the error text.
//   - #171 a logical filter (and/or/not) was silently ignored instead of
//     producing 400 invalidFilter (RFC 7644 §3.4.2.2).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Nerzal/gocloak/v13"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// scimFixUserID is the (UUID-shaped) user id used by the SCIM fix tests —
// the SCIM handlers validate the id with ValidateUserID, which requires a
// UUID.
const scimFixUserID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// withScimUserID attaches the {id} chi route parameter the SCIM user
// handlers read (they are called directly in these tests, so the router
// never populates it).
func withScimUserID(r *http.Request, id string) *http.Request {
	chiCtx := context.WithValue(r.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{id}},
	})
	return r.WithContext(chiCtx)
}

// scimPatchUserDB wires a fake DB for the SCIM PATCH path:
//   - the user lookup returns the given email/names/version,
//   - the version upsert reports the given number of affected rows,
//   - the users UPDATE fails when updateErr is set.
func scimPatchUserDB(email, first, last string, version int64, versionRows int64, updateErr error) *fakeDB {
	return &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			return fakeRow{scanFn: func(dest ...any) error {
				now := time.Now()
				switch len(dest) {
				case 7:
					// email, first_name, last_name, disabled, created_at, updated_at, version
					*(dest[0].(*string)) = email
					*(dest[1].(*string)) = first
					*(dest[2].(*string)) = last
					*(dest[3].(*bool)) = false
					*(dest[4].(*time.Time)) = now
					*(dest[5].(*time.Time)) = now
					*(dest[6].(*int64)) = version
				default:
					*(dest[0].(*string)) = "user-1"
					*(dest[1].(*string)) = email
					*(dest[2].(*string)) = first
					*(dest[3].(*string)) = last
					*(dest[4].(*bool)) = false
					*(dest[5].(*time.Time)) = now
					*(dest[6].(*time.Time)) = now
					*(dest[7].(*int64)) = version
				}
				return nil
			}}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if updateErr != nil && bytes.Contains([]byte(sql), []byte("UPDATE users")) {
				return pgconn.CommandTag{}, updateErr
			}
			if bytes.Contains([]byte(sql), []byte("scim_resource_versions")) {
				return pgconn.NewCommandTag(fmt.Sprintf("INSERT 0 %d", versionRows)), nil
			}
			return pgconn.CommandTag{}, nil
		},
	}
}

// ---- #141: PATCH reports the DB failure ----

func TestSCIMPatchUser_DBFailureIsNotSuccess(t *testing.T) {
	dbErr := errors.New("connection reset")
	var updates [][]string
	kc := &fakeKeycloak{
		updateUserFn: func(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error {
			updates = append(updates, []string{firstName, lastName})
			return nil
		},
	}
	h := NewHandler(scimPatchUserDB("a@b.com", "A", "B", 3, 1, dbErr), kc, &fakeFleet{}, zap.NewNop())

	patch := scimPatchRequest{
		Schemas:    []string{scimPatchSchema},
		Operations: []scimPatchOp{{Op: "replace", Path: "name.givenName", Value: "Changed"}},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/"+scimFixUserID, bytes.NewReader(body))
	req = withScimUserID(req, scimFixUserID)
	req = withScimOrg(req, middleware.DefaultOrgID)
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMPatchUser(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on a DB failure, got %d: %s", rec.Code, rec.Body.String())
	}
	// The identity provider must be restored to its pre-PATCH values, so the
	// two sides do not stay split-brain.
	if len(updates) != 2 {
		t.Fatalf("expected the Keycloak update and its compensating restore, got %d calls", len(updates))
	}
	if updates[0][0] != "Changed" {
		t.Errorf("first Keycloak write=%v, want the patched value", updates[0])
	}
	if updates[1][0] != "A" || updates[1][1] != "B" {
		t.Errorf("the compensating write must restore the previous values, got %v", updates[1])
	}
}

// TestSCIMPatchUser_StaleIfMatchIsPreconditionFailed proves the ETag the
// handler emits is honoured: a stale If-Match is 412, not a silent overwrite.
func TestSCIMPatchUser_StaleIfMatchIsPreconditionFailed(t *testing.T) {
	kc := &fakeKeycloak{
		updateUserFn: func(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error {
			t.Fatal("Keycloak must not be updated when the precondition fails")
			return nil
		},
	}
	h := NewHandler(scimPatchUserDB("a@b.com", "A", "B", 3, 1, nil), kc, &fakeFleet{}, zap.NewNop())

	patch := scimPatchRequest{
		Schemas:    []string{scimPatchSchema},
		Operations: []scimPatchOp{{Op: "replace", Path: "name.givenName", Value: "Changed"}},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", bytes.NewReader(body))
	req = withScimUserID(req, scimFixUserID)
	req = withScimOrg(req, middleware.DefaultOrgID)
	req.Header.Set("Content-Type", scimContentType)
	req.Header.Set("If-Match", `W/"1"`)
	rec := httptest.NewRecorder()
	h.SCIMPatchUser(rec, req)

	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 for a stale If-Match, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestSCIMPatchUser_MatchingIfMatchSucceeds proves a matching precondition
// still performs the update.
func TestSCIMPatchUser_MatchingIfMatchSucceeds(t *testing.T) {
	h := NewHandler(scimPatchUserDB("a@b.com", "A", "B", 3, 1, nil), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	patch := scimPatchRequest{
		Schemas:    []string{scimPatchSchema},
		Operations: []scimPatchOp{{Op: "replace", Path: "name.givenName", Value: "Changed"}},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", bytes.NewReader(body))
	req = withScimUserID(req, scimFixUserID)
	req = withScimOrg(req, middleware.DefaultOrgID)
	req.Header.Set("Content-Type", scimContentType)
	req.Header.Set("If-Match", `W/"3"`)
	rec := httptest.NewRecorder()
	h.SCIMPatchUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `W/"4"` {
		t.Errorf("ETag=%q want W/\"4\"", got)
	}
}

// TestSCIMPatchUser_ConcurrentModificationIsRejected proves the version bump
// is a CONDITIONAL write: when the stored version no longer matches the one
// the request read, the write affects no rows and the request is rejected
// with 412 instead of two concurrent PATCHes writing the same version.
func TestSCIMPatchUser_ConcurrentModificationIsRejected(t *testing.T) {
	kc := &fakeKeycloak{
		updateUserFn: func(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error {
			t.Fatal("Keycloak must not be updated when the version bump fails")
			return nil
		},
	}
	// versionRows = 0: the conditional version bump matched no row.
	h := NewHandler(scimPatchUserDB("a@b.com", "A", "B", 3, 0, nil), kc, &fakeFleet{}, zap.NewNop())

	patch := scimPatchRequest{
		Schemas:    []string{scimPatchSchema},
		Operations: []scimPatchOp{{Op: "replace", Path: "name.givenName", Value: "Changed"}},
	}
	body, _ := json.Marshal(patch)
	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", bytes.NewReader(body))
	req = withScimUserID(req, scimFixUserID)
	req = withScimOrg(req, middleware.DefaultOrgID)
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMPatchUser(rec, req)

	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 for a concurrent modification, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---- #142: group replace-members ----

func TestSCIMPatchGroup_ReplaceMembersFailsWhenCurrentMembersUnreadable(t *testing.T) {
	addCalled := 0
	kc := &fakeKeycloak{
		getGroupByIDFn: func(ctx context.Context, groupID string) (*gocloak.Group, error) {
			return groupWithOrg("g1", "Eng", middleware.DefaultOrgID), nil
		},
		listGroupMembersFn: func(ctx context.Context, groupID string) ([]*gocloak.User, error) {
			return nil, errors.New("keycloak unavailable")
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
			{Op: "replace", Path: "members", Value: []interface{}{
				map[string]interface{}{"value": "user-a"},
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

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the current member list cannot be read, got %d: %s", rec.Code, rec.Body.String())
	}
	if addCalled != 0 {
		t.Errorf("the new member set must not be added on top of the old one, got %d AddUserToGroup calls", addCalled)
	}
}

// ---- #143: user-create idempotency ----

func TestSCIMCreateUser_DBLookupErrorIsNotTreatedAsNotFound(t *testing.T) {
	created := false
	kc := &fakeKeycloak{
		createUserFn: func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
			created = true
			return nil, errors.New("must not be called")
		},
	}
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			return fakeRow{scanFn: func(dest ...any) error { return errors.New("connection reset") }}
		},
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Users",
		bytes.NewReader([]byte(`{"userName":"new@example.com"}`)))
	req = withScimOrg(req, middleware.DefaultOrgID)
	req.Header.Set("Content-Type", scimContentType)
	rec := httptest.NewRecorder()
	h.SCIMCreateUser(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on a DB lookup failure, got %d: %s", rec.Code, rec.Body.String())
	}
	if created {
		t.Error("no Keycloak user may be created when the idempotency lookup failed")
	}
}

// ---- #160: error matching ----

func TestIsNotFound_UsesErrorsIs(t *testing.T) {
	if !isNotFound(pgx.ErrNoRows) {
		t.Error("pgx.ErrNoRows must be reported as not-found")
	}
	if !isNotFound(fmt.Errorf("wrapped: %w", pgx.ErrNoRows)) {
		t.Error("a wrapped pgx.ErrNoRows must be reported as not-found")
	}
	if isNotFound(errors.New("no rows in result set")) {
		t.Error("an unrelated error whose text mentions 'no rows' must not be reported as not-found")
	}
	if isNotFound(nil) {
		t.Error("nil must not be reported as not-found")
	}
}

func TestIsUniqueViolation_UsesPgErrorCode(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	if !isUniqueViolation(pgErr) {
		t.Error("SQLSTATE 23505 must be reported as a unique violation")
	}
	if !isUniqueViolation(fmt.Errorf("wrapped: %w", pgErr)) {
		t.Error("a wrapped unique violation must be reported as a unique violation")
	}
	other := &pgconn.PgError{Code: "23503", Message: "duplicate key value violates unique constraint"}
	if isUniqueViolation(other) {
		t.Error("a different SQLSTATE must not be reported as a unique violation")
	}
	if isUniqueViolation(errors.New("duplicate key value violates unique constraint")) {
		t.Error("an error whose text mentions 'duplicate key' must not be reported as a unique violation")
	}
}

// ---- #171: logical filters ----

func TestSCIMListUsers_LogicalFilterIsInvalidFilter(t *testing.T) {
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			t.Fatal("no query may run for an invalid filter")
			return nil, errors.New("unexpected query")
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, `/scim/v2/Users?filter=userName+eq+%22a%22+and+userName+eq+%22b%22`, nil)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMListUsers(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a logical filter, got %d: %s", rec.Code, rec.Body.String())
	}
	var scimErr scimError
	if err := json.Unmarshal(rec.Body.Bytes(), &scimErr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if scimErr.ScimType != "invalidFilter" {
		t.Errorf("scimType=%q want invalidFilter", scimErr.ScimType)
	}
}

func TestSCIMListGroups_LogicalFilterIsInvalidFilter(t *testing.T) {
	kc := &fakeKeycloak{
		listGroupsByOrgFn: func(ctx context.Context, orgID string, first, max int) ([]*gocloak.Group, error) {
			t.Fatal("Keycloak must not be queried for an invalid filter")
			return nil, errors.New("unexpected call")
		},
	}
	h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, `/scim/v2/Groups?filter=displayName+eq+%22a%22+or+displayName+eq+%22b%22`, nil)
	req = withScimOrg(req, middleware.DefaultOrgID)
	rec := httptest.NewRecorder()
	h.SCIMListGroups(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a logical filter, got %d: %s", rec.Code, rec.Body.String())
	}
	var scimErr scimError
	if err := json.Unmarshal(rec.Body.Bytes(), &scimErr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if scimErr.ScimType != "invalidFilter" {
		t.Errorf("scimType=%q want invalidFilter", scimErr.ScimType)
	}
}
