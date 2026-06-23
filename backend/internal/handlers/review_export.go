package handlers

// E3 — Campaign export.
// GET /api/v1/campaigns/{id}/export?format=csv|json
// Downloads all review items for a campaign as CSV or JSON.

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// exportItem is a flattened row used for export.
type exportItem struct {
	ItemID       string `json:"itemId"`
	CampaignID   string `json:"campaignId"`
	UserEmail    string `json:"userEmail"`
	UserID       string `json:"userId"`
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	ResourceName string `json:"resourceName"`
	Decision     string `json:"decision"`
	DecidedBy    string `json:"decidedBy"`
	DecidedAt    string `json:"decidedAt"`
	CreatedAt    string `json:"createdAt"`
}

// ExportCampaign exports all review items for a campaign as CSV or JSON.
// Route: GET /api/v1/campaigns/{id}/export?format=csv|json
func (h *Handler) ExportCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID := chi.URLParam(r, "id")
	if !isValidUUID(campaignID) {
		respondError(w, http.StatusBadRequest, "invalid campaign id")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "csv" && format != "json" {
		respondError(w, http.StatusBadRequest, "format must be csv or json")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT ri.id::TEXT, ri.campaign_id::TEXT,
		        COALESCE(u.email, ''), ri.user_id::TEXT,
		        ri.resource_type, ri.resource_id, ri.resource_name,
		        COALESCE(ri.decision, ''), COALESCE(ri.decided_by, ''), ri.decided_at, ri.created_at
		 FROM review_items ri
		 LEFT JOIN users u ON u.keycloak_user_id = ri.user_id
		 WHERE ri.campaign_id = $1
		 ORDER BY ri.created_at`,
		campaignID,
	)
	if err != nil {
		h.logger.Error("export campaign: query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var items []exportItem
	for rows.Next() {
		var item exportItem
		var decidedAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(
			&item.ItemID, &item.CampaignID,
			&item.UserEmail, &item.UserID,
			&item.ResourceType, &item.ResourceID, &item.ResourceName,
			&item.Decision, &item.DecidedBy, &decidedAt, &createdAt,
		); err != nil {
			h.logger.Warn("export campaign: scan failed", zap.Error(err))
			continue
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		if decidedAt != nil {
			item.DecidedAt = decidedAt.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("export campaign: iterate failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="campaign-%s.csv"`, campaignID))
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"itemId", "campaignId", "userEmail", "userId", "resourceType", "resourceId", "resourceName", "decision", "decidedBy", "decidedAt", "createdAt"})
		for _, item := range items {
			_ = cw.Write([]string{
				item.ItemID, item.CampaignID, item.UserEmail, item.UserID,
				item.ResourceType, item.ResourceID, item.ResourceName,
				item.Decision, item.DecidedBy, item.DecidedAt, item.CreatedAt,
			})
		}
		cw.Flush()
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="campaign-%s.json"`, campaignID))
		if items == nil {
			items = []exportItem{}
		}
		_ = json.NewEncoder(w).Encode(items)
	}
}
