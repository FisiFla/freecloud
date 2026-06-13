package handlers

import (
	"context"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
)

// Ensure fake implements interface
var _ fleet.FleetClientInterface = (*fakeFleet)(nil)

type fakeFleet struct {
	createEnrollmentTokenFn func(ctx context.Context) (string, error)
	getHostsFn              func(ctx context.Context, query string) ([]fleet.Host, error)
	getHostSoftwareFn       func(ctx context.Context, hostID string) ([]fleet.Software, error)
	getHostSecurityStateFn  func(ctx context.Context, hostID string) (*fleet.SecurityState, error)
	issueRemoteLockFn       func(ctx context.Context, hostID string) error
	issueRemoteWipeFn       func(ctx context.Context, hostID string) error
	pingFn                  func(ctx context.Context) error
}

func (f *fakeFleet) CreateEnrollmentToken(ctx context.Context) (string, error) {
	if f.createEnrollmentTokenFn != nil { return f.createEnrollmentTokenFn(ctx) }
	return "fake-token-123", nil
}

func (f *fakeFleet) GetHosts(ctx context.Context, query string) ([]fleet.Host, error) {
	if f.getHostsFn != nil { return f.getHostsFn(ctx, query) }
	return []fleet.Host{{ID: "host-001", Hostname: "test-laptop", OsVersion: "macOS", Status: "online"}}, nil
}

func (f *fakeFleet) GetHostSoftware(ctx context.Context, hostID string) ([]fleet.Software, error) {
	if f.getHostSoftwareFn != nil { return f.getHostSoftwareFn(ctx, hostID) }
	return []fleet.Software{}, nil
}

func (f *fakeFleet) GetHostSecurityState(ctx context.Context, hostID string) (*fleet.SecurityState, error) {
	if f.getHostSecurityStateFn != nil { return f.getHostSecurityStateFn(ctx, hostID) }
	return &fleet.SecurityState{FirewallEnabled: true, DiskEncrypted: true}, nil
}

func (f *fakeFleet) IssueRemoteLock(ctx context.Context, hostID string) error {
	if f.issueRemoteLockFn != nil { return f.issueRemoteLockFn(ctx, hostID) }
	return nil
}

func (f *fakeFleet) IssueRemoteWipe(ctx context.Context, hostID string) error {
	if f.issueRemoteWipeFn != nil { return f.issueRemoteWipeFn(ctx, hostID) }
	return nil
}

func (f *fakeFleet) Ping(ctx context.Context) error {
	if f.pingFn != nil { return f.pingFn(ctx) }
	return nil
}
