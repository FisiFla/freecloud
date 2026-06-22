package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SCIMConnector is a generic SCIM 2.0 outbound connector. It POSTs/PATCHes
// to a configurable base URL using a bearer token. Unit tests inject an
// httptest.Server as the base URL.
type SCIMConnector struct {
	baseURL     string
	bearerToken string
	client      *http.Client
}

// NewSCIMConnector creates a SCIMConnector targeting the given base URL.
func NewSCIMConnector(baseURL, bearerToken string) *SCIMConnector {
	return &SCIMConnector{
		baseURL:     strings.TrimRight(baseURL, "/"),
		bearerToken: bearerToken,
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (s *SCIMConnector) Name() string { return "scim" }

// scimUser is the SCIM 2.0 User resource payload.
type scimUser struct {
	Schemas    []string   `json:"schemas"`
	UserName   string     `json:"userName"`
	Name       scimName   `json:"name"`
	Emails     []scimEmail `json:"emails"`
	Department string     `json:"department,omitempty"`
	Active     bool       `json:"active"`
}

type scimName struct {
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
}

type scimEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
}

type scimPatchOp struct {
	Schemas    []string        `json:"schemas"`
	Operations []scimOperation `json:"Operations"`
}

type scimOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path,omitempty"`
	Value interface{} `json:"value"`
}

// ProvisionUser creates a remote SCIM user. Returns the remote ID on success.
func (s *SCIMConnector) ProvisionUser(ctx context.Context, user ProvisionableUser) (string, error) {
	payload := scimUser{
		Schemas:    []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		UserName:   user.Email,
		Name:       scimName{GivenName: user.FirstName, FamilyName: user.LastName},
		Emails:     []scimEmail{{Value: user.Email, Primary: true}},
		Department: user.Department,
		Active:     true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("scim: marshal user: %w", err)
	}

	resp, err := s.doRequest(ctx, http.MethodPost, s.baseURL+"/Users", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("scim: provision user returned %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("scim: decode provision response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("scim: provision response missing id")
	}
	return result.ID, nil
}

// DeprovisionUser deactivates a remote SCIM user by PATCHing active=false.
func (s *SCIMConnector) DeprovisionUser(ctx context.Context, remoteID string) error {
	patch := scimPatchOp{
		Schemas: []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		Operations: []scimOperation{
			{Op: "replace", Path: "active", Value: false},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("scim: marshal deprovision patch: %w", err)
	}

	resp, err := s.doRequest(ctx, http.MethodPatch, s.baseURL+"/Users/"+remoteID, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("scim: deprovision returned %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// UpdateUser PATCHes name and department attributes on an existing remote user.
func (s *SCIMConnector) UpdateUser(ctx context.Context, remoteID string, user ProvisionableUser) error {
	patch := scimPatchOp{
		Schemas: []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		Operations: []scimOperation{
			{Op: "replace", Value: map[string]interface{}{
				"name": map[string]string{
					"givenName":  user.FirstName,
					"familyName": user.LastName,
				},
				"department": user.Department,
			}},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("scim: marshal update patch: %w", err)
	}

	resp, err := s.doRequest(ctx, http.MethodPatch, s.baseURL+"/Users/"+remoteID, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("scim: update user returned %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// SyncGroupMembership adds the user to each group by PATCHing the Group resource.
// Groups are identified by display name. If a group does not exist, it is created.
func (s *SCIMConnector) SyncGroupMembership(ctx context.Context, remoteID string, groups []string) error {
	for _, groupName := range groups {
		// Look up group by display name.
		groupID, err := s.findOrCreateGroup(ctx, groupName)
		if err != nil {
			return fmt.Errorf("scim: group %q: %w", groupName, err)
		}

		patch := scimPatchOp{
			Schemas: []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			Operations: []scimOperation{
				{Op: "add", Path: "members", Value: []map[string]string{{"value": remoteID}}},
			},
		}
		body, err := json.Marshal(patch)
		if err != nil {
			return fmt.Errorf("scim: marshal group patch: %w", err)
		}
		resp, err := s.doRequest(ctx, http.MethodPatch, s.baseURL+"/Groups/"+groupID, body)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("scim: add member to group %q returned %d", groupName, resp.StatusCode)
		}
	}
	return nil
}

// findOrCreateGroup returns the SCIM group ID for the given display name, creating it if absent.
func (s *SCIMConnector) findOrCreateGroup(ctx context.Context, displayName string) (string, error) {
	url := s.baseURL + "/Groups?filter=displayName+eq+%22" + displayName + "%22"
	resp, err := s.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var listResult struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"Resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResult); err == nil && len(listResult.Resources) > 0 {
		return listResult.Resources[0].ID, nil
	}

	// Create the group.
	payload := map[string]interface{}{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": displayName,
	}
	body, _ := json.Marshal(payload)
	resp2, err := s.doRequest(ctx, http.MethodPost, s.baseURL+"/Groups", body)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated && resp2.StatusCode != http.StatusOK {
		return "", fmt.Errorf("scim: create group returned %d", resp2.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&created); err != nil || created.ID == "" {
		return "", fmt.Errorf("scim: create group response missing id")
	}
	return created.ID, nil
}

func (s *SCIMConnector) doRequest(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("scim: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/scim+json")
	if s.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.bearerToken)
	}
	return s.client.Do(req)
}
