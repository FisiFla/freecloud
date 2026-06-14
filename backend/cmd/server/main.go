package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/config"
	"github.com/FisiFla/freecloud/backend/internal/db"
	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/handlers"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func main() {
	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

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

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
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

	// Initialize Keycloak client
	kcClient := keycloak.NewClient(
		cfg.KeycloakURL,
		cfg.KeycloakClientID,
		cfg.KeycloakClientSecret,
		cfg.KeycloakRealm,
	)

	// Initialize FleetDM client
	fleetClient := fleet.NewClient(cfg.FleetURL, cfg.FleetAPIToken)

	// Create handler
	handler := handlers.NewHandler(pool, kcClient, fleetClient, logger)

	// Initialize JWT auth middleware
	authMW := middleware.NewAuthMiddleware(cfg.KeycloakURL, cfg.KeycloakRealm, cfg.KeycloakAudience)

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
	r.Use(chimiddleware.RealIP)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{corsOrigin},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("server forced to shutdown", zap.Error(err))
	}

	logger.Info("server stopped")
}
