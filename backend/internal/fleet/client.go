package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

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
		return "", fmt.Errorf("create enrollment token: %w", err)
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse enrollment token response: %w", err)
	}

	zap.L().Info("created fleet enrollment token")
	return result.Token, nil
}

// GetHosts retrieves hosts from FleetDM, optionally filtered by a query.
func (f *FleetClient) GetHosts(ctx context.Context, query string) ([]Host, error) {
	path := "/api/v1/fleet/hosts"
	if query != "" {
		path += "?query=" + query
	}

	body, err := f.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get hosts: %w", err)
	}

	var result struct {
		Hosts []Host `json:"hosts"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse hosts response: %w", err)
	}

	return result.Hosts, nil
}

// GetHostSoftware retrieves software installed on a specific host.
func (f *FleetClient) GetHostSoftware(ctx context.Context, hostID string) ([]Software, error) {
	path := fmt.Sprintf("/api/v1/fleet/hosts/%s/software", hostID)
	body, err := f.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get host software: %w", err)
	}

	var result struct {
		Software []Software `json:"software"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse software response: %w", err)
	}

	return result.Software, nil
}

// GetHostSecurityState queries a host's details to determine security state.
func (f *FleetClient) GetHostSecurityState(ctx context.Context, hostID string) (*SecurityState, error) {
	path := fmt.Sprintf("/api/v1/fleet/hosts/%s", hostID)
	body, err := f.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get host security state: %w", err)
	}

	var result struct {
		Host struct {
			DiskEncryption bool `json:"disk_encryption"`
			Firewall       bool `json:"firewall"`
		} `json:"host"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse security state response: %w", err)
	}

	// Check for vulnerabilities by fetching software
	softwareList, err := f.GetHostSoftware(ctx, hostID)
	if err != nil {
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
	}

	return state, nil
}

// IssueRemoteLock issues a remote lock command to a host.
func (f *FleetClient) IssueRemoteLock(ctx context.Context, hostID string) error {
	path := fmt.Sprintf("/api/v1/fleet/hosts/%s/lock", hostID)
	_, err := f.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("issue remote lock for host %s: %w", hostID, err)
	}

	zap.L().Info("issued remote lock to host", zap.String("host_id", hostID))
	return nil
}

// IssueRemoteWipe issues a remote wipe command to a host.
func (f *FleetClient) IssueRemoteWipe(ctx context.Context, hostID string) error {
	path := fmt.Sprintf("/api/v1/fleet/hosts/%s/wipe", hostID)
	_, err := f.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("issue remote wipe for host %s: %w", hostID, err)
	}

	zap.L().Info("issued remote wipe to host", zap.String("host_id", hostID))
	return nil
}
