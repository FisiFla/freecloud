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
	"github.com/FisiFla/freecloud/backend/internal/snapshot"
)

// maxLockMessageLen bounds custom remote-lock messages sent to Fleet/MDM.
const maxLockMessageLen = 500

// RemoteLockResponse is returned by POST /api/v1/devices/{id}/lock.
type RemoteLockResponse struct {
	DeviceID string `json:"deviceId"`
	Locked   bool   `json:"locked"`
}

// requireDeviceInCallerOrg verifies deviceID belongs to the caller's active
// org before any device-scoped handler acts on it. Writes the response and
// returns false on failure: no org context (403), lookup error (500), or
// the device not existing/belonging to a different org (404 — the two are
// deliberately indistinguishable, see resourceInOrg).
func (h *Handler) requireDeviceInCallerOrg(w http.ResponseWriter, r *http.Request, deviceID string) bool {
	return h.requireResourceInCallerOrg(w, r, "devices", "fleet_host_id", deviceID, "device not found")
}

// RemoteLock issues a remote-lock command to the given Fleet host.
// Route: POST /api/v1/devices/{id}/lock (requires PermManageDevices).
func (h *Handler) RemoteLock(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	// Same host-id grammar as MoveHostToTeam — reject path/control/hidden ids early.
	if err := ValidateHostID(deviceID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireDeviceInCallerOrg(w, r, deviceID) {
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
		if err := h.writeAuditEntryBestEffort(actorID, "device_lock", "device", deviceID, map[string]interface{}{
			"device_id": deviceID,
		}); err != nil {
			h.logger.Warn("failed to write audit log for remote lock", zap.Error(err))
		}
		persistDeviceCommand(r.Context(), h, deviceID, "lock", actorID)
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

// requireUserInCallerOrg verifies userID belongs to the caller's active org
// before any user-scoped handler acts on it. Mirrors requireDeviceInCallerOrg.
func (h *Handler) requireUserInCallerOrg(w http.ResponseWriter, r *http.Request, userID string) bool {
	return h.requireResourceInCallerOrg(w, r, "users", "keycloak_user_id", userID, "user not found")
}

// GetDeviceSoftware returns the software inventory for the devices mapped to a
// user. Route: GET /api/v1/users/{id}/devices/software (requires PermReadCompliance).
func (h *Handler) GetDeviceSoftware(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if err := ValidateUserID(userID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireUserInCallerOrg(w, r, userID) {
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
	NeedsUpdate     bool     `json:"needsUpdate"`
}

// ComplianceSummary aggregates compliance metrics.
type ComplianceSummary struct {
	TotalDevices       int `json:"totalDevices"`
	CompliantDevices   int `json:"compliantDevices"`
	EncryptedDevices   int `json:"encryptedDevices"`
	FirewallEnabled    int `json:"firewallEnabled"`
	DevicesWithVulns   int `json:"devicesWithVulns"`
	NeedsUpdateDevices int `json:"needsUpdateDevices"`
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
	if !h.requireUserInCallerOrg(w, r, userID) {
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
// Optional query param ?needs_update=true filters the device list to only
// those with pending OS updates (summary always reflects the full fleet).
// Route: GET /api/v1/compliance (requires PermReadCompliance).
func (h *Handler) GetOrgCompliance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT d.fleet_host_id, COALESCE(d.hostname, ''), COALESCE(d.os_version, '')
		 FROM devices d
		 WHERE d.org_id = $1
		 ORDER BY d.hostname`,
		oc.OrgID,
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

	// A1: persist posture to the cache so TakeSnapshot can compute real
	// compliance_rate without a live Fleet round-trip.
	if h.snapshotter != nil {
		cacheEntries := make([]snapshot.PostureEntry, 0, len(postures))
		for _, p := range postures {
			cacheEntries = append(cacheEntries, snapshot.PostureEntry{
				HostID:          p.DeviceID,
				Compliant:       p.Compliant,
				DiskEncrypted:   p.DiskEncrypted,
				OsUpToDate:      !p.NeedsUpdate,
				NeedsUpdate:     p.NeedsUpdate,
				FirewallEnabled: p.FirewallEnabled,
			})
		}
		if err := h.snapshotter.SyncPostureCache(ctx, cacheEntries); err != nil {
			h.logger.Warn("failed to sync posture cache", zap.Error(err))
		}
	}

	// E3: optional filter — only return devices that need an OS update.
	if r.URL.Query().Get("needs_update") == "true" {
		filtered := postures[:0]
		for _, p := range postures {
			if p.NeedsUpdate {
				filtered = append(filtered, p)
			}
		}
		postures = filtered
	}

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

		// E3: fetch OS posture best-effort; don't fail on error.
		osVersion := dr.osVersion
		needsUpdate := false
		if osPosture, posErr := h.fleet.GetHostOSPosture(ctx, dr.id); posErr == nil {
			needsUpdate = osPosture.NeedsUpdate
			if osVersion == "" && osPosture.OsVersion != "" {
				osVersion = osPosture.OsVersion
			}
		} else {
			h.logger.Warn("failed to get OS posture",
				zap.String("device_id", dr.id),
				zap.Error(posErr),
			)
		}

		postures = append(postures, DeviceHostPosture{
			DeviceID:        dr.id,
			Hostname:        dr.hostname,
			OsVersion:       osVersion,
			DiskEncrypted:   state.DiskEncrypted,
			FirewallEnabled: state.FirewallEnabled,
			MDMEnrolled:     mdmEnrolled,
			Vulnerabilities: vulns,
			UnknownVulns:    state.UnknownVulns,
			Compliant:       compliant,
			NeedsUpdate:     needsUpdate,
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
		if needsUpdate {
			summary.NeedsUpdateDevices++
		}
	}

	return postures, summary
}

// ----- E1: Expanded MDM command set -----

// RemoteRestartResponse is returned by POST /api/v1/devices/{id}/restart.
type RemoteRestartResponse struct {
	DeviceID  string `json:"deviceId"`
	Restarted bool   `json:"restarted"`
}

// RemoteLockWithMessageResponse is returned by POST /api/v1/devices/{id}/lock-message.
type RemoteLockWithMessageResponse struct {
	DeviceID string `json:"deviceId"`
	Locked   bool   `json:"locked"`
	Message  string `json:"message"`
}

// RemoteClearPasscodeResponse is returned by POST /api/v1/devices/{id}/clear-passcode.
type RemoteClearPasscodeResponse struct {
	DeviceID string `json:"deviceId"`
	Cleared  bool   `json:"cleared"`
}

// RemoteRestart issues a remote restart command to the given Fleet host.
// Route: POST /api/v1/devices/{id}/restart (requires PermManageDevices).
func (h *Handler) RemoteRestart(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	if err := ValidateHostID(deviceID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireDeviceInCallerOrg(w, r, deviceID) {
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	if err := h.fleet.IssueRestart(ctx, deviceID); err != nil {
		h.logger.Error("remote restart failed", zap.String("device_id", deviceID), zap.Error(err))
		respondError(w, http.StatusBadGateway, "remote restart command failed")
		return
	}

	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "device_restart", "device", deviceID, map[string]interface{}{
			"device_id": deviceID,
		}); err != nil {
			h.logger.Warn("failed to write audit log for remote restart", zap.Error(err))
		}
		persistDeviceCommand(ctx, h, deviceID, "restart", actorID)
	}

	respondJSON(w, http.StatusOK, RemoteRestartResponse{DeviceID: deviceID, Restarted: true})
}

// RemoteLockWithMessage issues a remote lock command with a custom message.
// Route: POST /api/v1/devices/{id}/lock-message (requires PermManageDevices).
func (h *Handler) RemoteLockWithMessage(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	if err := ValidateHostID(deviceID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireDeviceInCallerOrg(w, r, deviceID) {
		return
	}

	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Cap lock message length so Fleet/MDM payloads stay bounded.
	if len(body.Message) > maxLockMessageLen {
		respondError(w, http.StatusBadRequest, "message must be ≤ 500 characters")
		return
	}
	for _, r := range body.Message {
		if r < 0x20 || r == 0x7f {
			respondError(w, http.StatusBadRequest, "message must not contain control characters")
			return
		}
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	if err := h.fleet.IssueLockWithMessage(ctx, deviceID, body.Message); err != nil {
		h.logger.Error("remote lock-with-message failed", zap.String("device_id", deviceID), zap.Error(err))
		respondError(w, http.StatusBadGateway, "remote lock command failed")
		return
	}

	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "device_lock_message", "device", deviceID, map[string]interface{}{
			"device_id": deviceID,
			"message":   body.Message,
		}); err != nil {
			h.logger.Warn("failed to write audit log for remote lock-with-message", zap.Error(err))
		}
		persistDeviceCommand(ctx, h, deviceID, "lock_message", actorID)
	}

	respondJSON(w, http.StatusOK, RemoteLockWithMessageResponse{
		DeviceID: deviceID,
		Locked:   true,
		Message:  body.Message,
	})
}

// RemoteClearPasscode issues a clear-passcode command to the given Fleet host.
// Route: POST /api/v1/devices/{id}/clear-passcode (requires PermManageDevices).
func (h *Handler) RemoteClearPasscode(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	if err := ValidateHostID(deviceID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireDeviceInCallerOrg(w, r, deviceID) {
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	if err := h.fleet.IssueClearPasscode(ctx, deviceID); err != nil {
		h.logger.Error("remote clear-passcode failed", zap.String("device_id", deviceID), zap.Error(err))
		respondError(w, http.StatusBadGateway, "clear passcode command failed")
		return
	}

	if h.db != nil {
		if err := h.writeAuditEntryBestEffort(actorID, "device_clear_passcode", "device", deviceID, map[string]interface{}{
			"device_id": deviceID,
		}); err != nil {
			h.logger.Warn("failed to write audit log for remote clear-passcode", zap.Error(err))
		}
		persistDeviceCommand(ctx, h, deviceID, "clear_passcode", actorID)
	}

	respondJSON(w, http.StatusOK, RemoteClearPasscodeResponse{DeviceID: deviceID, Cleared: true})
}

// ----- E2: Device command history -----

// DeviceCommand represents a persisted MDM command record.
type DeviceCommand struct {
	ID               string `json:"id"`
	HostID           string `json:"hostId"`
	CommandType      string `json:"commandType"`
	Status           string `json:"status"`
	RequestedBy      string `json:"requestedBy"`
	RequestedAt      string `json:"requestedAt"`
	UpdatedAt        string `json:"updatedAt"`
	FleetCommandUUID string `json:"fleetCommandUuid,omitempty"`
	Result           string `json:"result,omitempty"`
}

// persistDeviceCommand inserts a device_commands row best-effort (logs on failure,
// never blocks the response).
func persistDeviceCommand(ctx context.Context, h *Handler, hostID, commandType, requestedBy string) {
	if h.db == nil {
		return
	}
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		h.logger.Warn("failed to persist device command: no org context",
			zap.String("host_id", hostID), zap.String("command_type", commandType))
		return
	}
	_, err := h.db.Exec(ctx,
		`INSERT INTO device_commands (host_id, command_type, status, requested_by, org_id)
		 VALUES ($1, $2, 'sent', $3, $4)`,
		hostID, commandType, requestedBy, oc.OrgID,
	)
	if err != nil {
		h.logger.Warn("failed to persist device command",
			zap.String("host_id", hostID),
			zap.String("command_type", commandType),
			zap.Error(err),
		)
	}
}

// maxDeviceCommandHistory bounds GET device command history fan-out.
const maxDeviceCommandHistory = 50

// GetDeviceCommandHistory returns the last 50 commands issued to a device.
// Route: GET /api/v1/devices/{id}/commands (requires PermReadCompliance).
func (h *Handler) GetDeviceCommandHistory(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	if err := ValidateHostID(deviceID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireDeviceInCallerOrg(w, r, deviceID) {
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	oc := middleware.GetOrgContext(r.Context())

	rows, err := h.db.Query(r.Context(),
		`SELECT id, host_id, command_type, status, requested_by,
		        requested_at, updated_at,
		        COALESCE(fleet_command_uuid, ''), COALESCE(result, '')
		 FROM device_commands
		 WHERE host_id = $1 AND org_id = $2
		 ORDER BY requested_at DESC
		 LIMIT $3`,
		deviceID, oc.OrgID, maxDeviceCommandHistory,
	)
	if err != nil {
		h.logger.Error("failed to query device commands", zap.String("device_id", deviceID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	commands := make([]DeviceCommand, 0)
	for rows.Next() {
		var cmd DeviceCommand
		var requestedAt, updatedAt time.Time
		if err := rows.Scan(
			&cmd.ID, &cmd.HostID, &cmd.CommandType, &cmd.Status, &cmd.RequestedBy,
			&requestedAt, &updatedAt,
			&cmd.FleetCommandUUID, &cmd.Result,
		); err != nil {
			h.logger.Warn("failed to scan device command row", zap.Error(err))
			continue
		}
		cmd.RequestedAt = requestedAt.UTC().Format(time.RFC3339)
		cmd.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
		commands = append(commands, cmd)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating device command rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"commands": commands})
}
