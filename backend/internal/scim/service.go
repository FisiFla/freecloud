package scim

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
)

// DBPool is the subset of *pgxpool.Pool the SCIM Users/Groups endpoints
// use. It matches the query surface of handlers.DBPool (minus Begin:
// the only transaction on this path is the onboarding write, which stays
// behind the Host seam), so handlers can hand its pool straight over.
type DBPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// OnboardRequest carries the fields CreateUser passes to the onboarding
// persistence step. It mirrors handlers.OnboardRequest field-for-field;
// the handlers-side Host adapter converts between the two.
type OnboardRequest struct {
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	Email      string `json:"email"`
	Department string `json:"department"`
	Role       string `json:"role"`
}

// Host is the seam between this package and the handlers package
// (docs/HANDLERS_OWNERSHIP.md step 3b). The Users/Groups endpoints live
// here, but three things deliberately stay on the handlers side:
//
//   - PersistOnboard: the onboarding saga write (user row + org
//     membership + audit entry + enrollment token, one transaction).
//     Moving the saga itself is a much larger blast radius than this
//     extraction, so SCIM depends on it only through this method.
//   - WriteAuditEntryBestEffort: audit attribution resolves the org
//     from the request context on the handlers side.
//   - The input validators, shared with every other handlers surface.
//
// handlers supplies an implementation at wiring time (see
// handlers/scim.go); this package never imports handlers, so there is
// no cycle and the saga can move later without touching SCIM again.
type Host interface {
	// PersistOnboard persists a freshly created Keycloak user exactly
	// like the interactive onboard flow does (handlers.persistOnboard).
	PersistOnboard(ctx context.Context, kcUserID string, req OnboardRequest, actorID string, auditDetails any, enrollmentToken string, orgID string) error
	// WriteAuditEntryBestEffort records an audit entry without ever
	// failing the caller's core action.
	WriteAuditEntryBestEffort(ctx context.Context, actorID, action, targetType, targetID string, details any) error
	// ValidateUserID rejects empty, overlong, path-like, or non-UUID
	// user path ids (handlers.ValidateUserID).
	ValidateUserID(id string) error
	// ValidateOpaqueID rejects empty, overlong, path-like, or
	// control-bearing opaque ids (handlers.ValidateOpaqueID).
	ValidateOpaqueID(id, field string) error
	// IsValidEmail reports whether s is a plausibly-valid email address.
	IsValidEmail(s string) bool
}

// Service serves the SCIM 2.0 Users/Groups endpoints (/scim/v2/Users,
// /scim/v2/Groups, plus org-scoped variants). It is stateless beyond
// its injected dependencies, so handlers constructs it on demand from
// the same db/keycloak/logger it already holds.
type Service struct {
	db       DBPool
	keycloak keycloak.KeycloakClientInterface
	logger   *zap.Logger
	host     Host
}

// NewService builds a Service. host must be non-nil: CreateUser and the
// id validators go through it. db may be nil, in which case the
// endpoints answer 503 exactly as the handlers versions did.
func NewService(db DBPool, kc keycloak.KeycloakClientInterface, logger *zap.Logger, host Host) *Service {
	return &Service{db: db, keycloak: kc, logger: logger, host: host}
}

// isNotFound checks if a pgx error is a not-found (ErrNoRows) condition.
//
// The check uses errors.Is against the sentinel error rather than matching
// on the error text: a substring match on "no rows" breaks on driver or
// locale changes and can misclassify an unrelated error as "not found".
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, pgx.ErrNoRows)
}
