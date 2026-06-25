package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
)

// NewDynamicFleetClient returns a Fleet client facade that reads the saved
// Fleet settings on each call. Env config remains the fallback for bootstrap.
func NewDynamicFleetClient(db DBPool, fallbackURL, fallbackToken string, logger *zap.Logger) fleet.FleetClientInterface {
	return &dynamicFleetClient{
		db:            db,
		fallbackURL:   strings.TrimSpace(fallbackURL),
		fallbackToken: fallbackToken,
		logger:        logger,
	}
}

type dynamicFleetClient struct {
	db            DBPool
	fallbackURL   string
	fallbackToken string
	logger        *zap.Logger
}

func (d *dynamicFleetClient) client(ctx context.Context) fleet.FleetClientInterface {
	baseURL := d.fallbackURL
	token := d.fallbackToken

	if d.db != nil {
		var serverURL string
		var tokenEnc *string
		err := d.db.QueryRow(ctx,
			`SELECT server_url, api_token_enc FROM fleet_config WHERE id = 1`,
		).Scan(&serverURL, &tokenEnc)
		if err == nil {
			if trimmed := strings.TrimSpace(serverURL); trimmed != "" {
				baseURL = trimmed
			}
			if tokenEnc != nil && *tokenEnc != "" {
				plaintext, decErr := decryptProvisioningToken(*tokenEnc)
				if decErr != nil {
					d.logger.Warn("fleet config: decrypt token failed; using fallback token", zap.Error(decErr))
				} else {
					token = plaintext
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			d.logger.Warn("fleet config: load failed; using fallback config", zap.Error(err))
		}
	}

	return fleet.NewClient(baseURL, token)
}

func (d *dynamicFleetClient) CreateEnrollmentToken(ctx context.Context) (string, error) {
	return d.client(ctx).CreateEnrollmentToken(ctx)
}

func (d *dynamicFleetClient) GetHosts(ctx context.Context, query string) ([]fleet.Host, error) {
	return d.client(ctx).GetHosts(ctx, query)
}

func (d *dynamicFleetClient) GetHostSoftware(ctx context.Context, hostID string) ([]fleet.Software, error) {
	return d.client(ctx).GetHostSoftware(ctx, hostID)
}

func (d *dynamicFleetClient) GetHostSecurityState(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
	return d.client(ctx).GetHostSecurityState(ctx, hostID)
}

func (d *dynamicFleetClient) IssueRemoteLock(ctx context.Context, hostID string) error {
	return d.client(ctx).IssueRemoteLock(ctx, hostID)
}

func (d *dynamicFleetClient) IssueRemoteWipe(ctx context.Context, hostID string) error {
	return d.client(ctx).IssueRemoteWipe(ctx, hostID)
}

func (d *dynamicFleetClient) IssueRestart(ctx context.Context, hostID string) error {
	return d.client(ctx).IssueRestart(ctx, hostID)
}

func (d *dynamicFleetClient) IssueLockWithMessage(ctx context.Context, hostID string, message string) error {
	return d.client(ctx).IssueLockWithMessage(ctx, hostID, message)
}

func (d *dynamicFleetClient) IssueClearPasscode(ctx context.Context, hostID string) error {
	return d.client(ctx).IssueClearPasscode(ctx, hostID)
}

func (d *dynamicFleetClient) Ping(ctx context.Context) error {
	return d.client(ctx).Ping(ctx)
}

func (d *dynamicFleetClient) ListPolicies(ctx context.Context) ([]fleet.Policy, error) {
	return d.client(ctx).ListPolicies(ctx)
}

func (d *dynamicFleetClient) ListTeams(ctx context.Context) ([]fleet.Team, error) {
	return d.client(ctx).ListTeams(ctx)
}

func (d *dynamicFleetClient) CreateTeam(ctx context.Context, name, description string) (*fleet.Team, error) {
	return d.client(ctx).CreateTeam(ctx, name, description)
}

func (d *dynamicFleetClient) AssignPolicyToTeam(ctx context.Context, teamID int, policyID string) error {
	return d.client(ctx).AssignPolicyToTeam(ctx, teamID, policyID)
}

func (d *dynamicFleetClient) MoveHostToTeam(ctx context.Context, teamID int, hostIDs []string) error {
	return d.client(ctx).MoveHostToTeam(ctx, teamID, hostIDs)
}

func (d *dynamicFleetClient) GetHostOSPosture(ctx context.Context, hostID string) (*fleet.OSPosture, error) {
	return d.client(ctx).GetHostOSPosture(ctx, hostID)
}
