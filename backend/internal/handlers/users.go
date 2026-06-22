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
	Disabled       bool     `json:"disabled"`
	CreatedAt      string   `json:"createdAt,omitempty"`
	UpdatedAt      string   `json:"updatedAt,omitempty"`
	Devices        []Device `json:"devices,omitempty"`
	// C2: federated-user awareness
	IsFederated    bool   `json:"isFederated,omitempty"`
	FederationLink string `json:"federationLink,omitempty"`
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
//
// Devices are fetched in the same query via a LEFT JOIN to avoid an N+1
// pattern (previously one extra query per user).
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.db == nil {
		respondJSON(w, http.StatusOK, []User{})
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT u.keycloak_user_id, u.email, u.first_name, u.last_name,
		        COALESCE(u.department, ''), COALESCE(u.role, ''), COALESCE(u.disabled, false),
		        u.created_at, u.updated_at,
		        COALESCE(d.fleet_host_id::TEXT, ''),
		        COALESCE(d.hostname, ''),
		        COALESCE(d.os_version, ''),
		        COALESCE(d.last_seen_at::TEXT, ''),
		        COALESCE(d.created_at::TEXT, '')
		 FROM users u
		 LEFT JOIN users_devices_mapping m ON m.user_id = u.keycloak_user_id
		 LEFT JOIN devices d ON d.fleet_host_id = m.device_id
		 ORDER BY u.created_at DESC, d.hostname`,
	)
	if err != nil {
		h.logger.Error("failed to query users", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	// Preserve insertion order while collapsing duplicate user rows.
	order := make([]string, 0, 16)
	usersByID := make(map[string]*User)

	for rows.Next() {
		var (
			keycloakUserID, email, firstName, lastName, dept, role string
			disabled                                               bool
			createdAt, updatedAt                                   time.Time
			deviceID, hostname, osVersion, lastSeen, devCreated    string
		)
		if err := rows.Scan(&keycloakUserID, &email, &firstName, &lastName,
			&dept, &role, &disabled, &createdAt, &updatedAt,
			&deviceID, &hostname, &osVersion, &lastSeen, &devCreated,
		); err != nil {
			h.logger.Error("failed to scan user row", zap.Error(err))
			continue
		}

		u, ok := usersByID[keycloakUserID]
		if !ok {
			u = &User{
				ID:             keycloakUserID,
				KeycloakUserID: keycloakUserID,
				Email:          email,
				FirstName:      firstName,
				LastName:       lastName,
				Department:     dept,
				Role:           role,
				Disabled:       disabled,
				CreatedAt:      createdAt.Format(time.RFC3339),
				UpdatedAt:      updatedAt.Format(time.RFC3339),
				Devices:        []Device{},
			}
			usersByID[keycloakUserID] = u
			order = append(order, keycloakUserID)
		}

		if deviceID != "" {
			u.Devices = append(u.Devices, Device{
				FleetHostID: deviceID,
				Hostname:    hostname,
				OsVersion:   osVersion,
				LastSeenAt:  lastSeen,
				CreatedAt:   devCreated,
			})
		}
	}

	if err := rows.Err(); err != nil {
		h.logger.Error("error iterating users", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	result := make([]User, 0, len(order))
	for _, id := range order {
		result = append(result, *usersByID[id])
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
		        COALESCE(department, ''), COALESCE(role, ''), COALESCE(disabled, false), created_at, updated_at
	 FROM users WHERE keycloak_user_id = $1`,
		userID,
	).Scan(&u.KeycloakUserID, &u.Email, &u.FirstName, &u.LastName,
		&u.Department, &u.Role, &u.Disabled, &createdAt, &updatedAt)

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

	// C2: annotate federated-identity status from Keycloak.
	if h.keycloak != nil {
		if kcUser, kcErr := h.keycloak.GetUserByID(ctx, userID); kcErr == nil && kcUser != nil && kcUser.FederationLink != nil && *kcUser.FederationLink != "" {
			u.IsFederated = true
			u.FederationLink = *kcUser.FederationLink
		}
	}

	respondJSON(w, http.StatusOK, u)
}
