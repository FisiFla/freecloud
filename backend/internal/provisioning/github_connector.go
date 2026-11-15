package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/FisiFla/freecloud/backend/internal/httpx"
)

// GitHubConnector provisions users by adding/removing them from a GitHub organization.
// The remote ID is the GitHub username derived from the user's email prefix.
//
// MANUAL-VERIFY: live GitHub org sync requires a real Personal Access Token (PAT)
// with org:write and admin:org scopes, or a GitHub App installation token.
// Unit tests should use httptest to mock the GitHub API.
type GitHubConnector struct {
	orgName     string
	bearerToken string
	client      *http.Client
	// apiBase defaults to githubAPIBase. Overridable via
	// NewGitHubConnectorWithBaseURL for GitHub Enterprise Server's on-prem
	// API endpoint (A4's live-verification tool) or, from tests in this
	// package, an httptest.Server.
	apiBase string
}

const githubAPIBase = "https://api.github.com"

// NewGitHubConnector creates a GitHub org membership connector targeting
// github.com's API.
func NewGitHubConnector(orgName, bearerToken string) *GitHubConnector {
	return NewGitHubConnectorWithBaseURL(orgName, bearerToken, githubAPIBase)
}

// NewGitHubConnectorWithBaseURL creates a GitHub org membership connector
// targeting a specific API base URL — use this for GitHub Enterprise Server
// (on-prem), whose API root is not api.github.com.
func NewGitHubConnectorWithBaseURL(orgName, bearerToken, apiBase string) *GitHubConnector {
	return &GitHubConnector{
		orgName:     orgName,
		bearerToken: bearerToken,
		client:      httpx.NewOutboundClient(0),
		apiBase:     strings.TrimRight(apiBase, "/"),
	}
}

func (g *GitHubConnector) Name() string { return "github" }

// ProvisionUser adds the user to the GitHub organization.
// The username is derived from the email prefix (before "@").
func (g *GitHubConnector) ProvisionUser(ctx context.Context, user ProvisionableUser) (string, error) {
	username := githubUsername(user.Email)
	endpoint := fmt.Sprintf("%s/orgs/%s/memberships/%s", g.apiBase, url.PathEscape(g.orgName), url.PathEscape(username))

	payload := map[string]string{"role": "member"}
	body, _ := json.Marshal(payload)

	resp, err := g.doRequest(ctx, http.MethodPut, endpoint, body)
	if err != nil {
		return "", fmt.Errorf("github: provision user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := httpx.ReadAllBounded(resp.Body, 0)
		return "", fmt.Errorf("github: add org member returned %d: %s", resp.StatusCode, httpx.TruncateErrorBody(raw))
	}
	return username, nil
}

// DeprovisionUser removes the user from the GitHub organization.
func (g *GitHubConnector) DeprovisionUser(ctx context.Context, remoteID string) error {
	endpoint := fmt.Sprintf("%s/orgs/%s/members/%s", g.apiBase, url.PathEscape(g.orgName), url.PathEscape(remoteID))

	resp, err := g.doRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("github: deprovision user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		raw, _ := httpx.ReadAllBounded(resp.Body, 0)
		return fmt.Errorf("github: remove org member returned %d: %s", resp.StatusCode, httpx.TruncateErrorBody(raw))
	}
	return nil
}

// UpdateUser is a no-op for GitHub — org membership has no profile fields.
func (g *GitHubConnector) UpdateUser(_ context.Context, _ string, _ ProvisionableUser) error {
	return nil
}

// SyncGroupMembership is a no-op — GitHub team sync is out of scope for v1.4.
func (g *GitHubConnector) SyncGroupMembership(_ context.Context, _ string, _ []string) error {
	return nil
}

// githubUsername derives a GitHub username candidate from the email local part.
// Real deployments should maintain a userID→username mapping; this is a best-effort heuristic.
func githubUsername(email string) string {
	at := strings.IndexByte(email, '@')
	if at < 0 {
		return email
	}
	return email[:at]
}

// githubOrgNamePattern is GitHub's organization-name charset: alphanumerics
// and hyphens, starting with an alphanumeric, at most 39 characters.
var githubOrgNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// NormalizeGitHubOrgRef validates the admin-supplied GitHub "endpoint" value
// and returns the organization name to use. A plain org name is the expected
// form. Because the field was previously validated as an https URL, rows may
// hold github.com or api.github.com URLs instead — those are accepted and the
// org is extracted from the path so existing configurations keep working.
// Anything else is rejected.
func NormalizeGitHubOrgRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if githubOrgNamePattern.MatchString(ref) {
		return ref, nil
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("endpointUrl must be a GitHub organization name")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("endpointUrl must be a GitHub organization name")
	}
	host := strings.ToLower(u.Hostname())
	segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	var candidate string
	switch host {
	case "github.com", "www.github.com":
		// https://github.com/<org>[/...]
		if len(segments) >= 1 {
			candidate = segments[0]
		}
	case "api.github.com":
		// https://api.github.com/orgs/<org>
		if len(segments) >= 2 && segments[0] == "orgs" {
			candidate = segments[1]
		}
	default:
		return "", fmt.Errorf("endpointUrl must be a GitHub organization name")
	}
	candidate, _ = url.PathUnescape(candidate)
	if !githubOrgNamePattern.MatchString(candidate) {
		return "", fmt.Errorf("endpointUrl must be a GitHub organization name")
	}
	return candidate, nil
}

func (g *GitHubConnector) doRequest(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+g.bearerToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return g.client.Do(req)
}
