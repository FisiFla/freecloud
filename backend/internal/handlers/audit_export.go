package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// maxAuditExportRows caps the number of rows a single audit export streams.
// The export is streamed (never buffered), but an unbounded export would
// still hammer the database with a multi-million-row scan.
const maxAuditExportRows = 100000

// ExportAuditLogs streams audit log entries as CSV or JSON, honouring the same
// actor/action filters as ListAuditLogs.
//
// Route: GET /api/v1/audit-logs/export?format=csv|json&actor=...&action=...
// Permission-gated via PermExportAuditLogs in routes.go.
//
// The rows are STREAMED: the export used to collect every matching row into
// a slice before writing a single byte, so a multi-million-row audit table
// exhausted the process's memory. The number of exported rows is capped at
// maxAuditExportRows; when the cap is reached the response carries the
// X-FreeCloud-Export-Truncated header so the caller knows the export is
// partial.
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

	filterSQL := ` WHERE org_id = $1`
	args := []interface{}{oc.OrgID}
	argIdx := 2

	if actorFilter != "" {
		filterSQL += ` AND actor_id = $` + strconv.Itoa(argIdx)
		args = append(args, actorFilter)
		argIdx++
	}
	if actionFilter != "" {
		filterSQL += ` AND action = $` + strconv.Itoa(argIdx)
		args = append(args, actionFilter)
		argIdx++
	}
	if !fromTime.IsZero() {
		filterSQL += ` AND created_at >= $` + strconv.Itoa(argIdx)
		args = append(args, fromTime)
		argIdx++
	}
	if !toTime.IsZero() {
		filterSQL += ` AND created_at < $` + strconv.Itoa(argIdx)
		args = append(args, toTime)
		argIdx++
	}
	limitArg := argIdx
	args = append(args, maxAuditExportRows)

	query := `SELECT id, actor_id, action, COALESCE(target_type, ''), COALESCE(target_id, ''), details, created_at
		 FROM audit_logs` + filterSQL + ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(limitArg)

	// Detect whether the cap truncated the export, so the caller can tell
	// a complete export from a partial one. The check is a cheap
	// "is there a row beyond the cap" probe on the same filtered set.
	truncated := false
	var moreRows bool
	if err := h.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM audit_logs`+filterSQL+` OFFSET $`+strconv.Itoa(limitArg)+` LIMIT 1)`,
		args...,
	).Scan(&moreRows); err != nil {
		h.logger.Warn("failed to check whether the audit export was truncated", zap.Error(err))
	} else {
		truncated = moreRows
	}

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.logger.Error("failed to query audit logs for export", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("audit-log-%s.%s", ts, format)

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		if truncated {
			w.Header().Set("X-FreeCloud-Export-Truncated", "true")
		}
		w.WriteHeader(http.StatusOK)
		// Stream the array: one entry at a time, never the whole set in
		// memory.
		_, _ = w.Write([]byte("["))
		enc := json.NewEncoder(w)
		first := true
		for rows.Next() {
			entry, ok := h.scanAuditExportRow(rows)
			if !ok {
				continue
			}
			if !first {
				_, _ = w.Write([]byte(","))
			}
			first = false
			if err := enc.Encode(entry); err != nil {
				h.logger.Error("failed to write audit log entry to the export stream", zap.Error(err))
				return
			}
		}
		_, _ = w.Write([]byte("]"))

	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		if truncated {
			w.Header().Set("X-FreeCloud-Export-Truncated", "true")
		}
		w.WriteHeader(http.StatusOK)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "actor_id", "action", "target_type", "target_id", "details", "created_at"})
		for rows.Next() {
			entry, ok := h.scanAuditExportRow(rows)
			if !ok {
				continue
			}
			detailsStr := ""
			if entry.Details != nil {
				b, _ := json.Marshal(entry.Details)
				detailsStr = string(b)
			}
			_ = cw.Write([]string{
				entry.ID, entry.ActorID, entry.Action, entry.TargetType, entry.TargetID, detailsStr, entry.CreatedAt,
			})
		}
		cw.Flush()
	}
}

// scanAuditExportRow reads one audit row from the export cursor. It returns
// false when the row could not be scanned (logged, skipped).
func (h *Handler) scanAuditExportRow(rows pgx.Rows) (AuditLogEntry, bool) {
	var entry AuditLogEntry
	var detailsJSON []byte
	var createdAt time.Time
	if err := rows.Scan(&entry.ID, &entry.ActorID, &entry.Action,
		&entry.TargetType, &entry.TargetID, &detailsJSON, &createdAt); err != nil {
		h.logger.Error("failed to scan audit log for export", zap.Error(err))
		return entry, false
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
	return entry, true
}
