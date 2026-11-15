package handlers

// L3 lane: GetAuditIntegrity exposes the GLOBAL audit chain (rowsChecked,
// firstBreakSeq, chainError), exactly like VerifyAuditChain, so it must
// enforce the same system-admin requirement (issue #133).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// TestGetAuditIntegrityRequiresSystemAdmin mirrors
// TestVerifyAuditChainRequiresSystemAdmin: an org auditor must not see the
// global chain's integrity state.
func TestGetAuditIntegrityRequiresSystemAdmin(t *testing.T) {
	db := &auditChainDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	for _, role := range []middleware.Role{
		middleware.RoleAuditor,
		middleware.RoleHelpdesk,
		middleware.RoleReadOnly,
		middleware.RoleEndUser,
	} {
		rec := httptest.NewRecorder()
		h.GetAuditIntegrity(rec, requestWithRole(t, role))
		if rec.Code != http.StatusForbidden {
			t.Errorf("role %s: expected 403, got %d — body: %s", role, rec.Code, rec.Body.String())
		}
		if db.queried {
			t.Errorf("role %s: the chain must not be queried at all", role)
			db.queried = false
		}
	}
}

// TestGetAuditIntegrityRejectsAnonymous proves an unauthenticated request is
// rejected fail-closed.
func TestGetAuditIntegrityRejectsAnonymous(t *testing.T) {
	db := &auditChainDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/integrity", nil)
	rec := httptest.NewRecorder()
	h.GetAuditIntegrity(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if db.queried {
		t.Error("the chain must not be queried for an anonymous request")
	}
}

// TestGetAuditIntegrityAllowsSystemAdmin proves the endpoint still works for
// a system administrator.
func TestGetAuditIntegrityAllowsSystemAdmin(t *testing.T) {
	db := &auditChainDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	rec := httptest.NewRecorder()
	h.GetAuditIntegrity(rec, requestWithRole(t, middleware.RoleSuperAdmin))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for a system admin, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if !db.queried {
		t.Error("expected the chain to be queried for a system admin")
	}
}
