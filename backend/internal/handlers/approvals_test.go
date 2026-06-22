package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// keycloakNotCalled panics if CreateUser or DisableUser is called.
// Embeds the standard fakeKeycloak but overrides the methods under test.
type keycloakNotCalledFake struct {
	*fakeKeycloak
	t *testing.T
}

func (k *keycloakNotCalledFake) CreateUser(_ context.Context, _, _, _, _ string) (*keycloak.CreateUserResult, error) {
	k.t.Fatal("CreateUser must not be called before approval")
	return nil, nil
}

func (k *keycloakNotCalledFake) DisableUser(_ context.Context, _ string) error {
	k.t.Fatal("DisableUser must not be called before approval")
	return nil
}

// TestSubmitApprovalHelpdeskDoesNotCallKeycloak verifies that a helpdesk
// onboard request via the approval flow does NOT call Keycloak until approved.
func TestSubmitApprovalHelpdeskDoesNotCallKeycloak(t *testing.T) {
	insertedID := "00000000-0000-0000-0000-000000000001"
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			// RETURNING id from INSERT
			return fakeRow{scanFn: func(dest ...any) error {
				if p, ok := dest[0].(*string); ok {
					*p = insertedID
				}
				return nil
			}}
		},
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
	}

	kc := &keycloakNotCalledFake{
		fakeKeycloak: &fakeKeycloak{},
		t:            t,
	}

	h := &Handler{
		db:       db,
		keycloak: kc,
		fleet:    &fakeFleet{},
		logger:   zap.NewNop(),
	}
	h.scimBearerMW = SCIMBearerMiddleware("")
	h.accessEvalBearerMW = accessEvalBearerMiddleware("")

	body := `{"actionType":"onboard","payload":{"firstName":"Jo","lastName":"Doe","email":"jo@example.com","department":"Eng","role":"user"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approval-requests", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(setActorCtx(req.Context(), "helpdesk-user"))
	rec := httptest.NewRecorder()

	h.SubmitApproval(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false: %s", resp.Error)
	}
}

// TestDecideApprovalRejectAudited verifies that a rejection is audited and
// no Keycloak/Fleet calls are made.
func TestDecideApprovalRejectAudited(t *testing.T) {
	approvalID := "00000000-0000-0000-0000-000000000002"
	userID := "00000000-0000-0000-0000-000000000099"

	auditInserted := false
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			// Return pending offboard request
			return fakeRow{scanFn: func(dest ...any) error {
				// action_type, status, payload
				if len(dest) < 3 {
					return nil
				}
				if p, ok := dest[0].(*string); ok {
					*p = "offboard"
				}
				if p, ok := dest[1].(*string); ok {
					*p = "pending"
				}
				payload, _ := json.Marshal(map[string]string{"userId": userID})
				if p, ok := dest[2].(*[]byte); ok {
					*p = payload
				}
				return nil
			}}
		},
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if len(args) >= 2 {
				if s, ok := args[1].(string); ok && s == "approval.rejected" {
					auditInserted = true
				}
			}
			if strings.Contains(sql, "UPDATE approval_requests") {
				return pgconn.NewCommandTag("UPDATE 1"), nil
			}
			return pgconn.CommandTag{}, nil
		},
	}
	db.beginFn = func(_ context.Context) (pgx.Tx, error) {
		return &fakeTx{
			execFn: db.execFn,
			queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			},
		}, nil
	}

	kc := &keycloakNotCalledFake{fakeKeycloak: &fakeKeycloak{}, t: t}

	h := &Handler{
		db:       db,
		keycloak: kc,
		fleet:    &fakeFleet{},
		logger:   zap.NewNop(),
	}
	h.scimBearerMW = SCIMBearerMiddleware("")
	h.accessEvalBearerMW = accessEvalBearerMiddleware("")

	body := `{"decision":"rejected"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/approval-requests/"+approvalID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(setActorCtx(req.Context(), "admin-user"))
	req = withApprovalChiParam(req, "id", approvalID)
	rec := httptest.NewRecorder()

	h.DecideApproval(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !auditInserted {
		t.Error("expected audit record for rejection to be inserted")
	}
}

// TestDecideApprovalAlreadyDecidedConflict verifies that deciding an already-
// decided request returns 409 Conflict.
func TestDecideApprovalAlreadyDecidedConflict(t *testing.T) {
	approvalID := "00000000-0000-0000-0000-000000000003"

	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if len(dest) < 3 {
					return nil
				}
				if p, ok := dest[0].(*string); ok {
					*p = "onboard"
				}
				if p, ok := dest[1].(*string); ok {
					*p = "approved" // already decided
				}
				if p, ok := dest[2].(*[]byte); ok {
					*p = []byte("{}")
				}
				return nil
			}}
		},
	}

	h := &Handler{
		db:     db,
		logger: zap.NewNop(),
	}
	h.scimBearerMW = SCIMBearerMiddleware("")
	h.accessEvalBearerMW = accessEvalBearerMiddleware("")

	body := `{"decision":"approved"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/approval-requests/"+approvalID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(setActorCtx(req.Context(), "admin-user"))
	req = withApprovalChiParam(req, "id", approvalID)
	rec := httptest.NewRecorder()

	h.DecideApproval(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDecideApprovalExecutionFailureResetsPending(t *testing.T) {
	approvalID := "00000000-0000-0000-0000-000000000004"

	sawExecuting := false
	sawReset := false
	sawApproved := false
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if len(dest) < 3 {
					return nil
				}
				*(dest[0].(*string)) = "onboard"
				*(dest[1].(*string)) = "pending"
				payload, _ := json.Marshal(map[string]string{"email": "new@example.com"})
				*(dest[2].(*[]byte)) = payload
				return nil
			}}
		},
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			switch {
			case strings.Contains(sql, "status='pending', decided_by=NULL"):
				sawReset = true
				return pgconn.NewCommandTag("UPDATE 1"), nil
			case strings.Contains(sql, "status='executing'"):
				sawExecuting = true
				return pgconn.NewCommandTag("UPDATE 1"), nil
			case strings.Contains(sql, "status='approved'"):
				sawApproved = true
				return pgconn.NewCommandTag("UPDATE 1"), nil
			default:
				return pgconn.CommandTag{}, nil
			}
		},
	}
	kc := &fakeKeycloak{
		createUserFn: func(context.Context, string, string, string, string) (*keycloak.CreateUserResult, error) {
			return nil, errors.New("keycloak unavailable")
		},
	}
	h := &Handler{
		db:       db,
		keycloak: kc,
		fleet:    &fakeFleet{},
		logger:   zap.NewNop(),
	}

	body := `{"decision":"approved"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/approval-requests/"+approvalID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(setActorCtx(req.Context(), "admin-user"))
	req = withApprovalChiParam(req, "id", approvalID)
	rec := httptest.NewRecorder()

	h.DecideApproval(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if !sawExecuting {
		t.Fatal("expected approval to be claimed as executing")
	}
	if !sawReset {
		t.Fatal("expected failed execution to reset approval to pending")
	}
	if sawApproved {
		t.Fatal("must not mark approval approved after execution failure")
	}
}

// TestDecideApprovalSelfApprovalForbidden verifies that a user who is both the
// requester and the approver receives 403 and the request remains pending.
func TestDecideApprovalSelfApprovalForbidden(t *testing.T) {
	approvalID := "00000000-0000-0000-0000-000000000005"
	actorID := "same-user"

	executeCalled := false
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			// Return a pending request whose requester_id == actorID.
			return fakeRow{scanFn: func(dest ...any) error {
				if len(dest) < 4 {
					return nil
				}
				*(dest[0].(*string)) = "onboard"
				*(dest[1].(*string)) = "pending"
				payload, _ := json.Marshal(map[string]string{"email": "new@example.com"})
				*(dest[2].(*[]byte)) = payload
				*(dest[3].(*string)) = actorID // requester_id == actorID
				return nil
			}}
		},
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "status='executing'") {
				executeCalled = true
			}
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	}

	h := &Handler{
		db:       db,
		keycloak: &fakeKeycloak{},
		fleet:    &fakeFleet{},
		logger:   zap.NewNop(),
	}
	h.scimBearerMW = SCIMBearerMiddleware("")
	h.accessEvalBearerMW = accessEvalBearerMiddleware("")

	body := `{"decision":"approved"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/approval-requests/"+approvalID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(setActorCtx(req.Context(), actorID))
	req = withApprovalChiParam(req, "id", approvalID)
	rec := httptest.NewRecorder()

	h.DecideApproval(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if executeCalled {
		t.Fatal("execution must not be triggered when self-approval is blocked")
	}
}

// setActorCtx injects an actor ID into the context.
func setActorCtx(ctx context.Context, actorID string) context.Context {
	return context.WithValue(ctx, middleware.ActorIDKey, actorID)
}

// withApprovalChiParam injects a chi URL parameter into the request context.
func withApprovalChiParam(r *http.Request, key, val string) *http.Request {
	chiCtx := context.WithValue(r.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{key}, Values: []string{val}},
	})
	return r.WithContext(chiCtx)
}
