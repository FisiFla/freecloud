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
	// E1: expanded MDM command set
	issueRestartFn         func(ctx context.Context, hostID string) error
	issueLockWithMessageFn func(ctx context.Context, hostID string, message string) error
	issueClearPasscodeFn   func(ctx context.Context, hostID string) error
	pingFn                 func(ctx context.Context) error
	listPoliciesFn         func(ctx context.Context) ([]fleet.Policy, error)
	// B2: team-scoped policy
	listTeamsFn          func(ctx context.Context) ([]fleet.Team, error)
	createTeamFn         func(ctx context.Context, name, description string) (*fleet.Team, error)
	assignPolicyToTeamFn func(ctx context.Context, teamID int, policyID string) error
	moveHostToTeamFn     func(ctx context.Context, teamID int, hostIDs []string) error
	// E3: OS posture
	getHostOSPostureFn func(ctx context.Context, hostID string) (*fleet.OSPosture, error)
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

func (f *fakeFleet) ListPolicies(ctx context.Context) ([]fleet.Policy, error) {
	if f.listPoliciesFn != nil { return f.listPoliciesFn(ctx) }
	return []fleet.Policy{
		{ID: "pol-001", Name: "Disk Encryption Required", Description: "Ensure disk encryption is enabled"},
	}, nil
}

func (f *fakeFleet) ListTeams(ctx context.Context) ([]fleet.Team, error) {
	if f.listTeamsFn != nil { return f.listTeamsFn(ctx) }
	return []fleet.Team{{ID: 1, Name: "Default"}}, nil
}

func (f *fakeFleet) CreateTeam(ctx context.Context, name, description string) (*fleet.Team, error) {
	if f.createTeamFn != nil { return f.createTeamFn(ctx, name, description) }
	return &fleet.Team{ID: 42, Name: name, Description: description}, nil
}

func (f *fakeFleet) AssignPolicyToTeam(ctx context.Context, teamID int, policyID string) error {
	if f.assignPolicyToTeamFn != nil { return f.assignPolicyToTeamFn(ctx, teamID, policyID) }
	return nil
}

func (f *fakeFleet) MoveHostToTeam(ctx context.Context, teamID int, hostIDs []string) error {
	if f.moveHostToTeamFn != nil { return f.moveHostToTeamFn(ctx, teamID, hostIDs) }
	return nil
}

func (f *fakeFleet) IssueRestart(ctx context.Context, hostID string) error {
	if f.issueRestartFn != nil { return f.issueRestartFn(ctx, hostID) }
	return nil
}

func (f *fakeFleet) IssueLockWithMessage(ctx context.Context, hostID string, message string) error {
	if f.issueLockWithMessageFn != nil { return f.issueLockWithMessageFn(ctx, hostID, message) }
	return nil
}

func (f *fakeFleet) IssueClearPasscode(ctx context.Context, hostID string) error {
	if f.issueClearPasscodeFn != nil { return f.issueClearPasscodeFn(ctx, hostID) }
	return nil
}

func (f *fakeFleet) GetHostOSPosture(ctx context.Context, hostID string) (*fleet.OSPosture, error) {
	if f.getHostOSPostureFn != nil { return f.getHostOSPostureFn(ctx, hostID) }
	return &fleet.OSPosture{OsVersion: "macOS 15", NeedsUpdate: false}, nil
}
