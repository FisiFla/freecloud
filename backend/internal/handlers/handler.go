package handlers

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	db       *pgxpool.Pool
	keycloak *keycloak.KeycloakClient
	fleet    *fleet.FleetClient
	logger   *zap.Logger
}

// NewHandler creates a new Handler.
func NewHandler(db *pgxpool.Pool, kc *keycloak.KeycloakClient, fc *fleet.FleetClient, logger *zap.Logger) *Handler {
	return &Handler{
		db:       db,
		keycloak: kc,
		fleet:    fc,
		logger:   logger,
	}
}

// Health returns a simple health check response.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
