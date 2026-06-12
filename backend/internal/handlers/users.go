package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// User represents a row in the users table along with associated devices.
type User struct {
	ID             string   `json:"id"`
	KeycloakUserID string   `json:"keycloakUserId"`
	Email          string   `json:"email"`
	FirstName      string   `json:"firstName"`
	LastName       string   `json:"lastName"`
	Department     string   `json:"department,omitempty"`
	Role           string   `json:"role,omitempty"`
	CreatedAt      string   `json:"createdAt,omitempty"`
	UpdatedAt      string   `json:"updatedAt,omitempty"`
	Devices        []Device `json:"devices,omitempty"`
}

// Device represents a row in the devices table.
type Device struct {
	FleetHostID string `json:"fleetHostId"`
	Hostname    string `json:"hostname,omitempty"`
	OsVersion   string `json:"osVersion,omitempty"`
	LastSeenAt  string `json:"lastSeenAt,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// ListUsers returns all users with their associated devices.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.db == nil {
		respondJSON(w, http.StatusOK, []User{})
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT u.keycloak_user_id, u.email, u.first_name, u.last_name,
		        COALESCE(u.department, ''), COALESCE(u.role, ''),
		        u.created_at, u.updated_at
		 FROM users u
		 ORDER BY u.created_at DESC`,
	)
	if err != nil {
		h.logger.Error("failed to query users", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type userRow struct {
		KeycloakUserID string
		Email          string
		FirstName      string
		LastName       string
		Department     string
		Role           string
		CreatedAt      time.Time
		UpdatedAt      time.Time
	}

	var users []userRow
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.KeycloakUserID, &u.Email, &u.FirstName, &u.LastName,
			&u.Department, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			h.logger.Error("failed to scan user", zap.Error(err))
			continue
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating users", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	result := make([]User, 0, len(users))
	for _, u := range users {
		// Fetch devices for each user via LEFT JOIN
		deviceRows, err := h.db.Query(ctx,
			`SELECT d.fleet_host_id, COALESCE(d.hostname, ''), COALESCE(d.os_version, ''),
			        COALESCE(d.last_seen_at::TEXT, ''), COALESCE(d.created_at::TEXT, '')
			 FROM devices d
			 INNER JOIN users_devices_mapping m ON d.fleet_host_id = m.device_id
			 WHERE m.user_id = $1`,
			u.KeycloakUserID,
		)
		devices := []Device{}
		if err == nil {
			for deviceRows.Next() {
				var d Device
				if err := deviceRows.Scan(&d.FleetHostID, &d.Hostname, &d.OsVersion, &d.LastSeenAt, &d.CreatedAt); err != nil {
					h.logger.Warn("failed to scan device", zap.Error(err))
					continue
				}
				devices = append(devices, d)
			}
			deviceRows.Close()
		} else {
			h.logger.Warn("failed to query devices for user", zap.String("user_id", u.KeycloakUserID), zap.Error(err))
		}

		result = append(result, User{
			ID:             u.KeycloakUserID,
			KeycloakUserID: u.KeycloakUserID,
			Email:          u.Email,
			FirstName:      u.FirstName,
			LastName:       u.LastName,
			Department:     u.Department,
			Role:           u.Role,
			CreatedAt:      u.CreatedAt.Format(time.RFC3339),
			UpdatedAt:      u.UpdatedAt.Format(time.RFC3339),
			Devices:        devices,
		})
	}

	respondJSON(w, http.StatusOK, result)
}

// GetUser returns a single user by keycloak_user_id with associated devices.
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		respondError(w, http.StatusBadRequest, "id is required")
		return
	}

	ctx := r.Context()

	if h.db == nil {
		respondError(w, http.StatusNotFound, "user not found")
		return
	}

	var u User
	var createdAt, updatedAt time.Time
	err := h.db.QueryRow(ctx,
		`SELECT keycloak_user_id, email, first_name, last_name,
		        COALESCE(department, ''), COALESCE(role, ''), created_at, updated_at
		 FROM users WHERE keycloak_user_id = $1`,
		userID,
	).Scan(&u.KeycloakUserID, &u.Email, &u.FirstName, &u.LastName,
		&u.Department, &u.Role, &createdAt, &updatedAt)

	if err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("failed to query user", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	u.CreatedAt = createdAt.Format(time.RFC3339)
	u.UpdatedAt = updatedAt.Format(time.RFC3339)
	u.ID = u.KeycloakUserID

	// Fetch devices
	deviceRows, err := h.db.Query(ctx,
		`SELECT d.fleet_host_id, COALESCE(d.hostname, ''), COALESCE(d.os_version, ''),
		        COALESCE(d.last_seen_at::TEXT, ''), COALESCE(d.created_at::TEXT, '')
		 FROM devices d
		 INNER JOIN users_devices_mapping m ON d.fleet_host_id = m.device_id
		 WHERE m.user_id = $1`,
		userID,
	)
	if err != nil {
		h.logger.Warn("failed to query devices for user", zap.String("user_id", userID), zap.Error(err))
	} else {
		defer deviceRows.Close()
		for deviceRows.Next() {
			var d Device
			if err := deviceRows.Scan(&d.FleetHostID, &d.Hostname, &d.OsVersion, &d.LastSeenAt, &d.CreatedAt); err != nil {
				h.logger.Warn("failed to scan device", zap.Error(err))
				continue
			}
			u.Devices = append(u.Devices, d)
		}
	}

	if u.Devices == nil {
		u.Devices = []Device{}
	}

	respondJSON(w, http.StatusOK, u)
}
