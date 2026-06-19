package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"go.uber.org/zap"
)

// hostIDPattern restricts host IDs to a safe charset so they cannot be used
// for path traversal or query injection against the FleetDM API.
var hostIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Host represents a FleetDM host/device.
type Host struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	OsVersion string `json:"os_version"`
	Status    string `json:"status"`
}

// Software represents installed software on a host.
type Software struct {
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Vulnerabilities []string `json:"vulnerabilities"`
}

// SecurityState represents the security posture of a host.
type SecurityState struct {
	FirewallEnabled bool     `json:"firewall_enabled"`
	DiskEncrypted   bool     `json:"disk_encrypted"`
	Vulnerabilities []string `json:"vulnerabilities"`
	UnknownVulns    bool     `json:"unknown_vulns"`
}

// Policy represents a FleetDM policy.
type Policy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Query       string `json:"query,omitempty"`
	Description string `json:"description,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	TeamID      string `json:"team_id,omitempty"`
}

// AssignPolicyRequest carries the policy identifier when assigning a policy to
// a host or team via the FleetDM REST API.
type AssignPolicyRequest struct {
	PolicyID string `json:"policy_id"`
}

// Team represents a FleetDM team.
type Team struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// TeamPolicy represents a policy scoped to a team.
type TeamPolicy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Query       string `json:"query,omitempty"`
	Description string `json:"description,omitempty"`
	TeamID      int    `json:"team_id"`
}

// FleetClientInterface defines the operations used by handlers.
type FleetClientInterface interface {
	CreateEnrollmentToken(ctx context.Context) (string, error)
	GetHosts(ctx context.Context, query string) ([]Host, error)
	GetHostSoftware(ctx context.Context, hostID string) ([]Software, error)
	GetHostSecurityState(ctx context.Context, hostID string) (*SecurityState, error)
	IssueRemoteLock(ctx context.Context, hostID string) error
	IssueRemoteWipe(ctx context.Context, hostID string) error
	Ping(ctx context.Context) error
	// Global policies
	ListPolicies(ctx context.Context) ([]Policy, error)
	// B2: team-scoped MDM policy management (replaces host-scoped stub)
	ListTeams(ctx context.Context) ([]Team, error)
	CreateTeam(ctx context.Context, name, description string) (*Team, error)
	AssignPolicyToTeam(ctx context.Context, teamID int, policyID string) error
	MoveHostToTeam(ctx context.Context, teamID int, hostIDs []string) error
}

// FleetClient communicates with the FleetDM API.
type FleetClient struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

// NewClient creates a new FleetClient.
func NewClient(baseURL, apiToken string) *FleetClient {
	return &FleetClient{
		baseURL:  baseURL,
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// doRequest performs an authenticated HTTP request.
func (f *FleetClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	logger := zap.L()

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	url := f.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+f.apiToken)
	req.Header.Set("Content-Type", "application/json")

	logger.Debug("fleet request",
		zap.String("method", method),
		zap.String("url", url),
	)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fleet request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fleet API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// CreateEnrollmentToken creates a new enrollment token in FleetDM.
func (f *FleetClient) CreateEnrollmentToken(ctx context.Context) (string, error) {
	body, err := f.doRequest(ctx, http.MethodPost, "/api/v1/fleet/setup_experience/enrollment_tokens", nil)
	if err != nil {
		return "", fmt.Errorf("fleetdm enrollment token creation failed: %w", err)
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("fleetdm enrollment token parse failed: %w", err)
	}

	zap.L().Info("created fleet enrollment token")
	return result.Token, nil
}

// GetHosts retrieves hosts from FleetDM, optionally filtered by a query.
func (f *FleetClient) GetHosts(ctx context.Context, query string) ([]Host, error) {
	path := "/api/v1/fleet/hosts"
	if query != "" {
		path += "?query=" + url.QueryEscape(query)
	}

	body, err := f.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("fleet hosts: %w", err)
	}

	var result struct {
		Hosts []Host `json:"hosts"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("fleet parse hosts: %w", err)
	}

	if len(result.Hosts) == 0 {
		return []Host{}, nil
	}

	return result.Hosts, nil
}

// validateHostID rejects host IDs that could traverse or inject into the URL
// path segment. FleetDM host IDs are opaque tokens (UUIDs or numeric IDs).
func validateHostID(hostID string) error {
	if hostID == "" {
		return fmt.Errorf("hostID is empty")
	}
	if !hostIDPattern.MatchString(hostID) {
		return fmt.Errorf("invalid hostID format")
	}
	return nil
}

// GetHostSoftware retrieves software installed on a specific host.
func (f *FleetClient) GetHostSoftware(ctx context.Context, hostID string) ([]Software, error) {
	if err := validateHostID(hostID); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/v1/fleet/hosts/%s/software", hostID)
	body, err := f.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("fleet host software: %w", err)
	}

	var result struct {
		Software []Software `json:"software"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("fleet parse software: %w", err)
	}

	return result.Software, nil
}

// GetHostSecurityState queries a host's details to determine security state.
func (f *FleetClient) GetHostSecurityState(ctx context.Context, hostID string) (*SecurityState, error) {
	if err := validateHostID(hostID); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/v1/fleet/hosts/%s", hostID)
	body, err := f.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		zap.L().Error("fleet GetHostSecurityState failed, cannot verify device", zap.Error(err))
		return nil, fmt.Errorf("fleetdm unreachable: %w", err)
	}

	var result struct {
		Host struct {
			DiskEncryption bool `json:"disk_encryption"`
			Firewall       bool `json:"firewall"`
		} `json:"host"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		zap.L().Error("fleet GetHostSecurityState parse failed", zap.Error(err))
		return nil, fmt.Errorf("fleetdm parse error: %w", err)
	}

	softwareList, err := f.GetHostSoftware(ctx, hostID)
	unknownVulns := false
	if err != nil {
		unknownVulns = true
		zap.L().Warn("failed to fetch host software for vulnerability check",
			zap.String("host_id", hostID),
			zap.Error(err),
		)
	}

	var vulns []string
	for _, s := range softwareList {
		for _, v := range s.Vulnerabilities {
			vulns = append(vulns, fmt.Sprintf("%s %s: %s", s.Name, s.Version, v))
		}
	}

	state := &SecurityState{
		FirewallEnabled: result.Host.Firewall,
		DiskEncrypted:   result.Host.DiskEncryption,
		Vulnerabilities: vulns,
		UnknownVulns:    unknownVulns,
	}

	return state, nil
}

// Ping checks connectivity to the FleetDM server.
func (f *FleetClient) Ping(ctx context.Context) error {
	_, err := f.doRequest(ctx, http.MethodGet, "/api/v1/fleet/status", nil)
	return err
}

// IssueRemoteLock issues a remote lock command to a host.
func (f *FleetClient) IssueRemoteLock(ctx context.Context, hostID string) error {
	if err := validateHostID(hostID); err != nil {
		return err
	}
	path := fmt.Sprintf("/api/v1/fleet/hosts/%s/lock", hostID)
	_, err := f.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		zap.L().Error("fleet IssueRemoteLock failed",
			zap.String("host_id", hostID),
			zap.Error(err),
		)
		return fmt.Errorf("remote lock failed for host %s: %w", hostID, err)
	}

	zap.L().Info("issued remote lock to host", zap.String("host_id", hostID))
	return nil
}

// ListPolicies returns all global policies defined in FleetDM.
func (f *FleetClient) ListPolicies(ctx context.Context) ([]Policy, error) {
	body, err := f.doRequest(ctx, http.MethodGet, "/api/v1/fleet/policies", nil)
	if err != nil {
		return nil, fmt.Errorf("fleet list policies: %w", err)
	}

	var result struct {
		Policies []Policy `json:"policies"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("fleet parse policies: %w", err)
	}
	if result.Policies == nil {
		return []Policy{}, nil
	}
	return result.Policies, nil
}

// ListTeams returns all teams defined in FleetDM.
func (f *FleetClient) ListTeams(ctx context.Context) ([]Team, error) {
	body, err := f.doRequest(ctx, http.MethodGet, "/api/v1/fleet/teams", nil)
	if err != nil {
		return nil, fmt.Errorf("fleet list teams: %w", err)
	}
	var result struct {
		Teams []Team `json:"teams"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("fleet parse teams: %w", err)
	}
	if result.Teams == nil {
		return []Team{}, nil
	}
	return result.Teams, nil
}

// CreateTeam creates a new team in FleetDM and returns it.
func (f *FleetClient) CreateTeam(ctx context.Context, name, description string) (*Team, error) {
	payload := map[string]string{"name": name, "description": description}
	body, err := f.doRequest(ctx, http.MethodPost, "/api/v1/fleet/teams", payload)
	if err != nil {
		return nil, fmt.Errorf("fleet create team: %w", err)
	}
	var result struct {
		Team Team `json:"team"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("fleet parse create team: %w", err)
	}
	zap.L().Info("created fleet team", zap.String("name", name), zap.Int("id", result.Team.ID))
	return &result.Team, nil
}

// AssignPolicyToTeam assigns an existing global policy to a team by posting to
// the FleetDM team-policies endpoint (POST /api/v1/fleet/teams/{id}/policies).
func (f *FleetClient) AssignPolicyToTeam(ctx context.Context, teamID int, policyID string) error {
	if err := validateHostID(policyID); err != nil {
		return fmt.Errorf("invalid policyID: %w", err)
	}
	path := fmt.Sprintf("/api/v1/fleet/teams/%d/policies", teamID)
	_, err := f.doRequest(ctx, http.MethodPost, path, map[string]string{"policy_id": policyID})
	if err != nil {
		zap.L().Error("fleet AssignPolicyToTeam failed",
			zap.Int("team_id", teamID),
			zap.String("policy_id", policyID),
			zap.Error(err),
		)
		return fmt.Errorf("assign policy %s to team %d: %w", policyID, teamID, err)
	}
	zap.L().Info("assigned policy to team",
		zap.Int("team_id", teamID),
		zap.String("policy_id", policyID),
	)
	return nil
}

// MoveHostToTeam moves hosts to a team via PATCH /api/v1/fleet/hosts/transfer/teams.
// This is the canonical FleetDM REST endpoint for host team assignment.
func (f *FleetClient) MoveHostToTeam(ctx context.Context, teamID int, hostIDs []string) error {
	if len(hostIDs) == 0 {
		return nil
	}
	for _, id := range hostIDs {
		if err := validateHostID(id); err != nil {
			return fmt.Errorf("invalid host id %q: %w", id, err)
		}
	}
	payload := map[string]interface{}{
		"team_id":  teamID,
		"host_ids": hostIDs,
	}
	_, err := f.doRequest(ctx, http.MethodPost, "/api/v1/fleet/hosts/transfer/teams", payload)
	if err != nil {
		zap.L().Error("fleet MoveHostToTeam failed",
			zap.Int("team_id", teamID),
			zap.Strings("host_ids", hostIDs),
			zap.Error(err),
		)
		return fmt.Errorf("move hosts to team %d: %w", teamID, err)
	}
	zap.L().Info("moved hosts to team",
		zap.Int("team_id", teamID),
		zap.Int("count", len(hostIDs)),
	)
	return nil
}

// IssueRemoteWipe issues a remote wipe command to a host.
func (f *FleetClient) IssueRemoteWipe(ctx context.Context, hostID string) error {
	if err := validateHostID(hostID); err != nil {
		return err
	}
	path := fmt.Sprintf("/api/v1/fleet/hosts/%s/wipe", hostID)
	_, err := f.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		zap.L().Error("fleet IssueRemoteWipe failed",
			zap.String("host_id", hostID),
			zap.Error(err),
		)
		return fmt.Errorf("remote wipe failed for host %s: %w", hostID, err)
	}

	zap.L().Info("issued remote wipe to host", zap.String("host_id", hostID))
	return nil
}
