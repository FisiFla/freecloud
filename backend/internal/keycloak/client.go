package keycloak

import (
	"context"
	"fmt"

	"github.com/Nerzal/gocloak/v13"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// KeycloakClient wraps gocloak.GoCloak for FreeCloud operations.
type KeycloakClient struct {
	client     *gocloak.GoCloak
	clientID   string
	clientSecret string
	realm      string
}

// NewClient creates a new KeycloakClient.
func NewClient(url, clientID, clientSecret, realm string) *KeycloakClient {
	return &KeycloakClient{
		client:       gocloak.NewClient(url),
		clientID:     clientID,
		clientSecret: clientSecret,
		realm:        realm,
	}
}

// login obtains an admin token using client credentials.
func (k *KeycloakClient) login(ctx context.Context) (string, error) {
	token, err := k.client.LoginClient(ctx, k.clientID, k.clientSecret, k.realm)
	if err != nil {
		return "", fmt.Errorf("keycloak login: %w", err)
	}
	return token.AccessToken, nil
}

// CreateUser creates a Keycloak user, sets a temporary password, and assigns them
// to a group matching the provided department name.
func (k *KeycloakClient) CreateUser(ctx context.Context, firstName, lastName, email, department string) (*gocloak.User, error) {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}

	userID := uuid.New().String()
	user := gocloak.User{
		ID:        &userID,
		FirstName: &firstName,
		LastName:  &lastName,
		Email:     &email,
		Enabled:   gocloak.BoolP(true),
	}

	created, err := k.client.CreateUser(ctx, token, k.realm, user)
	if err != nil {
		return nil, fmt.Errorf("create keycloak user: %w", err)
	}

	logger.Info("created keycloak user",
		zap.String("user_id", created),
		zap.String("email", email),
	)

	// Set temporary password
	tempPass := uuid.New().String()[:12] + "!Aa1"
	err = k.client.SetPassword(ctx, token, created, k.realm, tempPass, true)
	if err != nil {
		logger.Warn("failed to set temporary password, continuing",
			zap.String("user_id", created),
			zap.Error(err),
		)
	}

	// Best-effort: send email with UPDATE_PASSWORD required action
	if err == nil {
		if execErr := k.client.ExecuteActionsEmail(ctx, token, k.realm, gocloak.ExecuteActionsEmail{
			UserID:  &created,
			Actions: &[]string{"UPDATE_PASSWORD"},
		}); execErr != nil {
			logger.Warn("failed to send execute-actions-email, continuing",
				zap.String("user_id", created),
				zap.Error(execErr),
			)
		}
	}

	// Assign to department group
	if department != "" {
		groups, err := k.client.GetGroups(ctx, token, k.realm, gocloak.GetGroupsParams{
			Search: &department,
		})
		if err != nil {
			logger.Warn("failed to fetch groups, skipping group assignment",
				zap.String("department", department),
				zap.Error(err),
			)
		} else {
			var groupID string
			for _, g := range groups {
				if g.Name != nil && *g.Name == department {
					groupID = *g.ID
					break
				}
			}
			if groupID != "" {
				err = k.client.AddUserToGroup(ctx, token, k.realm, created, groupID)
				if err != nil {
					logger.Warn("failed to add user to group, continuing",
						zap.String("user_id", created),
						zap.String("group_id", groupID),
						zap.Error(err),
					)
				}
			} else {
				logger.Warn("no matching group found for department, skipping",
					zap.String("department", department),
				)
			}
		}
	}

	// Return the user object with the ID populated
	result := gocloak.User{
		ID:        &created,
		FirstName: &firstName,
		LastName:  &lastName,
		Email:     &email,
		Enabled:   gocloak.BoolP(true),
	}
	return &result, nil
}

// DisableUser disables a Keycloak user by setting enabled=false.
func (k *KeycloakClient) DisableUser(ctx context.Context, userID string) error {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return err
	}

	user, err := k.client.GetUserByID(ctx, token, k.realm, userID)
	if err != nil {
		return fmt.Errorf("get user %s: %w", userID, err)
	}

	user.Enabled = gocloak.BoolP(false)
	err = k.client.UpdateUser(ctx, token, k.realm, *user)
	if err != nil {
		return fmt.Errorf("disable user %s: %w", userID, err)
	}

	logger.Info("disabled keycloak user", zap.String("user_id", userID))
	return nil
}

// LogoutAllSessions logs out all active sessions for a user.
func (k *KeycloakClient) LogoutAllSessions(ctx context.Context, userID string) error {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return err
	}

	err = k.client.LogoutUserSession(ctx, token, k.realm, userID)
	if err != nil {
		return fmt.Errorf("logout sessions for user %s: %w", userID, err)
	}

	logger.Info("logged out all sessions for user", zap.String("user_id", userID))
	return nil
}

// CreateClient creates an OIDC or SAML client in Keycloak and returns the client ID.
func (k *KeycloakClient) CreateClient(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string) (string, error) {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return "", err
	}

	clientID := uuid.New().String()

	client := gocloak.Client{
		ID:                        &clientID,
		ClientID:                  &name,
		Name:                      &name,
		Protocol:                  &protocol,
		RedirectURIs:              &redirectURIs,
		BaseURL:                   &baseURL,
		Enabled:                   gocloak.BoolP(true),
		StandardFlowEnabled:       gocloak.BoolP(true),
		ImplicitFlowEnabled:       gocloak.BoolP(false),
		DirectAccessGrantsEnabled: gocloak.BoolP(true),
		ServiceAccountsEnabled:    gocloak.BoolP(false),
		PublicClient:              gocloak.BoolP(false),
	}

	createdID, err := k.client.CreateClient(ctx, token, k.realm, client)
	if err != nil {
		return "", fmt.Errorf("create keycloak client: %w", err)
	}

	logger.Info("created keycloak client",
		zap.String("client_id", createdID),
		zap.String("name", name),
		zap.String("protocol", protocol),
	)

	return createdID, nil
}

// AssignUserToClient assigns a user to a Keycloak client (e.g., through a group or role).
// This uses the client role mapping approach.
func (k *KeycloakClient) AssignUserToClient(ctx context.Context, userID, clientID string) error {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return err
	}

	// Create a client role for mapping
	roleName := "user"
	role := gocloak.Role{
		Name: &roleName,
	}

	_, err = k.client.CreateClientRole(ctx, token, k.realm, clientID, role)
	if err != nil {
		// Role may already exist, continue
		logger.Debug("client role may already exist, continuing",
			zap.String("client_id", clientID),
			zap.Error(err),
		)
	}

	// Get the client role
	clientRole, err := k.client.GetClientRole(ctx, token, k.realm, clientID, roleName)
	if err != nil {
		return fmt.Errorf("get client role: %w", err)
	}

	// Assign role to user
	err = k.client.AddClientRoleToUser(ctx, token, k.realm, clientID, userID, []gocloak.Role{*clientRole})
	if err != nil {
		return fmt.Errorf("assign user %s to client %s: %w", userID, clientID, err)
	}

	logger.Info("assigned user to client",
		zap.String("user_id", userID),
		zap.String("client_id", clientID),
	)
	return nil
}

// DeleteClient deletes a client from Keycloak by its client ID.
func (k *KeycloakClient) DeleteClient(ctx context.Context, clientID string) error {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return err
	}

	err = k.client.DeleteClient(ctx, token, k.realm, clientID)
	if err != nil {
		return fmt.Errorf("delete keycloak client %s: %w", clientID, err)
	}

	logger.Info("deleted keycloak client", zap.String("client_id", clientID))
	return nil
}

// GetUserGroups returns the groups a user belongs to.
func (k *KeycloakClient) GetUserGroups(ctx context.Context, userID string) ([]*gocloak.Group, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}

	groups, err := k.client.GetUserGroups(ctx, token, k.realm, userID, gocloak.GetGroupsParams{})
	if err != nil {
		return nil, fmt.Errorf("get groups for user %s: %w", userID, err)
	}

	return groups, nil
}

// Ping checks connectivity to Keycloak by attempting a login.
func (k *KeycloakClient) Ping(ctx context.Context) error {
	_, err := k.login(ctx)
	return err
}
