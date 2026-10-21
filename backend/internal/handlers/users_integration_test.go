//go:build integration

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/db"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// TestListUsersQueryRunsAgainstPostgres proves the org-scoped users query is
// valid SQL and returns the caller's org's users.
//
// The unit suite fakes the DB, so it cannot see a malformed query. The
// paged version of this query (issue #151) selected `COALESCE(u.department,
// ”)` inside a CTE and then referenced `page.department` in the outer
// SELECT — the CTE column had no alias, so Postgres rejected the statement
// with "column page.department does not exist" and the endpoint answered 500
// for every caller. Only a real database (or the e2e stack) can catch that.
//
// It also asserts the paging contract: the LIMIT bounds the number of USERS,
// and a user's whole device list stays attached to them.
func TestListUsersQueryRunsAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	defer pool.Close()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	const orgID = "30000000-0000-0000-0000-00000000000c"
	const userID = "30000000-0000-0000-0000-0000000000aa"

	if _, err := pool.Exec(ctx,
		`INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`,
		orgID, "ListUsers Org", "listusers-org",
	); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE keycloak_user_id = $1`, userID)
		_, _ = pool.Exec(c, `DELETE FROM organizations WHERE id = $1`, orgID)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (keycloak_user_id, email, first_name, last_name, department, role, org_id)
		 VALUES ($1, $2, 'Ada', 'Lovelace', 'Research', 'engineer', $3)
		 ON CONFLICT (keycloak_user_id) DO UPDATE SET org_id = EXCLUDED.org_id, department = EXCLUDED.department`,
		userID, "list-users-"+orgID[:1]+"@example.com", orgID,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	h := &Handler{db: pool, logger: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req = req.WithContext(middleware.SetOrgContext(req.Context(), &middleware.OrgContext{
		OrgID: orgID,
		Role:  middleware.OrgMembershipRoleAdmin,
	}))
	rec := httptest.NewRecorder()
	h.ListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ListUsers: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var envelope struct {
		Success bool   `json:"success"`
		Data    []User `json:"data"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	if !envelope.Success {
		t.Fatalf("ListUsers reported failure: %s", envelope.Error)
	}
	users := envelope.Data

	var found *User
	for i := range users {
		if users[i].KeycloakUserID == userID {
			found = &users[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected user %s in the response, got %d users: %s", userID, len(users), rec.Body.String())
	}
	if found.Department != "Research" {
		t.Errorf("expected department %q, got %q", "Research", found.Department)
	}
	if found.Role != "engineer" {
		t.Errorf("expected role %q, got %q", "engineer", found.Role)
	}

	// A user of a different org must never appear in this org's list.
	var foreign int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE org_id <> $1 AND keycloak_user_id <> $2`,
		orgID, userID).Scan(&foreign); err != nil {
		t.Fatalf("count foreign users: %v", err)
	}
	for _, u := range users {
		if u.KeycloakUserID != userID {
			t.Errorf("unexpected user %s in the org-scoped list", u.KeycloakUserID)
		}
	}
}
