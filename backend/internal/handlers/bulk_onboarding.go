package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// BulkOnboardRow is one CSV row's parsed data.
type BulkOnboardRow struct {
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	Email      string `json:"email"`
	Department string `json:"department"`
	Role       string `json:"role"`
}

// BulkOnboardRowResult reports the outcome for a single CSV row.
type BulkOnboardRowResult struct {
	Row    int    `json:"row"`
	Email  string `json:"email"`
	Status string `json:"status"` // "succeeded" | "skipped-duplicate" | "failed"
	Reason string `json:"reason,omitempty"`
}

// BulkOnboardResponse is the top-level response for POST /api/v1/onboard/bulk.
type BulkOnboardResponse struct {
	Total     int                    `json:"total"`
	Succeeded int                    `json:"succeeded"`
	Skipped   int                    `json:"skipped"`
	Failed    int                    `json:"failed"`
	Results   []BulkOnboardRowResult `json:"results"`
}

// maxBulkRows caps the number of rows accepted in a single bulk request.
const maxBulkRows = 500

// BulkOnboard handles POST /api/v1/onboard/bulk.
//
// Accepts multipart/form-data with a "file" field containing a CSV, or a JSON
// array of OnboardRequest objects.  Per-row errors are partial-success: a row
// that fails does not abort the remaining rows.  Duplicate emails (already in
// the DB) are skipped and reported as "skipped-duplicate".
//
// The endpoint is admin-gated via the authMW and the isManagementEndpoint
// check in middleware/auth.go (prefix /api/v1/onboard).
func (h *Handler) BulkOnboard(w http.ResponseWriter, r *http.Request) {
	logger := h.logger

	rows, parseErr := parseBulkInput(r)
	if parseErr != nil {
		respondError(w, http.StatusBadRequest, parseErr.Error())
		return
	}
	if len(rows) == 0 {
		respondError(w, http.StatusBadRequest, "no rows provided")
		return
	}
	if len(rows) > maxBulkRows {
		respondError(w, http.StatusBadRequest,
			fmt.Sprintf("too many rows: maximum %d allowed", maxBulkRows))
		return
	}

	actorID := middleware.GetActorID(r.Context())
	resp := BulkOnboardResponse{
		Total:   len(rows),
		Results: make([]BulkOnboardRowResult, 0, len(rows)),
	}

	for i, row := range rows {
		rowNum := i + 2 // 1-indexed + header row

		// Normalize.
		row.FirstName = strings.TrimSpace(row.FirstName)
		row.LastName = strings.TrimSpace(row.LastName)
		row.Email = strings.ToLower(strings.TrimSpace(row.Email))
		row.Department = strings.TrimSpace(row.Department)
		row.Role = strings.TrimSpace(row.Role)

		// Validate.
		if reason := validateBulkRow(row); reason != "" {
			resp.Failed++
			resp.Results = append(resp.Results, BulkOnboardRowResult{
				Row:    rowNum,
				Email:  row.Email,
				Status: "failed",
				Reason: reason,
			})
			continue
		}

		// Idempotency check.
		if h.db != nil {
			var existingID string
			lookupErr := h.db.QueryRow(r.Context(),
				`SELECT keycloak_user_id FROM users WHERE email = $1`, row.Email,
			).Scan(&existingID)
			if lookupErr == nil {
				// Row already exists — skip.
				resp.Skipped++
				resp.Results = append(resp.Results, BulkOnboardRowResult{
					Row:    rowNum,
					Email:  row.Email,
					Status: "skipped-duplicate",
				})
				continue
			}
			// pgx.ErrNoRows → proceed; any other error → report as failure.
			if lookupErr.Error() != "no rows in result set" && !strings.Contains(lookupErr.Error(), "no rows") {
				logger.Error("bulk onboard idempotency lookup failed",
					zap.Int("row", rowNum), zap.Error(lookupErr))
				resp.Failed++
				resp.Results = append(resp.Results, BulkOnboardRowResult{
					Row:    rowNum,
					Email:  row.Email,
					Status: "failed",
					Reason: "database lookup error",
				})
				continue
			}
		}

		// Delegate to the same logic as the single-user onboard.
		req := OnboardRequest{
			FirstName:  row.FirstName,
			LastName:   row.LastName,
			Email:      row.Email,
			Department: row.Department,
			Role:       row.Role,
		}
		if err := h.onboardOne(r.Context(), req, actorID); err != nil {
			logger.Warn("bulk onboard row failed",
				zap.Int("row", rowNum),
				zap.String("email", row.Email),
				zap.Error(err),
			)
			resp.Failed++
			resp.Results = append(resp.Results, BulkOnboardRowResult{
				Row:    rowNum,
				Email:  row.Email,
				Status: "failed",
				Reason: err.Error(),
			})
			continue
		}

		resp.Succeeded++
		resp.Results = append(resp.Results, BulkOnboardRowResult{
			Row:    rowNum,
			Email:  row.Email,
			Status: "succeeded",
		})
	}

	// 207 Multi-Status when at least one row failed or was skipped.
	status := http.StatusOK
	if resp.Failed > 0 || resp.Skipped > 0 {
		status = http.StatusMultiStatus
	}
	respondJSON(w, status, resp)
}

// onboardOne runs the same Keycloak-create + Fleet-token + DB-persist pipeline
// as the single Onboard handler but for a single row without writing an HTTP
// response.  Returns a descriptive error on failure; success returns nil.
func (h *Handler) onboardOne(ctx context.Context, req OnboardRequest, actorID string) error {
	// Step 1: Keycloak user.
	result, err := h.keycloak.CreateUser(ctx, req.FirstName, req.LastName, req.Email, req.Department)
	if err != nil {
		return fmt.Errorf("identity provider: %w", err)
	}
	if !result.PasswordSet {
		return fmt.Errorf("password could not be set for user")
	}
	if result.User == nil || result.User.ID == nil || *result.User.ID == "" {
		return fmt.Errorf("identity provider returned empty user ID")
	}
	kcUserID := *result.User.ID

	// Compensation: clean up orphaned KC user if persistence fails.
	persisted := false
	defer func() {
		if persisted {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		h.logger.Warn("bulk onboard: rolling back orphaned Keycloak user",
			zap.String("kc_user_id", kcUserID))
		if delErr := h.keycloak.DeleteUser(cleanupCtx, kcUserID); delErr != nil {
			h.logger.Error("bulk onboard: failed to roll back orphaned Keycloak user",
				zap.String("kc_user_id", kcUserID), zap.Error(delErr))
		}
	}()

	// Step 2: Fleet enrollment token (best-effort).
	var enrollmentToken string
	if token, fleetErr := h.fleet.CreateEnrollmentToken(ctx); fleetErr != nil {
		h.logger.Warn("bulk onboard: fleet token failed, continuing", zap.Error(fleetErr))
	} else {
		enrollmentToken = token
	}

	// Step 3: Persist.
	if h.db != nil {
		auditDetails, _ := json.Marshal(map[string]interface{}{
			"email": req.Email, "department": req.Department, "role": req.Role,
			"source": "bulk_onboard",
		})
		persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if persistErr := h.persistOnboard(persistCtx, kcUserID, req, actorID, string(auditDetails), enrollmentToken); persistErr != nil {
			return fmt.Errorf("persist: %w", persistErr)
		}
	}
	persisted = true
	return nil
}

// validateBulkRow returns a non-empty reason string if the row is invalid.
func validateBulkRow(row BulkOnboardRow) string {
	if row.FirstName == "" {
		return "firstName is required"
	}
	if len(row.FirstName) > 100 {
		return "firstName must be ≤ 100 characters"
	}
	if row.LastName == "" {
		return "lastName is required"
	}
	if len(row.LastName) > 100 {
		return "lastName must be ≤ 100 characters"
	}
	if row.Email == "" {
		return "email is required"
	}
	if !isValidEmail(row.Email) {
		return "email must be a valid address"
	}
	if len(row.Department) > 100 {
		return "department must be ≤ 100 characters"
	}
	if len(row.Role) > 100 {
		return "role must be ≤ 100 characters"
	}
	return ""
}

// parseBulkInput reads CSV (multipart "file" field) or a JSON array from the
// request body.  Returns the parsed rows or an error.
func parseBulkInput(r *http.Request) ([]BulkOnboardRow, error) {
	ct := r.Header.Get("Content-Type")

	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil { // 8 MB
			return nil, fmt.Errorf("parse multipart form: %w", err)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			return nil, fmt.Errorf("missing 'file' field in form: %w", err)
		}
		defer f.Close()
		return parseCSV(f)
	}

	// JSON array fallback.
	var rows []BulkOnboardRow
	if err := json.NewDecoder(r.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("invalid request body: %w", err)
	}
	return rows, nil
}

// parseCSV parses a CSV with a header row and returns the rows.
// Expected header (case-insensitive): firstName, lastName, email, department, role
func parseCSV(r io.Reader) ([]BulkOnboardRow, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true

	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("CSV read header: %w", err)
	}
	// Normalize headers.
	colIdx := map[string]int{}
	for i, h := range headers {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	required := []string{"firstname", "lastname", "email"}
	for _, req := range required {
		if _, ok := colIdx[req]; !ok {
			return nil, fmt.Errorf("CSV missing required column: %s", req)
		}
	}

	var rows []BulkOnboardRow
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV parse error: %w", err)
		}
		get := func(col string) string {
			idx, ok := colIdx[col]
			if !ok || idx >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[idx])
		}
		rows = append(rows, BulkOnboardRow{
			FirstName:  get("firstname"),
			LastName:   get("lastname"),
			Email:      get("email"),
			Department: get("department"),
			Role:       get("role"),
		})
	}
	return rows, nil
}
