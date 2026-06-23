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
	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/handlers"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/notify"
	"github.com/FisiFla/freecloud/backend/internal/reconcile"
	"github.com/FisiFla/freecloud/backend/internal/siem"
	"github.com/FisiFla/freecloud/backend/internal/snapshot"
)

func main() {
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

	// Run migrations
	if err := db.RunMigrations(ctx, pool); err != nil {
		logger.Fatal("failed to run migrations", zap.Error(err))
	}

	// Self-bootstrap Keycloak idempotently under a pg advisory lock so only one
	// replica runs provisioning at a time. Returns the active service-account secret.
	kcSecret, err := func() (string, error) {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return "", fmt.Errorf("acquire conn for bootstrap lock: %w", err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(7919876543)"); err != nil {
			return "", fmt.Errorf("acquire bootstrap advisory lock: %w", err)
		}
		defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(7919876543)") }()

		result, err := bootstrap.Run(ctx, bootstrap.Config{
			KeycloakURL:                  cfg.KeycloakURL,
			AdminUsername:                cfg.BootstrapAdminUser,
			AdminPassword:                cfg.BootstrapAdminPassword,
			TargetRealm:                  cfg.KeycloakRealm,
			ServiceAccountSecretOverride: cfg.KeycloakClientSecret,
			CreateDemoUser:               os.Getenv("CREATE_DEMO_USER") == "true",
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

	// Initialize FleetDM client
	fleetClient := fleet.NewClient(cfg.FleetURL, cfg.FleetAPIToken)

	// Lifecycle context — cancelled when the shutdown signal arrives.
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	defer lifecycleCancel()

	// D1 — Build event notifier from config.
	var notifiers []notify.Notifier
	if cfg.NotifyEmail && cfg.SMTPHost != "" {
		var to []string
		for _, addr := range strings.Split(cfg.SMTPTo, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				to = append(to, addr)
			}
		}
		notifiers = append(notifiers, notify.NewEmailNotifier(notify.EmailConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			From:     cfg.SMTPFrom,
			To:       to,
			Password: cfg.SMTPPassword,
		}))
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

	// Start the Keycloak↔DB reconciliation job (FCEXP-21).
	// RECONCILE_INTERVAL=0 disables it; the default is 15m.
	rec := reconcile.New(kcClient, pool, logger)
	if eventNotifier != nil {
		rec.SetNotifier(eventNotifier)
	}
	rec.Start(lifecycleCtx, cfg.ReconcileInterval)

	// D2 — Analytics snapshot job.
	snap := snapshot.New(pool, logger)
	snap.Start(lifecycleCtx, cfg.SnapshotInterval)

	// D3 — SIEM streamer.
	siemSink := siem.BuildSink(cfg.SIEMSyslogNet, cfg.SIEMSyslogAddr, cfg.SIEMHTTPUrl, cfg.SIEMHTTPToken, logger)
	siemStreamer := siem.New(pool, siemSink, logger)
	siemStreamer.Start(lifecycleCtx, cfg.SIEMInterval)

	// C2 (FCEX3-14) — Audit retention/pruning.
	auditPruner := audit.NewPruner(pool, logger)
	auditPruner.Start(lifecycleCtx, cfg.AuditPruneInterval, cfg.AuditRetainFor)

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
	handler.SetLDAPBindPassword(cfg.LDAPBindPassword)

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

	// Register routes (auth + actor middleware applied inside)
	handlers.SetupRoutes(r, handler, authMW.Middleware)

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
