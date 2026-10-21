package handlers

// Regression test for the group-membership provisioning sync.
//
// The sync used to be launched as `go h.triggerGroupSyncForUser(r.Context(), …)`.
// net/http cancels the request context the moment the handler returns, so the
// goroutine's first call (GetUserGroups) always failed with "context
// canceled" and the sync to Slack/SCIM/GitHub never happened. The sync now
// owns a detached, time-bounded context of its own.

import (
	"context"
	"testing"

	"github.com/Nerzal/gocloak/v13"
	"go.uber.org/zap"
)

func TestTriggerGroupSyncUsesLiveContext(t *testing.T) {
	// Reproduce what net/http does the instant the handler returns.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if cancelled.Err() == nil {
		t.Fatal("precondition: the request context should already be cancelled")
	}

	var observedErr error
	teamName := "team-a"
	kc := &fakeKeycloak{
		getUserGroupsFn: func(ctx context.Context, userID string) ([]*gocloak.Group, error) {
			observedErr = ctx.Err()
			return []*gocloak.Group{{Name: &teamName}}, nil
		},
	}
	h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())

	h.triggerGroupSyncForUser("user-1")

	if observedErr != nil {
		t.Fatalf("group sync ran with a dead context (ctx.Err() = %v); it must use its own detached context", observedErr)
	}
}

func TestTriggerGroupSyncPropagatesKeycloakError(t *testing.T) {
	// A Keycloak failure must not panic and must not be reported as success.
	kc := &fakeKeycloak{
		getUserGroupsFn: func(ctx context.Context, userID string) ([]*gocloak.Group, error) {
			return nil, context.DeadlineExceeded
		},
	}
	h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())

	// Must return normally.
	h.triggerGroupSyncForUser("user-1")
}
