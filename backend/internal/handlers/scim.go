package handlers

// SCIM 2.0 Users and Groups endpoints — RFC 7644 / RFC 7643
//
// Scope: /scim/v2/Users and /scim/v2/Groups (plus org-scoped
// /scim/v2/orgs/{orgID}/… variants). Auth: dedicated bearer token from
// config.SCIMBearerToken (legacy Default Org) or per-org scim_bearer_tokens,
// checked by SCIMBearerMiddleware / SCIMOrgBearerMiddleware. These routes sit
// OUTSIDE the user-JWT group.
//
// The user lifecycle is delegated to the existing onboard/offboard logic so
// Keycloak + DB writes stay consistent.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
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
	Schemas    []string    `json:"schemas"`
	ID         string      `json:"id,omitempty"`
	ExternalID string      `json:"externalId,omitempty"`
	UserName   string      `json:"userName"`
	Name       scimName    `json:"name,omitempty"`
	Emails     []scimEmail `json:"emails,omitempty"`
	Active     bool        `json:"active"`
	Meta       scimMeta    `json:"meta,omitempty"`
}

// scimListResponse is the SCIM ListResponse envelope.
type scimListResponse struct {
	Schemas      []string   `json:"schemas"`
	TotalResults int        `json:"totalResults"`
	StartIndex   int        `json:"startIndex"`
	ItemsPerPage int        `json:"itemsPerPage"`
	Resources    []SCIMUser `json:"Resources"`
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
	scimUserSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimErrorSchema = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimContentType = "application/scim+json"
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

// SCIMBearerMiddleware returns middleware that enforces the legacy static SCIM
// bearer token (config.SCIMBearerToken). It is fail-closed: an empty token
// rejects ALL requests.
//
// C4 (Epic C multi-tenant): the legacy token authenticates on behalf of the
// Default Organization for backward compatibility with existing Okta/Entra
// integrations that predate multi-org support — see docs/adr/0005. It sets
// an OrgContext so every SCIM handler downstream can org-scope its query the
// same way whether the caller used the legacy path or a per-org token.
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
			if !constantTimeStringEqual(strings.TrimPrefix(auth, "Bearer "), token) {
				scimRespondError(w, http.StatusUnauthorized, "Invalid bearer token", "")
				return
			}
			ctx := middleware.SetOrgContext(r.Context(), &middleware.OrgContext{
				OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// scimOrgTokenDB is the minimal interface the org-scoped SCIM bearer
// middleware needs to look up a per-org token.
type scimOrgTokenDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// SCIMOrgBearerMiddleware authenticates the org-scoped SCIM base path
// (/scim/v2/orgs/{orgID}/...) against the scim_bearer_tokens table (C4,
// Migration043). The path's {orgID} must match the token's own org — a valid
// token for org A can never authenticate requests claiming to be for org B,
// which is exactly the cross-org SCIM-provisioning path this epic must not
// leave open.
func (h *Handler) SCIMOrgBearerMiddleware(db scimOrgTokenDB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pathOrgID := chi.URLParam(r, "orgID")
			if !isValidUUID(pathOrgID) {
				scimRespondError(w, http.StatusNotFound, "unknown organization", "")
				return
			}
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				scimRespondError(w, http.StatusUnauthorized, "Bearer token required", "")
				return
			}
			tokenStr := strings.TrimPrefix(auth, "Bearer ")
			if db == nil {
				scimRespondError(w, http.StatusServiceUnavailable, "SCIM provisioning is not configured", "")
				return
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(tokenStr)))
			var tokenOrgID string
			err := db.QueryRow(r.Context(),
				`SELECT org_id::TEXT FROM scim_bearer_tokens WHERE token_hash = $1 AND revoked_at IS NULL`,
				hash,
			).Scan(&tokenOrgID)
			if err != nil || tokenOrgID != pathOrgID {
				// Same 401 whether the token is unknown, revoked, or valid for a
				// DIFFERENT org — never leak which case it was to the caller.
				scimRespondError(w, http.StatusUnauthorized, "Invalid bearer token", "")
				return
			}
			ctx := middleware.SetOrgContext(r.Context(), &middleware.OrgContext{
				OrgID: tokenOrgID, Role: middleware.OrgMembershipRoleAdmin,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---- filter types (implementation in scim_filter.go) ----

// scimFilter is the simple flat filter representation used by the list
// handlers. ParseSCIMFilter in scim_filter.go produces it via the shim.
type scimFilter struct {
	attr  string
	op    string
	value string
}

// ---- handlers ----

// SCIMListUsers handles GET /scim/v2/Users
func (h *Handler) SCIMListUsers(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		scimRespondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}
	ctx := r.Context()

	// C4/C5: org-scoped read. Fail closed — no org context means no rows.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

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
	// filterOp and filterVal are used to build a SQL WHERE clause.
	// filterOp: "eq", "co", "sw" — derived from the SCIM filter node.
	var filterOp, filterVal string
	if filterRaw != "" {
		f := parseSCIMFilter(filterRaw)
		if f != nil && (f.attr == "username" || f.attr == "emails.value") {
			filterOp = f.op
			filterVal = strings.ToLower(f.value)
		}
	}

	query := `SELECT u.keycloak_user_id, u.email, u.first_name, u.last_name,
	                  COALESCE(u.disabled, false), u.created_at, u.updated_at,
	                  COALESCE(v.version, 1)
	           FROM users u
	           LEFT JOIN scim_resource_versions v ON v.user_id = u.keycloak_user_id
	           WHERE u.org_id = $1`
	args := []interface{}{oc.OrgID}
	argIdx := 2
	switch filterOp {
	case "eq":
		query += ` AND u.email = $` + strconv.Itoa(argIdx)
		args = append(args, filterVal)
		argIdx++
	case "co":
		query += ` AND u.email ILIKE $` + strconv.Itoa(argIdx)
		args = append(args, "%"+filterVal+"%")
		argIdx++
	case "sw":
		query += ` AND u.email ILIKE $` + strconv.Itoa(argIdx)
		args = append(args, filterVal+"%")
		argIdx++
	}
	query += ` ORDER BY u.created_at`
	offset := startIndex - 1
	if offset < 0 {
		offset = 0
	}
	// COUNT(*) with the same WHERE (before LIMIT) so SCIM clients paginate correctly.
	countQuery := `SELECT COUNT(*) FROM users u WHERE u.org_id = $1`
	countArgs := []interface{}{oc.OrgID}
	countArgIdx := 2
	switch filterOp {
	case "eq":
		countQuery += ` AND u.email = $` + strconv.Itoa(countArgIdx)
		countArgs = append(countArgs, filterVal)
		countArgIdx++
	case "co":
		countQuery += ` AND u.email ILIKE $` + strconv.Itoa(countArgIdx)
		countArgs = append(countArgs, "%"+filterVal+"%")
		countArgIdx++
	case "sw":
		countQuery += ` AND u.email ILIKE $` + strconv.Itoa(countArgIdx)
		countArgs = append(countArgs, filterVal+"%")
	}
	var totalResults int
	if err := h.db.QueryRow(ctx, countQuery, countArgs...).Scan(&totalResults); err != nil {
		h.logger.Error("scim list users count failed", zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
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
			disabled                       bool
			createdAt, updatedAt           time.Time
			version                        int64
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
		TotalResults: totalResults,
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

	// C4/C5: org-scoped read. Fail closed — no org context means "not found",
	// never an implicit cross-org lookup.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	var (
		id, email, firstName, lastName string
		disabled                       bool
		createdAt, updatedAt           time.Time
		version                        int64
	)
	err := h.db.QueryRow(ctx,
		`SELECT u.keycloak_user_id, u.email, u.first_name, u.last_name,
		        COALESCE(u.disabled, false), u.created_at, u.updated_at,
		        COALESCE(v.version, 1)
		 FROM users u
		 LEFT JOIN scim_resource_versions v ON v.user_id = u.keycloak_user_id
		 WHERE u.keycloak_user_id = $1 AND u.org_id = $2`,
		userID, oc.OrgID,
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

	// C4: the calling token's org (legacy bearer -> Default Org, per-org
	// bearer -> its own org — both are resolved into OrgContext by the
	// respective middleware in routes.go). Fail closed rather than guessing.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Idempotency: check for existing user. Email is unique realm-wide (see
	// docs/adr/0005), so this check is intentionally NOT org-scoped: a
	// duplicate email across orgs is always a conflict.
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
	if err := h.persistOnboard(persistCtx, kcUserID, onboardReq, "scim-provisioner", map[string]interface{}{
		"source": "scim",
	}, "", oc.OrgID); err != nil {
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
		disabled                     bool
		createdAt, updatedAt         time.Time
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

	// C4/C5: org-scoped write. Fail closed — no org context means "not found",
	// never an implicit cross-org lookup/mutation.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Load current user
	var (
		email, firstName, lastName string
		disabled                   bool
		createdAt, updatedAt       time.Time
		version                    int64
	)
	err := h.db.QueryRow(ctx,
		`SELECT u.email, u.first_name, u.last_name,
		        COALESCE(u.disabled, false), u.created_at, u.updated_at,
		        COALESCE(v.version, 1)
		 FROM users u
		 LEFT JOIN scim_resource_versions v ON v.user_id = u.keycloak_user_id
		 WHERE u.keycloak_user_id = $1 AND u.org_id = $2`, userID, oc.OrgID,
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
	newEmail := email
	for _, op := range patch.Operations {
		opLow := strings.ToLower(op.Op)
		pathLow := strings.ToLower(op.Path)

		// Detect filter-qualified paths like "emails[type eq \"work\"].value"
		// We strip the bracket segment and treat the remainder as a plain path.
		plainPath := pathLow
		if strings.Contains(pathLow, "[") {
			base, _ := ParseSCIMFilterPath(op.Path)
			if base != "" {
				// Resolve the leaf attribute after the bracket:
				// e.g. "emails[type eq \"work\"].value" → leaf is "emails.value"
				afterBracket := ""
				if close := strings.LastIndex(pathLow, "]"); close != -1 && close+1 < len(pathLow) {
					afterBracket = strings.TrimPrefix(pathLow[close+1:], ".")
				}
				if afterBracket != "" {
					plainPath = base + "." + afterBracket
				} else {
					plainPath = base
				}
			}
		}

		switch opLow {
		case "replace", "add":
			switch {
			case plainPath == "active":
				if b, ok := op.Value.(bool); ok {
					disabled = !b
				}
			case plainPath == "name.givenname":
				if s, ok := op.Value.(string); ok {
					firstName = strings.TrimSpace(s)
				}
			case plainPath == "name.familyname":
				if s, ok := op.Value.(string); ok {
					lastName = strings.TrimSpace(s)
				}
			case plainPath == "username":
				if s, ok := op.Value.(string); ok {
					v := strings.ToLower(strings.TrimSpace(s))
					if v != "" {
						newEmail = v
					}
				}
			case plainPath == "emails" || plainPath == "emails.value":
				// Value may be a string (direct), a single object, or array.
				switch v := op.Value.(type) {
				case string:
					if e := strings.ToLower(strings.TrimSpace(v)); e != "" {
						newEmail = e
					}
				case map[string]interface{}:
					if val, ok := v["value"].(string); ok {
						if e := strings.ToLower(strings.TrimSpace(val)); e != "" {
							newEmail = e
						}
					}
				case []interface{}:
					for _, item := range v {
						if m, ok := item.(map[string]interface{}); ok {
							if val, ok := m["value"].(string); ok {
								if e := strings.ToLower(strings.TrimSpace(val)); e != "" {
									newEmail = e
									break
								}
							}
						}
					}
				}
			case plainPath == "":
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
					if v, ok := m["userName"].(string); ok {
						if e := strings.ToLower(strings.TrimSpace(v)); e != "" {
							newEmail = e
						}
					}
				}
			}
		case "remove":
			// remove active → set disabled (treat as deactivation)
			// remove emails/userName → no-op (can't remove required attrs)
			if plainPath == "active" {
				disabled = true
			}
		}
	}

	// Propagate to Keycloak
	if err := h.keycloak.UpdateUser(ctx, userID, firstName, lastName, "", !disabled); err != nil {
		h.logger.Error("scim patch: keycloak update failed", zap.String("user_id", userID), zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "failed to update user in identity provider", "")
		return
	}

	// Persist changes. Include email if it changed.
	newVersion := version + 1
	if newEmail != email {
		_, dbErr := h.db.Exec(ctx,
			`UPDATE users SET email=$1, first_name=$2, last_name=$3, disabled=$4, updated_at=NOW()
			 WHERE keycloak_user_id=$5`,
			newEmail, firstName, lastName, disabled, userID)
		if dbErr != nil {
			h.logger.Warn("scim patch: db update (with email) failed", zap.String("user_id", userID), zap.Error(dbErr))
		}
		email = newEmail
	} else {
		_, dbErr := h.db.Exec(ctx,
			`UPDATE users SET first_name=$1, last_name=$2, disabled=$3, updated_at=NOW()
			 WHERE keycloak_user_id=$4`,
			firstName, lastName, disabled, userID)
		if dbErr != nil {
			h.logger.Warn("scim patch: db update failed", zap.String("user_id", userID), zap.Error(dbErr))
		}
	}
	_, _ = h.db.Exec(ctx,
		`INSERT INTO scim_resource_versions (user_id, version, updated_at) VALUES ($1, $2, NOW())
		 ON CONFLICT (user_id) DO UPDATE SET version = $2, updated_at = NOW()`,
		userID, newVersion)

	if err := h.writeAuditEntryBestEffort("scim-provisioner", "scim_patch_user", "user", userID, map[string]interface{}{
		"disabled": disabled,
	}); err != nil {
		h.logger.Warn("failed to write SCIM patch audit log", zap.Error(err))
	}

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

	// C4/C5: org-scoped write. Fail closed — no org context means "not found",
	// never an implicit cross-org lookup/mutation.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Verify existence
	var email string
	if err := h.db.QueryRow(ctx,
		`SELECT email FROM users WHERE keycloak_user_id = $1 AND org_id = $2`, userID, oc.OrgID,
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

	if err := h.writeAuditEntryBestEffort("scim-provisioner", "scim_delete_user", "user", userID, map[string]interface{}{
		"email": email,
	}); err != nil {
		h.logger.Warn("failed to write SCIM delete audit log", zap.Error(err))
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- SCIM Group types ----

// scimGroupMember is a SCIM Group member reference (RFC 7643 §4.2).
type scimGroupMember struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
	Ref     string `json:"$ref,omitempty"`
}

// SCIMGroup is the SCIM Group resource (RFC 7643 §4.2).
type SCIMGroup struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id,omitempty"`
	ExternalID  string            `json:"externalId,omitempty"`
	DisplayName string            `json:"displayName"`
	Members     []scimGroupMember `json:"members"`
	Meta        scimMeta          `json:"meta,omitempty"`
}

// scimGroupListResponse is the SCIM ListResponse for Groups.
type scimGroupListResponse struct {
	Schemas      []string    `json:"schemas"`
	TotalResults int         `json:"totalResults"`
	StartIndex   int         `json:"startIndex"`
	ItemsPerPage int         `json:"itemsPerPage"`
	Resources    []SCIMGroup `json:"Resources"`
}

// scimGroupFromKC converts a Keycloak group (with optional members) to a SCIMGroup.
func scimGroupFromKC(id, name string, members []scimGroupMember) SCIMGroup {
	return SCIMGroup{
		Schemas:     []string{scimGroupSchema},
		ID:          id,
		DisplayName: name,
		Members:     members,
		Meta:        scimMeta{ResourceType: "Group"},
	}
}

// SCIMListGroups handles GET /scim/v2/Groups
func (h *Handler) SCIMListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// C1: org-scoped read. Fail closed — no org context means no rows,
	// mirroring the User handlers (SCIMListUsers) — never fall back to
	// listing the whole realm's groups.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	startIndex := 1
	if v := r.URL.Query().Get("startIndex"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			startIndex = n
		}
	}
	// M7: honor + clamp count (max 1000), same bound as SCIMListUsers —
	// previously this was ignored entirely and the whole realm was fetched.
	count := 100
	if v := r.URL.Query().Get("count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			count = n
		}
	}

	filterRaw := r.URL.Query().Get("filter")
	var displayNameFilter string
	if filterRaw != "" {
		f := parseSCIMFilter(filterRaw)
		if f != nil && f.op == "eq" && strings.EqualFold(f.attr, "displayname") {
			displayNameFilter = f.value
		}
	}

	// M7/C1: fetch only THIS org's groups — never the whole realm. The org
	// filter is pushed into the Keycloak query itself (ListGroupsByOrg,
	// with a defensive re-check on the way back). startIndex/count paging
	// and the optional displayName filter are then applied in-process
	// against that already-org-scoped set, exactly as before this fix, just
	// no longer over every tenant's groups. maxOrgGroups bounds the single
	// upstream call; a truly huge single org would need real Keycloak-side
	// cursoring, but that's a pagination-completeness concern, not the
	// security fix this endpoint needed.
	const maxOrgGroups = 5000
	kcGroups, err := h.keycloak.ListGroupsByOrg(ctx, oc.OrgID, 0, maxOrgGroups)
	if err != nil {
		h.logger.Error("scim list groups: keycloak failed", zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	var groups []SCIMGroup
	for _, g := range kcGroups {
		if g.ID == nil || g.Name == nil {
			continue
		}
		if displayNameFilter != "" && !strings.EqualFold(*g.Name, displayNameFilter) {
			continue
		}
		groups = append(groups, scimGroupFromKC(*g.ID, *g.Name, []scimGroupMember{}))
	}
	if groups == nil {
		groups = []SCIMGroup{}
	}

	// Pagination (SCIM uses 1-based startIndex).
	offset := startIndex - 1
	if offset < 0 {
		offset = 0
	}
	total := len(groups)
	if offset < len(groups) {
		groups = groups[offset:]
	} else {
		groups = []SCIMGroup{}
	}
	if len(groups) > count {
		groups = groups[:count]
	}

	scimRespond(w, http.StatusOK, scimGroupListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: total,
		StartIndex:   startIndex,
		ItemsPerPage: len(groups),
		Resources:    groups,
	})
}

// SCIMGetGroup handles GET /scim/v2/Groups/{id}
func (h *Handler) SCIMGetGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if groupID == "" {
		scimRespondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	ctx := r.Context()

	// C1: org-scoped read. Fail closed — no org context means "not found".
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	g, err := h.keycloak.GetGroupByID(ctx, groupID)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			scimRespondError(w, http.StatusNotFound, "group not found", "")
			return
		}
		h.logger.Error("scim get group: keycloak failed", zap.String("group_id", groupID), zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	if g.ID == nil || g.Name == nil {
		scimRespondError(w, http.StatusInternalServerError, "incomplete group data", "")
		return
	}
	// C1: verify the group belongs to the caller's org — fail closed with
	// the SAME 404 as "doesn't exist" so a foreign-org group ID never
	// distinguishably leaks its existence.
	if keycloak.GroupOrgID(g) != oc.OrgID {
		scimRespondError(w, http.StatusNotFound, "group not found", "")
		return
	}

	members, err := h.keycloak.ListGroupMembers(ctx, groupID)
	if err != nil {
		h.logger.Warn("scim get group: failed to list members", zap.String("group_id", groupID), zap.Error(err))
		members = nil
	}

	scimMembers := make([]scimGroupMember, 0, len(members))
	for _, m := range members {
		if m.ID == nil {
			continue
		}
		display := ""
		if m.Email != nil {
			display = *m.Email
		}
		scimMembers = append(scimMembers, scimGroupMember{Value: *m.ID, Display: display})
	}

	grp := scimGroupFromKC(*g.ID, *g.Name, scimMembers)
	scimRespond(w, http.StatusOK, grp)
}

// scimUserInOrg reports whether userID belongs to orgID (per the users
// table), used by the SCIM Groups handlers (C1) to stop an org-scoped SCIM
// token from binding another tenant's user into one of its groups via a
// member add/replace/initial-members list. Fails closed: any error
// (including h.db being nil) is treated as "not verified" i.e. not in org.
func (h *Handler) scimUserInOrg(ctx context.Context, userID, orgID string) bool {
	ok, err := h.resourceInOrg(ctx, "users", "keycloak_user_id", userID, orgID)
	if err != nil {
		h.logger.Warn("scim: failed to verify member org membership; treating as not-in-org",
			zap.String("user_id", userID), zap.Error(err))
		return false
	}
	return ok
}

// SCIMCreateGroup handles POST /scim/v2/Groups
func (h *Handler) SCIMCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req SCIMGroup
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		scimRespondError(w, http.StatusBadRequest, "invalid JSON", "invalidValue")
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		scimRespondError(w, http.StatusBadRequest, "displayName is required", "invalidValue")
		return
	}
	ctx := r.Context()

	// C1: org-scoped write. Fail closed — no org context means no group.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	groupID, err := h.keycloak.CreateGroupWithOrgID(ctx, req.DisplayName, oc.OrgID)
	if err != nil {
		h.logger.Error("scim create group: keycloak failed", zap.String("name", req.DisplayName), zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "failed to create group", "")
		return
	}

	// Add initial members if provided — but only ones that actually belong
	// to the caller's org (C1): otherwise a per-org SCIM token could bind
	// another tenant's user into a group it just created in this org.
	var addedMembers []scimGroupMember
	for _, m := range req.Members {
		if m.Value == "" {
			continue
		}
		if !h.scimUserInOrg(ctx, m.Value, oc.OrgID) {
			h.logger.Warn("scim create group: skipped initial member outside caller's org",
				zap.String("group_id", groupID), zap.String("user_id", m.Value))
			continue
		}
		if err := h.keycloak.AddUserToGroup(ctx, m.Value, groupID); err != nil {
			h.logger.Warn("scim create group: failed to add initial member",
				zap.String("group_id", groupID), zap.String("user_id", m.Value), zap.Error(err))
			continue
		}
		addedMembers = append(addedMembers, m)
	}

	grp := scimGroupFromKC(groupID, req.DisplayName, addedMembers)
	if grp.Members == nil {
		grp.Members = []scimGroupMember{}
	}
	scimRespond(w, http.StatusCreated, grp)
}

// scimFilterUserIDsInOrg returns the subset of uids that belong to orgID,
// logging (and dropping) any that don't. Used by every SCIM Groups PATCH
// path that adds/removes/replaces members (C1) so an org-scoped SCIM token
// can never bind — or unbind — a foreign tenant's user via group membership.
func (h *Handler) scimFilterUserIDsInOrg(ctx context.Context, groupID, orgID string, uids []string) []string {
	out := make([]string, 0, len(uids))
	for _, uid := range uids {
		if h.scimUserInOrg(ctx, uid, orgID) {
			out = append(out, uid)
			continue
		}
		h.logger.Warn("scim patch group: skipped member outside caller's org",
			zap.String("group_id", groupID), zap.String("user_id", uid))
	}
	return out
}

// SCIMPatchGroup handles PATCH /scim/v2/Groups/{id}
// Supports: displayName replace, members add/remove.
func (h *Handler) SCIMPatchGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if groupID == "" {
		scimRespondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}

	var patch scimPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		scimRespondError(w, http.StatusBadRequest, "invalid JSON", "invalidValue")
		return
	}
	ctx := r.Context()

	// C1: org-scoped write. Fail closed — no org context means "not found".
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Verify group exists
	g, err := h.keycloak.GetGroupByID(ctx, groupID)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			scimRespondError(w, http.StatusNotFound, "group not found", "")
			return
		}
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	if g.ID == nil || g.Name == nil {
		scimRespondError(w, http.StatusInternalServerError, "incomplete group data", "")
		return
	}
	// C1: verify the group belongs to the caller's org — fail closed with
	// the SAME 404 as "doesn't exist" so a foreign-org group ID never
	// distinguishably leaks its existence.
	if keycloak.GroupOrgID(g) != oc.OrgID {
		scimRespondError(w, http.StatusNotFound, "group not found", "")
		return
	}

	displayName := *g.Name

	for _, op := range patch.Operations {
		opLow := strings.ToLower(op.Op)
		pathLow := strings.ToLower(op.Path)

		// Detect filter-qualified paths like "members[value eq \"user-id\"]".
		// If present, extract the target user ID from the inner filter.
		var filterQualifiedUserID string
		if strings.Contains(pathLow, "[") {
			base, inner := ParseSCIMFilterPath(op.Path)
			if base == "members" && inner != nil {
				// The inner filter should be something like: value eq "user-id"
				// We extract the value directly from the node.
				if inner.Op == scimOpEq && inner.Attr == "value" {
					filterQualifiedUserID = inner.Value
				}
				// Normalise the path so the switch below matches "members".
				pathLow = base
			}
		}

		switch {
		case (opLow == "replace" || opLow == "add") && (pathLow == "displayname" || pathLow == ""):
			// Replace displayName (direct or object)
			if pathLow == "displayname" {
				if s, ok := op.Value.(string); ok {
					displayName = strings.TrimSpace(s)
				}
			} else if m, ok := op.Value.(map[string]interface{}); ok {
				if v, ok := m["displayName"].(string); ok {
					displayName = strings.TrimSpace(v)
				}
			}

		case (opLow == "add") && pathLow == "members":
			if filterQualifiedUserID != "" {
				// Filter-qualified add: add a specific member by value filter.
				// C1: verify org membership before ever calling Keycloak.
				for _, uid := range h.scimFilterUserIDsInOrg(ctx, groupID, oc.OrgID, []string{filterQualifiedUserID}) {
					if err := h.keycloak.AddUserToGroup(ctx, uid, groupID); err != nil {
						h.logger.Warn("scim patch group: filter-qualified add member failed",
							zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
					}
				}
			} else {
				// Add members: value is []{"value": userID}. C1: only add
				// members that belong to the caller's org.
				userIDs := h.scimFilterUserIDsInOrg(ctx, groupID, oc.OrgID, extractMemberValues(op.Value))
				for _, uid := range userIDs {
					if err := h.keycloak.AddUserToGroup(ctx, uid, groupID); err != nil {
						h.logger.Warn("scim patch group: add member failed",
							zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
					}
				}
			}

		case (opLow == "remove") && pathLow == "members":
			if filterQualifiedUserID != "" {
				// Filter-qualified remove: remove a specific member. C1: only
				// act on a user that belongs to the caller's org.
				for _, uid := range h.scimFilterUserIDsInOrg(ctx, groupID, oc.OrgID, []string{filterQualifiedUserID}) {
					if err := h.keycloak.RemoveUserFromGroup(ctx, uid, groupID); err != nil {
						h.logger.Warn("scim patch group: filter-qualified remove member failed",
							zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
					}
				}
			} else {
				// Remove members: value may be []{"value": userID} or empty
				// (remove all, not supported). C1: org-verify each target.
				userIDs := h.scimFilterUserIDsInOrg(ctx, groupID, oc.OrgID, extractMemberValues(op.Value))
				for _, uid := range userIDs {
					if err := h.keycloak.RemoveUserFromGroup(ctx, uid, groupID); err != nil {
						h.logger.Warn("scim patch group: remove member failed",
							zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
					}
				}
			}

		case (opLow == "replace") && pathLow == "members":
			// Replace all members: first remove all, then add new set. The
			// removal side doesn't need an org check (it's un-binding
			// whatever is already there); the new set does (C1), same as add.
			existing, _ := h.keycloak.ListGroupMembers(ctx, groupID)
			for _, m := range existing {
				if m.ID == nil {
					continue
				}
				if err := h.keycloak.RemoveUserFromGroup(ctx, *m.ID, groupID); err != nil {
					h.logger.Warn("scim patch group: remove existing member failed",
						zap.String("group_id", groupID), zap.String("user_id", *m.ID), zap.Error(err))
				}
			}
			userIDs := h.scimFilterUserIDsInOrg(ctx, groupID, oc.OrgID, extractMemberValues(op.Value))
			for _, uid := range userIDs {
				if err := h.keycloak.AddUserToGroup(ctx, uid, groupID); err != nil {
					h.logger.Warn("scim patch group: add replacement member failed",
						zap.String("group_id", groupID), zap.String("user_id", uid), zap.Error(err))
				}
			}
		}
	}

	// Rename if displayName changed
	if displayName != *g.Name && displayName != "" {
		if err := h.keycloak.RenameGroup(ctx, groupID, displayName); err != nil {
			h.logger.Error("scim patch group: rename failed",
				zap.String("group_id", groupID), zap.String("name", displayName), zap.Error(err))
			scimRespondError(w, http.StatusInternalServerError, "failed to rename group", "")
			return
		}
	}

	// Return current state
	members, _ := h.keycloak.ListGroupMembers(ctx, groupID)
	scimMembers := make([]scimGroupMember, 0, len(members))
	for _, m := range members {
		if m.ID == nil {
			continue
		}
		display := ""
		if m.Email != nil {
			display = *m.Email
		}
		scimMembers = append(scimMembers, scimGroupMember{Value: *m.ID, Display: display})
	}

	grp := scimGroupFromKC(groupID, displayName, scimMembers)
	scimRespond(w, http.StatusOK, grp)
}

// SCIMDeleteGroup handles DELETE /scim/v2/Groups/{id}
func (h *Handler) SCIMDeleteGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	if groupID == "" {
		scimRespondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	ctx := r.Context()

	// C1: org-scoped write. Fail closed — no org context means "not found".
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		scimRespondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Verify existence and ownership
	g, err := h.keycloak.GetGroupByID(ctx, groupID)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			scimRespondError(w, http.StatusNotFound, "group not found", "")
			return
		}
		scimRespondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	// C1: verify the group belongs to the caller's org — fail closed with
	// the SAME 404 as "doesn't exist" so a foreign-org group ID never
	// distinguishably leaks its existence.
	if keycloak.GroupOrgID(g) != oc.OrgID {
		scimRespondError(w, http.StatusNotFound, "group not found", "")
		return
	}

	if err := h.keycloak.DeleteGroup(ctx, groupID); err != nil {
		h.logger.Error("scim delete group: keycloak failed", zap.String("group_id", groupID), zap.Error(err))
		scimRespondError(w, http.StatusInternalServerError, "failed to delete group", "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// extractMemberValues extracts user IDs from a SCIM members PATCH value.
// Handles: []interface{}{map{"value": uid}}, or a single map{"value": uid}.
func extractMemberValues(v interface{}) []string {
	var out []string
	switch arr := v.(type) {
	case []interface{}:
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if uid, ok := m["value"].(string); ok && uid != "" {
					out = append(out, uid)
				}
			}
		}
	case map[string]interface{}:
		if uid, ok := arr["value"].(string); ok && uid != "" {
			out = append(out, uid)
		}
	}
	return out
}

// isNotFound checks if a pgx error is a not-found (ErrNoRows) condition.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no rows") || err.Error() == "no rows in result set"
}
