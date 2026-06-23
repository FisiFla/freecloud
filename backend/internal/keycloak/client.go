package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
	CreateClient(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string, opts *SAMLOptions) (string, error)
	// GetSAMLIdPInitiatedURL returns the full Keycloak IdP-initiated SSO URL for a SAML client.
	GetSAMLIdPInitiatedURL(ctx context.Context, keycloakClientID string) (string, error)
	// GetSAMLMetadataXML returns the Keycloak realm SAML IdP metadata XML.
	// SPs import this to configure trust with FreeCloud as the IdP.
	GetSAMLMetadataXML(ctx context.Context) (string, error)
	DeleteClient(ctx context.Context, clientID string) error
	AssignUserToClient(ctx context.Context, userID, clientID string) error
	UnassignUserFromClient(ctx context.Context, userID, clientID string) error
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

	// D1: Account / password policy.
	// GetRealmPolicy returns the realm's current password-policy string and
	// brute-force protection settings.
	GetRealmPolicy(ctx context.Context) (*RealmPolicyResult, error)
	// UpdateRealmPolicy writes new password-policy + brute-force settings to the realm.
	UpdateRealmPolicy(ctx context.Context, req UpdateRealmPolicyRequest) error
	// B1 (MFA self-service): credential management.

	// GetUserCredentialsFull returns the full CredentialRepresentation slice for
	// the user, including ID and type fields needed for remove operations.
	GetUserCredentialsFull(ctx context.Context, userID string) ([]*gocloak.CredentialRepresentation, error)
	// DeleteCredential removes a single credential by its Keycloak credential ID.
	DeleteCredential(ctx context.Context, userID, credentialID string) error

	// B1: SCIM Group operations.

	// GetGroupByID fetches a single group by its Keycloak ID.
	GetGroupByID(ctx context.Context, groupID string) (*gocloak.Group, error)
	// ListGroupMembers returns the users that belong to a group.
	ListGroupMembers(ctx context.Context, groupID string) ([]*gocloak.User, error)
	// RenameGroup changes a group's display name.
	RenameGroup(ctx context.Context, groupID, newName string) error
	// DeleteGroup removes a group from the realm.
	DeleteGroup(ctx context.Context, groupID string) error

	// C1 (LDAP/AD federation)
	CreateFederationComponent(ctx context.Context, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) (string, error)
	GetFederationComponents(ctx context.Context) ([]*gocloak.Component, error)
	UpdateFederationComponent(ctx context.Context, componentID, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) error
	DeleteFederationComponent(ctx context.Context, componentID string) error
	TestLDAPConnection(ctx context.Context, componentID, connectionURL, bindDN, bindPassword string) error
	TriggerFederationSync(ctx context.Context, componentID, action string) error
	GetUserByID(ctx context.Context, userID string) (*gocloak.User, error)

	// B1 (first-run setup wizard): provisioning-state helpers.

	// HasAdminUser returns true when the realm has at least one user holding
	// the "admin" realm role. Used by the setup endpoints to determine whether
	// first-run provisioning is needed.
	HasAdminUser(ctx context.Context) (bool, error)

	// CreateAdminUser creates a user in Keycloak with the given email/password,
	// assigns the "admin" realm role, and returns the Keycloak user ID.
	// Used by the setup wizard.
	CreateAdminUser(ctx context.Context, email, password string) (string, error)
	// D2: SMTP configuration.
	// UpdateRealmSMTP writes the SMTP relay settings into the Keycloak realm so
	// that outbound emails (password reset, MFA) use the organisation's mail relay.
	UpdateRealmSMTP(ctx context.Context, cfg SMTPConfig) error

	// D3: Identity provider management.
	ListIdentityProviders(ctx context.Context) ([]*gocloak.IdentityProviderRepresentation, error)
	CreateIdentityProvider(ctx context.Context, alias, displayName, providerType string, config map[string]string) error
	UpdateIdentityProvider(ctx context.Context, alias, displayName string, config map[string]string) error
	DeleteIdentityProvider(ctx context.Context, alias string) error
}

// SMTPConfig holds the SMTP relay settings to write into a Keycloak realm.
type SMTPConfig struct {
	Host     string
	Port     string
	From     string
	Auth     bool
	User     string
	Password string
	SSL      bool
	StartTLS bool
}

// boolStr converts a bool to the "true"/"false" string Keycloak expects.
func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// CreateUserResult holds the outcome of a CreateUser operation.
type CreateUserResult struct {
	User           *gocloak.User
	PasswordSet    bool
	ResetEmailSent bool
	SetupWarning   string
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
	baseURL      string

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
		baseURL:      strings.TrimRight(url, "/"),
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
		Username:  &email,
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
			Username:  &email,
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

// SAMLOptions holds optional advanced SAML client configuration.
// All fields are optional; zero/empty values retain existing safe defaults.
type SAMLOptions struct {
	// SigningAlgorithm is the XML signature algorithm.
	// Allowed: "RSA_SHA256" (default), "RSA_SHA512", "RSA_SHA1".
	SigningAlgorithm string
	// EncryptAssertions controls whether Keycloak encrypts SAML assertions.
	EncryptAssertions bool
	// NameIDFormat overrides the NameID format.
	// Allowed: "persistent" (default), "transient", "email", "unspecified".
	NameIDFormat string
	// AttributeMappings adds extra user-attribute mappers to the SAML client.
	AttributeMappings []SAMLAttributeMapping
}

// SAMLAttributeMapping maps a Keycloak user attribute to a SAML assertion attribute.
type SAMLAttributeMapping struct {
	UserAttribute     string
	SAMLAttributeName string
}

// nonAlphanumRe matches characters that are not URL-safe (not a-z0-9).
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

// samlSlug converts an app name into a URL-safe slug for idpInitiatedSsoUrlName.
func samlSlug(name string) string {
	s := strings.ToLower(name)
	s = nonAlphanumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "app"
	}
	return s
}

// validSigningAlgorithms is the set of Keycloak-supported SAML signing algorithms.
var validSigningAlgorithms = map[string]bool{
	"RSA_SHA256": true,
	"RSA_SHA512": true,
	"RSA_SHA1":   true,
}

// validNameIDFormats is the set of supported NameID formats.
var validNameIDFormats = map[string]bool{
	"persistent":  true,
	"transient":   true,
	"email":       true,
	"unspecified": true,
}

// buildSAMLAttributes returns the Keycloak client attribute map for a SAML client.
// This is extracted as a pure function so it can be unit-tested without HTTP.
func buildSAMLAttributes(name, acsURL, entityID string, opts *SAMLOptions) map[string]string {
	signingAlgo := "RSA_SHA256"
	encryptAssertions := "false"
	nameIDFormat := "persistent"

	if opts != nil {
		if validSigningAlgorithms[opts.SigningAlgorithm] {
			signingAlgo = opts.SigningAlgorithm
		}
		if opts.EncryptAssertions {
			encryptAssertions = "true"
		}
		if validNameIDFormats[opts.NameIDFormat] {
			nameIDFormat = opts.NameIDFormat
		}
	}

	return map[string]string{
		"saml_name_id_format":    nameIDFormat,
		"saml_sp_entity_id":      entityID,
		"saml.assertion.consumer.service.post.binding.url": acsURL,
		"saml_assertion_consumer_url_post":                 acsURL,
		"saml.server.signature":                            "true",
		"saml.assertion.signature":                         "true",
		"saml.client.signature":                            "false",
		"saml.authnstatement":                              "true",
		"saml.onetimeuse.condition":                        "false",
		"saml.server.signature.keyinfo.ext":                "false",
		"saml.force.post.binding":                          "true",
		"saml.multivalued.roles":                           "false",
		"saml.encrypt":                                     encryptAssertions,
		"saml.server.signature.logout":                     "true",
		"saml_assertion_lifespan":                          "3600",
		"saml.signature.algorithm":                         signingAlgo,
		"saml.idp.initiated.sso.url.name":                 samlSlug(name),
	}
}

// samlProtocolMappers returns the standard X.500/SAML attribute mappers plus any
// extra attribute mappings specified in opts.
func samlProtocolMappers(opts *SAMLOptions) *[]gocloak.ProtocolMapperRepresentation {
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
	if opts != nil {
		for _, m := range opts.AttributeMappings {
			if m.UserAttribute == "" || m.SAMLAttributeName == "" {
				continue
			}
			mappers = append(mappers, gocloak.ProtocolMapperRepresentation{
				Name:            gocloak.StringP(m.SAMLAttributeName),
				Protocol:        gocloak.StringP("saml"),
				ProtocolMapper:  gocloak.StringP("saml-user-attribute-mapper"),
				ConsentRequired: gocloak.BoolP(false),
				Config: &map[string]string{
					"attribute.nameformat": "Basic",
					"user.attribute":       m.UserAttribute,
					"attribute.name":       m.SAMLAttributeName,
				},
			})
		}
	}
	return &mappers
}

// CreateClient creates an OIDC or SAML client in Keycloak and returns the client ID.
// For SAML clients, opts configures advanced signing, encryption, NameID, and attribute mapping;
// nil opts retains safe interoperable defaults.
func (k *KeycloakClient) CreateClient(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string, opts *SAMLOptions) (string, error) {
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

		attrs := buildSAMLAttributes(name, acsURL, entityID, opts)
		client.Attributes = &attrs
		client.ProtocolMappers = samlProtocolMappers(opts)
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

// GetSAMLIdPInitiatedURL returns the full Keycloak IdP-initiated SSO URL for the given
// SAML client. It fetches the client representation from Keycloak to read the
// saml.idp.initiated.sso.url.name attribute set at creation time.
// Returns an empty string (no error) if the attribute is absent or the client is not SAML.
func (k *KeycloakClient) GetSAMLIdPInitiatedURL(ctx context.Context, keycloakClientID string) (string, error) {
	token, err := k.login(ctx)
	if err != nil {
		return "", err
	}
	c, err := k.client.GetClient(ctx, token, k.realm, keycloakClientID)
	if err != nil {
		return "", fmt.Errorf("get keycloak client %s: %w", keycloakClientID, err)
	}
	if c.Attributes == nil {
		return "", nil
	}
	urlName := (*c.Attributes)["saml.idp.initiated.sso.url.name"]
	if urlName == "" {
		return "", nil
	}
	return k.baseURL + "/realms/" + k.realm + "/protocol/saml/clients/" + urlName, nil
}

// GetSAMLMetadataXML fetches the Keycloak realm SAML IdP metadata XML.
// The descriptor endpoint is public — no admin token is required.
// SPs import this document to configure trust with FreeCloud as the IdP.
func (k *KeycloakClient) GetSAMLMetadataXML(ctx context.Context) (string, error) {
	metaURL := k.baseURL + "/realms/" + k.realm + "/protocol/saml/descriptor"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return "", fmt.Errorf("build saml metadata request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch saml metadata: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read saml metadata body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("saml metadata endpoint returned %d", resp.StatusCode)
	}
	return string(body), nil
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

// UnassignUserFromClient removes the FreeCloud client role mapping from a user.
// If the role no longer exists (404 / not-found), the operation is treated as a
// no-op success — the mapping is already absent, mirroring how AssignUserToClient
// tolerates 409 Conflict on role creation.
func (k *KeycloakClient) UnassignUserFromClient(ctx context.Context, userID, clientID string) error {
	logger := zap.L()
	token, err := k.login(ctx)
	if err != nil {
		return err
	}

	roleName := "user"
	clientRole, err := k.client.GetClientRole(ctx, token, k.realm, clientID, roleName)
	if err != nil {
		if isNotFoundErr(err) {
			logger.Debug("client role not found during unassign, treating as no-op",
				zap.String("client_id", clientID),
			)
			return nil
		}
		return fmt.Errorf("get client role: %w", err)
	}

	if err := k.client.DeleteClientRolesFromUser(ctx, token, k.realm, clientID, userID, []gocloak.Role{*clientRole}); err != nil {
		return fmt.Errorf("unassign user %s from client %s: %w", userID, clientID, err)
	}

	logger.Info("unassigned user from client",
		zap.String("user_id", userID),
		zap.String("client_id", clientID),
	)
	return nil
}

// isNotFoundErr reports whether the given gocloak error represents a 404 Not Found.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "404") || strings.Contains(strings.ToLower(msg), "not found")
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

// GetUserCredentialsFull returns the full CredentialRepresentation slice for the
// user, including credential IDs needed to remove specific factors.
func (k *KeycloakClient) GetUserCredentialsFull(ctx context.Context, userID string) ([]*gocloak.CredentialRepresentation, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	creds, err := k.client.GetCredentials(ctx, token, k.realm, userID)
	if err != nil {
		return nil, fmt.Errorf("get credentials (full) for user %s: %w", userID, err)
	}
	return creds, nil
}

// DeleteCredential removes a single Keycloak credential by its ID.
func (k *KeycloakClient) DeleteCredential(ctx context.Context, userID, credentialID string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.DeleteCredentials(ctx, token, k.realm, userID, credentialID); err != nil {
		return fmt.Errorf("delete credential %s for user %s: %w", credentialID, userID, err)
	}
	zap.L().Info("deleted MFA credential",
		zap.String("user_id", userID), zap.String("credential_id", credentialID))
	return nil
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

// RealmPolicyResult holds the realm's password-policy string and brute-force settings.
type RealmPolicyResult struct {
	PasswordPolicy string `json:"passwordPolicy"`
	// Brute-force protection fields.
	BruteForceProtected bool  `json:"bruteForceProtected"`
	FailureFactor       int   `json:"failureFactor"`
	WaitIncrementSeconds int  `json:"waitIncrementSeconds"`
	MaxFailureWaitSeconds int  `json:"maxFailureWaitSeconds"`
	QuickLoginCheckMilliSeconds int64 `json:"quickLoginCheckMilliSeconds"`
	MinimumQuickLoginWaitSeconds int  `json:"minimumQuickLoginWaitSeconds"`
	MaxDeltaTimeSeconds  int   `json:"maxDeltaTimeSeconds"`
}

// UpdateRealmPolicyRequest holds the fields to write back to the realm.
type UpdateRealmPolicyRequest struct {
	PasswordPolicy      string `json:"passwordPolicy"`
	BruteForceProtected bool   `json:"bruteForceProtected"`
	FailureFactor       int    `json:"failureFactor"`
	WaitIncrementSeconds int   `json:"waitIncrementSeconds"`
	MaxFailureWaitSeconds int  `json:"maxFailureWaitSeconds"`
	QuickLoginCheckMilliSeconds int64 `json:"quickLoginCheckMilliSeconds"`
	MinimumQuickLoginWaitSeconds int  `json:"minimumQuickLoginWaitSeconds"`
	MaxDeltaTimeSeconds  int   `json:"maxDeltaTimeSeconds"`
}

// GetRealmPolicy reads the realm representation and returns the password-policy
// string plus brute-force protection settings.
func (k *KeycloakClient) GetRealmPolicy(ctx context.Context) (*RealmPolicyResult, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	realm, err := k.client.GetRealm(ctx, token, k.realm)
	if err != nil {
		return nil, fmt.Errorf("get realm %s: %w", k.realm, err)
	}

	result := &RealmPolicyResult{}
	if realm.PasswordPolicy != nil {
		result.PasswordPolicy = *realm.PasswordPolicy
	}
	if realm.BruteForceProtected != nil {
		result.BruteForceProtected = *realm.BruteForceProtected
	}
	if realm.FailureFactor != nil {
		result.FailureFactor = *realm.FailureFactor
	}
	if realm.WaitIncrementSeconds != nil {
		result.WaitIncrementSeconds = *realm.WaitIncrementSeconds
	}
	if realm.MaxFailureWaitSeconds != nil {
		result.MaxFailureWaitSeconds = *realm.MaxFailureWaitSeconds
	}
	if realm.QuickLoginCheckMilliSeconds != nil {
		result.QuickLoginCheckMilliSeconds = *realm.QuickLoginCheckMilliSeconds
	}
	if realm.MinimumQuickLoginWaitSeconds != nil {
		result.MinimumQuickLoginWaitSeconds = *realm.MinimumQuickLoginWaitSeconds
	}
	if realm.MaxDeltaTimeSeconds != nil {
		result.MaxDeltaTimeSeconds = *realm.MaxDeltaTimeSeconds
	}
	return result, nil
}

// UpdateRealmPolicy applies new password-policy and brute-force settings to the realm.
// It reads the current realm first to avoid clobbering unrelated fields.
func (k *KeycloakClient) UpdateRealmPolicy(ctx context.Context, req UpdateRealmPolicyRequest) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	realm, err := k.client.GetRealm(ctx, token, k.realm)
	if err != nil {
		return fmt.Errorf("get realm %s before update: %w", k.realm, err)
	}

	realm.PasswordPolicy = &req.PasswordPolicy
	realm.BruteForceProtected = &req.BruteForceProtected
	realm.FailureFactor = &req.FailureFactor
	realm.WaitIncrementSeconds = &req.WaitIncrementSeconds
	realm.MaxFailureWaitSeconds = &req.MaxFailureWaitSeconds
	realm.QuickLoginCheckMilliSeconds = &req.QuickLoginCheckMilliSeconds
	realm.MinimumQuickLoginWaitSeconds = &req.MinimumQuickLoginWaitSeconds
	realm.MaxDeltaTimeSeconds = &req.MaxDeltaTimeSeconds

	if err := k.client.UpdateRealm(ctx, token, *realm); err != nil {
		return fmt.Errorf("update realm %s policy: %w", k.realm, err)
	}
	zap.L().Info("updated realm account policy", zap.String("realm", k.realm))
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

// CreateFederationComponent creates an LDAP/AD user-storage provider component in Keycloak.
func (k *KeycloakClient) CreateFederationComponent(ctx context.Context, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) (string, error) {
	token, err := k.login(ctx)
	if err != nil {
		return "", err
	}
	providerType := "org.keycloak.storage.UserStorageProvider"
	providerID := "ldap"
	config := map[string][]string{
		"connectionUrl":         {connectionURL},
		"bindDn":                {bindDN},
		"bindCredential":        {bindPassword},
		"usersDn":               {usersDN},
		"vendor":                {vendor},
		"usernameLDAPAttribute": {"uid"},
		"uuidLDAPAttribute":     {"entryUUID"},
		"userObjectClasses":     {"inetOrgPerson, organizationalPerson"},
		"editMode":              {"READ_ONLY"},
		"syncRegistrations":     {"false"},
		"importEnabled":         {"true"},
	}
	comp := gocloak.Component{
		Name:            &name,
		ProviderID:      &providerID,
		ProviderType:    &providerType,
		ComponentConfig: &config,
	}
	id, err := k.client.CreateComponent(ctx, token, k.realm, comp)
	if err != nil {
		return "", fmt.Errorf("create ldap component: %w", err)
	}
	return id, nil
}

// GetFederationComponents returns all LDAP/AD user-storage provider components.
func (k *KeycloakClient) GetFederationComponents(ctx context.Context) ([]*gocloak.Component, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	providerType := "org.keycloak.storage.UserStorageProvider"
	comps, err := k.client.GetComponentsWithParams(ctx, token, k.realm, gocloak.GetComponentsParams{ProviderType: &providerType})
	if err != nil {
		return nil, fmt.Errorf("get federation components: %w", err)
	}
	return comps, nil
}

// UpdateFederationComponent updates an existing LDAP/AD user-storage component.
func (k *KeycloakClient) UpdateFederationComponent(ctx context.Context, componentID, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	providerType := "org.keycloak.storage.UserStorageProvider"
	providerID := "ldap"
	config := map[string][]string{
		"connectionUrl":         {connectionURL},
		"bindDn":                {bindDN},
		"bindCredential":        {bindPassword},
		"usersDn":               {usersDN},
		"vendor":                {vendor},
		"usernameLDAPAttribute": {"uid"},
		"uuidLDAPAttribute":     {"entryUUID"},
		"userObjectClasses":     {"inetOrgPerson, organizationalPerson"},
		"editMode":              {"READ_ONLY"},
		"syncRegistrations":     {"false"},
		"importEnabled":         {"true"},
	}
	comp := gocloak.Component{
		ID:              &componentID,
		Name:            &name,
		ProviderID:      &providerID,
		ProviderType:    &providerType,
		ComponentConfig: &config,
	}
	if err := k.client.UpdateComponent(ctx, token, k.realm, comp); err != nil {
		return fmt.Errorf("update ldap component %s: %w", componentID, err)
	}
	return nil
}

// DeleteFederationComponent removes an LDAP/AD user-storage component from Keycloak.
func (k *KeycloakClient) DeleteFederationComponent(ctx context.Context, componentID string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.DeleteComponent(ctx, token, k.realm, componentID); err != nil {
		return fmt.Errorf("delete ldap component %s: %w", componentID, err)
	}
	return nil
}

// TestLDAPConnection tests connectivity to an LDAP server via Keycloak's test endpoint.
func (k *KeycloakClient) TestLDAPConnection(ctx context.Context, componentID, connectionURL, bindDN, bindPassword string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	bodyMap := map[string]string{
		"action":         "testConnection",
		"connectionUrl":  connectionURL,
		"bindDn":         bindDN,
		"bindCredential": bindPassword,
		"componentId":    componentID,
	}
	bodyBytes, _ := json.Marshal(bodyMap)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/admin/realms/%s/testLDAPConnection", k.baseURL, k.realm),
		bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("test ldap connection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("test ldap connection: status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// TriggerFederationSync triggers a full or incremental sync for an LDAP/AD source.
func (k *KeycloakClient) TriggerFederationSync(ctx context.Context, componentID, action string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	syncURL := fmt.Sprintf("%s/admin/realms/%s/user-storage/%s/sync?action=%s",
		k.baseURL, k.realm, componentID, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, syncURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("trigger federation sync: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("trigger federation sync: status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// GetUserByID returns a single Keycloak user by their ID.
func (k *KeycloakClient) GetUserByID(ctx context.Context, userID string) (*gocloak.User, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	user, err := k.client.GetUserByID(ctx, token, k.realm, userID)
	if err != nil {
		return nil, fmt.Errorf("get user %s: %w", userID, err)
	}
	return user, nil
}

// HasAdminUser returns true when the realm has at least one user holding the
// "admin" realm role. Returns false (not an error) when the role does not yet
// exist — that is the expected state on a brand-new Keycloak realm.
func (k *KeycloakClient) HasAdminUser(ctx context.Context) (bool, error) {
	token, err := k.login(ctx)
	if err != nil {
		return false, err
	}
	users, err := k.client.GetUsersByRoleName(ctx, token, k.realm, "admin", gocloak.GetUsersByRoleParams{})
	if err != nil {
		if isNotFoundErr(err) {
			// Role doesn't exist yet — realm is unprovisioned.
			return false, nil
		}
		return false, fmt.Errorf("get users by role admin: %w", err)
	}
	return len(users) > 0, nil
}

// CreateAdminUser creates a Keycloak user with the given email and password,
// ensures the "admin" realm role exists, assigns it, and returns the user ID.
func (k *KeycloakClient) CreateAdminUser(ctx context.Context, email, password string) (string, error) {
	token, err := k.login(ctx)
	if err != nil {
		return "", err
	}

	// Create the user.
	userID, err := k.client.CreateUser(ctx, token, k.realm, gocloak.User{
		Username: &email,
		Email:    &email,
		Enabled:  gocloak.BoolP(true),
	})
	if err != nil {
		return "", fmt.Errorf("create admin user: %w", err)
	}

	// Set a permanent (non-temporary) password.
	if err := k.client.SetPassword(ctx, token, userID, k.realm, password, false); err != nil {
		return "", fmt.Errorf("set admin password: %w", err)
	}

	// Ensure the "admin" realm role exists; create it if missing.
	role, err := k.client.GetRealmRole(ctx, token, k.realm, "admin")
	if err != nil {
		if !isNotFoundErr(err) {
			return "", fmt.Errorf("get admin realm role: %w", err)
		}
		// Role missing — create it.
		if _, createErr := k.client.CreateRealmRole(ctx, token, k.realm, gocloak.Role{
			Name: gocloak.StringP("admin"),
		}); createErr != nil {
			return "", fmt.Errorf("create admin realm role: %w", createErr)
		}
		role, err = k.client.GetRealmRole(ctx, token, k.realm, "admin")
		if err != nil {
			return "", fmt.Errorf("get admin realm role after create: %w", err)
		}
	}

	// Assign the "admin" role to the new user.
	if err := k.client.AddRealmRoleToUser(ctx, token, k.realm, userID, []gocloak.Role{*role}); err != nil {
		return "", fmt.Errorf("assign admin role to user %s: %w", userID, err)
	}

	zap.L().Info("created admin user", zap.String("user_id", userID), zap.String("email", email))
	return userID, nil
}

// UpdateRealmSMTP writes SMTP relay settings into the Keycloak realm so that
// Keycloak's outbound emails (password reset, MFA) use the org's mail relay.
// It reads the current realm first to avoid clobbering unrelated fields.
func (k *KeycloakClient) UpdateRealmSMTP(ctx context.Context, cfg SMTPConfig) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	realm, err := k.client.GetRealm(ctx, token, k.realm)
	if err != nil {
		return fmt.Errorf("get realm %s before smtp update: %w", k.realm, err)
	}

	smtpMap := map[string]string{
		"host":     cfg.Host,
		"port":     cfg.Port,
		"from":     cfg.From,
		"auth":     boolStr(cfg.Auth),
		"user":     cfg.User,
		"password": cfg.Password,
		"ssl":      boolStr(cfg.SSL),
		"starttls": boolStr(cfg.StartTLS),
	}
	realm.SMTPServer = &smtpMap

	if err := k.client.UpdateRealm(ctx, token, *realm); err != nil {
		return fmt.Errorf("update realm %s smtp: %w", k.realm, err)
	}
	zap.L().Info("updated realm smtp settings", zap.String("realm", k.realm))
	return nil
}

// ListIdentityProviders returns all identity providers configured in the realm.
func (k *KeycloakClient) ListIdentityProviders(ctx context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
	token, err := k.login(ctx)
	if err != nil {
		return nil, err
	}
	providers, err := k.client.GetIdentityProviders(ctx, token, k.realm)
	if err != nil {
		return nil, fmt.Errorf("list identity providers: %w", err)
	}
	return providers, nil
}

// CreateIdentityProvider adds a new identity provider to the realm.
func (k *KeycloakClient) CreateIdentityProvider(ctx context.Context, alias, displayName, providerType string, config map[string]string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	rep := gocloak.IdentityProviderRepresentation{
		Alias:       &alias,
		DisplayName: &displayName,
		ProviderID:  &providerType,
		Enabled:     gocloak.BoolP(true),
		Config:      &config,
	}
	if _, err := k.client.CreateIdentityProvider(ctx, token, k.realm, rep); err != nil {
		return fmt.Errorf("create identity provider %q: %w", alias, err)
	}
	zap.L().Info("created identity provider", zap.String("alias", alias), zap.String("type", providerType))
	return nil
}

// UpdateIdentityProvider modifies an existing identity provider in the realm.
func (k *KeycloakClient) UpdateIdentityProvider(ctx context.Context, alias, displayName string, config map[string]string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	rep := gocloak.IdentityProviderRepresentation{
		Alias:       &alias,
		DisplayName: &displayName,
		Config:      &config,
	}
	if err := k.client.UpdateIdentityProvider(ctx, token, k.realm, alias, rep); err != nil {
		return fmt.Errorf("update identity provider %q: %w", alias, err)
	}
	zap.L().Info("updated identity provider", zap.String("alias", alias))
	return nil
}

// DeleteIdentityProvider removes an identity provider from the realm.
func (k *KeycloakClient) DeleteIdentityProvider(ctx context.Context, alias string) error {
	token, err := k.login(ctx)
	if err != nil {
		return err
	}
	if err := k.client.DeleteIdentityProvider(ctx, token, k.realm, alias); err != nil {
		return fmt.Errorf("delete identity provider %q: %w", alias, err)
	}
	zap.L().Info("deleted identity provider", zap.String("alias", alias))
	return nil
}
