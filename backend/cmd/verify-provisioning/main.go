// Command verify-provisioning runs a real create -> update -> deactivate
// round-trip against a live GitHub organization and/or Slack workspace using
// operator-supplied credentials (A4).
//
// This is OPTIONAL and lives outside the normal test suite: the contract
// tests in backend/internal/provisioning/{github,slack}_connector_contract_test.go
// already pin the exact request/response shapes against recorded fixtures on
// every `go test` run. This tool exists for an operator who wants to prove
// the connector also works against their real tenant before relying on it in
// production — something no CI run can safely do (it would require a
// standing test org/workspace and real, live-rotatable credentials in CI
// secrets).
//
// Each target is skipped entirely when its credentials are absent, so this
// is safe to leave wired into a Makefile target that runs unattended.
//
// GitHub usage:
//
//	GITHUB_SCIM_TOKEN=ghp_xxx GITHUB_SCIM_ORG=my-test-org \
//	  go run ./cmd/verify-provisioning
//
// (The env var is named GITHUB_SCIM_TOKEN for consistency with the ticket's
// naming, even though FreeCloud's GitHub connector uses GitHub's org-
// membership REST API, not SCIM — see docs/DEPLOYMENT.md "Outbound
// Provisioning" and the contract-test file's header comment for why.)
//
// Slack usage: parked. Live Slack SCIM verification requires a paid Slack
// plan with SCIM provisioning enabled (see docs/DEPLOYMENT.md "Outbound
// Provisioning"). If/when available, set SLACK_SCIM_TOKEN and this tool will
// exercise it the same way; until then it always reports "skipped".
//
// GITHUB_SCIM_BASE_URL / SLACK_SCIM_BASE_URL override the real API base URLs
// (useful for GitHub Enterprise Server's on-prem API endpoint).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/FisiFla/freecloud/backend/internal/provisioning"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ranAny := false

	if token, org := os.Getenv("GITHUB_SCIM_TOKEN"), os.Getenv("GITHUB_SCIM_ORG"); token != "" && org != "" {
		ranAny = true
		if err := verifyGitHub(ctx, org, token); err != nil {
			fmt.Fprintf(os.Stderr, "GitHub live verification FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("GitHub live verification: PASSED")
	} else {
		fmt.Println("GitHub live verification: SKIPPED (set GITHUB_SCIM_TOKEN + GITHUB_SCIM_ORG to run)")
	}

	if token := os.Getenv("SLACK_SCIM_TOKEN"); token != "" {
		ranAny = true
		if err := verifySlack(ctx, token); err != nil {
			fmt.Fprintf(os.Stderr, "Slack live verification FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Slack live verification: PASSED")
	} else {
		fmt.Println("Slack live verification: SKIPPED (parked — requires a paid Slack plan with SCIM enabled; see docs/DEPLOYMENT.md)")
	}

	if !ranAny {
		fmt.Println("\nNo live credentials supplied — nothing was verified against a real tenant.")
		fmt.Println("This is expected in CI. See this file's header comment for how to run it locally.")
	}
}

// verifyGitHub drives create -> update -> deactivate against a real GitHub
// organization, then cleans up by removing the test user.
func verifyGitHub(ctx context.Context, org, token string) error {
	base := os.Getenv("GITHUB_SCIM_BASE_URL")
	connector := provisioning.NewGitHubConnector(org, token)
	if base != "" {
		connector = provisioning.NewGitHubConnectorWithBaseURL(org, token, base)
	}

	// The test user must already exist as a GitHub account (org membership
	// invites an existing account by username) — operators run this against
	// a disposable bot/service account they control in the target org.
	testUsername := os.Getenv("GITHUB_SCIM_TEST_USERNAME")
	if testUsername == "" {
		return fmt.Errorf("GITHUB_SCIM_TEST_USERNAME must be set to an existing GitHub account you control (org membership invites an existing user, it cannot create one)")
	}

	user := provisioning.ProvisionableUser{
		Email:      testUsername + "@users.noreply.github.com",
		FirstName:  "FreeCloud",
		LastName:   "Verify",
		Department: "verification",
	}

	fmt.Printf("GitHub: provisioning %s into org %s...\n", testUsername, org)
	remoteID, err := connector.ProvisionUser(ctx, user)
	if err != nil {
		return fmt.Errorf("provision: %w", err)
	}
	fmt.Printf("GitHub: provisioned, remote_id=%s\n", remoteID)

	fmt.Println("GitHub: updating (no-op for org membership, but must not error)...")
	if err := connector.UpdateUser(ctx, remoteID, user); err != nil {
		return fmt.Errorf("update: %w", err)
	}

	fmt.Println("GitHub: deprovisioning (removing org membership)...")
	if err := connector.DeprovisionUser(ctx, remoteID); err != nil {
		return fmt.Errorf("deprovision (cleanup): %w", err)
	}
	fmt.Println("GitHub: cleaned up successfully")
	return nil
}

// verifySlack drives create -> update -> deactivate against a real Slack
// workspace, then cleans up by deactivating the test user (Slack users can't
// be permanently deleted via SCIM — deactivation is the terminal state).
func verifySlack(ctx context.Context, token string) error {
	base := os.Getenv("SLACK_SCIM_BASE_URL")
	connector := provisioning.NewSlackConnector(token)
	if base != "" {
		connector = provisioning.NewSlackConnectorWithBaseURL(token, base)
	}

	testEmail := os.Getenv("SLACK_SCIM_TEST_EMAIL")
	if testEmail == "" {
		return fmt.Errorf("SLACK_SCIM_TEST_EMAIL must be set to a disposable test address in your workspace's domain")
	}

	user := provisioning.ProvisionableUser{
		Email:      testEmail,
		FirstName:  "FreeCloud",
		LastName:   "Verify",
		Department: "verification",
	}

	fmt.Printf("Slack: provisioning %s...\n", testEmail)
	remoteID, err := connector.ProvisionUser(ctx, user)
	if err != nil {
		return fmt.Errorf("provision: %w", err)
	}
	fmt.Printf("Slack: provisioned, remote_id=%s\n", remoteID)

	user.Department = "verification-updated"
	fmt.Println("Slack: updating profile...")
	if err := connector.UpdateUser(ctx, remoteID, user); err != nil {
		return fmt.Errorf("update: %w", err)
	}

	fmt.Println("Slack: deactivating (cleanup)...")
	if err := connector.DeprovisionUser(ctx, remoteID); err != nil {
		return fmt.Errorf("deprovision (cleanup): %w", err)
	}
	fmt.Println("Slack: cleaned up successfully")
	return nil
}
