package handlers

import (
	"context"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/Nerzal/gocloak/v13"
)

// Ensure fake implements interface
var _ keycloak.KeycloakClientInterface = (*fakeKeycloak)(nil)

type fakeKeycloak struct {
	createUserFn             func(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error)
	deleteUserFn             func(ctx context.Context, userID string) error
	disableUserFn            func(ctx context.Context, userID string) error
	updateUserFn             func(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error
	logoutSessionsFn         func(ctx context.Context, userID string) error
	getUserSessionsFn        func(ctx context.Context, userID string) ([]*gocloak.UserSessionRepresentation, error)
	createClientFn           func(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string, opts *keycloak.SAMLOptions) (string, error)
	getSAMLIdPInitiatedURLFn func(ctx context.Context, keycloakClientID string) (string, error)
	getSAMLMetadataXMLFn     func(ctx context.Context) (string, error)
	deleteClientFn           func(ctx context.Context, clientID string) error
	assignUserToClientFn     func(ctx context.Context, userID, clientID string) error
	unassignUserFromClientFn func(ctx context.Context, userID, clientID string) error
	getUserGroupsFn          func(ctx context.Context, userID string) ([]*gocloak.Group, error)
	listGroupsFn             func(ctx context.Context) ([]*gocloak.Group, error)
	createGroupFn            func(ctx context.Context, name string) (string, error)
	// C1: org-aware group operations. Both default to delegating to the
	// plain (non-org) fakes above when unset, so existing tests that only
	// wire listGroupsFn/createGroupFn keep working unchanged.
	createGroupWithOrgIDFn func(ctx context.Context, name, orgID string) (string, error)
	listGroupsByOrgFn      func(ctx context.Context, orgID string, first, max int) ([]*gocloak.Group, error)
	addUserToGroupFn         func(ctx context.Context, userID, groupID string) error
	removeUserFromGroupFn    func(ctx context.Context, userID, groupID string) error
	listRealmRolesFn         func(ctx context.Context) ([]*gocloak.Role, error)
	assignRealmRoleToUserFn  func(ctx context.Context, userID string, roles []gocloak.Role) error
	sendPasswordResetFn      func(ctx context.Context, userID string) error
	pingFn                   func(ctx context.Context) error
	getUserCredentialsFn     func(ctx context.Context, userID string) ([]string, error)
	getUserRequiredActionsFn func(ctx context.Context, userID string) ([]string, error)
	setRequiredActionFn      func(ctx context.Context, userID, action string) error
	sendPasswordResetEmailFn func(ctx context.Context, userID string) error
	listUsersFn              func(ctx context.Context) ([]gocloak.User, error)
	// B1: SCIM group operations
	getGroupByIDFn     func(ctx context.Context, groupID string) (*gocloak.Group, error)
	listGroupMembersFn func(ctx context.Context, groupID string) ([]*gocloak.User, error)
	renameGroupFn      func(ctx context.Context, groupID, newName string) error
	deleteGroupFn      func(ctx context.Context, groupID string) error
	// D1: account policy
	getRealmPolicyFn    func(ctx context.Context) (*keycloak.RealmPolicyResult, error)
	updateRealmPolicyFn func(ctx context.Context, req keycloak.UpdateRealmPolicyRequest) error

	// B1: MFA self-service credential operations
	getUserCredentialsFullFn func(ctx context.Context, userID string) ([]*gocloak.CredentialRepresentation, error)
	deleteCredentialFn       func(ctx context.Context, userID, credentialID string) error
	// C1: LDAP/AD federation
	createFederationComponentFn func(ctx context.Context, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) (string, error)
	getFederationComponentsFn   func(ctx context.Context) ([]*gocloak.Component, error)
	updateFederationComponentFn func(ctx context.Context, componentID, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) error
	deleteFederationComponentFn func(ctx context.Context, componentID string) error
	testLDAPConnectionFn        func(ctx context.Context, componentID, connectionURL, bindDN, bindPassword string) error
	triggerFederationSyncFn     func(ctx context.Context, componentID, action string) error
	getUserByIDFn               func(ctx context.Context, userID string) (*gocloak.User, error)
	// B1 (setup wizard): provisioning-state helpers.
	hasAdminUserFn    func(ctx context.Context) (bool, error)
	createAdminUserFn func(ctx context.Context, email, password string) (string, error)
	// D2: SMTP
	updateRealmSMTPFn func(ctx context.Context, cfg keycloak.SMTPConfig) error
	// D3: Identity providers
	listIdentityProvidersFn   func(ctx context.Context) ([]*gocloak.IdentityProviderRepresentation, error)
	createIdentityProviderFn  func(ctx context.Context, alias, displayName, providerType string, config map[string]string) error
	updateIdentityProviderFn  func(ctx context.Context, alias, displayName string, config map[string]string) error
	deleteIdentityProviderFn  func(ctx context.Context, alias string) error
}

func (f *fakeKeycloak) CreateUser(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
	if f.createUserFn != nil {
		return f.createUserFn(ctx, firstName, lastName, email, department)
	}
	uid := "kc-user-123"
	user := &gocloak.User{ID: &uid, FirstName: &firstName, LastName: &lastName, Email: &email}
	return &keycloak.CreateUserResult{User: user, PasswordSet: true, ResetEmailSent: true}, nil
}

func (f *fakeKeycloak) DeleteUser(ctx context.Context, userID string) error {
	if f.deleteUserFn != nil {
		return f.deleteUserFn(ctx, userID)
	}
	return nil
}

func (f *fakeKeycloak) DisableUser(ctx context.Context, userID string) error {
	if f.disableUserFn != nil {
		return f.disableUserFn(ctx, userID)
	}
	return nil
}

func (f *fakeKeycloak) UpdateUser(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error {
	if f.updateUserFn != nil {
		return f.updateUserFn(ctx, userID, firstName, lastName, department, enabled)
	}
	return nil
}

func (f *fakeKeycloak) LogoutAllSessions(ctx context.Context, userID string) error {
	if f.logoutSessionsFn != nil {
		return f.logoutSessionsFn(ctx, userID)
	}
	return nil
}

func (f *fakeKeycloak) GetUserSessions(ctx context.Context, userID string) ([]*gocloak.UserSessionRepresentation, error) {
	if f.getUserSessionsFn != nil {
		return f.getUserSessionsFn(ctx, userID)
	}
	return nil, nil
}

func (f *fakeKeycloak) CreateClient(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string, opts *keycloak.SAMLOptions) (string, error) {
	if f.createClientFn != nil {
		return f.createClientFn(ctx, name, protocol, redirectURIs, baseURL, opts)
	}
	return "kc-client-123", nil
}

func (f *fakeKeycloak) GetSAMLIdPInitiatedURL(ctx context.Context, keycloakClientID string) (string, error) {
	if f.getSAMLIdPInitiatedURLFn != nil {
		return f.getSAMLIdPInitiatedURLFn(ctx, keycloakClientID)
	}
	return "", nil
}

func (f *fakeKeycloak) GetSAMLMetadataXML(ctx context.Context) (string, error) {
	if f.getSAMLMetadataXMLFn != nil {
		return f.getSAMLMetadataXMLFn(ctx)
	}
	return `<?xml version="1.0" encoding="UTF-8"?><EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="stub"/>`, nil
}

func (f *fakeKeycloak) DeleteClient(ctx context.Context, clientID string) error {
	if f.deleteClientFn != nil {
		return f.deleteClientFn(ctx, clientID)
	}
	return nil
}

func (f *fakeKeycloak) AssignUserToClient(ctx context.Context, userID, clientID string) error {
	if f.assignUserToClientFn != nil {
		return f.assignUserToClientFn(ctx, userID, clientID)
	}
	return nil
}

func (f *fakeKeycloak) UnassignUserFromClient(ctx context.Context, userID, clientID string) error {
	if f.unassignUserFromClientFn != nil {
		return f.unassignUserFromClientFn(ctx, userID, clientID)
	}
	return nil
}

func (f *fakeKeycloak) GetUserGroups(ctx context.Context, userID string) ([]*gocloak.Group, error) {
	if f.getUserGroupsFn != nil {
		return f.getUserGroupsFn(ctx, userID)
	}
	return nil, nil
}

func (f *fakeKeycloak) ListGroups(ctx context.Context) ([]*gocloak.Group, error) {
	if f.listGroupsFn != nil {
		return f.listGroupsFn(ctx)
	}
	return []*gocloak.Group{}, nil
}

func (f *fakeKeycloak) CreateGroup(ctx context.Context, name string) (string, error) {
	if f.createGroupFn != nil {
		return f.createGroupFn(ctx, name)
	}
	return "group-fake-123", nil
}

func (f *fakeKeycloak) CreateGroupWithOrgID(ctx context.Context, name, orgID string) (string, error) {
	if f.createGroupWithOrgIDFn != nil {
		return f.createGroupWithOrgIDFn(ctx, name, orgID)
	}
	return f.CreateGroup(ctx, name)
}

func (f *fakeKeycloak) ListGroupsByOrg(ctx context.Context, orgID string, first, max int) ([]*gocloak.Group, error) {
	if f.listGroupsByOrgFn != nil {
		return f.listGroupsByOrgFn(ctx, orgID, first, max)
	}
	all, err := f.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	var matched []*gocloak.Group
	for _, g := range all {
		if keycloak.GroupOrgID(g) == orgID {
			matched = append(matched, g)
		}
	}
	if first < 0 {
		first = 0
	}
	if first >= len(matched) {
		return []*gocloak.Group{}, nil
	}
	end := len(matched)
	if max > 0 && first+max < end {
		end = first + max
	}
	return matched[first:end], nil
}

func (f *fakeKeycloak) AddUserToGroup(ctx context.Context, userID, groupID string) error {
	if f.addUserToGroupFn != nil {
		return f.addUserToGroupFn(ctx, userID, groupID)
	}
	return nil
}

func (f *fakeKeycloak) RemoveUserFromGroup(ctx context.Context, userID, groupID string) error {
	if f.removeUserFromGroupFn != nil {
		return f.removeUserFromGroupFn(ctx, userID, groupID)
	}
	return nil
}

func (f *fakeKeycloak) ListRealmRoles(ctx context.Context) ([]*gocloak.Role, error) {
	if f.listRealmRolesFn != nil {
		return f.listRealmRolesFn(ctx)
	}
	return []*gocloak.Role{}, nil
}

func (f *fakeKeycloak) AssignRealmRoleToUser(ctx context.Context, userID string, roles []gocloak.Role) error {
	if f.assignRealmRoleToUserFn != nil {
		return f.assignRealmRoleToUserFn(ctx, userID, roles)
	}
	return nil
}

func (f *fakeKeycloak) SendPasswordReset(ctx context.Context, userID string) error {
	if f.sendPasswordResetFn != nil {
		return f.sendPasswordResetFn(ctx, userID)
	}
	return nil
}

func (f *fakeKeycloak) Ping(ctx context.Context) error {
	if f.pingFn != nil {
		return f.pingFn(ctx)
	}
	return nil
}

func (f *fakeKeycloak) GetUserCredentials(ctx context.Context, userID string) ([]string, error) {
	if f.getUserCredentialsFn != nil {
		return f.getUserCredentialsFn(ctx, userID)
	}
	return nil, nil
}

func (f *fakeKeycloak) GetUserRequiredActions(ctx context.Context, userID string) ([]string, error) {
	if f.getUserRequiredActionsFn != nil {
		return f.getUserRequiredActionsFn(ctx, userID)
	}
	return nil, nil
}

func (f *fakeKeycloak) SetRequiredAction(ctx context.Context, userID, action string) error {
	if f.setRequiredActionFn != nil {
		return f.setRequiredActionFn(ctx, userID, action)
	}
	return nil
}

func (f *fakeKeycloak) SendPasswordResetEmail(ctx context.Context, userID string) error {
	if f.sendPasswordResetEmailFn != nil {
		return f.sendPasswordResetEmailFn(ctx, userID)
	}
	return nil
}

func (f *fakeKeycloak) ListUsers(ctx context.Context) ([]gocloak.User, error) {
	if f.listUsersFn != nil {
		return f.listUsersFn(ctx)
	}
	return nil, nil
}

func (f *fakeKeycloak) GetUserCredentialsFull(ctx context.Context, userID string) ([]*gocloak.CredentialRepresentation, error) {
	if f.getUserCredentialsFullFn != nil {
		return f.getUserCredentialsFullFn(ctx, userID)
	}
	return []*gocloak.CredentialRepresentation{}, nil
}

func (f *fakeKeycloak) DeleteCredential(ctx context.Context, userID, credentialID string) error {
	if f.deleteCredentialFn != nil {
		return f.deleteCredentialFn(ctx, userID, credentialID)
	}
	return nil
}

func (f *fakeKeycloak) GetGroupByID(ctx context.Context, groupID string) (*gocloak.Group, error) {
	if f.getGroupByIDFn != nil {
		return f.getGroupByIDFn(ctx, groupID)
	}
	name := "fake-group"
	return &gocloak.Group{ID: &groupID, Name: &name}, nil
}

func (f *fakeKeycloak) ListGroupMembers(ctx context.Context, groupID string) ([]*gocloak.User, error) {
	if f.listGroupMembersFn != nil {
		return f.listGroupMembersFn(ctx, groupID)
	}
	return []*gocloak.User{}, nil
}

func (f *fakeKeycloak) RenameGroup(ctx context.Context, groupID, newName string) error {
	if f.renameGroupFn != nil {
		return f.renameGroupFn(ctx, groupID, newName)
	}
	return nil
}

func (f *fakeKeycloak) DeleteGroup(ctx context.Context, groupID string) error {
	if f.deleteGroupFn != nil {
		return f.deleteGroupFn(ctx, groupID)
	}
	return nil
}

func (f *fakeKeycloak) GetRealmPolicy(ctx context.Context) (*keycloak.RealmPolicyResult, error) {
	if f.getRealmPolicyFn != nil {
		return f.getRealmPolicyFn(ctx)
	}
	return &keycloak.RealmPolicyResult{
		PasswordPolicy:      "length(12) and upperCase(1) and lowerCase(1) and digits(1) and specialChars(1)",
		BruteForceProtected: true,
		FailureFactor:       5,
	}, nil
}

func (f *fakeKeycloak) UpdateRealmPolicy(ctx context.Context, req keycloak.UpdateRealmPolicyRequest) error {
	if f.updateRealmPolicyFn != nil {
		return f.updateRealmPolicyFn(ctx, req)
	}
	return nil
}
// C1: LDAP/AD federation fakes

func (f *fakeKeycloak) CreateFederationComponent(ctx context.Context, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) (string, error) {
	if f.createFederationComponentFn != nil {
		return f.createFederationComponentFn(ctx, name, connectionURL, bindDN, bindPassword, usersDN, vendor)
	}
	return "fake-component-id", nil
}

func (f *fakeKeycloak) GetFederationComponents(ctx context.Context) ([]*gocloak.Component, error) {
	if f.getFederationComponentsFn != nil {
		return f.getFederationComponentsFn(ctx)
	}
	return []*gocloak.Component{}, nil
}

func (f *fakeKeycloak) UpdateFederationComponent(ctx context.Context, componentID, name, connectionURL, bindDN, bindPassword, usersDN, vendor string) error {
	if f.updateFederationComponentFn != nil {
		return f.updateFederationComponentFn(ctx, componentID, name, connectionURL, bindDN, bindPassword, usersDN, vendor)
	}
	return nil
}

func (f *fakeKeycloak) DeleteFederationComponent(ctx context.Context, componentID string) error {
	if f.deleteFederationComponentFn != nil {
		return f.deleteFederationComponentFn(ctx, componentID)
	}
	return nil
}

func (f *fakeKeycloak) TestLDAPConnection(ctx context.Context, componentID, connectionURL, bindDN, bindPassword string) error {
	if f.testLDAPConnectionFn != nil {
		return f.testLDAPConnectionFn(ctx, componentID, connectionURL, bindDN, bindPassword)
	}
	return nil
}

func (f *fakeKeycloak) TriggerFederationSync(ctx context.Context, componentID, action string) error {
	if f.triggerFederationSyncFn != nil {
		return f.triggerFederationSyncFn(ctx, componentID, action)
	}
	return nil
}

func (f *fakeKeycloak) GetUserByID(ctx context.Context, userID string) (*gocloak.User, error) {
	if f.getUserByIDFn != nil {
		return f.getUserByIDFn(ctx, userID)
	}
	return &gocloak.User{ID: &userID}, nil
}

func (f *fakeKeycloak) HasAdminUser(ctx context.Context) (bool, error) {
	if f.hasAdminUserFn != nil {
		return f.hasAdminUserFn(ctx)
	}
	return false, nil
}

func (f *fakeKeycloak) CreateAdminUser(ctx context.Context, email, password string) (string, error) {
	if f.createAdminUserFn != nil {
		return f.createAdminUserFn(ctx, email, password)
	}
	return "fake-admin-id", nil
}

func (f *fakeKeycloak) UpdateRealmSMTP(ctx context.Context, cfg keycloak.SMTPConfig) error {
	if f.updateRealmSMTPFn != nil {
		return f.updateRealmSMTPFn(ctx, cfg)
	}
	return nil
}

func (f *fakeKeycloak) ListIdentityProviders(ctx context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
	if f.listIdentityProvidersFn != nil {
		return f.listIdentityProvidersFn(ctx)
	}
	alias := "google"
	displayName := "Google"
	providerID := "google"
	enabled := true
	return []*gocloak.IdentityProviderRepresentation{
		{Alias: &alias, DisplayName: &displayName, ProviderID: &providerID, Enabled: &enabled},
	}, nil
}

func (f *fakeKeycloak) CreateIdentityProvider(ctx context.Context, alias, displayName, providerType string, config map[string]string) error {
	if f.createIdentityProviderFn != nil {
		return f.createIdentityProviderFn(ctx, alias, displayName, providerType, config)
	}
	return nil
}

func (f *fakeKeycloak) UpdateIdentityProvider(ctx context.Context, alias, displayName string, config map[string]string) error {
	if f.updateIdentityProviderFn != nil {
		return f.updateIdentityProviderFn(ctx, alias, displayName, config)
	}
	return nil
}

func (f *fakeKeycloak) DeleteIdentityProvider(ctx context.Context, alias string) error {
	if f.deleteIdentityProviderFn != nil {
		return f.deleteIdentityProviderFn(ctx, alias)
	}
	return nil
}
