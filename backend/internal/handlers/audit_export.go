package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ExportAuditLogs streams audit log entries as CSV or JSON, honouring the same
// actor/action filters as ListAuditLogs but without pagination limits (it
// exports everything matching the filter).
//
// Route: GET /api/v1/audit-logs/export?format=csv|json&actor=...&action=...
// Permission-gated via PermExportAuditLogs in routes.go.
func (h *Handler) ExportAuditLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		respondError(w, http.StatusBadRequest, "format must be 'csv' or 'json'")
		return
	}

	actorFilter := r.URL.Query().Get("actor")
	actionFilter := r.URL.Query().Get("action")

	var fromTime, toTime time.Time
	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			fromTime = t
		} else {
			respondError(w, http.StatusBadRequest, "invalid 'from' param: use RFC3339 format")
			return
		}
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			toTime = t
		} else {
			respondError(w, http.StatusBadRequest, "invalid 'to' param: use RFC3339 format")
			return
		}
	}

	// C5: org-scoped export — see ListAuditLogs for the fail-closed rationale.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	query := `SELECT id, actor_id, action, COALESCE(target_type, ''), COALESCE(target_id, ''), details, created_at
		 FROM audit_logs WHERE org_id = $1`
	args := []interface{}{oc.OrgID}
	argIdx := 2

	if actorFilter != "" {
		query += ` AND actor_id = $` + strconv.Itoa(argIdx)
		args = append(args, actorFilter)
		argIdx++
	}
	if actionFilter != "" {
		query += ` AND action = $` + strconv.Itoa(argIdx)
		args = append(args, actionFilter)
		argIdx++
	}
	if !fromTime.IsZero() {
		query += ` AND created_at >= $` + strconv.Itoa(argIdx)
		args = append(args, fromTime)
		argIdx++
	}
	if !toTime.IsZero() {
		query += ` AND created_at < $` + strconv.Itoa(argIdx)
		args = append(args, toTime)
		argIdx++
	}
	query += ` ORDER BY created_at DESC`
	// No LIMIT — export everything matching the filter.

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.logger.Error("failed to query audit logs for export", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	// Collect all entries before streaming (so we can fail before writing headers).
	var entries []AuditLogEntry
	for rows.Next() {
		var entry AuditLogEntry
		var detailsJSON []byte
		var createdAt time.Time
		if err := rows.Scan(&entry.ID, &entry.ActorID, &entry.Action,
			&entry.TargetType, &entry.TargetID, &detailsJSON, &createdAt); err != nil {
			h.logger.Error("failed to scan audit log for export", zap.Error(err))
			continue
		}
		if len(detailsJSON) > 0 {
			if err := json.Unmarshal(detailsJSON, &entry.Details); err != nil {
				entry.Details = map[string]interface{}{}
			}
		}
		if entry.Details == nil {
			entry.Details = map[string]interface{}{}
		}
		entry.CreatedAt = createdAt.Format(time.RFC3339)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating audit logs for export", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("audit-log-%s.%s", ts, format)

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(entries)

	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "actor_id", "action", "target_type", "target_id", "details", "created_at"})
		for _, e := range entries {
			detailsStr := ""
			if e.Details != nil {
				b, _ := json.Marshal(e.Details)
				detailsStr = string(b)
			}
			_ = cw.Write([]string{
				e.ID, e.ActorID, e.Action, e.TargetType, e.TargetID, detailsStr, e.CreatedAt,
			})
		}
		cw.Flush()
	}
}
