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
	logoutSessionsFn         func(ctx context.Context, userID string) error
	getUserSessionsFn        func(ctx context.Context, userID string) ([]*gocloak.UserSessionRepresentation, error)
	createClientFn           func(ctx context.Context, name, protocol string, redirectURIs []string, baseURL string) (string, error)
	deleteClientFn           func(ctx context.Context, clientID string) error
	assignUserToClientFn     func(ctx context.Context, userID, clientID string) error
	getUserGroupsFn          func(ctx context.Context, userID string) ([]*gocloak.Group, error)
	pingFn                   func(ctx context.Context) error
	getUserCredentialsFn     func(ctx context.Context, userID string) ([]string, error)
	getUserRequiredActionsFn func(ctx context.Context, userID string) ([]string, error)
	setRequiredActionFn      func(ctx context.Context, userID, action string) error
	sendPasswordResetEmailFn func(ctx context.Context, userID string) error
	listUsersFn              func(ctx context.Context) ([]gocloak.User, error)
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
