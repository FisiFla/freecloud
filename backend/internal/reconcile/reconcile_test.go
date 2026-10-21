package reconcile

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/Nerzal/gocloak/v13"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"

	keycloakpkg "github.com/FisiFla/freecloud/backend/internal/keycloak"
)

func nopLogger() *zap.Logger { return zap.NewNop() }

// fakeKC is a minimal KeycloakClientInterface that only drives ListUsers.
type fakeKC struct {
	users []gocloak.User
	err   error
}

func (f *fakeKC) CreateUser(_ context.Context, _, _, _, _ string) (*keycloakpkg.CreateUserResult, error) {
	return nil, nil
}
func (f *fakeKC) DeleteUser(_ context.Context, _ string) error        { return nil }
func (f *fakeKC) DisableUser(_ context.Context, _ string) error       { return nil }
func (f *fakeKC) LogoutAllSessions(_ context.Context, _ string) error { return nil }
func (f *fakeKC) GetUserSessions(_ context.Context, _ string) ([]*gocloak.UserSessionRepresentation, error) {
	return nil, nil
}
func (f *fakeKC) CreateClient(_ context.Context, _, _ string, _ []string, _ string, _ *keycloakpkg.SAMLOptions) (string, error) {
	return "", nil
}
func (f *fakeKC) GetSAMLIdPInitiatedURL(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeKC) GetSAMLMetadataXML(_ context.Context) (string, error)               { return "", nil }
func (f *fakeKC) DeleteClient(_ context.Context, _ string) error              { return nil }
func (f *fakeKC) AssignUserToClient(_ context.Context, _, _ string) error     { return nil }
func (f *fakeKC) UnassignUserFromClient(_ context.Context, _, _ string) error { return nil }
func (f *fakeKC) GetUserGroups(_ context.Context, _ string) ([]*gocloak.Group, error) {
	return nil, nil
}
func (f *fakeKC) UpdateUser(_ context.Context, _, _, _, _ string, _ bool) error { return nil }
func (f *fakeKC) SendPasswordReset(_ context.Context, _ string) error           { return nil }
func (f *fakeKC) ListGroups(_ context.Context) ([]*gocloak.Group, error)        { return nil, nil }
func (f *fakeKC) CreateGroup(_ context.Context, _ string) (string, error)       { return "", nil }
func (f *fakeKC) CreateGroupWithOrgID(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (f *fakeKC) ListGroupsByOrg(_ context.Context, _ string, _, _ int) ([]*gocloak.Group, error) {
	return nil, nil
}
func (f *fakeKC) AddUserToGroup(_ context.Context, _, _ string) error           { return nil }
func (f *fakeKC) RemoveUserFromGroup(_ context.Context, _, _ string) error      { return nil }
func (f *fakeKC) ListRealmRoles(_ context.Context) ([]*gocloak.Role, error)     { return nil, nil }
func (f *fakeKC) AssignRealmRoleToUser(_ context.Context, _ string, _ []gocloak.Role) error {
	return nil
}
func (f *fakeKC) Ping(_ context.Context) error { return nil }
func (f *fakeKC) GetUserCredentials(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeKC) GetUserRequiredActions(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeKC) SetRequiredAction(_ context.Context, _, _ string) error   { return nil }
func (f *fakeKC) SendPasswordResetEmail(_ context.Context, _ string) error { return nil }
func (f *fakeKC) ListUsers(_ context.Context) ([]gocloak.User, error) {
	return f.users, f.err
}

// B1: SCIM group operations (not used by reconciler — satisfy interface)
func (f *fakeKC) GetGroupByID(_ context.Context, _ string) (*gocloak.Group, error) { return nil, nil }
func (f *fakeKC) ListGroupMembers(_ context.Context, _ string) ([]*gocloak.User, error) {
	return nil, nil
}
func (f *fakeKC) RenameGroup(_ context.Context, _, _ string) error { return nil }
func (f *fakeKC) DeleteGroup(_ context.Context, _ string) error    { return nil }

// D1: account policy (not used by reconciler — satisfy interface)
func (f *fakeKC) GetRealmPolicy(_ context.Context) (*keycloakpkg.RealmPolicyResult, error) {
	return &keycloakpkg.RealmPolicyResult{}, nil
}
func (f *fakeKC) UpdateRealmPolicy(_ context.Context, _ keycloakpkg.UpdateRealmPolicyRequest) error {
	return nil
}
// B1: MFA self-service credential operations (not used by reconciler)
func (f *fakeKC) GetUserCredentialsFull(_ context.Context, _ string) ([]*gocloak.CredentialRepresentation, error) {
	return nil, nil
}
func (f *fakeKC) DeleteCredential(_ context.Context, _, _ string) error { return nil }
// C1: LDAP/AD federation (not used by reconciler — satisfy interface)
func (f *fakeKC) CreateFederationComponent(_ context.Context, _, _, _, _, _, _ string) (string, error) {
	return "fake-component-id", nil
}
func (f *fakeKC) GetFederationComponents(_ context.Context) ([]*gocloak.Component, error) {
	return nil, nil
}
func (f *fakeKC) UpdateFederationComponent(_ context.Context, _, _, _, _, _, _, _ string) error {
	return nil
}
func (f *fakeKC) DeleteFederationComponent(_ context.Context, _ string) error { return nil }
func (f *fakeKC) TestLDAPConnection(_ context.Context, _, _, _, _ string) error { return nil }
func (f *fakeKC) TriggerFederationSync(_ context.Context, _, _ string) error    { return nil }
func (f *fakeKC) GetUserByID(_ context.Context, userID string) (*gocloak.User, error) {
	return &gocloak.User{ID: &userID}, nil
}

// B1 setup wizard (not used by reconciler — satisfy interface)
func (f *fakeKC) HasAdminUser(_ context.Context) (bool, error)                  { return false, nil }
func (f *fakeKC) CreateAdminUser(_ context.Context, _, _ string) (string, error) { return "", nil }
// D2: SMTP configuration (not used by reconciler — satisfy interface)
func (f *fakeKC) UpdateRealmSMTP(_ context.Context, _ keycloakpkg.SMTPConfig) error { return nil }

// D3: Identity provider management (not used by reconciler — satisfy interface)
func (f *fakeKC) ListIdentityProviders(_ context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
	return nil, nil
}
func (f *fakeKC) CreateIdentityProvider(_ context.Context, _, _, _ string, _ map[string]string) error {
	return nil
}
func (f *fakeKC) UpdateIdentityProvider(_ context.Context, _, _ string, _ map[string]string) error {
	return nil
}
func (f *fakeKC) DeleteIdentityProvider(_ context.Context, _ string) error { return nil }

// fakeRows implements pgx.Rows backed by a string slice.
type fakeRows struct {
	ids []string
	pos int
	err error
}

func (r *fakeRows) Next() bool { r.pos++; return r.pos <= len(r.ids) }
func (r *fakeRows) Close()     {}
func (r *fakeRows) Err() error { return r.err }
func (r *fakeRows) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	*dest[0].(*string) = r.ids[r.pos-1]
	return nil
}

// Satisfy remaining pgx.Rows methods (unused by reconciler).
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

// fakePool is a DBPool backed by an in-memory slice of user IDs.
type fakePool struct {
	ids  []string
	qerr error // error to return from Query
}

func (p *fakePool) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	if p.qerr != nil {
		return nil, p.qerr
	}
	return &fakeRows{ids: p.ids}, nil
}

func (p *fakePool) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (p *fakePool) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

// ptr returns a pointer to s, used to construct gocloak.User.
func ptr(s string) *string { return &s }

func kcUsers(ids ...string) []gocloak.User {
	out := make([]gocloak.User, len(ids))
	for i, id := range ids {
		out[i] = gocloak.User{ID: ptr(id)}
	}
	return out
}

func TestRunNoDrift(t *testing.T) {
	r := New(
		&fakeKC{users: kcUsers("a", "b")},
		&fakePool{ids: []string{"a", "b"}},
		nopLogger(),
	)
	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphansInKeycloak) != 0 || len(result.OrphansInDB) != 0 {
		t.Errorf("expected no drift, got %+v", result)
	}
}

func TestRunOrphanInKeycloak(t *testing.T) {
	// "c" is in Keycloak but not in DB.
	r := New(
		&fakeKC{users: kcUsers("a", "b", "c")},
		&fakePool{ids: []string{"a", "b"}},
		nopLogger(),
	)
	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphansInDB) != 0 {
		t.Errorf("expected no DB orphans, got %v", result.OrphansInDB)
	}
	if len(result.OrphansInKeycloak) != 1 || result.OrphansInKeycloak[0] != "c" {
		t.Errorf("expected [c] as Keycloak orphan, got %v", result.OrphansInKeycloak)
	}
}

func TestRunOrphanInDB(t *testing.T) {
	// "d" is in DB but not in Keycloak.
	r := New(
		&fakeKC{users: kcUsers("a", "b")},
		&fakePool{ids: []string{"a", "b", "d"}},
		nopLogger(),
	)
	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphansInKeycloak) != 0 {
		t.Errorf("expected no Keycloak orphans, got %v", result.OrphansInKeycloak)
	}
	if len(result.OrphansInDB) != 1 || result.OrphansInDB[0] != "d" {
		t.Errorf("expected [d] as DB orphan, got %v", result.OrphansInDB)
	}
}

func TestRunBothDirectionsDrift(t *testing.T) {
	r := New(
		&fakeKC{users: kcUsers("shared", "kc-only")},
		&fakePool{ids: []string{"shared", "db-only"}},
		nopLogger(),
	)
	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sort.Strings(result.OrphansInKeycloak)
	sort.Strings(result.OrphansInDB)
	if len(result.OrphansInKeycloak) != 1 || result.OrphansInKeycloak[0] != "kc-only" {
		t.Errorf("orphans_in_keycloak: want [kc-only], got %v", result.OrphansInKeycloak)
	}
	if len(result.OrphansInDB) != 1 || result.OrphansInDB[0] != "db-only" {
		t.Errorf("orphans_in_db: want [db-only], got %v", result.OrphansInDB)
	}
}

func TestRunEmpty(t *testing.T) {
	r := New(&fakeKC{}, &fakePool{}, nopLogger())
	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphansInKeycloak) != 0 || len(result.OrphansInDB) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

func TestRunKeycloakError(t *testing.T) {
	r := New(
		&fakeKC{err: errors.New("keycloak down")},
		&fakePool{ids: []string{"a"}},
		nopLogger(),
	)
	_, err := r.Run(context.Background())
	if err == nil {
		t.Error("expected error when Keycloak fails, got nil")
	}
}

func TestRunDBError(t *testing.T) {
	r := New(
		&fakeKC{users: kcUsers("a")},
		&fakePool{qerr: errors.New("db down")},
		nopLogger(),
	)
	_, err := r.Run(context.Background())
	if err == nil {
		t.Error("expected error when DB fails, got nil")
	}
}

func TestRunIgnoresNilAndEmptyIDUsers(t *testing.T) {
	// Keycloak may return users with nil or empty IDs; they must be ignored.
	r := New(
		&fakeKC{users: []gocloak.User{{ID: nil}, {ID: ptr("")}, {ID: ptr("real")}}},
		&fakePool{ids: []string{"real"}},
		nopLogger(),
	)
	result, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.OrphansInKeycloak) != 0 || len(result.OrphansInDB) != 0 {
		t.Errorf("expected no drift (nil IDs ignored), got %+v", result)
	}
}

// TestRunReturnsSortedDrift proves the drift lists are sorted: they are built
// from map iteration, whose order Go randomises, so /api/v1/admin/drift
// returned a different order on every call (issue #178).
func TestRunReturnsSortedDrift(t *testing.T) {
	r := New(
		&fakeKC{users: kcUsers("kc-c", "kc-a", "kc-b")},
		&fakePool{ids: []string{"db-c", "db-a", "db-b"}},
		nopLogger(),
	)
	// Run repeatedly: map iteration order differs between runs, so a
	// deterministic result requires the lists to be sorted.
	for i := 0; i < 20; i++ {
		result, err := r.Run(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantKC := []string{"kc-a", "kc-b", "kc-c"}
		wantDB := []string{"db-a", "db-b", "db-c"}
		if !equalStrings(result.OrphansInKeycloak, wantKC) {
			t.Fatalf("run %d: orphans_in_keycloak: want %v, got %v", i, wantKC, result.OrphansInKeycloak)
		}
		if !equalStrings(result.OrphansInDB, wantDB) {
			t.Fatalf("run %d: orphans_in_db: want %v, got %v", i, wantDB, result.OrphansInDB)
		}
	}
}

// equalStrings compares two string slices element by element.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRunAndRecordUpdatesGaugesOnFailure proves a FAILED reconcile still
// updates the metric state: previously the gauges kept their old values and
// the last-run timestamp never moved, so a broken reconcile was
// indistinguishable from a healthy one (issue #164).
func TestRunAndRecordUpdatesGaugesOnFailure(t *testing.T) {
	r := New(
		&fakeKC{err: errors.New("keycloak down")},
		&fakePool{ids: []string{"a"}},
		nopLogger(),
	)

	// Start from a known-good state.
	driftOrphansInKeycloak.Set(7)
	driftOrphansInDB.Set(9)
	reconcileLastRunFailed.Set(0)
	before := lastRunTimestampValue(t)

	r.runAndRecord(context.Background())

	if got := gaugeValue(t, driftOrphansInKeycloak); got != driftUnknown {
		t.Errorf("orphans_in_keycloak: want the failure sentinel %v, got %v", driftUnknown, got)
	}
	if got := gaugeValue(t, driftOrphansInDB); got != driftUnknown {
		t.Errorf("orphans_in_db: want the failure sentinel %v, got %v", driftUnknown, got)
	}
	if got := gaugeValue(t, reconcileLastRunFailed); got != 1 {
		t.Errorf("last_run_failed: want 1, got %v", got)
	}
	if after := lastRunTimestampValue(t); after <= before {
		t.Errorf("the last-run timestamp must be bumped on failure: before=%v after=%v", before, after)
	}
}

// TestRunAndRecordUpdatesGaugesOnSuccess proves a successful run clears the
// failure flag and records the real drift counts.
func TestRunAndRecordUpdatesGaugesOnSuccess(t *testing.T) {
	r := New(
		&fakeKC{users: kcUsers("a", "kc-only")},
		&fakePool{ids: []string{"a"}},
		nopLogger(),
	)
	reconcileLastRunFailed.Set(1)

	r.runAndRecord(context.Background())

	if got := gaugeValue(t, driftOrphansInKeycloak); got != 1 {
		t.Errorf("orphans_in_keycloak: want 1, got %v", got)
	}
	if got := gaugeValue(t, driftOrphansInDB); got != 0 {
		t.Errorf("orphans_in_db: want 0, got %v", got)
	}
	if got := gaugeValue(t, reconcileLastRunFailed); got != 0 {
		t.Errorf("last_run_failed: want 0, got %v", got)
	}
}

// gaugeValue reads the current value of a prometheus gauge.
func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	return testutil.ToFloat64(g)
}

// lastRunTimestampValue reads the current last-run timestamp.
func lastRunTimestampValue(t *testing.T) float64 {
	t.Helper()
	return gaugeValue(t, lastRunTimestamp)
}
