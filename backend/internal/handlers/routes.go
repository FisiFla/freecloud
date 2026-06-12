package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SetupRoutes registers all API routes on the provided chi router.
func SetupRoutes(r chi.Router, h *Handler) {
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		})

		r.Post("/onboard", h.Onboard)
		r.Post("/offboard/{userId}", h.Offboard)
		r.Post("/auth/device-check", h.DeviceCheck)
		r.Post("/apps/create", h.CreateApp)
		r.Post("/apps/{appId}/assign", h.AssignApp)
		r.Get("/apps", h.ListApps)
		r.Get("/audit-logs", h.ListAuditLogs)
	})
}
