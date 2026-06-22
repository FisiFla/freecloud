package provisioning

import "context"

// SlackConnector provisions users via the Slack SCIM API.
// It delegates entirely to a SCIMConnector targeting the Slack SCIM v2 endpoint.
//
// MANUAL-VERIFY: live Slack tenant sync requires a real OAuth token with
// admin.users:read and admin.users:write scopes. Unit tests use an httptest mock.
type SlackConnector struct {
	scim *SCIMConnector
}

const slackSCIMBaseURL = "https://api.slack.com/scim/v2"

// NewSlackConnector creates a Slack provisioning connector.
func NewSlackConnector(bearerToken string) *SlackConnector {
	return &SlackConnector{scim: NewSCIMConnector(slackSCIMBaseURL, bearerToken)}
}

func (s *SlackConnector) Name() string { return "slack" }

func (s *SlackConnector) ProvisionUser(ctx context.Context, user ProvisionableUser) (string, error) {
	return s.scim.ProvisionUser(ctx, user)
}

func (s *SlackConnector) DeprovisionUser(ctx context.Context, remoteID string) error {
	return s.scim.DeprovisionUser(ctx, remoteID)
}

func (s *SlackConnector) UpdateUser(ctx context.Context, remoteID string, user ProvisionableUser) error {
	return s.scim.UpdateUser(ctx, remoteID, user)
}

func (s *SlackConnector) SyncGroupMembership(ctx context.Context, remoteID string, groups []string) error {
	return s.scim.SyncGroupMembership(ctx, remoteID, groups)
}
