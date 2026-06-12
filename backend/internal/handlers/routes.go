package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// SetupRoutes registers all API routes on the provided chi router.
// authMW is applied to all /api/v1 routes except /health.
func SetupRoutes(r chi.Router, h *Handler, authMW func(http.Handler) http.Handler) {
	r.Get("/api/v1/health", h.Health)

	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.ActorIDMiddleware)
		r.Post("/api/v1/onboard", h.Onboard)
		r.Post("/api/v1/offboard/{userId}", h.Offboard)
		r.Post("/api/v1/auth/device-check", h.DeviceCheck)
		r.Post("/api/v1/apps/create", h.CreateApp)
		r.Post("/api/v1/apps/{appId}/assign", h.AssignApp)
		r.Get("/api/v1/apps", h.ListApps)
		r.Get("/api/v1/audit-logs", h.ListAuditLogs)
		r.Get("/api/v1/users", h.ListUsers)
		r.Get("/api/v1/users/{id}", h.GetUser)
	})
}
