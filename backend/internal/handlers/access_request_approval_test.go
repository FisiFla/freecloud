package handlers

// Regression tests for the access-request approval path.
//
// Approving an access request used to insert the app_assignments row and
// return {"status":"approved"} without ever granting the user the Keycloak
// client, so the IdP still denied access. The approval now assigns the client
// first and compensates the grant if the local write cannot be committed.

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

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

const (
	approvalRequestID = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	approvalRequester = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	approvalAppID     = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	approvalOrgID     = "00000000-0000-0000-0000-000000000001"
	approvalKCClient  = "freecloud-app"
)

// approvalTestHandler builds a handler whose transaction answers the
// access-request UPDATE and the connected_apps lookup.
func approvalTestHandler(t *testing.T, kc *fakeKeycloak, insertErr error) *Handler {
	t.Helper()
	tx := &fakeTx{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			switch {
			case strings.Contains(sql, "UPDATE access_requests"):
				return fakeRow{scanFn: func(dest ...any) error {
					*dest[0].(*string) = approvalRequester
					*dest[1].(*string) = approvalAppID
					return nil
				}}
			case strings.Contains(sql, "FROM connected_apps"):
				return fakeRow{scanFn: func(dest ...any) error {
					*dest[0].(*string) = approvalOrgID
					*dest[1].(*string) = approvalKCClient
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if insertErr != nil && strings.Contains(sql, "INSERT INTO app_assignments") {
				return pgconn.CommandTag{}, insertErr
			}
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		},
	}
	db := &fakeDB{beginFn: func(ctx context.Context) (pgx.Tx, error) { return tx, nil }}
	return NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
}

func approvalRequest(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"decision": "approved"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/portal/access-requests/"+approvalRequestID, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{approvalRequestID}},
	})
	ctx := middleware.SetClaims(chiCtx, &middleware.JWTClaims{
		Sub:  "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Role: middleware.RoleSuperAdmin,
	})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: approvalOrgID, Role: "org-admin"})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.AdminDecideAccessRequest(rec, req)
	return rec
}

func TestAdminDecideAccessRequestApprovalGrantsKeycloakClient(t *testing.T) {
	var grantedUser, grantedClient string
	kc := &fakeKeycloak{
		assignUserToClientFn: func(ctx context.Context, userID, clientID string) error {
			grantedUser, grantedClient = userID, clientID
			return nil
		},
	}
	h := approvalTestHandler(t, kc, nil)

	rec := approvalRequest(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if grantedUser != approvalRequester {
		t.Errorf("expected the requester %s to be granted the client, got %q", approvalRequester, grantedUser)
	}
	if grantedClient != approvalKCClient {
		t.Errorf("expected client %s to be granted, got %q", approvalKCClient, grantedClient)
	}
}

func TestAdminDecideAccessRequestApprovalCompensatesOnInsertFailure(t *testing.T) {
	var grantedUser, grantedClient string
	var revokedUser, revokedClient string
	kc := &fakeKeycloak{
		assignUserToClientFn: func(ctx context.Context, userID, clientID string) error {
			grantedUser, grantedClient = userID, clientID
			return nil
		},
		unassignUserFromClientFn: func(ctx context.Context, userID, clientID string) error {
			revokedUser, revokedClient = userID, clientID
			return nil
		},
	}
	h := approvalTestHandler(t, kc, errors.New("insert failed"))

	rec := approvalRequest(t, h)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if grantedUser == "" {
		t.Fatal("expected the client to have been granted before the local write")
	}
	if revokedUser != grantedUser || revokedClient != grantedClient {
		t.Errorf("expected the IdP grant to be rolled back (%s/%s), got %s/%s",
			grantedUser, grantedClient, revokedUser, revokedClient)
	}
}

func TestAdminDecideAccessRequestApprovalFailsWhenKeycloakAssignFails(t *testing.T) {
	kc := &fakeKeycloak{
		assignUserToClientFn: func(ctx context.Context, userID, clientID string) error {
			return errors.New("idp unavailable")
		},
	}
	h := approvalTestHandler(t, kc, nil)

	rec := approvalRequest(t, h)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the IdP grant fails, got %d: %s", rec.Code, rec.Body.String())
	}
}
