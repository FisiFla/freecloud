package handlers

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func TestM02_ExecuteApproval_OffboardRejectsInvalidUserID(t *testing.T) {
	// Production: approval offboard payload must pass ValidateUserID before disable.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	ctx := middleware.SetOrgContext(context.Background(), &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
	err := h.executeApprovedAction(ctx, "actor", "offboard", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", map[string]interface{}{"userId": "not-uuid"})
	if err == nil {
		t.Fatal("expected error for invalid offboard userId")
	}
}
