package handlers

import (
	"context"

	"github.com/Nerzal/gocloak/v13"
	"github.com/FisiFla/freecloud/backend/internal/keycloak"
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
	createClientFn           func(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string) (string, error)
	deleteClientFn           func(ctx context.Context, clientID string) error
	assignUserToClientFn     func(ctx context.Context, userID, clientID string) error
	getUserGroupsFn          func(ctx context.Context, userID string) ([]*gocloak.Group, error)
	listGroupsFn             func(ctx context.Context) ([]*gocloak.Group, error)
	createGroupFn            func(ctx context.Context, name string) (string, error)
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
	getGroupByIDFn      func(ctx context.Context, groupID string) (*gocloak.Group, error)
	listGroupMembersFn  func(ctx context.Context, groupID string) ([]*gocloak.User, error)
	renameGroupFn       func(ctx context.Context, groupID, newName string) error
	deleteGroupFn       func(ctx context.Context, groupID string) error
}

func (f *fakeKeycloak) CreateUser(ctx context.Context, firstName, lastName, email, department string) (*keycloak.CreateUserResult, error) {
	if f.createUserFn != nil { return f.createUserFn(ctx, firstName, lastName, email, department) }
	uid := "kc-user-123"
	user := &gocloak.User{ID: &uid, FirstName: &firstName, LastName: &lastName, Email: &email}
	return &keycloak.CreateUserResult{User: user, PasswordSet: true, ResetEmailSent: true}, nil
}

func (f *fakeKeycloak) DeleteUser(ctx context.Context, userID string) error {
	if f.deleteUserFn != nil { return f.deleteUserFn(ctx, userID) }
	return nil
}

func (f *fakeKeycloak) DisableUser(ctx context.Context, userID string) error {
	if f.disableUserFn != nil { return f.disableUserFn(ctx, userID) }
	return nil
}

func (f *fakeKeycloak) UpdateUser(ctx context.Context, userID, firstName, lastName, department string, enabled bool) error {
	if f.updateUserFn != nil { return f.updateUserFn(ctx, userID, firstName, lastName, department, enabled) }
	return nil
}

func (f *fakeKeycloak) LogoutAllSessions(ctx context.Context, userID string) error {
	if f.logoutSessionsFn != nil { return f.logoutSessionsFn(ctx, userID) }
	return nil
}

func (f *fakeKeycloak) GetUserSessions(ctx context.Context, userID string) ([]*gocloak.UserSessionRepresentation, error) {
	if f.getUserSessionsFn != nil { return f.getUserSessionsFn(ctx, userID) }
	return nil, nil
}

func (f *fakeKeycloak) CreateClient(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string) (string, error) {
	if f.createClientFn != nil { return f.createClientFn(ctx, name, protocol, redirectURIs, baseURL) }
	return "kc-client-123", nil
}

func (f *fakeKeycloak) DeleteClient(ctx context.Context, clientID string) error {
	if f.deleteClientFn != nil { return f.deleteClientFn(ctx, clientID) }
	return nil
}

func (f *fakeKeycloak) AssignUserToClient(ctx context.Context, userID, clientID string) error {
	if f.assignUserToClientFn != nil { return f.assignUserToClientFn(ctx, userID, clientID) }
	return nil
}

func (f *fakeKeycloak) GetUserGroups(ctx context.Context, userID string) ([]*gocloak.Group, error) {
	if f.getUserGroupsFn != nil { return f.getUserGroupsFn(ctx, userID) }
	return nil, nil
}

func (f *fakeKeycloak) ListGroups(ctx context.Context) ([]*gocloak.Group, error) {
	if f.listGroupsFn != nil { return f.listGroupsFn(ctx) }
	return []*gocloak.Group{}, nil
}

func (f *fakeKeycloak) CreateGroup(ctx context.Context, name string) (string, error) {
	if f.createGroupFn != nil { return f.createGroupFn(ctx, name) }
	return "group-fake-123", nil
}

func (f *fakeKeycloak) AddUserToGroup(ctx context.Context, userID, groupID string) error {
	if f.addUserToGroupFn != nil { return f.addUserToGroupFn(ctx, userID, groupID) }
	return nil
}

func (f *fakeKeycloak) RemoveUserFromGroup(ctx context.Context, userID, groupID string) error {
	if f.removeUserFromGroupFn != nil { return f.removeUserFromGroupFn(ctx, userID, groupID) }
	return nil
}

func (f *fakeKeycloak) ListRealmRoles(ctx context.Context) ([]*gocloak.Role, error) {
	if f.listRealmRolesFn != nil { return f.listRealmRolesFn(ctx) }
	return []*gocloak.Role{}, nil
}

func (f *fakeKeycloak) AssignRealmRoleToUser(ctx context.Context, userID string, roles []gocloak.Role) error {
	if f.assignRealmRoleToUserFn != nil { return f.assignRealmRoleToUserFn(ctx, userID, roles) }
	return nil
}

func (f *fakeKeycloak) SendPasswordReset(ctx context.Context, userID string) error {
	if f.sendPasswordResetFn != nil { return f.sendPasswordResetFn(ctx, userID) }
	return nil
}

func (f *fakeKeycloak) Ping(ctx context.Context) error {
	if f.pingFn != nil { return f.pingFn(ctx) }
	return nil
}

func (f *fakeKeycloak) GetUserCredentials(ctx context.Context, userID string) ([]string, error) {
	if f.getUserCredentialsFn != nil { return f.getUserCredentialsFn(ctx, userID) }
	return nil, nil
}

func (f *fakeKeycloak) GetUserRequiredActions(ctx context.Context, userID string) ([]string, error) {
	if f.getUserRequiredActionsFn != nil { return f.getUserRequiredActionsFn(ctx, userID) }
	return nil, nil
}

func (f *fakeKeycloak) SetRequiredAction(ctx context.Context, userID, action string) error {
	if f.setRequiredActionFn != nil { return f.setRequiredActionFn(ctx, userID, action) }
	return nil
}

func (f *fakeKeycloak) SendPasswordResetEmail(ctx context.Context, userID string) error {
	if f.sendPasswordResetEmailFn != nil { return f.sendPasswordResetEmailFn(ctx, userID) }
	return nil
}

func (f *fakeKeycloak) ListUsers(ctx context.Context) ([]gocloak.User, error) {
	if f.listUsersFn != nil { return f.listUsersFn(ctx) }
	return nil, nil
}

func (f *fakeKeycloak) GetGroupByID(ctx context.Context, groupID string) (*gocloak.Group, error) {
	if f.getGroupByIDFn != nil { return f.getGroupByIDFn(ctx, groupID) }
	name := "fake-group"
	return &gocloak.Group{ID: &groupID, Name: &name}, nil
}

func (f *fakeKeycloak) ListGroupMembers(ctx context.Context, groupID string) ([]*gocloak.User, error) {
	if f.listGroupMembersFn != nil { return f.listGroupMembersFn(ctx, groupID) }
	return []*gocloak.User{}, nil
}

func (f *fakeKeycloak) RenameGroup(ctx context.Context, groupID, newName string) error {
	if f.renameGroupFn != nil { return f.renameGroupFn(ctx, groupID, newName) }
	return nil
}

func (f *fakeKeycloak) DeleteGroup(ctx context.Context, groupID string) error {
	if f.deleteGroupFn != nil { return f.deleteGroupFn(ctx, groupID) }
	return nil
}
