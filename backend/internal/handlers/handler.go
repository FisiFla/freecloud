package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/reconcile"
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

	// scimBearerMW is the SCIM bearer-token middleware. Set via SetSCIMBearerToken.
	// Defaults to a middleware that rejects all requests (fail closed).
	scimBearerMW func(http.Handler) http.Handler

	// accessEvalBearerMW authenticates POST /api/v1/access/evaluate requests.
	// Set via SetAccessEvalToken. Defaults to fail-closed (rejects all).
	accessEvalBearerMW func(http.Handler) http.Handler

	// reconciler is optional — nil when RECONCILE_INTERVAL=0 or not yet wired.
	reconciler *reconcile.Reconciler
}

// SetFleetWebhookSecret sets the shared secret used to verify Fleet enrollment
// callback signatures. Called once at startup from main.
func (h *Handler) SetFleetWebhookSecret(secret string) {
	h.fleetWebhookSecret = secret
}

// NewHandler creates a new Handler.
func NewHandler(db DBPool, kc keycloak.KeycloakClientInterface, fc fleet.FleetClientInterface, logger *zap.Logger) *Handler {
	h := &Handler{
		db:       db,
		keycloak: kc,
		fleet:    fc,
		logger:   logger,
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
