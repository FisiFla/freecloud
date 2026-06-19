package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// RemoteLockResponse is returned by POST /api/v1/devices/{id}/lock.
type RemoteLockResponse struct {
	DeviceID string `json:"deviceId"`
	Locked   bool   `json:"locked"`
}

// RemoteLock issues a remote-lock command to the given Fleet host.
// Route: POST /api/v1/devices/{id}/lock (requires PermManageDevices).
func (h *Handler) RemoteLock(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	if deviceID == "" {
		respondError(w, http.StatusBadRequest, "device id is required")
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	h.logger.Info("remote lock requested",
		zap.String("device_id", deviceID),
		zap.String("actor_id", actorID),
	)

	if err := h.fleet.IssueRemoteLock(ctx, deviceID); err != nil {
		h.logger.Error("remote lock failed",
			zap.String("device_id", deviceID),
			zap.Error(err),
		)
		respondError(w, http.StatusBadGateway, "remote lock command failed")
		return
	}

	// Detached audit write — client disconnect must not drop the record.
	if h.db != nil {
		details, _ := json.Marshal(map[string]interface{}{
			"device_id": deviceID,
		})
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := h.db.Exec(auditCtx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "device_lock", "device", deviceID, details,
		); err != nil {
			h.logger.Warn("failed to write audit log for remote lock", zap.Error(err))
		}
	}

	respondJSON(w, http.StatusOK, RemoteLockResponse{
		DeviceID: deviceID,
		Locked:   true,
	})
}

// ----- B2: Software inventory -----

// DeviceSoftwareHost groups software for a single host.
type DeviceSoftwareHost struct {
	DeviceID string           `json:"deviceId"`
	Hostname string           `json:"hostname,omitempty"`
	Software []fleet.Software `json:"software"`
}

// DeviceSoftwareResponse is the full inventory for a user's devices.
type DeviceSoftwareResponse struct {
	UserID  string               `json:"userId"`
	Devices []DeviceSoftwareHost `json:"devices"`
}

// GetDeviceSoftware returns the software inventory for the devices mapped to a
// user. Route: GET /api/v1/users/{id}/devices/software (requires PermReadCompliance).
func (h *Handler) GetDeviceSoftware(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		respondError(w, http.StatusBadRequest, "user id is required")
		return
	}
	if !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "user id must be a valid UUID")
		return
	}

	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT d.fleet_host_id, COALESCE(d.hostname, '')
		 FROM devices d
		 INNER JOIN users_devices_mapping m ON d.fleet_host_id = m.device_id
		 WHERE m.user_id = $1`,
		userID,
	)
	if err != nil {
		h.logger.Error("failed to query devices for user", zap.String("user_id", userID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var deviceList []complianceDevice
	for rows.Next() {
		var dr complianceDevice
		if err := rows.Scan(&dr.id, &dr.hostname); err != nil {
			h.logger.Warn("failed to scan device row", zap.Error(err))
			continue
		}
		deviceList = append(deviceList, dr)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating device rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	hosts := make([]DeviceSoftwareHost, 0, len(deviceList))
	for _, dr := range deviceList {
		sw, err := h.fleet.GetHostSoftware(ctx, dr.id)
		if err != nil {
			h.logger.Warn("failed to fetch software for device",
				zap.String("device_id", dr.id),
				zap.Error(err),
			)
			sw = []fleet.Software{}
		}
		if sw == nil {
			sw = []fleet.Software{}
		}
		hosts = append(hosts, DeviceSoftwareHost{
			DeviceID: dr.id,
			Hostname: dr.hostname,
			Software: sw,
		})
	}

	respondJSON(w, http.StatusOK, DeviceSoftwareResponse{
		UserID:  userID,
		Devices: hosts,
	})
}

// ----- B3: Compliance / posture dashboard -----

// complianceDevice is an internal type used when building compliance postures.
type complianceDevice struct {
	id        string
	hostname  string
	osVersion string
}

// DeviceHostPosture holds the compliance posture for a single device.
type DeviceHostPosture struct {
	DeviceID        string   `json:"deviceId"`
	Hostname        string   `json:"hostname,omitempty"`
	OsVersion       string   `json:"osVersion,omitempty"`
	DiskEncrypted   bool     `json:"diskEncrypted"`
	FirewallEnabled bool     `json:"firewallEnabled"`
	MDMEnrolled     bool     `json:"mdmEnrolled"`
	Vulnerabilities []string `json:"vulnerabilities"`
	UnknownVulns    bool     `json:"unknownVulns"`
	Compliant       bool     `json:"compliant"`
}

// ComplianceSummary aggregates compliance metrics.
type ComplianceSummary struct {
	TotalDevices     int `json:"totalDevices"`
	CompliantDevices int `json:"compliantDevices"`
	EncryptedDevices int `json:"encryptedDevices"`
	FirewallEnabled  int `json:"firewallEnabled"`
	DevicesWithVulns int `json:"devicesWithVulns"`
}

// ComplianceResponse is returned by the compliance endpoints.
type ComplianceResponse struct {
	UserID  string              `json:"userId,omitempty"`
	Summary ComplianceSummary   `json:"summary"`
	Devices []DeviceHostPosture `json:"devices"`
}

// GetUserCompliance returns compliance posture for a single user's devices.
// Route: GET /api/v1/users/{id}/devices/compliance (requires PermReadCompliance).
func (h *Handler) GetUserCompliance(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		respondError(w, http.StatusBadRequest, "user id is required")
		return
	}
	if !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "user id must be a valid UUID")
		return
	}

	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT d.fleet_host_id, COALESCE(d.hostname, ''), COALESCE(d.os_version, '')
		 FROM devices d
		 INNER JOIN users_devices_mapping m ON d.fleet_host_id = m.device_id
		 WHERE m.user_id = $1`,
		userID,
	)
	if err != nil {
		h.logger.Error("failed to query devices", zap.String("user_id", userID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	deviceList, scanErr := scanComplianceDevices(h, rows)
	if scanErr != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	postures, summary := h.buildCompliancePostures(ctx, deviceList)

	respondJSON(w, http.StatusOK, ComplianceResponse{
		UserID:  userID,
		Summary: summary,
		Devices: postures,
	})
}

// GetOrgCompliance returns compliance posture across all org devices.
// Route: GET /api/v1/compliance (requires PermReadCompliance).
func (h *Handler) GetOrgCompliance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT d.fleet_host_id, COALESCE(d.hostname, ''), COALESCE(d.os_version, '')
		 FROM devices d
		 ORDER BY d.hostname`,
	)
	if err != nil {
		h.logger.Error("failed to query all devices", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	deviceList, scanErr := scanComplianceDevices(h, rows)
	if scanErr != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	postures, summary := h.buildCompliancePostures(ctx, deviceList)

	respondJSON(w, http.StatusOK, ComplianceResponse{
		Summary: summary,
		Devices: postures,
	})
}

// scanComplianceDevices reads fleet_host_id/hostname/os_version rows into a
// complianceDevice slice. Errors from rows.Err() are logged and returned.
func scanComplianceDevices(h *Handler, rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]complianceDevice, error) {
	var list []complianceDevice
	for rows.Next() {
		var dr complianceDevice
		if err := rows.Scan(&dr.id, &dr.hostname, &dr.osVersion); err != nil {
			h.logger.Warn("failed to scan device row", zap.Error(err))
			continue
		}
		list = append(list, dr)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating device rows", zap.Error(err))
		return nil, err
	}
	return list, nil
}

// buildCompliancePostures fetches Fleet security state for each device and
// computes per-device compliance plus org-level summary counts.
func (h *Handler) buildCompliancePostures(ctx context.Context, deviceList []complianceDevice) ([]DeviceHostPosture, ComplianceSummary) {
	postures := make([]DeviceHostPosture, 0, len(deviceList))
	summary := ComplianceSummary{TotalDevices: len(deviceList)}

	for _, dr := range deviceList {
		state, err := h.fleet.GetHostSecurityState(ctx, dr.id)
		if err != nil {
			h.logger.Warn("failed to get security state",
				zap.String("device_id", dr.id),
				zap.Error(err),
			)
			postures = append(postures, DeviceHostPosture{
				DeviceID:        dr.id,
				Hostname:        dr.hostname,
				OsVersion:       dr.osVersion,
				Compliant:       false,
				UnknownVulns:    true,
				Vulnerabilities: []string{},
			})
			continue
		}

		vulns := state.Vulnerabilities
		if vulns == nil {
			vulns = []string{}
		}

		// A device is considered MDM-enrolled when Fleet can answer about it.
		mdmEnrolled := true
		compliant := state.DiskEncrypted && state.FirewallEnabled && len(vulns) == 0 && !state.UnknownVulns

		postures = append(postures, DeviceHostPosture{
			DeviceID:        dr.id,
			Hostname:        dr.hostname,
			OsVersion:       dr.osVersion,
			DiskEncrypted:   state.DiskEncrypted,
			FirewallEnabled: state.FirewallEnabled,
			MDMEnrolled:     mdmEnrolled,
			Vulnerabilities: vulns,
			UnknownVulns:    state.UnknownVulns,
			Compliant:       compliant,
		})

		if compliant {
			summary.CompliantDevices++
		}
		if state.DiskEncrypted {
			summary.EncryptedDevices++
		}
		if state.FirewallEnabled {
			summary.FirewallEnabled++
		}
		if len(vulns) > 0 {
			summary.DevicesWithVulns++
		}
	}

	return postures, summary
}
