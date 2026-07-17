package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/notify"
	"github.com/FisiFla/freecloud/backend/internal/provisioning"
	"github.com/FisiFla/freecloud/backend/internal/reconcile"
	"github.com/FisiFla/freecloud/backend/internal/snapshot"
)

// DBPool is the subset of *pgxpool.Pool the handlers use. Depending on an
// interface (rather than the concrete pool) lets unit tests inject a fake
// database to exercise the persistence and rollback paths. *pgxpool.Pool
// satisfies it.
type DBPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	db       DBPool
	keycloak keycloak.KeycloakClientInterface
	fleet    fleet.FleetClientInterface
	logger   *zap.Logger

	// fleetWebhookSecret authenticates Fleet enrollment callbacks (HMAC-SHA256).
	// Empty means the callback rejects everything (fail closed).
	fleetWebhookSecret string

	// deviceCookieSecret HMAC-signs freecloud-device-id cookies when set.
	// Empty falls back to fleetWebhookSecret (see deviceCookieSigningSecret).
	deviceCookieSecret string

	// scimBearerMW is the SCIM bearer-token middleware. Set via SetSCIMBearerToken.
	// Defaults to a middleware that rejects all requests (fail closed).
	scimBearerMW func(http.Handler) http.Handler

	// accessEvalBearerMW authenticates POST /api/v1/access/evaluate requests.
	// Set via SetAccessEvalToken. Defaults to fail-closed (rejects all).
	accessEvalBearerMW func(http.Handler) http.Handler

	// reconciler is optional — nil when RECONCILE_INTERVAL=0 or not yet wired.
	reconciler *reconcile.Reconciler

	// notifier fires event notifications (D1). Nil means notifications are disabled.
	notifier notify.Notifier

	// snapshotter serves the analytics series endpoint (D2). Nil means disabled.
	snapshotter *snapshot.Snapshotter

	// provisionEngine drives outbound SCIM/Slack/GitHub provisioning (A1).
	// Nil when no provisioning-enabled apps are configured.
	provisionEngine *provisioning.Engine
	// ldapBindPassword is the bind password for LDAP/AD federation sources (C1).
	ldapBindPassword string

	// auditRetainFor is the configured audit retention window (from AUDIT_RETAIN_FOR).
	// 0 means keep forever. Exposed via GET /api/v1/audit-logs/integrity.
	auditRetainFor time.Duration
	// setupMu is the fallback, in-process-only guard acquireSetupLock uses
	// when pgPool is nil (unit tests with a fake DBPool). It provides no
	// additional safety in production — see acquireSetupLock in setup.go.
	setupMu sync.Mutex
	// pgPool, when non-nil, is the same underlying database as db but
	// retained as the concrete *pgxpool.Pool so Setup can take a
	// cross-replica pg advisory lock (H3) via db.AcquireAdvisoryLock, which
	// needs a dedicated connection (pgxpool.Pool.Acquire) rather than the
	// shared DBPool query interface. Populated automatically in NewHandler
	// when the caller's db happens to be a real *pgxpool.Pool — true for
	// every production instance (see main.go) — and stays nil for the fakes
	// unit tests inject.
	pgPool *pgxpool.Pool
	// geoIP resolves client IPs to ISO country codes for geo-allowlist conditions (D1).
	// Defaults to noopGeoIP{} which always returns "" (unknown) — fail-closed for geo conditions.
	geoIP GeoIPLookup
}

// SetFleetWebhookSecret sets the shared secret used to verify Fleet enrollment
// callback signatures. Called once at startup from main.
func (h *Handler) SetFleetWebhookSecret(secret string) {
	h.fleetWebhookSecret = secret
}

// SetDeviceCookieSecret sets the optional dedicated HMAC key for device-identity
// cookies. When empty, mint/verify fall back to the Fleet webhook secret.
func (h *Handler) SetDeviceCookieSecret(secret string) {
	h.deviceCookieSecret = secret
}

// NewHandler creates a new Handler.
func NewHandler(db DBPool, kc keycloak.KeycloakClientInterface, fc fleet.FleetClientInterface, logger *zap.Logger) *Handler {
	h := &Handler{
		db:       db,
		keycloak: kc,
		fleet:    fc,
		logger:   logger,
		geoIP:    noopGeoIP{},
	}
	// H3: capture the real pool (if that's what was passed) so Setup can take
	// a distributed advisory lock. See the pgPool field doc above.
	if p, ok := db.(*pgxpool.Pool); ok {
		h.pgPool = p
	}
	// Default SCIM middleware: fail closed — rejects all requests until a token
	// is configured via SetSCIMBearerToken.
	h.scimBearerMW = SCIMBearerMiddleware("")
	// Default access-eval middleware: fail closed until SetAccessEvalToken is called.
	h.accessEvalBearerMW = accessEvalBearerMiddleware("")
	return h
}

// SetSCIMBearerToken configures the SCIM bearer-token middleware. Must be
// called at startup before the server starts accepting requests.
func (h *Handler) SetSCIMBearerToken(token string) {
	h.scimBearerMW = SCIMBearerMiddleware(token)
}

// SetAccessEvalToken configures the access-evaluation bearer-token middleware.
// Must be called at startup before the server starts accepting requests.
func (h *Handler) SetAccessEvalToken(token string) {
	h.accessEvalBearerMW = accessEvalBearerMiddleware(token)
}

// SetReconciler wires the reconciliation job into the handler so it can
// serve the drift report endpoint (D1).
func (h *Handler) SetReconciler(r *reconcile.Reconciler) {
	h.reconciler = r
}

// SetNotifier wires the event notifier (D1 / FCEX2-17).
func (h *Handler) SetNotifier(n notify.Notifier) {
	h.notifier = n
}

// SetSnapshotter wires the analytics snapshotter (D2 / FCEX2-18).
func (h *Handler) SetSnapshotter(s *snapshot.Snapshotter) {
	h.snapshotter = s
}

// SetProvisionEngine wires the outbound provisioning engine (A1 / v1.4).
func (h *Handler) SetProvisionEngine(e *provisioning.Engine) {
	h.provisionEngine = e
}

// SetLDAPBindPassword wires the LDAP bind password (resolved via config.LDAPBindPassword).
func (h *Handler) SetLDAPBindPassword(pw string) { h.ldapBindPassword = pw }

// SetAuditRetainFor wires the configured audit retention window.
func (h *Handler) SetAuditRetainFor(d time.Duration) { h.auditRetainFor = d }

// SetGeoIPLookup wires a live GeoIP resolver for geo-allowlist conditions (D1).
// Without this, geo conditions always fail closed (country unknown → deny).
func (h *Handler) SetGeoIPLookup(g GeoIPLookup) { h.geoIP = g }

// Health returns a simple health check response.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Healthz is a liveness probe: 200 as long as the process is serving.
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readyz is a readiness probe: 200 only when the database and Keycloak are
// reachable, so an orchestrator stops routing to an instance that can't serve
// real traffic. Returns 503 with per-dependency status otherwise.
func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]string{}
	ready := true

	if h.db == nil {
		checks["database"] = "not configured"
		ready = false
	} else if err := h.db.QueryRow(ctx, "SELECT 1").Scan(new(int)); err != nil {
		checks["database"] = "unreachable"
		ready = false
	} else {
		checks["database"] = "ok"
	}

	if err := h.keycloak.Ping(ctx); err != nil {
		checks["keycloak"] = "unreachable"
		ready = false
	} else {
		checks["keycloak"] = "ok"
	}

	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	respondJSON(w, status, checks)
}

// HealthKeycloak checks connectivity to the Keycloak server.
func (h *Handler) HealthKeycloak(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := h.keycloak.Ping(ctx)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unreachable"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HealthFleet checks connectivity to the FleetDM server.
func (h *Handler) HealthFleet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := h.fleet.Ping(ctx)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unreachable"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
