package handlers

// L3 lane: DecideApproval must compare requester and decider on the immutable
// Keycloak subject — not only on the mutable preferred_username. The stored
// requester_id is a username; it is resolved to a subject through users.email
// (this deployment sets Keycloak username = email). The username check stays
// as an additional fail-closed condition.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

const approvalTestRequestID = "cccccccc-cccc-cccc-cccc-cccccccccccc"

// approvalDecideDB mocks a pending onboard request from requesterID and a
// users.email → keycloak_user_id directory. updateRan records whether the
// status UPDATE executed.
func approvalDecideDB(requesterID string, directory map[string]string, updateRan *bool) DBPool {
	return &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			switch {
			case strings.Contains(sql, "FROM approval_requests"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "onboard"
					*(dest[1].(*string)) = "pending"
					*(dest[2].(*[]byte)) = []byte(`{"email":"newhire@example.com","firstName":"New","lastName":"Hire"}`)
					*(dest[3].(*string)) = requesterID
					return nil
				}}
			case strings.Contains(sql, "INSERT INTO approval_decisions"):
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*time.Time)) = time.Now().UTC()
					return nil
				}}
			case strings.Contains(sql, "FROM users WHERE email"):
				email, _ := args[0].(string)
				sub, ok := directory[email]
				if !ok {
					return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
				}
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = sub
					return nil
				}}
			default:
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
		},
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "UPDATE approval_requests") {
				*updateRan = true
			}
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	}
}

// deciderCtx builds a human decider identity in the default org.
func deciderCtx(sub, username string) context.Context {
	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub:               sub,
		PreferredUsername: username,
		Email:             username,
		Role:              middleware.RoleSuperAdmin,
	})
	ctx = context.WithValue(ctx, middleware.ActorIDKey, username)
	return middleware.SetOrgContext(ctx, &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
}

func approvalDecideRequest(t *testing.T, ctx context.Context, decision string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/approvals/"+approvalTestRequestID+"/decide",
		strings.NewReader(`{"decision":"`+decision+`"}`))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(ctx, chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{
			Keys:   []string{"id"},
			Values: []string{approvalTestRequestID},
		},
	})
	return req.WithContext(chiCtx)
}

// TestDecideApprovalSelfApprovalBySubjectForbidden is the core regression:
// the requester renamed (alice-old → alice-new) after submitting, so the
// mutable usernames differ — but the immutable subject matches, so the
// decider is still the requester and must be rejected.
func TestDecideApprovalSelfApprovalBySubjectForbidden(t *testing.T) {
	var updateRan bool
	directory := map[string]string{"alice-old@example.com": "user-sub-1"}
	h := NewHandler(approvalDecideDB("alice-old@example.com", directory, &updateRan), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	rec := httptest.NewRecorder()
	h.DecideApproval(rec, approvalDecideRequest(t, deciderCtx("user-sub-1", "alice-new@example.com"), "rejected"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("renamed self-decider: expected 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if updateRan {
		t.Error("renamed self-decider: the status UPDATE must not run")
	}
}

// TestDecideApprovalUsernameCheckKept pins the additional username condition:
// a recycled username (same preferred_username, different subject) still
// fails closed.
func TestDecideApprovalUsernameCheckKept(t *testing.T) {
	var updateRan bool
	directory := map[string]string{"alice@example.com": "user-sub-1"}
	h := NewHandler(approvalDecideDB("alice@example.com", directory, &updateRan), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	rec := httptest.NewRecorder()
	h.DecideApproval(rec, approvalDecideRequest(t, deciderCtx("user-sub-2", "alice@example.com"), "rejected"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("recycled username: expected fail-closed 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if updateRan {
		t.Error("recycled username: the status UPDATE must not run")
	}
}

// TestDecideApprovalStoredSubMatchForbidden covers stored requests whose
// requester_id already is a subject (ActorIDMiddleware falls back to sub
// when preferred_username is empty): direct subject equality rejects.
func TestDecideApprovalStoredSubMatchForbidden(t *testing.T) {
	var updateRan bool
	h := NewHandler(approvalDecideDB("user-sub-1", map[string]string{}, &updateRan), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	rec := httptest.NewRecorder()
	h.DecideApproval(rec, approvalDecideRequest(t, deciderCtx("user-sub-1", "bob@example.com"), "rejected"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("stored-sub match: expected 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if updateRan {
		t.Error("stored-sub match: the status UPDATE must not run")
	}
}

// TestDecideApprovalDifferentPersonAllowed proves a genuinely different
// decider (different username AND different resolved subject) still passes.
func TestDecideApprovalDifferentPersonAllowed(t *testing.T) {
	var updateRan bool
	directory := map[string]string{"alice@example.com": "user-sub-1"}
	h := NewHandler(approvalDecideDB("alice@example.com", directory, &updateRan), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	rec := httptest.NewRecorder()
	h.DecideApproval(rec, approvalDecideRequest(t, deciderCtx("user-sub-2", "bob@example.com"), "rejected"))

	if rec.Code != http.StatusOK {
		t.Fatalf("different decider: expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if !updateRan {
		t.Error("different decider: the status UPDATE must run")
	}
}
