// Package provisioning drives outbound SCIM/Slack/GitHub provisioning (Epic A).
// The Engine holds a map of per-app Connectors and maintains provisioning_state rows
// to track which remote accounts exist and whether they're in sync.
package provisioning

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// DBPool is the minimal database interface the provisioning engine needs.
type DBPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ProvisioningStatus is the lifecycle state of a remote account.
type ProvisioningStatus string

const (
	StatusPending       ProvisioningStatus = "pending"
	StatusProvisioned   ProvisioningStatus = "provisioned"
	StatusDeprovisioned ProvisioningStatus = "deprovisioned"
	StatusError         ProvisioningStatus = "error"
	StatusPermanentErr  ProvisioningStatus = "permanent_error"
)

const maxRetries = 3

// ProvisionableUser carries the attributes needed to create a remote account.
type ProvisionableUser struct {
	ID         string
	Email      string
	FirstName  string
	LastName   string
	Department string
	Groups     []string
}

// Connector is implemented by each downstream SaaS connector.
type Connector interface {
	// ProvisionUser creates the account and returns the remote ID assigned by the SaaS.
	ProvisionUser(ctx context.Context, user ProvisionableUser) (remoteID string, err error)
	// DeprovisionUser deactivates/removes the account identified by remoteID.
	DeprovisionUser(ctx context.Context, remoteID string) error
	// UpdateUser refreshes profile attributes on an existing remote account.
	UpdateUser(ctx context.Context, remoteID string, user ProvisionableUser) error
	// SyncGroupMembership updates group/team membership for a remote account.
	SyncGroupMembership(ctx context.Context, remoteID string, groups []string) error
	// Name returns a short label used in metrics and logs.
	Name() string
}

// Engine drives connectors and persists state.
type Engine struct {
	db         DBPool
	mu         sync.RWMutex
	connectors map[string]Connector // keyed by app UUID
	logger     *zap.Logger
}

// NewEngine creates an Engine backed by the given DB pool.
func NewEngine(db DBPool, logger *zap.Logger) *Engine {
	return &Engine{
		db:         db,
		connectors: make(map[string]Connector),
		logger:     logger,
	}
}

// RegisterConnector associates a connector with an app ID.
func (e *Engine) RegisterConnector(appID string, c Connector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.connectors[appID] = c
}

// ReplaceConnectors atomically swaps the active connector registry.
func (e *Engine) ReplaceConnectors(connectors map[string]Connector) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.connectors = connectors
}

// ConnectorCount returns the number of registered connectors.
func (e *Engine) ConnectorCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.connectors)
}

// HasConnector reports whether an app has an active connector.
func (e *Engine) HasConnector(appID string) bool {
	_, ok := e.connector(appID)
	return ok
}

func (e *Engine) connector(appID string) (Connector, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	c, ok := e.connectors[appID]
	return c, ok
}

// ProvisionUser creates or updates a remote account for userID on the given app.
func (e *Engine) ProvisionUser(ctx context.Context, appID string, user ProvisionableUser) error {
	c, ok := e.connector(appID)
	if !ok {
		return fmt.Errorf("provisioning: no connector for app %s", appID)
	}

	// Upsert a pending state row so we have something to update on completion.
	_, err := e.db.Exec(ctx,
		`INSERT INTO provisioning_state (app_id, user_id, status)
		 VALUES ($1, $2, 'pending')
		 ON CONFLICT (app_id, user_id) DO UPDATE
		   SET status = CASE WHEN provisioning_state.status = 'provisioned' THEN 'provisioned' ELSE 'pending' END,
		       updated_at = NOW()`,
		appID, user.ID,
	)
	if err != nil {
		return fmt.Errorf("provisioning: upsert pending state: %w", err)
	}

	// Check whether already provisioned — if so, update instead.
	var existingRemoteID string
	var existingStatus string
	scanErr := e.db.QueryRow(ctx,
		`SELECT COALESCE(remote_id, ''), status FROM provisioning_state WHERE app_id = $1 AND user_id = $2`,
		appID, user.ID,
	).Scan(&existingRemoteID, &existingStatus)
	if scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
		return fmt.Errorf("provisioning: read state: %w", scanErr)
	}

	var remoteID string
	if existingStatus == string(StatusProvisioned) && existingRemoteID != "" {
		// Already provisioned — push an update instead.
		if err := c.UpdateUser(ctx, existingRemoteID, user); err != nil {
			return e.recordError(ctx, appID, user.ID, existingRemoteID, err)
		}
		remoteID = existingRemoteID
	} else {
		// New provisioning.
		rid, err := c.ProvisionUser(ctx, user)
		if err != nil {
			return e.recordError(ctx, appID, user.ID, "", err)
		}
		remoteID = rid
	}

	_, err = e.db.Exec(ctx,
		`UPDATE provisioning_state
		 SET status = 'provisioned', remote_id = $3, last_sync_at = NOW(),
		     last_error = NULL, retry_count = 0, next_retry_at = NULL, updated_at = NOW()
		 WHERE app_id = $1 AND user_id = $2`,
		appID, user.ID, remoteID,
	)
	if err != nil {
		e.logger.Error("provisioning: update state after success", zap.Error(err))
	}
	e.logger.Info("provisioning: user provisioned",
		zap.String("app_id", appID), zap.String("user_id", user.ID), zap.String("remote_id", remoteID))
	return nil
}

// DeprovisionUser deactivates the remote account for userID on the given app.
func (e *Engine) DeprovisionUser(ctx context.Context, appID string, userID string) error {
	c, ok := e.connector(appID)
	if !ok {
		return fmt.Errorf("provisioning: no connector for app %s", appID)
	}

	var remoteID string
	err := e.db.QueryRow(ctx,
		`SELECT COALESCE(remote_id, '') FROM provisioning_state WHERE app_id = $1 AND user_id = $2`,
		appID, userID,
	).Scan(&remoteID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Nothing to deprovision.
		return nil
	}
	if err != nil {
		return fmt.Errorf("provisioning: read remote_id: %w", err)
	}

	if remoteID == "" {
		// Account was never successfully provisioned.
		_, _ = e.db.Exec(ctx,
			`UPDATE provisioning_state SET status = 'deprovisioned', updated_at = NOW() WHERE app_id = $1 AND user_id = $2`,
			appID, userID,
		)
		return nil
	}

	if err := c.DeprovisionUser(ctx, remoteID); err != nil {
		return e.recordError(ctx, appID, userID, remoteID, err)
	}

	_, err = e.db.Exec(ctx,
		`UPDATE provisioning_state
		 SET status = 'deprovisioned', last_sync_at = NOW(),
		     last_error = NULL, retry_count = 0, next_retry_at = NULL, updated_at = NOW()
		 WHERE app_id = $1 AND user_id = $2`,
		appID, userID,
	)
	if err != nil {
		e.logger.Error("provisioning: update state after deprovision", zap.Error(err))
	}
	e.logger.Info("provisioning: user deprovisioned",
		zap.String("app_id", appID), zap.String("user_id", userID))
	return nil
}

// SyncGroupMembership pushes group membership to the remote app if provisioned.
func (e *Engine) SyncGroupMembership(ctx context.Context, appID string, userID string, groups []string) error {
	c, ok := e.connector(appID)
	if !ok {
		return fmt.Errorf("provisioning: no connector for app %s", appID)
	}

	var remoteID string
	var status string
	err := e.db.QueryRow(ctx,
		`SELECT COALESCE(remote_id, ''), status FROM provisioning_state WHERE app_id = $1 AND user_id = $2`,
		appID, userID,
	).Scan(&remoteID, &status)
	if errors.Is(err, pgx.ErrNoRows) || status != string(StatusProvisioned) || remoteID == "" {
		return nil // not yet provisioned — skip
	}
	if err != nil {
		return fmt.Errorf("provisioning: read state for group sync: %w", err)
	}

	if syncErr := c.SyncGroupMembership(ctx, remoteID, groups); syncErr != nil {
		e.logger.Warn("provisioning: group sync failed",
			zap.String("app_id", appID), zap.String("user_id", userID), zap.Error(syncErr))
		return syncErr
	}

	_, _ = e.db.Exec(ctx,
		`UPDATE provisioning_state SET last_sync_at = NOW(), updated_at = NOW() WHERE app_id = $1 AND user_id = $2`,
		appID, userID,
	)
	return nil
}

// ReconcileAll re-syncs stale or errored provisioning_state rows, processed
// one organization at a time (Epic C / v1.7 multi-tenant): each row already
// carries its own org_id (Migration043) and its connector is looked up by
// app_id, which itself belongs to exactly one org, so cross-org mixing was
// never possible here — but reconciling org-by-org (rather than one global
// pass over every org's rows interleaved) keeps failures, logs, and future
// per-org controls (pause a single tenant's provisioning, rate-limit one
// org's outbound calls) scoped and auditable per tenant instead of only
// inferable after the fact from a shared log stream.
//
// It processes entries where: status = 'error' AND next_retry_at <= NOW(),
// or last_sync_at is older than 24 hours.
func (e *Engine) ReconcileAll(ctx context.Context) error {
	type staleEntry struct {
		appID      string
		userID     string
		remoteID   string
		status     string
		retryCount int
		email      string
		firstName  string
		lastName   string
		department string
	}

	rows, err := e.db.Query(ctx,
		`SELECT ps.org_id::TEXT, ps.app_id::TEXT, ps.user_id::TEXT, COALESCE(ps.remote_id, ''), ps.status, ps.retry_count,
		        u.email, u.first_name, u.last_name, COALESCE(u.department, '')
		 FROM provisioning_state ps
		 JOIN users u ON u.keycloak_user_id = ps.user_id AND u.org_id = ps.org_id
		 WHERE (ps.status = 'error' AND (ps.next_retry_at IS NULL OR ps.next_retry_at <= NOW()))
		    OR (ps.status = 'provisioned' AND (ps.last_sync_at IS NULL OR ps.last_sync_at < NOW() - INTERVAL '24 hours'))
		 ORDER BY ps.org_id, ps.updated_at ASC`,
	)
	if err != nil {
		return fmt.Errorf("provisioning: reconcile query: %w", err)
	}
	defer rows.Close()

	// byOrg preserves the query's org_id-major ORDER BY, so each org's entries
	// are contiguous — a plain slice-of-(orgID, entries) keeps that order
	// (unlike a map, whose iteration order is undefined).
	type orgBatch struct {
		orgID   string
		entries []staleEntry
	}
	var batches []orgBatch
	for rows.Next() {
		var orgID string
		var se staleEntry
		if err := rows.Scan(&orgID, &se.appID, &se.userID, &se.remoteID, &se.status, &se.retryCount,
			&se.email, &se.firstName, &se.lastName, &se.department); err != nil {
			e.logger.Warn("provisioning: reconcile scan error", zap.Error(err))
			continue
		}
		if len(batches) == 0 || batches[len(batches)-1].orgID != orgID {
			batches = append(batches, orgBatch{orgID: orgID})
		}
		last := &batches[len(batches)-1]
		last.entries = append(last.entries, se)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("provisioning: reconcile iterate: %w", err)
	}

	for _, batch := range batches {
		e.logger.Info("provisioning: reconciling organization",
			zap.String("org_id", batch.orgID), zap.Int("stale_entries", len(batch.entries)))
		for _, se := range batch.entries {
			e.reconcileOne(ctx, se.appID, se.userID, se.remoteID, se.status, se.retryCount,
				se.email, se.firstName, se.lastName, se.department)
		}
	}
	return nil
}

// reconcileOne processes a single stale provisioning_state row. Extracted
// from ReconcileAll's per-org loop so the org batching above stays readable.
func (e *Engine) reconcileOne(ctx context.Context, appID, userID, remoteID, status string, retryCount int, email, firstName, lastName, department string) {
	c, ok := e.connector(appID)
	if !ok {
		return
	}
	user := ProvisionableUser{
		ID:         userID,
		Email:      email,
		FirstName:  firstName,
		LastName:   lastName,
		Department: department,
	}

	if status == string(StatusProvisioned) {
		// Periodic re-sync: update existing account.
		if remoteID != "" {
			if err := c.UpdateUser(ctx, remoteID, user); err != nil {
				e.logger.Warn("provisioning: reconcile update failed",
					zap.String("app_id", appID), zap.String("user_id", userID), zap.Error(err))
				return
			}
		}
		_, _ = e.db.Exec(ctx,
			`UPDATE provisioning_state SET last_sync_at = NOW(), updated_at = NOW() WHERE app_id = $1 AND user_id = $2`,
			appID, userID,
		)
		return
	}

	// Retry errored entry.
	if retryCount >= maxRetries {
		_, _ = e.db.Exec(ctx,
			`UPDATE provisioning_state SET status = 'permanent_error', updated_at = NOW() WHERE app_id = $1 AND user_id = $2`,
			appID, userID,
		)
		e.logger.Warn("provisioning: permanent error — max retries exceeded",
			zap.String("app_id", appID), zap.String("user_id", userID))
		return
	}
	rid, err := c.ProvisionUser(ctx, user)
	if err != nil {
		// Use the known retryCount from the stale entry rather than re-reading
		// from DB, so ReconcileAll doesn't require a DB round-trip just for count.
		newCount := retryCount + 1
		if newCount >= maxRetries {
			_, _ = e.db.Exec(ctx,
				`UPDATE provisioning_state SET status = 'permanent_error', last_error = $3, retry_count = $4, updated_at = NOW() WHERE app_id = $1 AND user_id = $2`,
				appID, userID, err.Error(), newCount,
			)
			e.logger.Warn("provisioning: permanent error — max retries exceeded",
				zap.String("app_id", appID), zap.String("user_id", userID))
		} else {
			backoff := time.Duration(5*1<<retryCount) * time.Minute
			if backoff > 2*time.Hour {
				backoff = 2 * time.Hour
			}
			nextRetry := time.Now().Add(backoff)
			_, _ = e.db.Exec(ctx,
				`UPDATE provisioning_state SET status = 'error', last_error = $3, retry_count = $4, next_retry_at = $5, updated_at = NOW() WHERE app_id = $1 AND user_id = $2`,
				appID, userID, err.Error(), newCount, nextRetry,
			)
		}
		return
	}
	_, _ = e.db.Exec(ctx,
		`UPDATE provisioning_state
		 SET status = 'provisioned', remote_id = $3, last_sync_at = NOW(),
		     last_error = NULL, retry_count = 0, next_retry_at = NULL, updated_at = NOW()
		 WHERE app_id = $1 AND user_id = $2`,
		appID, userID, rid,
	)
}

// recordError increments retry_count and computes the next backoff time.
// After maxRetries, it transitions to permanent_error.
func (e *Engine) recordError(ctx context.Context, appID, userID, remoteID string, cause error) error {
	var retryCount int
	_ = e.db.QueryRow(ctx,
		`SELECT retry_count FROM provisioning_state WHERE app_id = $1 AND user_id = $2`,
		appID, userID,
	).Scan(&retryCount)

	newRetryCount := retryCount + 1
	newStatus := string(StatusError)
	if newRetryCount >= maxRetries {
		newStatus = string(StatusPermanentErr)
	}

	// Exponential backoff: 5m × 2^retryCount, capped at 2h.
	backoff := time.Duration(5*1<<retryCount) * time.Minute
	if backoff > 2*time.Hour {
		backoff = 2 * time.Hour
	}
	nextRetry := time.Now().Add(backoff)

	ridPtr := &remoteID
	if remoteID == "" {
		ridPtr = nil
	}

	// Embed the status literal in the SQL so the test/audit layer can grep it.
	var (
		statusSQL string
		dbErr     error
	)
	if newStatus == string(StatusPermanentErr) {
		statusSQL = `UPDATE provisioning_state
		 SET status = 'permanent_error', last_error = $3, retry_count = $4, next_retry_at = $5,
		     remote_id = COALESCE($6, remote_id), updated_at = NOW()
		 WHERE app_id = $1 AND user_id = $2`
		_, dbErr = e.db.Exec(ctx, statusSQL,
			appID, userID, cause.Error(), newRetryCount, nextRetry, ridPtr)
	} else {
		statusSQL = `UPDATE provisioning_state
		 SET status = 'error', last_error = $3, retry_count = $4, next_retry_at = $5,
		     remote_id = COALESCE($6, remote_id), updated_at = NOW()
		 WHERE app_id = $1 AND user_id = $2`
		_, dbErr = e.db.Exec(ctx, statusSQL,
			appID, userID, cause.Error(), newRetryCount, nextRetry, ridPtr)
	}
	if dbErr != nil {
		e.logger.Error("provisioning: failed to record error state", zap.Error(dbErr))
	}
	e.logger.Warn("provisioning: connector error",
		zap.String("app_id", appID), zap.String("user_id", userID),
		zap.Int("retry_count", newRetryCount), zap.Error(cause))
	return fmt.Errorf("provisioning: connector error (retry %d/%d): %w", newRetryCount, maxRetries, cause)
}

// ApplyAttributeMap applies the attribute map overrides to the default SCIM field mapping.
// Default fields: userName=user.Email, givenName=user.FirstName, familyName=user.LastName, department=user.Department.
// For each entry in attrMap, if the key matches a default field name, the key is renamed to the value.
func ApplyAttributeMap(user ProvisionableUser, attrMap map[string]string) map[string]any {
	defaults := map[string]any{
		"userName":   user.Email,
		"givenName":  user.FirstName,
		"familyName": user.LastName,
		"department": user.Department,
	}
	if len(attrMap) == 0 {
		return defaults
	}
	result := make(map[string]any, len(defaults))
	for field, value := range defaults {
		if remoteKey, ok := attrMap[field]; ok && remoteKey != "" {
			result[remoteKey] = value
		} else {
			result[field] = value
		}
	}
	return result
}
