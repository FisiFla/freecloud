package handlers

// D1 — Password & account-policy API.
//
// Two endpoints, both super-admin only:
//   GET  /api/v1/account-policy  — read current Keycloak realm password-policy + brute-force settings
//   PUT  /api/v1/account-policy  — write new settings (validates + audits before applying)

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// AccountPolicyResponse is the read-side DTO returned by GET /api/v1/account-policy.
// It mirrors keycloak.RealmPolicyResult but uses JSON names matching the frontend.
type AccountPolicyResponse struct {
	PasswordPolicy              string `json:"passwordPolicy"`
	BruteForceProtected         bool   `json:"bruteForceProtected"`
	FailureFactor               int    `json:"failureFactor"`
	WaitIncrementSeconds        int    `json:"waitIncrementSeconds"`
	MaxFailureWaitSeconds       int    `json:"maxFailureWaitSeconds"`
	QuickLoginCheckMilliSeconds int64  `json:"quickLoginCheckMilliSeconds"`
	MinimumQuickLoginWaitSeconds int   `json:"minimumQuickLoginWaitSeconds"`
	MaxDeltaTimeSeconds         int    `json:"maxDeltaTimeSeconds"`
	// Parsed policy fields for convenience (derived from PasswordPolicy string).
	MinLength    int `json:"minLength"`
	UpperCase    int `json:"upperCase"`
	LowerCase    int `json:"lowerCase"`
	Digits       int `json:"digits"`
	SpecialChars int `json:"specialChars"`
	PasswordHistory int `json:"passwordHistory"`
	PasswordExpireDays int `json:"passwordExpireDays"`
}

// UpdateAccountPolicyRequest is the body for PUT /api/v1/account-policy.
type UpdateAccountPolicyRequest struct {
	// Structured fields — the handler serialises them into the Keycloak policy string.
	MinLength          int  `json:"minLength"`
	UpperCase          int  `json:"upperCase"`
	LowerCase          int  `json:"lowerCase"`
	Digits             int  `json:"digits"`
	SpecialChars       int  `json:"specialChars"`
	PasswordHistory    int  `json:"passwordHistory"`
	PasswordExpireDays int  `json:"passwordExpireDays"`
	// Brute-force fields.
	BruteForceProtected          bool  `json:"bruteForceProtected"`
	FailureFactor                int   `json:"failureFactor"`
	WaitIncrementSeconds         int   `json:"waitIncrementSeconds"`
	MaxFailureWaitSeconds        int   `json:"maxFailureWaitSeconds"`
	QuickLoginCheckMilliSeconds  int64 `json:"quickLoginCheckMilliSeconds"`
	MinimumQuickLoginWaitSeconds int   `json:"minimumQuickLoginWaitSeconds"`
	MaxDeltaTimeSeconds          int   `json:"maxDeltaTimeSeconds"`
}

// policyParam extracts an integer value from a Keycloak password policy string.
// For example, policyParam("length(12) and upperCase(1)", "length") returns 12.
// Returns 0 when the component is absent.
func policyParam(policy, name string) int {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\((\d+)\)`)
	m := re.FindStringSubmatch(policy)
	if len(m) < 2 {
		return 0
	}
	v, _ := strconv.Atoi(m[1])
	return v
}

// buildPolicyString serialises structured policy fields into the Keycloak
// password-policy string format: "length(N) and upperCase(N) and ...".
// Components with a zero value are omitted (Keycloak treats absence as no
// restriction, not zero). History and expiry at 0 are similarly omitted.
func buildPolicyString(req UpdateAccountPolicyRequest) string {
	parts := []string{}
	if req.MinLength > 0 {
		parts = append(parts, fmt.Sprintf("length(%d)", req.MinLength))
	}
	if req.UpperCase > 0 {
		parts = append(parts, fmt.Sprintf("upperCase(%d)", req.UpperCase))
	}
	if req.LowerCase > 0 {
		parts = append(parts, fmt.Sprintf("lowerCase(%d)", req.LowerCase))
	}
	if req.Digits > 0 {
		parts = append(parts, fmt.Sprintf("digits(%d)", req.Digits))
	}
	if req.SpecialChars > 0 {
		parts = append(parts, fmt.Sprintf("specialChars(%d)", req.SpecialChars))
	}
	if req.PasswordHistory > 0 {
		parts = append(parts, fmt.Sprintf("passwordHistory(%d)", req.PasswordHistory))
	}
	if req.PasswordExpireDays > 0 {
		parts = append(parts, fmt.Sprintf("forceExpiredPasswordChange(%d)", req.PasswordExpireDays))
	}
	return strings.Join(parts, " and ")
}

// validateAccountPolicy returns a slice of field-level errors.  Fail closed:
// if any value is out of range the whole update is rejected.
func validateAccountPolicy(req UpdateAccountPolicyRequest) []ValidationError {
	var errs []ValidationError
	if req.MinLength < 0 || req.MinLength > 256 {
		errs = append(errs, ValidationError{Field: "minLength", Message: "must be between 0 and 256"})
	}
	if req.UpperCase < 0 || req.UpperCase > 64 {
		errs = append(errs, ValidationError{Field: "upperCase", Message: "must be between 0 and 64"})
	}
	if req.LowerCase < 0 || req.LowerCase > 64 {
		errs = append(errs, ValidationError{Field: "lowerCase", Message: "must be between 0 and 64"})
	}
	if req.Digits < 0 || req.Digits > 64 {
		errs = append(errs, ValidationError{Field: "digits", Message: "must be between 0 and 64"})
	}
	if req.SpecialChars < 0 || req.SpecialChars > 64 {
		errs = append(errs, ValidationError{Field: "specialChars", Message: "must be between 0 and 64"})
	}
	if req.PasswordHistory < 0 || req.PasswordHistory > 100 {
		errs = append(errs, ValidationError{Field: "passwordHistory", Message: "must be between 0 and 100"})
	}
	if req.PasswordExpireDays < 0 || req.PasswordExpireDays > 3650 {
		errs = append(errs, ValidationError{Field: "passwordExpireDays", Message: "must be between 0 and 3650"})
	}
	if req.FailureFactor < 0 || req.FailureFactor > 1000 {
		errs = append(errs, ValidationError{Field: "failureFactor", Message: "must be between 0 and 1000"})
	}
	if req.WaitIncrementSeconds < 0 || req.WaitIncrementSeconds > 86400 {
		errs = append(errs, ValidationError{Field: "waitIncrementSeconds", Message: "must be between 0 and 86400"})
	}
	if req.MaxFailureWaitSeconds < 0 || req.MaxFailureWaitSeconds > 86400 {
		errs = append(errs, ValidationError{Field: "maxFailureWaitSeconds", Message: "must be between 0 and 86400"})
	}
	if req.QuickLoginCheckMilliSeconds < 0 || req.QuickLoginCheckMilliSeconds > 3600000 {
		errs = append(errs, ValidationError{Field: "quickLoginCheckMilliSeconds", Message: "must be between 0 and 3600000"})
	}
	if req.MinimumQuickLoginWaitSeconds < 0 || req.MinimumQuickLoginWaitSeconds > 86400 {
		errs = append(errs, ValidationError{Field: "minimumQuickLoginWaitSeconds", Message: "must be between 0 and 86400"})
	}
	if req.MaxDeltaTimeSeconds < 0 || req.MaxDeltaTimeSeconds > 2592000 {
		errs = append(errs, ValidationError{Field: "maxDeltaTimeSeconds", Message: "must be between 0 and 2592000"})
	}
	return errs
}

// realmPolicyToResponse maps a keycloak.RealmPolicyResult to the API response DTO.
func realmPolicyToResponse(p *keycloak.RealmPolicyResult) AccountPolicyResponse {
	return AccountPolicyResponse{
		PasswordPolicy:               p.PasswordPolicy,
		BruteForceProtected:          p.BruteForceProtected,
		FailureFactor:                p.FailureFactor,
		WaitIncrementSeconds:         p.WaitIncrementSeconds,
		MaxFailureWaitSeconds:        p.MaxFailureWaitSeconds,
		QuickLoginCheckMilliSeconds:  p.QuickLoginCheckMilliSeconds,
		MinimumQuickLoginWaitSeconds: p.MinimumQuickLoginWaitSeconds,
		MaxDeltaTimeSeconds:          p.MaxDeltaTimeSeconds,
		MinLength:                    policyParam(p.PasswordPolicy, "length"),
		UpperCase:                    policyParam(p.PasswordPolicy, "upperCase"),
		LowerCase:                    policyParam(p.PasswordPolicy, "lowerCase"),
		Digits:                       policyParam(p.PasswordPolicy, "digits"),
		SpecialChars:                 policyParam(p.PasswordPolicy, "specialChars"),
		PasswordHistory:              policyParam(p.PasswordPolicy, "passwordHistory"),
		PasswordExpireDays:           policyParam(p.PasswordPolicy, "forceExpiredPasswordChange"),
	}
}

// GetAccountPolicy returns the realm's current password-policy and brute-force settings.
//
// Route: GET /api/v1/account-policy
func (h *Handler) GetAccountPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := h.keycloak.GetRealmPolicy(r.Context())
	if err != nil {
		h.logger.Error("failed to get realm policy", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to retrieve account policy")
		return
	}
	respondJSON(w, http.StatusOK, realmPolicyToResponse(policy))
}

// UpdateAccountPolicy validates, applies, and audits new password-policy + lockout settings.
//
// Route: PUT /api/v1/account-policy
func (h *Handler) UpdateAccountPolicy(w http.ResponseWriter, r *http.Request) {
	var req UpdateAccountPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if errs := validateAccountPolicy(req); len(errs) > 0 {
		respondValidationErrors(w, errs)
		return
	}

	kcReq := keycloak.UpdateRealmPolicyRequest{
		PasswordPolicy:               buildPolicyString(req),
		BruteForceProtected:          req.BruteForceProtected,
		FailureFactor:                req.FailureFactor,
		WaitIncrementSeconds:         req.WaitIncrementSeconds,
		MaxFailureWaitSeconds:        req.MaxFailureWaitSeconds,
		QuickLoginCheckMilliSeconds:  req.QuickLoginCheckMilliSeconds,
		MinimumQuickLoginWaitSeconds: req.MinimumQuickLoginWaitSeconds,
		MaxDeltaTimeSeconds:          req.MaxDeltaTimeSeconds,
	}

	if err := h.keycloak.UpdateRealmPolicy(r.Context(), kcReq); err != nil {
		h.logger.Error("failed to update realm policy", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to update account policy")
		return
	}

	// Audit — best-effort.
	actorID := middleware.GetActorID(r.Context())
	if h.db != nil {
		if auditErr := h.writeAuditEntryBestEffort(actorID, "update_account_policy", "realm", "default", map[string]interface{}{
			"passwordPolicy":               kcReq.PasswordPolicy,
			"bruteForceProtected":          req.BruteForceProtected,
			"failureFactor":                req.FailureFactor,
			"waitIncrementSeconds":         req.WaitIncrementSeconds,
			"maxFailureWaitSeconds":        req.MaxFailureWaitSeconds,
			"maxDeltaTimeSeconds":          req.MaxDeltaTimeSeconds,
		}); auditErr != nil {
			h.logger.Warn("failed to write audit log for update_account_policy", zap.Error(auditErr))
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"updated":       true,
		"passwordPolicy": kcReq.PasswordPolicy,
	})
}
