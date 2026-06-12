//go:build ignore

package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FisiFla/freecloud/backend/internal/config"
	"github.com/FisiFla/freecloud/backend/internal/db"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}

	if err := db.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	log.Println("Migrations complete.")
}
