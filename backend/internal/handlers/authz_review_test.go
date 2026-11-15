package handlers

// L3 lane: a reviewer must not confirm their own access. DecideCampaignItem
// loads the item's user and rejects (403) when it is the caller, mirroring
// DecideApproval's self-approval ban.

import (
	"context"
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
	reviewTestCampaignID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	reviewTestItemID     = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// reviewerCtx builds a human reviewer identity in the default org.
func reviewerCtx(sub, username string) context.Context {
	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub:               sub,
		PreferredUsername: username,
		Email:             username,
		Role:              middleware.RoleAuditor,
	})
	ctx = context.WithValue(ctx, middleware.ActorIDKey, username)
	return middleware.SetOrgContext(ctx, &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
}

// reviewDecideDB mocks one pending campaign item belonging to itemUserID and
// records whether the decision UPDATE ran.
func reviewDecideDB(itemUserID string, updated *bool) DBPool {
	return &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "SELECT 1 FROM review_campaigns") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			// SELECT user_id FROM review_items ...
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = itemUserID
				return nil
			}}
		},
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(sql)), "UPDATE") {
				*updated = true
			}
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	}
}

func reviewDecideRequest(t *testing.T, ctx context.Context) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/campaigns/"+reviewTestCampaignID+"/items/"+reviewTestItemID+"/decide",
		strings.NewReader(`{"decision":"confirm"}`))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := context.WithValue(ctx, chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{
			Keys:   []string{"id", "itemId"},
			Values: []string{reviewTestCampaignID, reviewTestItemID},
		},
	})
	return req.WithContext(chiCtx)
}

func TestDecideCampaignItemSelfDecisionForbidden(t *testing.T) {
	var updated bool
	h := NewHandler(reviewDecideDB("user-1", &updated), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	rec := httptest.NewRecorder()
	h.DecideCampaignItem(rec, reviewDecideRequest(t, reviewerCtx("user-1", "reviewer@example.com")))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("self-decision: expected 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if updated {
		t.Error("self-decision: the decision UPDATE must not run")
	}
}

func TestDecideCampaignItemOtherUserAllowed(t *testing.T) {
	var updated bool
	h := NewHandler(reviewDecideDB("user-2", &updated), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	rec := httptest.NewRecorder()
	h.DecideCampaignItem(rec, reviewDecideRequest(t, reviewerCtx("user-1", "reviewer@example.com")))

	if rec.Code != http.StatusOK {
		t.Fatalf("other-user decision: expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if !updated {
		t.Error("other-user decision: the decision UPDATE must run")
	}
}

func TestDecideCampaignItemNoIdentityForbidden(t *testing.T) {
	var updated bool
	h := NewHandler(reviewDecideDB("user-2", &updated), &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	ctx := middleware.SetOrgContext(context.Background(), &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
	rec := httptest.NewRecorder()
	h.DecideCampaignItem(rec, reviewDecideRequest(t, ctx))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("no identity: expected fail-closed 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if updated {
		t.Error("no identity: the decision UPDATE must not run")
	}
}
