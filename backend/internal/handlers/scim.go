package handlers

// SCIM 2.0 /Users endpoint — RFC 7644 / RFC 7643
//
// Scope: Users resource only. /scim/v2/Groups is deferred (stub 501 below).
// Auth: dedicated bearer token from config.SCIMBearerToken, checked by
//       SCIMBearerMiddleware. These routes sit OUTSIDE the user-JWT group.
//
// The user lifecycle is delegated to the existing onboard/offboard logic so
// Keycloak + DB writes stay consistent.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// ---- SCIM JSON types ----

// scimMeta is the SCIM resource metadata block.
type scimMeta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Version      string `json:"version,omitempty"`
	Location     string `json:"location,omitempty"`
}

// scimName is the SCIM User name sub-attribute.
type scimName struct {
	Formatted  string `json:"formatted,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
}

// scimEmail is a SCIM multi-value email entry.
type scimEmail struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

// SCIMUser is the SCIM User resource representation (RFC 7643 §4.1).
type SCIMUser struct {
	Schemas    []string   `json:"schemas"`
	ID         string     `json:"id,omitempty"`
	ExternalID string     `json:"externalId,omitempty"`
	UserName   string     `json:"userName"`
	Name       scimName   `json:"name,omitempty"`
	Emails     []scimEmail `json:"emails,omitempty"`
	Active     bool       `json:"active"`
	Meta       scimMeta   `json:"meta,omitempty"`
}

// scimListResponse is the SCIM ListResponse envelope.
type scimListResponse struct {
	Schemas      []string    `json:"schemas"`
	TotalResults int         `json:"totalResults"`
	StartIndex   int         `json:"startIndex"`
	ItemsPerPage int         `json:"itemsPerPage"`
	Resources    []SCIMUser  `json:"Resources"`
}

// scimError is the SCIM error response (RFC 7644 §3.12).
type scimError struct {
	Schemas  []string `json:"schemas"`
	Status   string   `json:"status"`
	Detail   string   `json:"detail,omitempty"`
	ScimType string   `json:"scimType,omitempty"`
}

// scimPatchOp is a single PATCH operation.
type scimPatchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path,omitempty"`
	Value interface{} `json:"value"`
}

// scimPatchRequest is the SCIM PATCH body.
type scimPatchRequest struct {
	Schemas    []string      `json:"schemas"`
	Operations []scimPatchOp `json:"Operations"`
}

const (
	scimUserSchema     = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimListSchema     = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema    = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimErrorSchema    = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimContentType    = "application/scim+json"
)

// ---- helpers ----

func scimRespondError(w http.ResponseWriter, status int, detail, scimType string) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(scimError{
		Schemas:  []string{scimErrorSchema},
		Status:   strconv.Itoa(status),
		Detail:   detail,
		ScimType: scimType,
	})
}

func scimRespond(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func scimUserFromRow(id, email, firstName, lastName string, disabled bool, createdAt, updatedAt time.Time, version int64) SCIMUser {
	return SCIMUser{
		Schemas:  []string{scimUserSchema},
		ID:       id,
		UserName: email,
		Name: scimName{
			GivenName:  firstName,
			FamilyName: lastName,
			Formatted:  firstName + " " + lastName,
		},
		Emails: []scimEmail{{Value: email, Type: "work", Primary: true}},
		Active: !disabled,
		Meta: scimMeta{
			ResourceType: "User",
			Created:      createdAt.Format(time.RFC3339),
			LastModified: updatedAt.Format(time.RFC3339),
			Version:      fmt.Sprintf("W/\"%d\"", version),
		},
	}
}

// SCIMBearerMiddleware returns middleware that enforces the SCIM bearer token.
// It is fail-closed: an empty token rejects ALL requests.
func SCIMBearerMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				scimRespondError(w, http.StatusServiceUnavailable, "SCIM provisioning is not configured", "")
				return
			}
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				scimRespondError(w, http.StatusUnauthorized, "Bearer token required", "")
				return
			}
			if strings.TrimPrefix(auth, "Bearer ") != token {
				scimRespondError(w, http.StatusUnauthorized, "Invalid bearer token", "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- filter parsing ----
// Supports only: userName eq "value"  (case-insensitive attribute name)
type scimFilter struct {
	attr  string
	op    string
	value string
}

func parseSCIMFilter(raw string) *scimFilter {
	// Very minimal: "attr op \"value\""
	parts := strings.Fields(raw)
	if len(parts) < 3 {
		return nil
	}
	val := strings.Join(parts[2:], " ")
	val = strings.Trim(val, "\"'")
	return &scimFilter{
		attr:  strings.ToLower(parts[0]),
		op:    strings.ToLower(parts[1]),
		value: val,
	}
}

// ---- handlers ----

// SCIMListUsers handles GET /scim/v2/Users
func (h *Handler) SCIMListUsers(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		scimRespondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}
	ctx := r.Context()

	startIndex := 1
	if v := r.URL.Query().Get("startIndex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			startIndex = n
		}
	}
	count := 100
	if v := r.URL.Query().Get("count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			count = n
		}
	}

	filterRaw := r.URL.Query().Get("filter")
	var emailFilter string
	if filterRaw != "" {
		f := parseSCIMFilter(filterRaw)
		if f != nil && f.op == "eq" && (f.attr == "username" || f.attr == "emails.value") {
			emailFilter = strings.ToLower(f.value)
		}
	}

	query := `SELECT u.keycloak_user_id, u.email, u.first_name, u.last_name,
	                  COALESCE(u.disabled, false), u.created_at, u.updated_at,
	                  COALESCE(v.version, 1)
	           FROM users u
	           LEFT JOIN scim_resource_versions v ON v.user_id = u.keycloak_user_id`
	args := []interface{}{}
	argIdx := 1
	if emailFilter != "" {
		query += ` WHERE u.email = $` + strconv.Itoa(argIdx)
		args = append(args, emailFilter)
		argIdx++
	}
	query += ` ORDER BY u.created_at`
	offset := startIndex - 1
	if offset < 0 {
		offset = 0
	}
	query += fmt.Sprintf(` LIMIT $%d OFFSET $%d`, argIdx, argIdx+1)
	args = append(args, count, offset)

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.logger.Error("scim list users query failed", zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	defer rows.Close()

	var users []SCIMUser
	for rows.Next() {
		var (
			id, email, firstName, lastName string
			disabled                        bool
			createdAt, updatedAt            time.Time
			version                         int64
		)
		if err := rows.Scan(&id, &email, &firstName, &lastName, &disabled, &createdAt, &updatedAt, &version); err != nil {
			h.logger.Warn("scim list users scan failed", zap.Error(err))
			continue
		}
		users = append(users, scimUserFromRow(id, email, firstName, lastName, disabled, createdAt, updatedAt, version))
	}
	if err := rows.Err(); err != nil {
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	if users == nil {
		users = []SCIMUser{}
	}

	scimRespond(w, http.StatusOK, scimListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: len(users),
		StartIndex:   startIndex,
		ItemsPerPage: len(users),
		Resources:    users,
	})
}

// SCIMGetUser handles GET /scim/v2/Users/{id}
func (h *Handler) SCIMGetUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		scimRespondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	if h.db == nil {
		scimRespondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}
	ctx := r.Context()

	var (
		id, email, firstName, lastName string
		disabled                        bool
		createdAt, updatedAt            time.Time
		version                         int64
	)
	err := h.db.QueryRow(ctx,
		`SELECT u.keycloak_user_id, u.email, u.first_name, u.last_name,
		        COALESCE(u.disabled, false), u.created_at, u.updated_at,
		        COALESCE(v.version, 1)
		 FROM users u
		 LEFT JOIN scim_resource_versions v ON v.user_id = u.keycloak_user_id
		 WHERE u.keycloak_user_id = $1`,
		userID,
	).Scan(&id, &email, &firstName, &lastName, &disabled, &createdAt, &updatedAt, &version)
	if err != nil {
		if isNotFound(err) {
			scimRespondError(w, http.StatusNotFound, "user not found", "")
			return
		}
		h.logger.Error("scim get user query failed", zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	u := scimUserFromRow(id, email, firstName, lastName, disabled, createdAt, updatedAt, version)
	w.Header().Set("ETag", u.Meta.Version)
	scimRespond(w, http.StatusOK, u)
}

// SCIMCreateUser handles POST /scim/v2/Users — maps to the onboard flow.
func (h *Handler) SCIMCreateUser(w http.ResponseWriter, r *http.Request) {
	var req SCIMUser
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		scimRespondError(w, http.StatusBadRequest, "invalid JSON", "invalidValue")
		return
	}

	// Normalise — userName is required, email falls back to userName
	req.UserName = strings.ToLower(strings.TrimSpace(req.UserName))
	email := req.UserName
	for _, e := range req.Emails {
		if e.Primary || email == "" {
			email = strings.ToLower(strings.TrimSpace(e.Value))
		}
	}
	if email == "" || !isValidEmail(email) {
		scimRespondError(w, http.StatusBadRequest, "userName must be a valid email address", "invalidValue")
		return
	}

	if h.db == nil {
		scimRespondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}

	firstName := strings.TrimSpace(req.Name.GivenName)
	lastName := strings.TrimSpace(req.Name.FamilyName)
	if firstName == "" {
		firstName = "Unknown"
	}
	if lastName == "" {
		lastName = "Unknown"
	}

	ctx := r.Context()

	// Idempotency: check for existing user
	var existingID string
	if err := h.db.QueryRow(ctx,
		`SELECT keycloak_user_id FROM users WHERE email = $1`, email,
	).Scan(&existingID); err == nil {
		scimRespondError(w, http.StatusConflict, "user with this userName already exists", "uniqueness")
		return
	}

	// Create user in Keycloak via existing logic
	result, err := h.keycloak.CreateUser(ctx, firstName, lastName, email, "")
	if err != nil {
		h.logger.Error("scim create user: keycloak failed", zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "failed to create user in identity provider", "")
		return
	}
	if result.User == nil || result.User.ID == nil || *result.User.ID == "" {
		scimRespondError(w, http.StatusInternalServerError, "identity provider returned empty user ID", "")
		return
	}
	kcUserID := *result.User.ID

	// Rollback on DB failure
	persisted := false
	defer func() {
		if persisted {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if delErr := h.keycloak.DeleteUser(cleanupCtx, kcUserID); delErr != nil {
			h.logger.Error("scim create: failed to roll back keycloak user",
				zap.String("kc_user_id", kcUserID), zap.Error(delErr))
		}
	}()

	// Persist using the same approach as onboard
	onboardReq := OnboardRequest{
		FirstName:  firstName,
		LastName:   lastName,
		Email:      email,
		Department: "",
		Role:       "",
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.persistOnboard(persistCtx, kcUserID, onboardReq, "scim-provisioner", `{"source":"scim"}`, ""); err != nil {
		h.logger.Error("scim create: persist failed", zap.String("kc_user_id", kcUserID), zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "failed to persist user", "")
		return
	}

	// Init SCIM version row
	_, _ = h.db.Exec(persistCtx,
		`INSERT INTO scim_resource_versions (user_id, version) VALUES ($1, 1)
		 ON CONFLICT (user_id) DO NOTHING`, kcUserID)

	persisted = true

	// Fetch the just-created row for canonical timestamps
	var (
		id, dbEmail, dbFirst, dbLast string
		disabled                      bool
		createdAt, updatedAt          time.Time
	)
	version := int64(1)
	_ = h.db.QueryRow(persistCtx,
		`SELECT keycloak_user_id, email, first_name, last_name, COALESCE(disabled,false), created_at, updated_at
		 FROM users WHERE keycloak_user_id = $1`, kcUserID,
	).Scan(&id, &dbEmail, &dbFirst, &dbLast, &disabled, &createdAt, &updatedAt)
	if id == "" {
		id = kcUserID
		dbEmail = email
		dbFirst = firstName
		dbLast = lastName
		createdAt = time.Now()
		updatedAt = createdAt
	}

	created := scimUserFromRow(id, dbEmail, dbFirst, dbLast, disabled, createdAt, updatedAt, version)
	w.Header().Set("ETag", created.Meta.Version)
	scimRespond(w, http.StatusCreated, created)
}

// SCIMPatchUser handles PATCH /scim/v2/Users/{id}
func (h *Handler) SCIMPatchUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		scimRespondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	if h.db == nil {
		scimRespondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}

	var patch scimPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		scimRespondError(w, http.StatusBadRequest, "invalid JSON", "invalidValue")
		return
	}

	ctx := r.Context()

	// Load current user
	var (
		email, firstName, lastName string
		disabled                    bool
		createdAt, updatedAt        time.Time
		version                     int64
	)
	err := h.db.QueryRow(ctx,
		`SELECT u.email, u.first_name, u.last_name,
		        COALESCE(u.disabled, false), u.created_at, u.updated_at,
		        COALESCE(v.version, 1)
		 FROM users u
		 LEFT JOIN scim_resource_versions v ON v.user_id = u.keycloak_user_id
		 WHERE u.keycloak_user_id = $1`, userID,
	).Scan(&email, &firstName, &lastName, &disabled, &createdAt, &updatedAt, &version)
	if err != nil {
		if isNotFound(err) {
			scimRespondError(w, http.StatusNotFound, "user not found", "")
			return
		}
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// Apply operations
	for _, op := range patch.Operations {
		switch strings.ToLower(op.Op) {
		case "replace", "add":
			switch {
			case strings.EqualFold(op.Path, "active"):
				if b, ok := op.Value.(bool); ok {
					disabled = !b
				}
			case strings.EqualFold(op.Path, "name.givenName"):
				if s, ok := op.Value.(string); ok {
					firstName = strings.TrimSpace(s)
				}
			case strings.EqualFold(op.Path, "name.familyName"):
				if s, ok := op.Value.(string); ok {
					lastName = strings.TrimSpace(s)
				}
			case op.Path == "":
				// Value is an object — merge fields
				if m, ok := op.Value.(map[string]interface{}); ok {
					if v, ok := m["active"].(bool); ok {
						disabled = !v
					}
					if n, ok := m["name"].(map[string]interface{}); ok {
						if v, ok := n["givenName"].(string); ok {
							firstName = strings.TrimSpace(v)
						}
						if v, ok := n["familyName"].(string); ok {
							lastName = strings.TrimSpace(v)
						}
					}
				}
			}
		}
	}

	// Propagate to Keycloak
	if err := h.keycloak.UpdateUser(ctx, userID, firstName, lastName, "", !disabled); err != nil {
		h.logger.Error("scim patch: keycloak update failed", zap.String("user_id", userID), zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "failed to update user in identity provider", "")
		return
	}

	// If deactivating, also disable via Keycloak's dedicated path (already done via UpdateUser above)
	// and soft-disable in DB
	newVersion := version + 1
	_, dbErr := h.db.Exec(ctx,
		`UPDATE users SET first_name=$1, last_name=$2, disabled=$3, updated_at=NOW()
		 WHERE keycloak_user_id=$4`,
		firstName, lastName, disabled, userID)
	if dbErr != nil {
		h.logger.Warn("scim patch: db update failed", zap.String("user_id", userID), zap.Error(dbErr))
	}
	_, _ = h.db.Exec(ctx,
		`INSERT INTO scim_resource_versions (user_id, version, updated_at) VALUES ($1, $2, NOW())
		 ON CONFLICT (user_id) DO UPDATE SET version = $2, updated_at = NOW()`,
		userID, newVersion)

	// Write audit log (detached context)
	auditCtx, acancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer acancel()
	_, _ = h.db.Exec(auditCtx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		"scim-provisioner", "scim_patch_user", "user", userID,
		map[string]interface{}{"disabled": disabled})

	updatedAt = time.Now()
	u := scimUserFromRow(userID, email, firstName, lastName, disabled, createdAt, updatedAt, newVersion)
	w.Header().Set("ETag", u.Meta.Version)
	scimRespond(w, http.StatusOK, u)
}

// SCIMDeleteUser handles DELETE /scim/v2/Users/{id} — maps to the offboard flow.
func (h *Handler) SCIMDeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		scimRespondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	if h.db == nil {
		scimRespondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}
	ctx := r.Context()

	// Verify existence
	var email string
	if err := h.db.QueryRow(ctx,
		`SELECT email FROM users WHERE keycloak_user_id = $1`, userID,
	).Scan(&email); err != nil {
		if isNotFound(err) {
			scimRespondError(w, http.StatusNotFound, "user not found", "")
			return
		}
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// Disable in Keycloak (SCIM delete → deactivate, not hard-delete, to preserve audit trail)
	if err := h.keycloak.DisableUser(ctx, userID); err != nil {
		h.logger.Error("scim delete: disable failed", zap.String("user_id", userID), zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "failed to deactivate user", "")
		return
	}
	_, _ = h.db.Exec(ctx,
		`UPDATE users SET disabled=true, updated_at=NOW() WHERE keycloak_user_id=$1`, userID)

	// Audit (detached context)
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = h.db.Exec(auditCtx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		"scim-provisioner", "scim_delete_user", "user", userID,
		map[string]interface{}{"email": email})

	w.WriteHeader(http.StatusNoContent)
}

// SCIMGroupsStub returns 501 — Groups support is deferred.
func (h *Handler) SCIMGroupsStub(w http.ResponseWriter, r *http.Request) {
	scimRespondError(w, http.StatusNotImplemented, "/scim/v2/Groups is not yet implemented", "")
}

// isNotFound checks if a pgx error is a not-found (ErrNoRows) condition.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no rows") || err.Error() == "no rows in result set"
}
