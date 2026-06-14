package handlers

import (
	"net/http"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// DeviceCheckResponse is the JSON response for the device check endpoint.
// The request body is ignored; the user identity is derived from the JWT token.
type DeviceCheckResponse struct {
	Passed   bool      `json:"passed"`
	Failures []Failure `json:"failures,omitempty"`
}

// Failure describes a security check that did not pass.
type Failure struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// DeviceCheck checks the security state of a user's device.
func (h *Handler) DeviceCheck(w http.ResponseWriter, r *http.Request) {
	// Derive user ID from JWT claims
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.Sub == "" {
		respondError(w, http.StatusUnauthorized, "valid JWT claims required")
		return
	}
	userID := claims.Sub

	ctx := r.Context()

	// Look up device mapping for this user
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT device_id FROM users_devices_mapping WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		h.logger.Error("failed to query device mapping", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var deviceIDs []string
	for rows.Next() {
		var deviceID string
		if err := rows.Scan(&deviceID); err != nil {
			h.logger.Error("failed to scan device ID", zap.Error(err))
			continue
		}
		deviceIDs = append(deviceIDs, deviceID)
	}

	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating device rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if len(deviceIDs) == 0 {
		respondError(w, http.StatusNotFound, "no device found for user")
		return
	}

	var failures []Failure

	for _, devID := range deviceIDs {
		state, err := h.fleet.GetHostSecurityState(ctx, devID)
		if err != nil {
			h.logger.Error("failed to get security state for device",
				zap.String("device_id", devID),
				zap.Error(err),
			)
			failures = append(failures, Failure{
				Type:   "security_check_error",
				Detail: "Unable to check security state for device " + devID,
			})
			continue
		}

		if !state.FirewallEnabled {
			failures = append(failures, Failure{
				Type:   "firewall_disabled",
				Detail: "Firewall is disabled on device " + devID,
			})
		}
		if !state.DiskEncrypted {
			failures = append(failures, Failure{
				Type:   "disk_not_encrypted",
				Detail: "Disk encryption is not enabled on device " + devID,
			})
		}
		for _, v := range state.Vulnerabilities {
			failures = append(failures, Failure{
				Type:   "vulnerability",
				Detail: v,
			})
		}
		if state.UnknownVulns {
			h.logger.Warn("vulnerability data incomplete for device, posture may be inaccurate",
				zap.String("device_id", devID),
			)
			failures = append(failures, Failure{
				Type:   "vulnerability_data_missing",
				Detail: "Vulnerability data unavailable for device " + devID,
			})
		}
	}

	if len(failures) > 0 {
		respondJSON(w, http.StatusForbidden, DeviceCheckResponse{
			Passed:   false,
			Failures: failures,
		})
		return
	}

	respondJSON(w, http.StatusOK, DeviceCheckResponse{Passed: true})
}
