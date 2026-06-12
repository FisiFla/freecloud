package handlers

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// DeviceCheckRequest is the JSON request body for the device check endpoint.
type DeviceCheckRequest struct {
	KeycloakUserID string `json:"keycloakUserId"`
}

// DeviceCheckResponse is the JSON response for the device check endpoint.
type DeviceCheckResponse struct {
	Passed   bool       `json:"passed"`
	Failures []Failure  `json:"failures,omitempty"`
}

// Failure describes a security check that did not pass.
type Failure struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// DeviceCheck checks the security state of a user's device.
func (h *Handler) DeviceCheck(w http.ResponseWriter, r *http.Request) {
	var req DeviceCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.KeycloakUserID == "" {
		respondError(w, http.StatusBadRequest, "keycloakUserId is required")
		return
	}

	ctx := r.Context()

	// Look up device mapping for this user
	rows, err := h.db.Query(ctx,
		`SELECT device_id FROM users_devices_mapping WHERE user_id = $1`,
		req.KeycloakUserID,
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
