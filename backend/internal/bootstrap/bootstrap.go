package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Nerzal/gocloak/v13"
	"go.uber.org/zap"
)

// Config holds parameters for bootstrapping Keycloak.
// ServiceAccountSecretOverride, if non-empty, is used as the freecloud-service
// client secret (operator override); otherwise a fresh secret is generated on
// first run or the existing secret is regenerated on subsequent runs.
type Config struct {
	KeycloakURL string
	// AdminUsername / AdminPassword are master-realm admin credentials.
	AdminUsername string
	AdminPassword string
	// TargetRealm is the realm to create/configure. Defaults to "freecloud".
	TargetRealm string
	// ServiceClientID is the confidential backend service client. Default: "freecloud-service".
	ServiceClientID string
	// DashboardClientID is the public frontend OIDC client. Default: "freecloud-dashboard".
	DashboardClientID string
	// PostureFlowAlias is the browser-flow copy name. Default: "browser-with-posture".
	PostureFlowAlias string
	// ServiceAccountSecretOverride pins the service-account secret. If empty, a
	// random secret is generated/regenerated on every bootstrap run.
	ServiceAccountSecretOverride string
	// CreateDemoUser creates a demo user for dev/e2e environments.
	CreateDemoUser bool
	// DemoPassword is the demo user password. Generated if empty.
	DemoPassword string
}

// Result is returned by Run.
type Result struct {
	// ServiceAccountSecret is the secret currently set on the freecloud-service client.
	// Use this to initialise the runtime Keycloak client.
	ServiceAccountSecret string
}

func (c *Config) defaults() {
	if c.TargetRealm == "" {
		c.TargetRealm = "freecloud"
	}
	if c.ServiceClientID == "" {
		c.ServiceClientID = "freecloud-service"
	}
	if c.DashboardClientID == "" {
		c.DashboardClientID = "freecloud-dashboard"
	}
	if c.PostureFlowAlias == "" {
		c.PostureFlowAlias = "browser-with-posture"
	}
}

// Run bootstraps Keycloak idempotently. It is safe to call on every startup.
// Returns the service-account secret that is active after bootstrap.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	cfg.defaults()
	logger := zap.L().With(zap.String("realm", cfg.TargetRealm))

	gc := gocloak.NewClient(cfg.KeycloakURL)

	logger.Info("bootstrap: authenticating as master admin")
	// Keycloak may still be starting when the backend boots (docker compose gives
	// no readiness ordering between services). Retry the admin login with backoff
	// until Keycloak is reachable or the context deadline passes.
	var jwt *gocloak.JWT
	var err error
	for attempt := 1; ; attempt++ {
		jwt, err = gc.LoginAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword, "master")
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("bootstrap: master admin login failed (keycloak never became reachable): %w", err)
		}
		logger.Warn("bootstrap: keycloak not ready, retrying", zap.Int("attempt", attempt), zap.Error(err))
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("bootstrap: master admin login cancelled: %w", ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
	token := jwt.AccessToken

	if err := ensureRealm(ctx, gc, token, cfg, logger); err != nil {
		return nil, err
	}

	if err := ensureGroups(ctx, gc, token, cfg.TargetRealm, logger); err != nil {
		return nil, err
	}

	if err := ensureAdminRole(ctx, gc, token, cfg.TargetRealm, logger); err != nil {
		return nil, err
	}

	secret, err := ensureServiceClient(ctx, gc, token, cfg, logger)
	if err != nil {
		return nil, err
	}

	if err := ensureDashboardClient(ctx, gc, token, cfg, logger); err != nil {
		return nil, err
	}

	if err := ensureServiceAccountRoles(ctx, gc, token, cfg, logger); err != nil {
		return nil, err
	}

	if err := ensurePostureFlow(ctx, gc, token, cfg, logger); err != nil {
		return nil, err
	}

	if cfg.CreateDemoUser {
		if err := ensureDemoUser(ctx, gc, token, cfg, logger); err != nil {
			return nil, err
		}
	}

	logger.Info("bootstrap: complete")
	return &Result{ServiceAccountSecret: secret}, nil
}

// ensureAdminRole creates the "admin" realm role if absent, so the realm is
// fully provisioned before any admin user exists. The setup-status check lists
// users by this role; if the role were missing, Keycloak returns "Could not
// find role" and the status endpoint would 500 instead of reporting
// unprovisioned.
func ensureAdminRole(ctx context.Context, gc *gocloak.GoCloak, token, realm string, logger *zap.Logger) error {
	if _, err := gc.GetRealmRole(ctx, token, realm, "admin"); err == nil {
		return nil
	}
	_, err := gc.CreateRealmRole(ctx, token, realm, gocloak.Role{Name: gocloak.StringP("admin")})
	if err != nil && !strings.Contains(err.Error(), "409") && !strings.Contains(strings.ToLower(err.Error()), "exist") {
		return fmt.Errorf("create admin realm role: %w", err)
	}
	logger.Info("bootstrap: ensured admin realm role")
	return nil
}

// ensureRealm creates the target realm if it does not already exist.
func ensureRealm(ctx context.Context, gc *gocloak.GoCloak, token string, cfg Config, logger *zap.Logger) error {
	_, err := gc.GetRealm(ctx, token, cfg.TargetRealm)
	if err == nil {
		logger.Info("bootstrap: realm already exists")
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("bootstrap: check realm: %w", err)
	}

	logger.Info("bootstrap: creating realm")
	_, err = gc.CreateRealm(ctx, token, gocloak.RealmRepresentation{
		Realm:                   gocloak.StringP(cfg.TargetRealm),
		Enabled:                 gocloak.BoolP(true),
		DisplayName:             gocloak.StringP("FreeCloud"),
		LoginWithEmailAllowed:   gocloak.BoolP(true),
		DuplicateEmailsAllowed:  gocloak.BoolP(false),
		ResetPasswordAllowed:    gocloak.BoolP(true),
		EditUsernameAllowed:     gocloak.BoolP(true),
		RegistrationAllowed:     gocloak.BoolP(false),
	})
	return err
}

// ensureGroups creates the standard department groups if absent.
func ensureGroups(ctx context.Context, gc *gocloak.GoCloak, token, realm string, logger *zap.Logger) error {
	names := []string{"Engineering", "Marketing", "Sales", "Operations"}
	for _, name := range names {
		n := name
		groups, err := gc.GetGroups(ctx, token, realm, gocloak.GetGroupsParams{Search: &n})
		if err != nil {
			return fmt.Errorf("bootstrap: list groups: %w", err)
		}
		found := false
		for _, g := range groups {
			if g.Name != nil && *g.Name == n {
				found = true
				break
			}
		}
		if found {
			logger.Info("bootstrap: group already exists", zap.String("group", n))
			continue
		}
		if _, err := gc.CreateGroup(ctx, token, realm, gocloak.Group{Name: &n}); err != nil {
			return fmt.Errorf("bootstrap: create group %q: %w", n, err)
		}
		logger.Info("bootstrap: created group", zap.String("group", n))
	}
	return nil
}

// ensureServiceClient creates (or updates) the freecloud-service confidential
// client and returns the active secret.
func ensureServiceClient(ctx context.Context, gc *gocloak.GoCloak, token string, cfg Config, logger *zap.Logger) (string, error) {
	clientID := cfg.ServiceClientID
	clients, err := gc.GetClients(ctx, token, cfg.TargetRealm, gocloak.GetClientsParams{ClientID: &clientID})
	if err != nil {
		return "", fmt.Errorf("bootstrap: list clients: %w", err)
	}

	// Determine the secret to use.
	secretValue := cfg.ServiceAccountSecretOverride
	if secretValue == "" {
		secretValue, err = generateHex(32)
		if err != nil {
			return "", fmt.Errorf("bootstrap: generate secret: %w", err)
		}
	}

	if len(clients) == 0 || clients[0].ID == nil {
		// Create the client.
		falseVal := false
		_, err = gc.CreateClient(ctx, token, cfg.TargetRealm, gocloak.Client{
			ClientID:                  &clientID,
			Name:                      gocloak.StringP("FreeCloud Backend Service"),
			Enabled:                   gocloak.BoolP(true),
			PublicClient:              &falseVal,
			ServiceAccountsEnabled:    gocloak.BoolP(true),
			AuthorizationServicesEnabled: &falseVal,
			StandardFlowEnabled:       &falseVal,
			DirectAccessGrantsEnabled: &falseVal,
			// Without full scope, KC 25 omits the SA's assigned realm-management
			// roles (manage-users/view-users/…) from its token, so admin calls 403.
			FullScopeAllowed:          gocloak.BoolP(true),
			Secret:                    &secretValue,
		})
		if err != nil {
			return "", fmt.Errorf("bootstrap: create service client: %w", err)
		}
		logger.Info("bootstrap: created freecloud-service client")
		return secretValue, nil
	}

	// Client exists. If an override secret is set, update it; otherwise
	// regenerate to get a fresh known value.
	clientUUID := *clients[0].ID

	// Ensure full scope on an already-existing client too, so the SA token
	// carries its realm-management roles (otherwise admin calls 403).
	if clients[0].FullScopeAllowed == nil || !*clients[0].FullScopeAllowed {
		clients[0].FullScopeAllowed = gocloak.BoolP(true)
		if err := gc.UpdateClient(ctx, token, cfg.TargetRealm, *clients[0]); err != nil {
			return "", fmt.Errorf("bootstrap: enable full scope on service client: %w", err)
		}
		logger.Info("bootstrap: enabled full scope on freecloud-service client")
	}

	if cfg.ServiceAccountSecretOverride != "" {
		// Set the exact override secret via UpdateClient (PUT body includes secret).
		clients[0].Secret = &secretValue
		if err := gc.UpdateClient(ctx, token, cfg.TargetRealm, *clients[0]); err != nil {
			return "", fmt.Errorf("bootstrap: update service client secret: %w", err)
		}
		logger.Info("bootstrap: synced freecloud-service client secret (override)")
		return secretValue, nil
	}

	// No override — regenerate.
	cred, err := gc.RegenerateClientSecret(ctx, token, cfg.TargetRealm, clientUUID)
	if err != nil {
		return "", fmt.Errorf("bootstrap: regenerate service client secret: %w", err)
	}
	if cred.Value == nil {
		return "", fmt.Errorf("bootstrap: regenerated secret is nil")
	}
	logger.Info("bootstrap: regenerated freecloud-service client secret")
	return *cred.Value, nil
}

// ensureDashboardClient creates the freecloud-dashboard public OIDC client if absent.
func ensureDashboardClient(ctx context.Context, gc *gocloak.GoCloak, token string, cfg Config, logger *zap.Logger) error {
	clientID := cfg.DashboardClientID
	clients, err := gc.GetClients(ctx, token, cfg.TargetRealm, gocloak.GetClientsParams{ClientID: &clientID})
	if err != nil {
		return fmt.Errorf("bootstrap: list dashboard clients: %w", err)
	}
	if len(clients) > 0 {
		logger.Info("bootstrap: freecloud-dashboard client already exists")
		return nil
	}

	trueVal := true
	falseVal := false
	_, err = gc.CreateClient(ctx, token, cfg.TargetRealm, gocloak.Client{
		ClientID:                  &clientID,
		Name:                      gocloak.StringP("FreeCloud Dashboard"),
		Enabled:                   gocloak.BoolP(true),
		PublicClient:              &trueVal,
		StandardFlowEnabled:       &trueVal,
		ImplicitFlowEnabled:       &falseVal,
		DirectAccessGrantsEnabled: &falseVal,
		ServiceAccountsEnabled:    &falseVal,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: create dashboard client: %w", err)
	}
	logger.Info("bootstrap: created freecloud-dashboard client")
	return nil
}

// ensureServiceAccountRoles grants manage-users and manage-clients realm-management
// roles to the freecloud-service service account (idempotent).
func ensureServiceAccountRoles(ctx context.Context, gc *gocloak.GoCloak, token string, cfg Config, logger *zap.Logger) error {
	saUsername := "service-account-" + cfg.ServiceClientID
	exact := true
	users, err := gc.GetUsers(ctx, token, cfg.TargetRealm, gocloak.GetUsersParams{
		Username: &saUsername,
		Exact:    &exact,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: get service account user: %w", err)
	}
	if len(users) == 0 || users[0].ID == nil {
		logger.Warn("bootstrap: service account user not found, skipping role grants")
		return nil
	}
	saUserID := *users[0].ID

	// Get realm-management client UUID.
	rmClientID := "realm-management"
	rmClients, err := gc.GetClients(ctx, token, cfg.TargetRealm, gocloak.GetClientsParams{ClientID: &rmClientID})
	if err != nil {
		return fmt.Errorf("bootstrap: get realm-management client: %w", err)
	}
	if len(rmClients) == 0 || rmClients[0].ID == nil {
		logger.Warn("bootstrap: realm-management client not found, skipping role grants")
		return nil
	}
	rmClientUUID := *rmClients[0].ID

	// Get already-assigned roles to avoid duplicates.
	existing, err := gc.GetClientRolesByUserID(ctx, token, cfg.TargetRealm, rmClientUUID, saUserID)
	if err != nil {
		return fmt.Errorf("bootstrap: get existing SA roles: %w", err)
	}
	existingSet := make(map[string]bool, len(existing))
	for _, r := range existing {
		if r.Name != nil {
			existingSet[*r.Name] = true
		}
	}

	var toGrant []gocloak.Role
	// The backend acts as the realm's administrator — it manages users, clients,
	// realm roles, realm SMTP, and identity providers on the operator's behalf.
	// Grant the realm-management "realm-admin" composite: narrower sets
	// (manage-users + manage-clients) miss realm-role reads (GetRealmRole),
	// users-by-role, realm updates (SMTP), and IdP management, all of which 403.
	for _, roleName := range []string{"realm-admin"} {
		if existingSet[roleName] {
			logger.Info("bootstrap: SA role already granted", zap.String("role", roleName))
			continue
		}
		r, err := gc.GetClientRole(ctx, token, cfg.TargetRealm, rmClientUUID, roleName)
		if err != nil {
			return fmt.Errorf("bootstrap: get role %q: %w", roleName, err)
		}
		toGrant = append(toGrant, *r)
	}

	if len(toGrant) > 0 {
		if err := gc.AddClientRolesToUser(ctx, token, cfg.TargetRealm, rmClientUUID, saUserID, toGrant); err != nil {
			return fmt.Errorf("bootstrap: grant SA roles: %w", err)
		}
		logger.Info("bootstrap: granted SA roles", zap.Int("count", len(toGrant)))
	}
	return nil
}

// ensurePostureFlow copies the browser flow to browser-with-posture, adds the
// freecloud-posture-check execution (REQUIRED), and binds it as the realm
// browser flow — all idempotent.
func ensurePostureFlow(ctx context.Context, gc *gocloak.GoCloak, token string, cfg Config, logger *zap.Logger) error {
	flows, err := gc.GetAuthenticationFlows(ctx, token, cfg.TargetRealm)
	if err != nil {
		return fmt.Errorf("bootstrap: get authentication flows: %w", err)
	}
	for _, f := range flows {
		if f.Alias != nil && *f.Alias == cfg.PostureFlowAlias {
			logger.Info("bootstrap: posture flow already exists")
			return nil
		}
	}

	// Copy the browser flow. gocloak v13 has no typed method for this, so use raw HTTP.
	baseURL := strings.TrimRight(cfg.KeycloakURL, "/")
	copyURL := fmt.Sprintf("%s/admin/realms/%s/authentication/flows/browser/copy", baseURL, cfg.TargetRealm)
	body, _ := json.Marshal(map[string]string{"newName": cfg.PostureFlowAlias})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copyURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("bootstrap: build copy-flow request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("bootstrap: copy browser flow: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bootstrap: copy browser flow: status %d: %s", resp.StatusCode, string(b))
	}
	logger.Info("bootstrap: copied browser flow", zap.String("alias", cfg.PostureFlowAlias))

	// Add the posture-check execution.
	provider := "freecloud-posture-check"
	if err := gc.CreateAuthenticationExecution(ctx, token, cfg.TargetRealm, cfg.PostureFlowAlias,
		gocloak.CreateAuthenticationExecutionRepresentation{Provider: &provider}); err != nil {
		return fmt.Errorf("bootstrap: add posture execution: %w", err)
	}
	logger.Info("bootstrap: added freecloud-posture-check execution")

	// Find the execution we just added and mark it REQUIRED.
	executions, err := gc.GetAuthenticationExecutions(ctx, token, cfg.TargetRealm, cfg.PostureFlowAlias)
	if err != nil {
		return fmt.Errorf("bootstrap: get executions: %w", err)
	}
	var postureExec *gocloak.ModifyAuthenticationExecutionRepresentation
	for _, e := range executions {
		if e.ProviderID != nil && *e.ProviderID == provider {
			postureExec = e
			break
		}
	}
	if postureExec == nil {
		return fmt.Errorf("bootstrap: could not find posture execution after creation")
	}
	required := "REQUIRED"
	postureExec.Requirement = &required
	if err := gc.UpdateAuthenticationExecution(ctx, token, cfg.TargetRealm, cfg.PostureFlowAlias, *postureExec); err != nil {
		return fmt.Errorf("bootstrap: set posture execution REQUIRED: %w", err)
	}
	logger.Info("bootstrap: set posture execution REQUIRED")

	// Bind as realm browser flow.
	realm, err := gc.GetRealm(ctx, token, cfg.TargetRealm)
	if err != nil {
		return fmt.Errorf("bootstrap: get realm for browser flow binding: %w", err)
	}
	realm.BrowserFlow = &cfg.PostureFlowAlias
	if err := gc.UpdateRealm(ctx, token, *realm); err != nil {
		return fmt.Errorf("bootstrap: bind browser flow: %w", err)
	}
	logger.Info("bootstrap: bound posture flow as realm browser flow")
	return nil
}

// ensureDemoUser creates demo@freecloud.local if absent.
func ensureDemoUser(ctx context.Context, gc *gocloak.GoCloak, token string, cfg Config, logger *zap.Logger) error {
	username := "demo"
	exact := true
	users, err := gc.GetUsers(ctx, token, cfg.TargetRealm, gocloak.GetUsersParams{
		Username: &username,
		Exact:    &exact,
	})
	if err != nil {
		return fmt.Errorf("bootstrap: check demo user: %w", err)
	}
	if len(users) > 0 {
		logger.Info("bootstrap: demo user already exists")
		return nil
	}

	password := cfg.DemoPassword
	if password == "" {
		password, err = generateHex(8)
		if err != nil {
			return fmt.Errorf("bootstrap: generate demo password: %w", err)
		}
	}

	email := "demo@freecloud.local"
	first := "Demo"
	last := "User"
	userID, err := gc.CreateUser(ctx, token, cfg.TargetRealm, gocloak.User{
		Username:  &username,
		Email:     &email,
		FirstName: &first,
		LastName:  &last,
		Enabled:   gocloak.BoolP(true),
	})
	if err != nil {
		return fmt.Errorf("bootstrap: create demo user: %w", err)
	}
	if err := gc.SetPassword(ctx, token, userID, cfg.TargetRealm, password, false); err != nil {
		return fmt.Errorf("bootstrap: set demo user password: %w", err)
	}
	logger.Info("bootstrap: created demo user", zap.String("user_id", userID))
	return nil
}

// generateHex returns a cryptographically-random hex string of the given byte length.
func generateHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// isNotFound reports whether the error from gocloak represents a 404.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "404") || strings.Contains(strings.ToLower(msg), "not found")
}
