package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"strings"

	"github.com/FisiFla/freecloud/backend/internal/audit"
	"github.com/FisiFla/freecloud/backend/internal/bootstrap"
	"github.com/FisiFla/freecloud/backend/internal/config"
	"github.com/FisiFla/freecloud/backend/internal/db"
	"github.com/FisiFla/freecloud/backend/internal/geoip"
	"github.com/FisiFla/freecloud/backend/internal/handlers"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/leader"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/notify"
	"github.com/FisiFla/freecloud/backend/internal/provisioning"
	"github.com/FisiFla/freecloud/backend/internal/reconcile"
	"github.com/FisiFla/freecloud/backend/internal/siem"
	"github.com/FisiFla/freecloud/backend/internal/snapshot"
)

// main dispatches to the migrate subcommand ("server migrate") or the normal
// HTTP server (bare "server"). See runMigrate / runServer (B2, v1.7 HA):
// migrations are decoupled from server startup so a rolling multi-replica
// deploy can run exactly one migrate job before any replica serves traffic,
// instead of every replica racing to migrate on boot.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate()
		return
	}
	runServer()
}

// runMigrate connects to the database, applies any pending migrations under
// the schema.RunMigrations advisory lock, and exits. Intended to be run as a
// one-shot job (e.g. docker compose `migrate` service with
// `depends_on: service_completed_successfully`) before the server starts.
func runMigrate() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer func() { _ = logger.Sync() }()
	zap.ReplaceGlobals(logger)

	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("migrate: failed to create database pool", zap.Error(err))
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Fatal("migrate: failed to ping database", zap.Error(err))
	}

	if err := db.RunMigrations(ctx, pool); err != nil {
		logger.Fatal("migrate: failed to run migrations", zap.Error(err))
	}

	logger.Info("migrate: complete", zap.Int("schema_version", db.LatestMigrationID()))
}

func runServer() {
	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer func() { _ = logger.Sync() }()

	// Replace the global logger
	zap.ReplaceGlobals(logger)

	// Load configuration
	cfg := config.Load()

	// Validate configuration for non-development environments
	if err := cfg.Validate(); err != nil {
		logger.Fatal("configuration validation failed", zap.Error(err))
	}

	logger.Info("starting freecloud backend",
		zap.String("port", cfg.Port),
		zap.String("database_url", config.RedactDSN(cfg.DatabaseURL)),
	)

	// Connect to PostgreSQL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("failed to parse database URL", zap.Error(err))
	}
	// Bound every query server-side so a slow or stuck query can't hold a pooled
	// connection indefinitely (otherwise the HTTP write timeout drops the client
	// while the query keeps running and leaks the connection).
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		logger.Fatal("failed to create database pool", zap.Error(err))
	}
	defer pool.Close()

	// Verify database connection
	if err := pool.Ping(ctx); err != nil {
		logger.Fatal("failed to ping database", zap.Error(err))
	}
	logger.Info("database connection established")

	// B2 (v1.7 HA): migrations are no longer run here. Verify the schema is
	// current instead — if a `migrate` job hasn't completed yet, wait for it
	// (WAIT_FOR_SCHEMA_TIMEOUT, useful when compose starts the server and the
	// migrate job concurrently) or fail with a clear operator message.
	if err := db.WaitForSchema(ctx, pool, cfg.WaitForSchemaTimeout); err != nil {
		logger.Fatal("schema check failed", zap.Error(err))
	}
	logger.Info("database schema is current", zap.Int("schema_version", db.LatestMigrationID()))

	// Self-bootstrap Keycloak idempotently under a pg advisory lock so only one
	// replica runs provisioning at a time. Returns the active service-account secret.
	kcSecret, err := func() (string, error) {
		// Acquiring the lock can legitimately take as long as another
		// replica's bootstrap run does (Keycloak cold start + full realm
		// provisioning, up to the 4-minute budget below) — use a dedicated,
		// generous timeout here instead of the 10s DB-connect ctx above, which
		// would otherwise time out a waiting replica while the leader is still
		// mid-bootstrap.
		//
		// The wait is done via db.AcquireAdvisoryLock's try-lock poll, NOT a
		// blocking `pg_advisory_lock`: the backend pool sets a server-side
		// statement_timeout (15s, see poolCfg above), and a blocking advisory
		// lock is — from Postgres's perspective — one long-running statement,
		// so the statement_timeout cancels the wait ("canceling statement due
		// to statement timeout") regardless of the client context's deadline.
		// A poll loop's individual statements are always fast, so only the
		// context/timeout below governs how long we wait.
		lockCtx, lockCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer lockCancel()

		conn, err := pool.Acquire(lockCtx)
		if err != nil {
			return "", fmt.Errorf("acquire conn for bootstrap lock: %w", err)
		}
		defer conn.Release()
		if err := db.AcquireAdvisoryLock(lockCtx, conn, 7919876543, 5*time.Minute, func() {
			logger.Info("bootstrap: waiting for another replica to finish bootstrapping")
		}); err != nil {
			return "", fmt.Errorf("acquire bootstrap advisory lock: %w", err)
		}
		defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(7919876543)") }()

		// Keycloak bootstrap can take minutes on a cold start (Keycloak boot +
		// realm provisioning). bootstrap.Run is idempotent (safe to call on
		// every startup — see its doc comment), so a replica that waited out
		// another replica's bootstrap under the lock above simply re-verifies
		// everything is already in place instead of skipping the call.
		bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer bootstrapCancel()
		// E2E_SEED_ADMIN seeds a known admin user + enables direct-access-grant on
		// the dashboard client so the e2e harness can mint a real admin JWT
		// directly from Keycloak's token endpoint. FAIL-CLOSED: only honoured
		// when APP_ENV is development/test (config.IsDevOrE2E) — the flag itself
		// is ignored in any other environment, so a stray env var can never
		// enable this in production.
		seedE2EAdmin := config.IsDevOrE2E() && os.Getenv("E2E_SEED_ADMIN") == "true"
		result, err := bootstrap.Run(bootstrapCtx, bootstrap.Config{
			KeycloakURL:                  cfg.KeycloakURL,
			AdminUsername:                cfg.BootstrapAdminUser,
			AdminPassword:                cfg.BootstrapAdminPassword,
			TargetRealm:                  cfg.KeycloakRealm,
			ServiceAccountSecretOverride: cfg.KeycloakClientSecret,
			CreateDemoUser:               os.Getenv("CREATE_DEMO_USER") == "true",
			SeedE2EAdmin:                 seedE2EAdmin,
			E2EAdminUsername:             os.Getenv("E2E_ADMIN_USERNAME"),
			E2EAdminPassword:             os.Getenv("E2E_ADMIN_PASSWORD"),
		})
		if err != nil {
			return "", err
		}
		return result.ServiceAccountSecret, nil
	}()
	if err != nil {
		logger.Fatal("keycloak bootstrap failed", zap.Error(err))
	}
	logger.Info("keycloak bootstrap complete")

	// Initialize Keycloak client
	kcClient := keycloak.NewClient(
		cfg.KeycloakURL,
		cfg.KeycloakClientID,
		kcSecret,
		cfg.KeycloakRealm,
	)

	// Initialize FleetDM client facade. It reads saved UI settings at runtime
	// and falls back to env config during bootstrap.
	fleetClient := handlers.NewDynamicFleetClient(pool, cfg.FleetURL, cfg.FleetAPIToken, logger)

	// Lifecycle context — cancelled when the shutdown signal arrives.
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	defer lifecycleCancel()

	// D1 — Build event notifier from config.
	var notifiers []notify.Notifier
	if cfg.NotifyEmail {
		var to []string
		for _, addr := range strings.Split(cfg.SMTPTo, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				to = append(to, addr)
			}
		}
		notifiers = append(notifiers, handlers.NewDynamicEmailNotifier(pool, notify.EmailConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPFrom,
			From:     cfg.SMTPFrom,
			To:       to,
			Password: cfg.SMTPPassword,
		}, logger))
		logger.Info("event notifier: email channel enabled")
	}
	if cfg.NotifySlack && cfg.SlackWebhookURL != "" {
		notifiers = append(notifiers, notify.NewSlackNotifier(cfg.SlackWebhookURL))
		logger.Info("event notifier: slack channel enabled")
	}
	if cfg.NotifyWebhook && cfg.WebhookURL != "" {
		notifiers = append(notifiers, notify.NewWebhookNotifier(cfg.WebhookURL, cfg.WebhookSecret))
		logger.Info("event notifier: webhook channel enabled")
	}
	toggles := notify.EventToggles{
		Offboard:   cfg.NotifyEventOffboard,
		Drift:      cfg.NotifyEventDrift,
		Compliance: cfg.NotifyEventCompliance,
	}
	var eventNotifier notify.Notifier
	if len(notifiers) > 0 {
		eventNotifier = notify.NewMultiNotifier(toggles, logger, notifiers...)
	}

	// B3 (v1.7 HA): leader election for background jobs that must run on
	// exactly one instance. Each job gets its own advisory lock id (distinct
	// from the bootstrap lock 7919876543 and the migration lock 8241093571 in
	// internal/db/schema.go) and its own Elector, so one job's instance
	// failing over doesn't affect another job's leadership.
	reconcileLeader := leader.New(leader.PoolAdapter{Pool: pool}, "reconcile", 8241093601, logger)
	snapshotLeader := leader.New(leader.PoolAdapter{Pool: pool}, "snapshot", 8241093602, logger)
	auditRetentionLeader := leader.New(leader.PoolAdapter{Pool: pool}, "audit_retention", 8241093603, logger)
	reconcileLeader.Start(lifecycleCtx)
	snapshotLeader.Start(lifecycleCtx)
	auditRetentionLeader.Start(lifecycleCtx)

	// Start the Keycloak↔DB reconciliation job (FCEXP-21).
	// RECONCILE_INTERVAL=0 disables it; the default is 15m.
	rec := reconcile.New(kcClient, pool, logger)
	if eventNotifier != nil {
		rec.SetNotifier(eventNotifier)
	}
	rec.SetLeaderGate(reconcileLeader.IsLeader)
	rec.Start(lifecycleCtx, cfg.ReconcileInterval)

	// D2 — Analytics snapshot job.
	snap := snapshot.New(pool, logger)
	snap.SetLeaderGate(snapshotLeader.IsLeader)
	snap.Start(lifecycleCtx, cfg.SnapshotInterval)

	// D3 — SIEM streamer.
	siemSink := siem.BuildSink(cfg.SIEMSyslogNet, cfg.SIEMSyslogAddr, cfg.SIEMHTTPUrl, cfg.SIEMHTTPToken, logger)
	siemStreamer := siem.New(pool, siemSink, logger)
	siemStreamer.Start(lifecycleCtx, cfg.SIEMInterval)

	// C2 (FCEX3-14) — Audit retention/pruning.
	auditPruner := audit.NewPruner(pool, logger)
	auditPruner.SetLeaderGate(auditRetentionLeader.IsLeader)
	auditPruner.Start(lifecycleCtx, cfg.AuditPruneInterval, cfg.AuditRetainFor)

	provisionEngine := provisioning.NewEngine(pool, logger)
	provisionCtx, provisionCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := handlers.ReloadProvisioningConnectors(provisionCtx, provisionEngine, pool, logger); err != nil {
		logger.Warn("provisioning connectors: initial load failed", zap.Error(err))
	}
	provisionCancel()

	// Create handler
	handler := handlers.NewHandler(pool, kcClient, fleetClient, logger)
	handler.SetFleetWebhookSecret(cfg.FleetWebhookSecret)
	handler.SetSCIMBearerToken(cfg.SCIMBearerToken)
	handler.SetAccessEvalToken(cfg.AccessEvalToken)
	handler.SetReconciler(rec)
	if eventNotifier != nil {
		handler.SetNotifier(eventNotifier)
	}
	handler.SetSnapshotter(snap)
	handler.SetProvisionEngine(provisionEngine)
	handler.SetLDAPBindPassword(cfg.LDAPBindPassword)

	// A2 — Live GeoIP (optional). GEOIP_MMDB_PATH unset keeps the handlers
	// package's no-op default (fails closed: unknown country whenever a
	// policy sets a geo allowlist). When set, a bad/corrupt/unreadable mmdb
	// must refuse to start rather than silently keep every geo-gated login
	// denied — Open() itself is fail-closed, so any error here is fatal.
	if cfg.GeoIPMMDBPath != "" {
		geoResolver, err := geoip.Open(cfg.GeoIPMMDBPath)
		if err != nil {
			logger.Fatal("geoip: failed to load GEOIP_MMDB_PATH", zap.Error(err))
		}
		defer geoResolver.Close()
		handler.SetGeoIPLookup(geoResolver)
		logger.Info("geoip: live GeoIP resolver loaded", zap.String("path", cfg.GeoIPMMDBPath))
	}

	// Initialize JWT auth middleware, wrapping it with API token support (C2).
	baseAuth := middleware.NewAuthMiddleware(cfg.KeycloakURL, cfg.KeycloakRealm, cfg.KeycloakAudience)
	authMW := middleware.NewAPITokenMiddleware(baseAuth, pool)

	// CORS origin from env or secure default
	corsOrigin := os.Getenv("CORS_ORIGIN")
	if corsOrigin == "" {
		corsOrigin = "http://localhost:3000"
	}

	// Setup router
	r := chi.NewRouter()

	// Global middleware
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.Metrics)
	// Baseline security response headers on every response. The frontend sets a
	// richer set (including a Content-Security-Policy) in next.config.js; this
	// covers the JSON API surface.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			if rid := chimiddleware.GetReqID(req.Context()); rid != "" {
				h.Set("X-Request-Id", rid)
			}
			next.ServeHTTP(w, req)
		})
	})
	// NOTE: chi RealIP is intentionally NOT installed. It rewrites RemoteAddr
	// from X-Forwarded-For/X-Real-IP headers, which are client-spoofable and
	// would let attackers bypass the rate limiter by rotating the header.
	// If a trusted reverse proxy is added in front, install RealIP with an
	// explicit trusted-proxy allowlist at that point.
	// Bound request bodies (1 MiB) to prevent memory exhaustion via large POSTs.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			next.ServeHTTP(w, r)
		})
	})
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{corsOrigin},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Prometheus metrics scrape target. Unauthenticated by design; restrict it
	// at the reverse proxy / network layer if the API is publicly exposed.
	r.Handle("/metrics", promhttp.Handler())

	// B1 (v1.7 HA): rate limiter factory — Redis-backed (shared across
	// replicas) when REDIS_URL is set, in-memory otherwise. config.Validate()
	// already refused to start in production without REDIS_URL, so this only
	// falls back to in-memory in dev/test.
	newLimiter, closeLimiterFactory, err := middleware.NewLimiterFactory(cfg.RedisURL, logger)
	if err != nil {
		logger.Fatal("failed to initialize rate limiter", zap.Error(err))
	}
	defer closeLimiterFactory()

	// Register routes (auth + actor middleware applied inside)
	handlers.SetupRoutes(r, handler, authMW.Middleware, newLimiter)

	// Create HTTP server
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		logger.Info("server starting", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")
	// Stop background jobs (reconciler, etc.) before draining HTTP.
	lifecycleCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("server forced to shutdown", zap.Error(err))
	}

	logger.Info("server stopped")
}
