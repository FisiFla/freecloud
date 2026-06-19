package keycloak

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Nerzal/gocloak/v13"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// KeycloakClientInterface defines the operations used by handlers.
type KeycloakClientInterface interface {
	CreateUser(ctx context.Context, firstName, lastName, email, department string) (*CreateUserResult, error)
	DeleteUser(ctx context.Context, userID string) error
	DisableUser(ctx context.Context, userID string) error
	UpdateUser(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error
	LogoutAllSessions(ctx context.Context, userID string) error
	GetUserSessions(ctx context.Context, userID string) ([]*gocloak.UserSessionRepresentation, error)
	CreateClient(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string) (string, error)
	DeleteClient(ctx context.Context, clientID string) error
	AssignUserToClient(ctx context.Context, userID, clientID string) error
	GetUserGroups(ctx context.Context, userID string) ([]*gocloak.Group, error)
	ListGroups(ctx context.Context) ([]*gocloak.Group, error)
	CreateGroup(ctx context.Context, name string) (string, error)
	AddUserToGroup(ctx context.Context, userID, groupID string) error
	RemoveUserFromGroup(ctx context.Context, userID, groupID string) error
	ListRealmRoles(ctx context.Context) ([]*gocloak.Role, error)
	AssignRealmRoleToUser(ctx context.Context, userID string, roles []gocloak.Role) error
	SendPasswordReset(ctx context.Context, userID string) error
	Ping(ctx context.Context) error

	// C2: MFA surfacing + require-MFA.
	// GetUserCredentials returns the credential types currently registered for
	// the user (e.g. "otp", "webauthn").
	GetUserCredentials(ctx context.Context, userID string) ([]string, error)
	// GetUserRequiredActions returns the pending required actions on the user.
	GetUserRequiredActions(ctx context.Context, userID string) ([]string, error)
	// SetRequiredAction adds a required action to the user (idempotent).
	SetRequiredAction(ctx context.Context, userID, action string) error

	// C3: Self-service password reset.
	// SendPasswordResetEmail triggers an execute-actions email with UPDATE_PASSWORD.
	SendPasswordResetEmail(ctx context.Context, userID string) error

	// ListUsers returns all enabled users in the realm. Used by the reconciliation
	// job to detect Keycloak↔DB drift.
	ListUsers(ctx context.Context) ([]gocloak.User, error)

	// B1: SCIM Group operations.

	// GetGroupByID fetches a single group by its Keycloak ID.
	GetGroupByID(ctx context.Context, groupID string) (*gocloak.Group, error)
	// ListGroupMembers returns the users that belong to a group.
	ListGroupMembers(ctx context.Context, groupID string) ([]*gocloak.User, error)
	// RenameGroup changes a group's display name.
	RenameGroup(ctx context.Context, groupID, newName string) error
	// DeleteGroup removes a group from the realm.
	DeleteGroup(ctx context.Context, groupID string) error
}

// CreateUserResult holds the outcome of a CreateUser operation.
type CreateUserResult struct {
	User         *gocloak.User
	PasswordSet  bool
	ResetEmailSent bool
	SetupWarning string
}

// tokenExpiryBuffer is how long before actual expiry a cached admin token is
// considered stale, so a request never goes out with an about-to-expire token.
const tokenExpiryBuffer = 30 * time.Second

// KeycloakClient wraps gocloak.GoCloak for FreeCloud operations.
type KeycloakClient struct {
	client       *gocloak.GoCloak
	clientID     string
	clientSecret string
	realm        string

	// Cached client-credentials admin token. Keycloak admin tokens are short
	// lived (default 60s), and previously every operation fetched a fresh one —
	// doubling the round-trips and risking Keycloak rate limits under load.
	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
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

// login returns a valid admin token, reusing the cached one until it is within
// tokenExpiryBuffer of expiry. Thread-safe.
func (k *KeycloakClient) login(ctx context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.cachedToken != "" && time.Now().Add(tokenExpiryBuffer).Before(k.tokenExpiry) {
		return k.cachedToken, nil
	}

	token, err := k.client.LoginClient(ctx, k.clientID, k.clientSecret, k.realm)
	if err != nil {
		return "", fmt.Errorf("keycloak login: %w", err)
	}
	k.cachedToken = token.AccessToken
	k.tokenExpiry = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	return k.cachedToken, nil
}

// CreateUser creates a Keycloak user, sets a temporary password, and assigns them
// to a group matching the provided department name.
func (k *KeycloakClient) CreateUser(ctx context.Context, firstName, lastName, email, department string) (*CreateUserResult, error) {
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

	result := &CreateUserResult{
		User: &gocloak.User{
			ID:        &created,
			FirstName: &firstName,
			LastName:  &lastName,
			Email:     &email,
			Enabled:   gocloak.BoolP(true),
		},
	}

	// Set temporary password
	tempPass := uuid.New().String()[:12] + "!Aa1"
	err = k.client.SetPassword(ctx, token, created, k.realm, tempPass, true)
	if err != nil {
		return nil, fmt.Errorf("set password for user %s: %w", created, err)
	}
	result.PasswordSet = true

	// Send email with UPDATE_PASSWORD required action
	if execErr := k.client.ExecuteActionsEmail(ctx, token, k.realm, gocloak.ExecuteActionsEmail{
		UserID:  &created,
		Actions: &[]string{"UPDATE_PASSWORD"},
	}); execErr != nil {
		logger.Warn("failed to send execute-actions-email",
			zap.String("user_id", created),
			zap.Error(execErr),
		)
		result.ResetEmailSent = false
		result.SetupWarning = "Password reset email could not be sent"
	} else {
		result.ResetEmailSent = true
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

	return result, nil
}

// DeleteUser permanently deletes a Keycloak user by ID. It is used to roll back
// an orphaned user when a subsequent onboarding step (DB persistence) fails.
func (k *KeycloakClient) DeleteUser(ctx context.Context, userID string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.DeleteUser(ctx, token, k.realm, userID); err != nil {
		return fmt.Errorf("delete keycloak user %s: %w", userID, err)
	}
	zap.L().Info("deleted keycloak user", zap.String("user_id", userID))
	return nil
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
//
// Keycloak's LogoutUserSession takes a *session* ID, not a user ID, so we first
// enumerate the user's active sessions via GetUserSessions and log each one out
// individually. Returns nil if there are no active sessions.
func (k *KeycloakClient) LogoutAllSessions(ctx context.Context, userID string) error {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return err
	}

	sessions, err := k.client.GetUserSessions(ctx, token, k.realm, userID)
	if err != nil {
		return fmt.Errorf("get sessions for user %s: %w", userID, err)
	}

	var lastErr error
	loggedOut := 0
	for _, s := range sessions {
		if s.ID == nil || *s.ID == "" {
			continue
		}
		if err := k.client.LogoutUserSession(ctx, token, k.realm, *s.ID); err != nil {
			logger.Warn("failed to logout session, continuing",
				zap.String("user_id", userID),
				zap.String("session_id", *s.ID),
				zap.Error(err),
			)
			lastErr = err
			continue
		}
		loggedOut++
	}

	if lastErr != nil {
		return fmt.Errorf("logged out %d/%d sessions for user %s, last error: %w",
			loggedOut, len(sessions), userID, lastErr)
	}

	logger.Info("logged out all sessions for user",
		zap.String("user_id", userID),
		zap.Int("sessions", loggedOut),
	)
	return nil
}

// GetUserSessions returns the active sessions for a user.
func (k *KeycloakClient) GetUserSessions(ctx context.Context, userID string) ([]*gocloak.UserSessionRepresentation, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	return k.client.GetUserSessions(ctx, token, k.realm, userID)
}

// UpdateUser updates mutable profile fields for a Keycloak user.
func (k *KeycloakClient) UpdateUser(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}

	user, err := k.client.GetUserByID(ctx, token, k.realm, userID)
	if err != nil {
		return fmt.Errorf("get user %s: %w", userID, err)
	}

	user.FirstName = &firstName
	user.LastName = &lastName
	user.Enabled = &enabled
	// Store department in user attributes so it survives round-trips.
	if user.Attributes == nil {
		attrs := map[string][]string{}
		user.Attributes = &attrs
	}
	(*user.Attributes)["department"] = []string{department}

	if err := k.client.UpdateUser(ctx, token, k.realm, *user); err != nil {
		return fmt.Errorf("update keycloak user %s: %w", userID, err)
	}
	zap.L().Info("updated keycloak user", zap.String("user_id", userID))
	return nil
}

// SendPasswordReset triggers a Keycloak UPDATE_PASSWORD required-action email.
func (k *KeycloakClient) SendPasswordReset(ctx context.Context, userID string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.ExecuteActionsEmail(ctx, token, k.realm, gocloak.ExecuteActionsEmail{
		UserID:  &userID,
		Actions: &[]string{"UPDATE_PASSWORD"},
	}); err != nil {
		return fmt.Errorf("execute actions email for user %s: %w", userID, err)
	}
	zap.L().Info("sent password reset email", zap.String("user_id", userID))
	return nil
}

// ListGroups returns all groups in the realm.
func (k *KeycloakClient) ListGroups(ctx context.Context) ([]*gocloak.Group, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := k.client.GetGroups(ctx, token, k.realm, gocloak.GetGroupsParams{})
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return groups, nil
}

// CreateGroup creates a new realm group and returns its ID.
func (k *KeycloakClient) CreateGroup(ctx context.Context, name string) (string, error) {
	token, err := k.login(ctx)
	if err != nil {
		return "", err
	}
	groupID, err := k.client.CreateGroup(ctx, token, k.realm, gocloak.Group{Name: &name})
	if err != nil {
		return "", fmt.Errorf("create group %q: %w", name, err)
	}
	zap.L().Info("created group", zap.String("group_id", groupID), zap.String("name", name))
	return groupID, nil
}

// AddUserToGroup adds a user to a Keycloak group.
func (k *KeycloakClient) AddUserToGroup(ctx context.Context, userID, groupID string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.AddUserToGroup(ctx, token, k.realm, userID, groupID); err != nil {
		return fmt.Errorf("add user %s to group %s: %w", userID, groupID, err)
	}
	return nil
}

// RemoveUserFromGroup removes a user from a Keycloak group.
func (k *KeycloakClient) RemoveUserFromGroup(ctx context.Context, userID, groupID string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.DeleteUserFromGroup(ctx, token, k.realm, userID, groupID); err != nil {
		return fmt.Errorf("remove user %s from group %s: %w", userID, groupID, err)
	}
	return nil
}

// ListRealmRoles returns all realm-level roles.
func (k *KeycloakClient) ListRealmRoles(ctx context.Context) ([]*gocloak.Role, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	roles, err := k.client.GetRealmRoles(ctx, token, k.realm, gocloak.GetRoleParams{})
	if err != nil {
		return nil, fmt.Errorf("list realm roles: %w", err)
	}
	return roles, nil
}

// AssignRealmRoleToUser assigns one or more realm roles to a user.
func (k *KeycloakClient) AssignRealmRoleToUser(ctx context.Context, userID string, roles []gocloak.Role) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.AddRealmRoleToUser(ctx, token, k.realm, userID, roles); err != nil {
		return fmt.Errorf("assign realm roles to user %s: %w", userID, err)
	}
	return nil
}

// samlProtocolMappers returns the standard set of X.500/SAML attribute mappers
// for email, firstName, lastName, and username. These are required for most SP
// implementations to receive user identity in the assertion.
func samlProtocolMappers() *[]gocloak.ProtocolMapperRepresentation {
	trueStr := "true"
	mappers := []gocloak.ProtocolMapperRepresentation{
		{
			Name:            gocloak.StringP("X500 email"),
			Protocol:        gocloak.StringP("saml"),
			ProtocolMapper:  gocloak.StringP("saml-user-property-mapper"),
			ConsentRequired: gocloak.BoolP(false),
			Config: &map[string]string{
				"attribute.nameformat": "URI Reference",
				"user.attribute":       "email",
				"attribute.name":       "urn:oid:1.2.840.113549.1.9.1",
				"friendly.name":        "email",
			},
		},
		{
			Name:            gocloak.StringP("X500 givenName"),
			Protocol:        gocloak.StringP("saml"),
			ProtocolMapper:  gocloak.StringP("saml-user-property-mapper"),
			ConsentRequired: gocloak.BoolP(false),
			Config: &map[string]string{
				"attribute.nameformat": "URI Reference",
				"user.attribute":       "firstName",
				"attribute.name":       "urn:oid:2.5.4.42",
				"friendly.name":        "givenName",
			},
		},
		{
			Name:            gocloak.StringP("X500 surname"),
			Protocol:        gocloak.StringP("saml"),
			ProtocolMapper:  gocloak.StringP("saml-user-property-mapper"),
			ConsentRequired: gocloak.BoolP(false),
			Config: &map[string]string{
				"attribute.nameformat": "URI Reference",
				"user.attribute":       "lastName",
				"attribute.name":       "urn:oid:2.5.4.4",
				"friendly.name":        "sn",
			},
		},
		{
			Name:            gocloak.StringP("username"),
			Protocol:        gocloak.StringP("saml"),
			ProtocolMapper:  gocloak.StringP("saml-user-property-mapper"),
			ConsentRequired: gocloak.BoolP(false),
			Config: &map[string]string{
				"attribute.nameformat": "Basic",
				"user.attribute":       "username",
				"attribute.name":       "username",
			},
		},
		{
			Name:            gocloak.StringP("role list"),
			Protocol:        gocloak.StringP("saml"),
			ProtocolMapper:  gocloak.StringP("saml-role-list-mapper"),
			ConsentRequired: gocloak.BoolP(false),
			Config: &map[string]string{
				"single":               trueStr,
				"attribute.name":       "Role",
				"attribute.nameformat": "Basic",
			},
		},
	}
	return &mappers
}

// CreateClient creates an OIDC or SAML client in Keycloak and returns the client ID.
func (k *KeycloakClient) CreateClient(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string) (string, error) {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return "", err
	}

	// Lower-case protocol name as required by Keycloak.
	kcProtocol := strings.ToLower(protocol)

	client := gocloak.Client{
		ClientID:                  &name,
		Name:                      &name,
		Protocol:                  &kcProtocol,
		RedirectURIs:              &redirectURIs,
		BaseURL:                   &baseURL,
		Enabled:                   gocloak.BoolP(true),
		StandardFlowEnabled:       gocloak.BoolP(true),
		ImplicitFlowEnabled:       gocloak.BoolP(false),
		DirectAccessGrantsEnabled: gocloak.BoolP(false),
		ServiceAccountsEnabled:    gocloak.BoolP(false),
		PublicClient:              gocloak.BoolP(false),
	}

	if kcProtocol == "saml" {
		// Derive the SP entity ID from baseURL (falling back to name) and ACS URL
		// from the first redirect URI. Both are required for SAML to function.
		entityID := baseURL
		if entityID == "" {
			entityID = name
		}
		acsURL := ""
		if len(redirectURIs) > 0 {
			acsURL = redirectURIs[0]
		}

		client.Attributes = &map[string]string{
			// NameID format: persistent is the most interoperable default.
			"saml_name_id_format":                                "persistent",
			// SP entity ID
			"saml_sp_entity_id":                                  entityID,
			// ACS URL (POST binding)
			"saml.assertion.consumer.service.post.binding.url":   acsURL,
			"saml_assertion_consumer_url_post":                   acsURL,
			// Signing
			"saml.server.signature":                              "true",
			"saml.assertion.signature":                           "true",
			"saml.client.signature":                              "false",
			// Token details
			"saml.authnstatement":                                "true",
			"saml.onetimeuse.condition":                          "false",
			"saml.server.signature.keyinfo.ext":                  "false",
			"saml.force.post.binding":                            "true",
			"saml.multivalued.roles":                             "false",
			"saml.encrypt":                                       "false",
			// Logout
			"saml.server.signature.logout":                       "true",
			// Session
			"saml_assertion_lifespan":                            "3600",
		}
		client.ProtocolMappers = samlProtocolMappers()
	}

	createdID, err := k.client.CreateClient(ctx, token, k.realm, client)
	if err != nil {
		return "", fmt.Errorf("create keycloak client: %w", err)
	}

	logger.Info("created keycloak client",
		zap.String("client_id", createdID),
		zap.String("name", name),
		zap.String("protocol", kcProtocol),
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

	// Create a client role for mapping (idempotent — ignore "already exists").
	roleName := "user"
	role := gocloak.Role{
		Name: &roleName,
	}

	if _, err := k.client.CreateClientRole(ctx, token, k.realm, clientID, role); err != nil {
		// A 409 means the role already exists, which is fine. Any other error is a real failure.
		if !isConflictErr(err) {
			return fmt.Errorf("create client role %q for client %s: %w", roleName, clientID, err)
		}
		logger.Debug("client role already exists, continuing",
			zap.String("client_id", clientID),
		)
	}

	// Get the client role
	clientRole, err := k.client.GetClientRole(ctx, token, k.realm, clientID, roleName)
	if err != nil {
		return fmt.Errorf("get client role: %w", err)
	}

	// Assign role to user
	err = k.client.AddClientRolesToUser(ctx, token, k.realm, clientID, userID, []gocloak.Role{*clientRole})
	if err != nil {
		return fmt.Errorf("assign user %s to client %s: %w", userID, clientID, err)
	}

	logger.Info("assigned user to client",
		zap.String("user_id", userID),
		zap.String("client_id", clientID),
	)
	return nil
}

// isConflictErr reports whether the given gocloak error represents a 409 Conflict,
// which we treat as "already exists" for idempotent create operations.
func isConflictErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// gocloak wraps HTTP errors with the status code in the message.
	return strings.Contains(msg, "409") || strings.Contains(strings.ToLower(msg), "conflict")
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

// GetUserCredentials returns the credential types currently registered for a
// user (e.g. "otp", "webauthn").  Keycloak's GetCredentials endpoint returns
// the full credential representation; we extract only the type field.
func (k *KeycloakClient) GetUserCredentials(ctx context.Context, userID string) ([]string, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	creds, err := k.client.GetCredentials(ctx, token, k.realm, userID)
	if err != nil {
		return nil, fmt.Errorf("get credentials for user %s: %w", userID, err)
	}
	types := make([]string, 0, len(creds))
	for _, c := range creds {
		if c.Type != nil {
			types = append(types, *c.Type)
		}
	}
	return types, nil
}

// GetUserRequiredActions returns the pending required actions for a user.
func (k *KeycloakClient) GetUserRequiredActions(ctx context.Context, userID string) ([]string, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	user, err := k.client.GetUserByID(ctx, token, k.realm, userID)
	if err != nil {
		return nil, fmt.Errorf("get user %s: %w", userID, err)
	}
	if user.RequiredActions == nil {
		return nil, nil
	}
	return *user.RequiredActions, nil
}

// SetRequiredAction adds the given required action to the user, preserving any
// existing required actions (idempotent).
func (k *KeycloakClient) SetRequiredAction(ctx context.Context, userID, action string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	user, err := k.client.GetUserByID(ctx, token, k.realm, userID)
	if err != nil {
		return fmt.Errorf("get user %s: %w", userID, err)
	}

	existing := []string{}
	if user.RequiredActions != nil {
		existing = *user.RequiredActions
	}
	// Idempotent: only add if not already present.
	for _, a := range existing {
		if a == action {
			return nil // already set
		}
	}
	updated := append(existing, action)
	user.RequiredActions = &updated
	if err := k.client.UpdateUser(ctx, token, k.realm, *user); err != nil {
		return fmt.Errorf("set required action %q for user %s: %w", action, userID, err)
	}
	zap.L().Info("set required action on user",
		zap.String("user_id", userID), zap.String("action", action))
	return nil
}

// SendPasswordResetEmail sends an execute-actions email with UPDATE_PASSWORD to
// the user, triggering the Keycloak self-service password reset flow.
func (k *KeycloakClient) SendPasswordResetEmail(ctx context.Context, userID string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if execErr := k.client.ExecuteActionsEmail(ctx, token, k.realm, gocloak.ExecuteActionsEmail{
		UserID:  &userID,
		Actions: &[]string{"UPDATE_PASSWORD"},
	}); execErr != nil {
		return fmt.Errorf("execute-actions-email for user %s: %w", userID, execErr)
	}
	zap.L().Info("sent password reset email", zap.String("user_id", userID))
	return nil
}

// ListUsers returns all users in the realm. Used by the reconciliation job.
// Pagination is handled internally using Keycloak's offset/limit params.
func (k *KeycloakClient) ListUsers(ctx context.Context) ([]gocloak.User, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}

	const pageSize = 100
	var all []gocloak.User
	first := 0
	for {
		page, err := k.client.GetUsers(ctx, token, k.realm, gocloak.GetUsersParams{
			First: gocloak.IntP(first),
			Max:   gocloak.IntP(pageSize),
		})
		if err != nil {
			return nil, fmt.Errorf("list keycloak users (offset %d): %w", first, err)
		}
		for _, u := range page {
			if u != nil {
				all = append(all, *u)
			}
		}
		if len(page) < pageSize {
			break
		}
		first += pageSize
	}
	return all, nil
}

// GetGroupByID fetches a single Keycloak group by its ID.
func (k *KeycloakClient) GetGroupByID(ctx context.Context, groupID string) (*gocloak.Group, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	g, err := k.client.GetGroup(ctx, token, k.realm, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group %s: %w", groupID, err)
	}
	return g, nil
}

// ListGroupMembers returns the users that belong to a group.
func (k *KeycloakClient) ListGroupMembers(ctx context.Context, groupID string) ([]*gocloak.User, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	members, err := k.client.GetGroupMembers(ctx, token, k.realm, groupID, gocloak.GetGroupsParams{})
	if err != nil {
		return nil, fmt.Errorf("list members of group %s: %w", groupID, err)
	}
	return members, nil
}

// RenameGroup changes a group's display name.
func (k *KeycloakClient) RenameGroup(ctx context.Context, groupID, newName string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.UpdateGroup(ctx, token, k.realm, gocloak.Group{ID: &groupID, Name: &newName}); err != nil {
		return fmt.Errorf("rename group %s: %w", groupID, err)
	}
	zap.L().Info("renamed group", zap.String("group_id", groupID), zap.String("name", newName))
	return nil
}

// DeleteGroup removes a group from the realm.
func (k *KeycloakClient) DeleteGroup(ctx context.Context, groupID string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.DeleteGroup(ctx, token, k.realm, groupID); err != nil {
		return fmt.Errorf("delete group %s: %w", groupID, err)
	}
	zap.L().Info("deleted group", zap.String("group_id", groupID))
	return nil
}
