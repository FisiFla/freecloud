package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/db"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// setupAdvisoryLockID is the pg advisory lock key serializing first-run admin
// provisioning across replicas (H3). Distinct from db's migrationLockID
// (8241093571) and main.go's leader-election lock ids (8241093601-603) so
// unrelated coordination never collides.
const setupAdvisoryLockID int64 = 8241093572

// SetupStatus handles GET /api/v1/setup/status.
// Returns {"provisioned": bool} — unauthenticated, read-only.
// "Provisioned" is derived from Keycloak state: true when at least one user
// holds the "admin" realm role (no migration required).
func (h *Handler) SetupStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provisioned, err := h.keycloak.HasAdminUser(ctx)
	if err != nil {
		h.logger.Error("setup status check failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"provisioned": provisioned})
}

// Setup handles POST /api/v1/setup.
// Body: {adminEmail, adminPassword, orgName}
// Fail-closed: returns 409 once provisioned.
//
// H3: first-run provisioning is serialized by a distributed pg advisory lock
// (db.AcquireAdvisoryLock) rather than a per-process sync.Mutex — under the
// v1.7 multi-replica HA topology, each replica has its own in-process mutex,
// so two replicas racing to be first could both pass the "no admin exists"
// check before either created one. Body validation happens before taking the
// lock (a pure function of the request, not shared state) so a malformed
// request never pays for the round trip.
func (h *Handler) Setup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body struct {
		AdminEmail    string `json:"adminEmail"`
		AdminPassword string `json:"adminPassword"`
		OrgName       string `json:"orgName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var errs []ValidationError
	if !isValidEmail(body.AdminEmail) {
		errs = append(errs, ValidationError{Field: "adminEmail", Message: "invalid email address"})
	}
	if !isStrongEnough(body.AdminPassword) {
		errs = append(errs, ValidationError{Field: "adminPassword", Message: "password must be at least 8 characters"})
	}
	if strings.TrimSpace(body.OrgName) == "" {
		errs = append(errs, ValidationError{Field: "orgName", Message: "organization name is required"})
	}
	if len(errs) > 0 {
		respondValidationErrors(w, errs)
		return
	}

	unlock, err := h.acquireSetupLock(ctx)
	if err != nil {
		h.logger.Error("setup: failed to acquire provisioning lock", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer unlock()

	// Fail-closed: check provisioned FIRST, INSIDE the lock, so a sibling
	// replica that raced us here and lost cannot also provision.
	provisioned, err := h.keycloak.HasAdminUser(ctx)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if provisioned {
		respondError(w, http.StatusConflict, "already provisioned")
		return
	}

	_, err = h.keycloak.CreateAdminUser(ctx, body.AdminEmail, body.AdminPassword)
	if err != nil {
		h.logger.Error("setup: create admin user failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create admin user")
		return
	}

	// Persist the operator-supplied org name onto the seeded Default
	// Organization row — best-effort: the admin user is already created at
	// this point, so a naming hiccup shouldn't fail the whole setup.
	if h.db != nil {
		if _, err := h.db.Exec(ctx,
			`UPDATE organizations SET name = $1, updated_at = NOW() WHERE id = $2`,
			body.OrgName, middleware.DefaultOrgID,
		); err != nil {
			h.logger.Warn("setup: failed to persist organization name", zap.Error(err))
		}
	}

	respondJSON(w, http.StatusCreated, map[string]string{"message": "provisioned"})
}

// acquireSetupLock serializes Setup's check-then-act sequence. When h.pgPool
// is wired (always true in production — see NewHandler), it takes a
// cross-replica pg advisory lock via db.AcquireAdvisoryLock. Otherwise (unit
// tests constructing Handler with a fake DBPool that isn't a real
// *pgxpool.Pool) it falls back to the in-process setupMu, which is all that's
// needed to keep those tests deterministic — it provides no additional
// safety in production, where the fake path is never reached.
// On success, the returned unlock func releases whichever lock was taken and
// must be called exactly once (typically via defer). On error, no lock is
// held and unlock is nil — callers must check err first.
func (h *Handler) acquireSetupLock(ctx context.Context) (unlock func(), err error) {
	if h.pgPool == nil {
		h.setupMu.Lock()
		return h.setupMu.Unlock, nil
	}

	conn, err := h.pgPool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.AcquireAdvisoryLock(ctx, conn, setupAdvisoryLockID, 30*time.Second, func() {
		h.logger.Info("setup: waiting for another replica's setup to finish")
	}); err != nil {
		conn.Release()
		return nil, err
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, setupAdvisoryLockID)
		conn.Release()
	}, nil
}

// isStrongEnough requires length ≥ 12 and at least one letter + one digit.
// First-admin is the highest-privilege account and is created over an
// unauthenticated endpoint during the setup window.
func isStrongEnough(s string) bool {
	if len(s) < 12 {
		return false
	}
	hasLetter, hasDigit, hasNonSpace := false, false, false
	for _, r := range s {
		if !unicode.IsSpace(r) {
			hasNonSpace = true
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	return hasNonSpace && hasLetter && hasDigit
}
