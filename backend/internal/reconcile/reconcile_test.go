package reconcile

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/Nerzal/gocloak/v13"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
func (f *fakeKC) CreateClient(_ context.Context, _, _ string, _ []string, _ string) (string, error) {
	return "", nil
}
func (f *fakeKC) DeleteClient(_ context.Context, _ string) error           { return nil }
func (f *fakeKC) AssignUserToClient(_ context.Context, _, _ string) error  { return nil }
func (f *fakeKC) GetUserGroups(_ context.Context, _ string) ([]*gocloak.Group, error) {
	return nil, nil
}
func (f *fakeKC) Ping(_ context.Context) error { return nil }
func (f *fakeKC) GetUserCredentials(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeKC) GetUserRequiredActions(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeKC) SetRequiredAction(_ context.Context, _, _ string) error  { return nil }
func (f *fakeKC) SendPasswordResetEmail(_ context.Context, _ string) error { return nil }
func (f *fakeKC) ListUsers(_ context.Context) ([]gocloak.User, error) {
	return f.users, f.err
}

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
func (r *fakeRows) CommandTag() pgconn.CommandTag                      { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription       { return nil }
func (r *fakeRows) Values() ([]any, error)                             { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                                { return nil }
func (r *fakeRows) Conn() *pgx.Conn                                    { return nil }

// fakePool is a DBPool backed by an in-memory slice of user IDs.
type fakePool struct {
	ids    []string
	qerr   error // error to return from Query
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
