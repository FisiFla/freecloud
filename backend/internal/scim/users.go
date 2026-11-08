package scim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ---- helpers ----

func respondError(w http.ResponseWriter, status int, detail, scimType string) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Schemas:  []string{SchemaError},
		Status:   strconv.Itoa(status),
		Detail:   detail,
		ScimType: scimType,
	})
}

func userFromRow(id, email, firstName, lastName string, disabled bool, createdAt, updatedAt time.Time, version int64) User {
	return User{
		Schemas:  []string{SchemaUser},
		ID:       id,
		UserName: email,
		Name: Name{
			GivenName:  firstName,
			FamilyName: lastName,
			Formatted:  firstName + " " + lastName,
		},
		Emails: []Email{{Value: email, Type: "work", Primary: true}},
		Active: !disabled,
		Meta: Meta{
			ResourceType: "User",
			Created:      createdAt.Format(time.RFC3339),
			LastModified: updatedAt.Format(time.RFC3339),
			Version:      fmt.Sprintf("W/\"%d\"", version),
		},
	}
}

// ---- handlers ----

// ListUsers handles GET /scim/v2/Users
func (s *Service) ListUsers(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}
	ctx := r.Context()

	// C4/C5: org-scoped read. Fail closed — no org context means no rows.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
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
		// A logical filter (and/or/not) cannot be expressed as a single
		// SQL predicate. Ignoring it silently returned every user instead
		// of the filtered set, so it is rejected with 400 invalidFilter as
		// RFC 7644 §3.4.2.2 requires.
		f, err := ParseFlatFilterStrict(filterRaw)
		if err != nil {
			respondError(w, http.StatusBadRequest, "filter: logical expressions (and/or/not) are not supported", "invalidFilter")
			return
		}
		if f != nil && (f.Attr == "username" || f.Attr == "emails.value") {
			filterOp = f.Op
			filterVal = strings.ToLower(f.Value)
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
	// The count query numbers its own parameters: $1 is org_id, so the
	// filter value is always $2 regardless of which branch is taken.
	const countFilterArgIdx = 2
	switch filterOp {
	case "eq":
		countQuery += ` AND u.email = $` + strconv.Itoa(countFilterArgIdx)
		countArgs = append(countArgs, filterVal)
	case "co":
		countQuery += ` AND u.email ILIKE $` + strconv.Itoa(countFilterArgIdx)
		countArgs = append(countArgs, "%"+filterVal+"%")
	case "sw":
		countQuery += ` AND u.email ILIKE $` + strconv.Itoa(countFilterArgIdx)
		countArgs = append(countArgs, filterVal+"%")
	}
	var totalResults int
	if err := s.db.QueryRow(ctx, countQuery, countArgs...).Scan(&totalResults); err != nil {
		s.logger.Error("scim list users count failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	query += fmt.Sprintf(` LIMIT $%d OFFSET $%d`, argIdx, argIdx+1)
	args = append(args, count, offset)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		s.logger.Error("scim list users query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var (
			id, email, firstName, lastName string
			disabled                       bool
			createdAt, updatedAt           time.Time
			version                        int64
		)
		if err := rows.Scan(&id, &email, &firstName, &lastName, &disabled, &createdAt, &updatedAt, &version); err != nil {
			s.logger.Warn("scim list users scan failed", zap.Error(err))
			continue
		}
		users = append(users, userFromRow(id, email, firstName, lastName, disabled, createdAt, updatedAt, version))
	}
	if err := rows.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	if users == nil {
		users = []User{}
	}

	Respond(w, http.StatusOK, UserListResponse{
		Schemas:      []string{SchemaListResponse},
		TotalResults: totalResults,
		StartIndex:   startIndex,
		ItemsPerPage: len(users),
		Resources:    users,
	})
}

// GetUser handles GET /scim/v2/Users/{id}
func (s *Service) GetUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if err := s.host.ValidateUserID(userID); err != nil {
		respondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}
	ctx := r.Context()

	// C4/C5: org-scoped read. Fail closed — no org context means "not found",
	// never an implicit cross-org lookup.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	var (
		id, email, firstName, lastName string
		disabled                       bool
		createdAt, updatedAt           time.Time
		version                        int64
	)
	err := s.db.QueryRow(ctx,
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
			respondError(w, http.StatusNotFound, "user not found", "")
			return
		}
		s.logger.Error("scim get user query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	u := userFromRow(id, email, firstName, lastName, disabled, createdAt, updatedAt, version)
	w.Header().Set("ETag", u.Meta.Version)
	Respond(w, http.StatusOK, u)
}

// CreateUser handles POST /scim/v2/Users — maps to the onboard flow.
func (s *Service) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON", "invalidValue")
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
	if email == "" || !s.host.IsValidEmail(email) {
		respondError(w, http.StatusBadRequest, "userName must be a valid email address", "invalidValue")
		return
	}

	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "database not available", "")
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
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Idempotency: check for existing user. Email is unique realm-wide (see
	// docs/adr/0005), so this check is intentionally NOT org-scoped: a
	// duplicate email across orgs is always a conflict.
	//
	// Only "no rows" means "create the user". Any other DB error (a dead
	// connection, a permission problem) is surfaced as a 500 — treating it
	// as "not found" created a duplicate Keycloak user on every transient
	// database hiccup.
	var existingID string
	lookupErr := s.db.QueryRow(ctx,
		`SELECT keycloak_user_id FROM users WHERE email = $1`, email,
	).Scan(&existingID)
	switch {
	case lookupErr == nil:
		respondError(w, http.StatusConflict, "user with this userName already exists", "uniqueness")
		return
	case errors.Is(lookupErr, pgx.ErrNoRows):
		// No existing user — proceed.
	default:
		s.logger.Error("scim create user: idempotency lookup failed", zap.Error(lookupErr))
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// Create user in Keycloak via existing logic
	result, err := s.keycloak.CreateUser(ctx, firstName, lastName, email, "")
	if err != nil {
		s.logger.Error("scim create user: keycloak failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create user in identity provider", "")
		return
	}
	if result.User == nil || result.User.ID == nil || *result.User.ID == "" {
		respondError(w, http.StatusInternalServerError, "identity provider returned empty user ID", "")
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
		if delErr := s.keycloak.DeleteUser(cleanupCtx, kcUserID); delErr != nil {
			s.logger.Error("scim create: failed to roll back keycloak user",
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
	if err := s.host.PersistOnboard(persistCtx, kcUserID, onboardReq, "scim-provisioner", map[string]interface{}{
		"source": "scim",
	}, "", oc.OrgID); err != nil {
		s.logger.Error("scim create: persist failed", zap.String("kc_user_id", kcUserID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to persist user", "")
		return
	}

	// Init SCIM version row
	_, _ = s.db.Exec(persistCtx,
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
	_ = s.db.QueryRow(persistCtx,
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

	created := userFromRow(id, dbEmail, dbFirst, dbLast, disabled, createdAt, updatedAt, version)
	w.Header().Set("ETag", created.Meta.Version)
	Respond(w, http.StatusCreated, created)
}

// PatchUser handles PATCH /scim/v2/Users/{id}
func (s *Service) PatchUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if err := s.host.ValidateUserID(userID); err != nil {
		respondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}

	var patch PatchRequest
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON", "invalidValue")
		return
	}

	ctx := r.Context()

	// C4/C5: org-scoped write. Fail closed — no org context means "not found",
	// never an implicit cross-org lookup/mutation.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Load current user
	var (
		email, firstName, lastName string
		disabled                   bool
		createdAt, updatedAt       time.Time
		version                    int64
	)
	err := s.db.QueryRow(ctx,
		`SELECT u.email, u.first_name, u.last_name,
		        COALESCE(u.disabled, false), u.created_at, u.updated_at,
		        COALESCE(v.version, 1)
		 FROM users u
		 LEFT JOIN scim_resource_versions v ON v.user_id = u.keycloak_user_id
		 WHERE u.keycloak_user_id = $1 AND u.org_id = $2`, userID, oc.OrgID,
	).Scan(&email, &firstName, &lastName, &disabled, &createdAt, &updatedAt, &version)
	if err != nil {
		if isNotFound(err) {
			respondError(w, http.StatusNotFound, "user not found", "")
			return
		}
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// RFC 7644 §3.14: honour If-Match. The handler emits an ETag on every
	// read, so a client that read-then-writes can protect against a
	// concurrent modification. A stale precondition is 412, not a silent
	// overwrite.
	if ifMatch, present, ok := ifMatchVersion(r); present {
		if !ok {
			respondError(w, http.StatusBadRequest, "If-Match header is malformed", "invalidValue")
			return
		}
		// A negative version is the "*" wildcard: it matches any existing
		// resource and imposes no version constraint.
		if ifMatch >= 0 && ifMatch != version {
			s.logger.Info("scim patch: If-Match precondition failed",
				zap.String("user_id", userID),
				zap.Int64("if_match", ifMatch),
				zap.Int64("current_version", version))
			respondError(w, http.StatusPreconditionFailed, "resource version does not match If-Match", "")
			return
		}
	}

	// Apply operations
	newEmail := email
	// The pre-PATCH attribute values, kept for the compensating write to
	// the identity provider when the local DB write fails.
	origFirstName, origLastName, origDisabled := firstName, lastName, disabled
	for _, op := range patch.Operations {
		opLow := strings.ToLower(op.Op)
		pathLow := strings.ToLower(op.Path)

		// Detect filter-qualified paths like "emails[type eq \"work\"].value"
		// We strip the bracket segment and treat the remainder as a plain path.
		plainPath := pathLow
		if strings.Contains(pathLow, "[") {
			base, _ := ParseFilterPath(op.Path)
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

	// Take the optimistic lock BEFORE mutating anything.
	//
	// The version bump is a CONDITIONAL write: it only succeeds while the
	// stored version still equals the version this request read. Two
	// concurrent PATCHes therefore cannot both write the same version — the
	// loser gets 412 instead of silently overwriting the winner. Doing this
	// first means the loser never applies its changes at all.
	newVersion := version + 1
	tag, verErr := s.db.Exec(ctx,
		`INSERT INTO scim_resource_versions (user_id, version, updated_at) VALUES ($1, $2, NOW())
		 ON CONFLICT (user_id) DO UPDATE SET version = EXCLUDED.version, updated_at = NOW()
		 WHERE scim_resource_versions.version = $3`,
		userID, newVersion, version)
	if verErr != nil {
		s.logger.Error("scim patch: version bump failed", zap.String("user_id", userID), zap.Error(verErr))
		respondError(w, http.StatusInternalServerError, "failed to update resource version", "")
		return
	}
	if tag.RowsAffected() == 0 {
		s.logger.Info("scim patch: concurrent modification detected",
			zap.String("user_id", userID), zap.Int64("expected_version", version))
		respondError(w, http.StatusPreconditionFailed, "resource was modified concurrently", "")
		return
	}

	// Propagate to Keycloak
	if err := s.keycloak.UpdateUser(ctx, userID, firstName, lastName, "", !disabled); err != nil {
		s.logger.Error("scim patch: keycloak update failed", zap.String("user_id", userID), zap.Error(err))
		s.restoreVersion(ctx, userID, version, newVersion)
		respondError(w, http.StatusInternalServerError, "failed to update user in identity provider", "")
		return
	}

	// Persist changes. Include email if it changed. A failed DB write is a
	// 5xx, not a 200: the client must not believe the local record was
	// updated when it was not.
	if newEmail != email {
		_, dbErr := s.db.Exec(ctx,
			`UPDATE users SET email=$1, first_name=$2, last_name=$3, disabled=$4, updated_at=NOW()
			 WHERE keycloak_user_id=$5`,
			newEmail, firstName, lastName, disabled, userID)
		if dbErr != nil {
			s.logger.Error("scim patch: db update (with email) failed", zap.String("user_id", userID), zap.Error(dbErr))
			s.restoreVersion(ctx, userID, version, newVersion)
			s.restoreKeycloakUser(ctx, userID, origFirstName, origLastName, origDisabled)
			respondError(w, http.StatusInternalServerError, "failed to persist user changes", "")
			return
		}
		email = newEmail
	} else {
		_, dbErr := s.db.Exec(ctx,
			`UPDATE users SET first_name=$1, last_name=$2, disabled=$3, updated_at=NOW()
			 WHERE keycloak_user_id=$4`,
			firstName, lastName, disabled, userID)
		if dbErr != nil {
			s.logger.Error("scim patch: db update failed", zap.String("user_id", userID), zap.Error(dbErr))
			s.restoreVersion(ctx, userID, version, newVersion)
			s.restoreKeycloakUser(ctx, userID, origFirstName, origLastName, origDisabled)
			respondError(w, http.StatusInternalServerError, "failed to persist user changes", "")
			return
		}
	}

	if err := s.host.WriteAuditEntryBestEffort(r.Context(), "scim-provisioner", "scim_patch_user", "user", userID, map[string]interface{}{
		"disabled": disabled,
	}); err != nil {
		s.logger.Warn("failed to write SCIM patch audit log", zap.Error(err))
	}

	updatedAt = time.Now()
	u := userFromRow(userID, email, firstName, lastName, disabled, createdAt, updatedAt, newVersion)
	w.Header().Set("ETag", u.Meta.Version)
	Respond(w, http.StatusOK, u)
}

// scimRestoreVersion rolls the SCIM resource version back from attempted to
// prev after a PATCH could not be applied. The write is conditional so it
// never clobbers a concurrent writer's bump.
func (s *Service) restoreVersion(ctx context.Context, userID string, prev, attempted int64) {
	if _, err := s.db.Exec(ctx,
		`UPDATE scim_resource_versions SET version = $2, updated_at = NOW()
		 WHERE user_id = $1 AND version = $3`, userID, prev, attempted); err != nil {
		s.logger.Warn("scim patch: failed to roll back resource version",
			zap.String("user_id", userID), zap.Error(err))
	}
}

// scimRestoreKeycloakUser writes the pre-PATCH attribute values back to the
// identity provider. It is the compensating write for the case where
// Keycloak was updated but the local DB write failed — without it the two
// sides stay split-brain forever.
func (s *Service) restoreKeycloakUser(ctx context.Context, userID, firstName, lastName string, disabled bool) {
	restoreCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.keycloak.UpdateUser(restoreCtx, userID, firstName, lastName, "", !disabled); err != nil {
		s.logger.Error("scim patch: failed to restore keycloak user after a db failure",
			zap.String("user_id", userID), zap.Error(err))
	}
}

// ifMatchVersion parses the If-Match request header (RFC 7232 §3.1).
//
// It returns (version, present, ok):
//   - present is false when the header is absent — no precondition.
//   - ok is false when the header is present but malformed.
//   - version is negative for the "*" wildcard, which matches any existing
//     resource and imposes no version constraint.
func ifMatchVersion(r *http.Request) (int64, bool, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, false, true
	}
	if raw == "*" {
		return -1, true, true
	}
	// A weak validator: W/"3".
	value := strings.TrimPrefix(raw, "W/")
	value = strings.TrimPrefix(value, "w/")
	value = strings.Trim(value, `"`)
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, true, false
	}
	return version, true, true
}

// DeleteUser handles DELETE /scim/v2/Users/{id} — maps to the offboard flow.
func (s *Service) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if err := s.host.ValidateUserID(userID); err != nil {
		respondError(w, http.StatusBadRequest, "id is required", "invalidValue")
		return
	}
	if s.db == nil {
		respondError(w, http.StatusServiceUnavailable, "database not available", "")
		return
	}
	ctx := r.Context()

	// C4/C5: org-scoped write. Fail closed — no org context means "not found",
	// never an implicit cross-org lookup/mutation.
	oc := middleware.GetOrgContext(ctx)
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context", "")
		return
	}

	// Verify existence
	var email string
	if err := s.db.QueryRow(ctx,
		`SELECT email FROM users WHERE keycloak_user_id = $1 AND org_id = $2`, userID, oc.OrgID,
	).Scan(&email); err != nil {
		if isNotFound(err) {
			respondError(w, http.StatusNotFound, "user not found", "")
			return
		}
		respondError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// Disable in Keycloak (SCIM delete → deactivate, not hard-delete, to preserve audit trail)
	if err := s.keycloak.DisableUser(ctx, userID); err != nil {
		s.logger.Error("scim delete: disable failed", zap.String("user_id", userID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to deactivate user", "")
		return
	}
	_, _ = s.db.Exec(ctx,
		`UPDATE users SET disabled=true, updated_at=NOW() WHERE keycloak_user_id=$1`, userID)

	if err := s.host.WriteAuditEntryBestEffort(r.Context(), "scim-provisioner", "scim_delete_user", "user", userID, map[string]interface{}{
		"email": email,
	}); err != nil {
		s.logger.Warn("failed to write SCIM delete audit log", zap.Error(err))
	}

	w.WriteHeader(http.StatusNoContent)
}
