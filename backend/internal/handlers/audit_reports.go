package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/audit"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ReportType enumerates the on-demand reports available via the reports endpoint.
// "compliance" — org compliance posture snapshot.
// "access-review" — access review campaign status.
const (
	reportTypeCompliance   = "compliance"
	reportTypeAccessReview = "access-review"
)

// complianceReportRow is one row in the compliance report.
type complianceReportRow struct {
	DeviceID        string `json:"deviceId"`
	Hostname        string `json:"hostname"`
	OSVersion       string `json:"osVersion"`
	DiskEncrypted   bool   `json:"diskEncrypted"`
	FirewallEnabled bool   `json:"firewallEnabled"`
	MDMEnrolled     bool   `json:"mdmEnrolled"`
	VulnCount       int    `json:"vulnCount"`
	Compliant       bool   `json:"compliant"`
	NeedsUpdate     bool   `json:"needsUpdate"`
}

// accessReviewReportRow is one row in the access review report.
type accessReviewReportRow struct {
	CampaignID   string `json:"campaignId"`
	CampaignName string `json:"campaignName"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt"`
	ClosedAt     string `json:"closedAt,omitempty"`
	TotalItems   int    `json:"totalItems"`
	Confirmed    int    `json:"confirmed"`
	Revoked      int    `json:"revoked"`
	Pending      int    `json:"pending"`
}

// DownloadReport generates an on-demand compliance or access-review report.
//
// Route: GET /api/v1/reports?type=compliance|access-review&format=csv|json
// &from=<RFC3339>&to=<RFC3339>
// Permission-gated via PermExportAuditLogs in routes.go.
func (h *Handler) DownloadReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actorID := middleware.GetActorID(ctx)

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	reportType := r.URL.Query().Get("type")
	if reportType == "" {
		respondError(w, http.StatusBadRequest, "type is required: compliance or access-review")
		return
	}
	if reportType != reportTypeCompliance && reportType != reportTypeAccessReview {
		respondError(w, http.StatusBadRequest, "type must be 'compliance' or 'access-review'")
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

	var fromTime, toTime time.Time
	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid 'from' param: use RFC3339 format")
			return
		}
		fromTime = t
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid 'to' param: use RFC3339 format")
			return
		}
		toTime = t
	}

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("report-%s-%s.%s", reportType, ts, format)

	switch reportType {
	case reportTypeCompliance:
		h.downloadComplianceReport(w, r, format, filename, fromTime, toTime, actorID)
	case reportTypeAccessReview:
		h.downloadAccessReviewReport(w, r, format, filename, fromTime, toTime, actorID)
	}
}

func (h *Handler) downloadComplianceReport(
	w http.ResponseWriter, r *http.Request,
	format, filename string,
	fromTime, toTime time.Time, actorID string,
) {
	ctx := r.Context()

	// Reuse existing device query — compliance posture is computed in memory via Fleet.
	rows, err := h.db.Query(ctx,
		`SELECT d.fleet_host_id, COALESCE(d.hostname, ''), COALESCE(d.os_version, '')
		 FROM devices d ORDER BY d.hostname`,
	)
	if err != nil {
		h.logger.Error("compliance report: failed to query devices", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	deviceList, scanErr := scanComplianceDevices(h, rows)
	if scanErr != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	postures, _ := h.buildCompliancePostures(ctx, deviceList)

	// Build report rows.
	var report []complianceReportRow
	for _, p := range postures {
		report = append(report, complianceReportRow{
			DeviceID:        p.DeviceID,
			Hostname:        p.Hostname,
			OSVersion:       p.OsVersion,
			DiskEncrypted:   p.DiskEncrypted,
			FirewallEnabled: p.FirewallEnabled,
			MDMEnrolled:     p.MDMEnrolled,
			VulnCount:       len(p.Vulnerabilities),
			Compliant:       p.Compliant,
			NeedsUpdate:     p.NeedsUpdate,
		})
	}

	if err := h.writeAuditEntryBestEffort(actorID, "report.download", "report", reportTypeCompliance,
		map[string]interface{}{"format": format, "rows": len(report)}); err != nil {
		h.logger.Warn("failed to write report download audit entry", zap.Error(err))
	}

	streamReport(w, format, filename, report,
		[]string{"device_id", "hostname", "os_version", "disk_encrypted", "firewall_enabled", "mdm_enrolled", "vuln_count", "compliant", "needs_update"},
		func(row complianceReportRow) []string {
			return []string{
				row.DeviceID, row.Hostname, row.OSVersion,
				fmt.Sprintf("%t", row.DiskEncrypted),
				fmt.Sprintf("%t", row.FirewallEnabled),
				fmt.Sprintf("%t", row.MDMEnrolled),
				fmt.Sprintf("%d", row.VulnCount),
				fmt.Sprintf("%t", row.Compliant),
				fmt.Sprintf("%t", row.NeedsUpdate),
			}
		})
}

func (h *Handler) downloadAccessReviewReport(
	w http.ResponseWriter, r *http.Request,
	format, filename string,
	fromTime, toTime time.Time, actorID string,
) {
	ctx := r.Context()

	query := `SELECT
		rc.id, rc.name, rc.status, rc.created_at, rc.closed_at,
		COUNT(ri.id) AS total,
		COUNT(ri.id) FILTER (WHERE ri.decision = 'confirm') AS confirmed,
		COUNT(ri.id) FILTER (WHERE ri.decision = 'revoke') AS revoked,
		COUNT(ri.id) FILTER (WHERE ri.decision IS NULL) AS pending
	FROM review_campaigns rc
	LEFT JOIN review_items ri ON ri.campaign_id = rc.id
	WHERE 1=1`

	args := []interface{}{}
	argIdx := 1

	if !fromTime.IsZero() {
		query += fmt.Sprintf(` AND rc.created_at >= $%d`, argIdx)
		args = append(args, fromTime)
		argIdx++
	}
	if !toTime.IsZero() {
		query += fmt.Sprintf(` AND rc.created_at < $%d`, argIdx)
		args = append(args, toTime)
		argIdx++
	}

	query += ` GROUP BY rc.id ORDER BY rc.created_at DESC`
	_ = argIdx

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.logger.Error("access-review report: failed to query campaigns", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var report []accessReviewReportRow
	for rows.Next() {
		var row accessReviewReportRow
		var createdAt time.Time
		var closedAt *time.Time
		if err := rows.Scan(
			&row.CampaignID, &row.CampaignName, &row.Status, &createdAt, &closedAt,
			&row.TotalItems, &row.Confirmed, &row.Revoked, &row.Pending,
		); err != nil {
			h.logger.Error("access-review report: failed to scan row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		row.CreatedAt = createdAt.Format(time.RFC3339)
		if closedAt != nil {
			row.ClosedAt = closedAt.Format(time.RFC3339)
		}
		report = append(report, row)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("access-review report: iterate error", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.writeAuditEntryBestEffort(actorID, "report.download", "report", reportTypeAccessReview,
		map[string]interface{}{"format": format, "rows": len(report)}); err != nil {
		h.logger.Warn("failed to write report download audit entry", zap.Error(err))
	}

	streamReport(w, format, filename, report,
		[]string{"campaign_id", "campaign_name", "status", "created_at", "closed_at", "total_items", "confirmed", "revoked", "pending"},
		func(row accessReviewReportRow) []string {
			return []string{
				row.CampaignID, row.CampaignName, row.Status,
				row.CreatedAt, row.ClosedAt,
				fmt.Sprintf("%d", row.TotalItems),
				fmt.Sprintf("%d", row.Confirmed),
				fmt.Sprintf("%d", row.Revoked),
				fmt.Sprintf("%d", row.Pending),
			}
		})
}

// streamReport writes rows as CSV or JSON to w.
func streamReport[T any](
	w http.ResponseWriter,
	format, filename string,
	rows []T,
	headers []string,
	rowFn func(T) []string,
) {
	ts := time.Now().UTC().Format("20060102-150405")
	_ = ts // filename already computed by caller

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if rows == nil {
			rows = []T{}
		}
		_ = enc.Encode(rows)

	default: // csv
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		cw := csv.NewWriter(w)
		_ = cw.Write(headers)
		for _, row := range rows {
			_ = cw.Write(rowFn(row))
		}
		cw.Flush()
	}
}

// AuditIntegrityResponse combines chain-verification result with retention config.
type AuditIntegrityResponse struct {
	// Chain holds the hash-chain verification result (reuses audit.VerifyResult).
	ChainOK       bool   `json:"chainOk"`
	RowsChecked   int    `json:"rowsChecked"`
	FirstBreakSeq int64  `json:"firstBreakSeq,omitempty"`
	ChainError    string `json:"chainError,omitempty"`
	// RetainForSeconds is the configured retention window in seconds.
	// 0 means keep forever (no pruning configured).
	RetainForSeconds float64 `json:"retainForSeconds"`
	// RetainForHuman is a human-readable form of the retention window.
	RetainForHuman string `json:"retainForHuman"`
}

// GetAuditIntegrity returns chain-verification status and the configured retention
// window in a single call. Read-only; reuses audit.VerifyChain.
//
// Route: GET /api/v1/audit-logs/integrity
// Permission-gated via PermReadAuditLogs in routes.go.
func (h *Handler) GetAuditIntegrity(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	result, err := audit.VerifyChain(r.Context(), h.db)
	if err != nil {
		h.logger.Error("audit integrity check failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	retainHuman := "keep forever"
	if h.auditRetainFor > 0 {
		retainHuman = h.auditRetainFor.String()
	}

	respondJSON(w, http.StatusOK, AuditIntegrityResponse{
		ChainOK:          result.OK,
		RowsChecked:      result.RowsChecked,
		FirstBreakSeq:    result.FirstBreakSeq,
		ChainError:       result.Error,
		RetainForSeconds: h.auditRetainFor.Seconds(),
		RetainForHuman:   retainHuman,
	})
}
