// Failing-first tests for the Fleet config hardening (security lane L2,
// issues 4, 8): repointing to a new host requires the token, the env
// fallback token is never sent to a DB-configured URL, repoints are audited
// without the token, and no token hash is written.
package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// fleetRowQueryFn serves one fleet_config row (serverURL, tokenEnc) for any
// non-ownership query.
func fleetRowQueryFn(serverURL string, tokenEnc *string) func(context.Context, string, ...any) pgx.Row {
	return ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
		return fakeRow{scanFn: func(dest ...any) error {
			*(dest[0].(*string)) = serverURL
			*(dest[1].(**string)) = tokenEnc
			return nil
		}}
	})
}

func TestUpsertFleetConfig_RepointRequiresToken(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	storedToken := "c3RvcmVkLXRva2Vu" // base64("stored-token") stand-in
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: fleetRowQueryFn("https://198.51.100.10", &storedToken),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	// Same host, no token: the stored token stays — allowed.
	// (TEST-NET literals: save-time validation fails closed on DNS, so the
	// fixtures use IPs rather than unresolvable example hostnames.)
	body := `{"serverUrl":"https://198.51.100.10/api","apiToken":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/fleet", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertFleetConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-host update without token: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Different host, no token, stored token present: refuse.
	body = `{"serverUrl":"https://203.0.113.10","apiToken":""}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/fleet", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.UpsertFleetConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("repoint without token: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "token") {
		t.Fatalf("the 400 must explain the token is required: %s", rec.Body.String())
	}
}

func TestUpsertFleetConfig_RepointWithTokenAuditsHosts(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	storedToken := "c3RvcmVkLXRva2Vu"
	var execs []struct {
		sql  string
		args []any
	}
	record := func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
		execs = append(execs, struct {
			sql  string
			args []any
		}{sql, args})
		return pgconn.CommandTag{}, nil
	}
	db := &fakeDB{
		execFn: record,
		// The audit writer inserts inside a transaction, so capture tx
		// writes too.
		beginFn: func(context.Context) (pgx.Tx, error) {
			return &fakeTx{execFn: record}, nil
		},
		queryRowFn: fleetRowQueryFn("https://198.51.100.10", &storedToken),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	const newToken = "brand-new-token"
	body := `{"serverUrl":"https://203.0.113.10","apiToken":"` + newToken + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/fleet", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertFleetConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("repoint with token: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var auditBlobs []string
	for _, e := range execs {
		for _, arg := range e.args {
			if s, ok := arg.(string); ok {
				if strings.Contains(s, "198.51.100.10") || strings.Contains(s, "203.0.113.10") {
					auditBlobs = append(auditBlobs, s)
				}
				if strings.Contains(s, newToken) && !strings.Contains(e.sql, "fleet_config") {
					t.Fatalf("the token leaked into a non-config write: sql=%q", e.sql)
				}
			}
			if b, ok := arg.([]byte); ok {
				if strings.Contains(string(b), "198.51.100.10") || strings.Contains(string(b), "203.0.113.10") {
					auditBlobs = append(auditBlobs, string(b))
				}
				if strings.Contains(string(b), newToken) {
					t.Fatalf("the token leaked into a write: sql=%q", e.sql)
				}
			}
		}
	}
	joined := strings.Join(auditBlobs, "\n")
	if !strings.Contains(joined, "198.51.100.10") || !strings.Contains(joined, "203.0.113.10") {
		t.Fatalf("the repoint audit must record old and new hosts, got: %q", joined)
	}
	if strings.Contains(joined, newToken) {
		t.Fatalf("the repoint audit must never contain the token: %q", joined)
	}
}

func TestUpsertFleetConfig_WritesNoTokenHash(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	const token = "fleet-token-abc"
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	var execArgs [][]any
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
			execArgs = append(execArgs, args)
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	body := `{"serverUrl":"https://203.0.113.10","apiToken":"` + token + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/fleet", bytes.NewReader([]byte(body)))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpsertFleetConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, args := range execArgs {
		for _, arg := range args {
			if s, ok := arg.(*string); ok && s != nil && *s == wantHash {
				t.Fatalf("a write persisted the unsalted token hash %q", wantHash)
			}
			if s, ok := arg.(string); ok && s == wantHash {
				t.Fatalf("a write persisted the unsalted token hash %q", wantHash)
			}
		}
	}
	// The upsert answers {"saved":true}, so "configured" is verified at the
	// write: api_token_enc must carry a non-empty ciphertext (from which GET
	// derives APITokenConfigured), and no write may carry the hash.
	tokenPersisted := false
	for _, args := range execArgs {
		for _, arg := range args {
			if s, ok := arg.(*string); ok && s != nil && *s != "" && *s != wantHash {
				tokenPersisted = true
			}
		}
	}
	if !tokenPersisted {
		t.Error("no write persisted an encrypted token")
	}
}

// TestFleetEndpointCredentials_EnvTokenNeverToDBURL pins issue 4's second
// half: with a DB-configured URL, only the DB token is used — the env
// fallback token is never substituted.
func TestFleetEndpointCredentials_EnvTokenNeverToDBURL(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	db := &fakeDB{
		queryRowFn: fleetRowQueryFn("https://db.example.com", nil),
	}
	t.Setenv("FLEET_URL", "https://env.example.com")
	t.Setenv("FLEET_API_TOKEN", "env-fallback-token")
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	url, token, fromDB, err := h.fleetEndpointCredentials(context.Background())
	if err != nil {
		t.Fatalf("credentials: %v", err)
	}
	if url != "https://db.example.com" {
		t.Fatalf("url = %q", url)
	}
	if !fromDB {
		t.Fatal("fromDB must be true for a DB-configured URL")
	}
	if token != "" {
		t.Fatalf("token = %q: the env fallback token must never be sent to a DB-configured URL", token)
	}
}

// TestDynamicFleetClient_TokenScoping pins the token scoping at the HTTP
// layer: the env token goes to the env URL only.
func TestDynamicFleetClient_TokenScoping(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Run("db url without db token sends no token", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Write([]byte(`{"teams":[]}`))
		}))
		defer srv.Close()

		db := &fakeDB{queryRowFn: fleetRowQueryFn(srv.URL, nil)}
		dyn := NewDynamicFleetClient(db, "https://env.example.com", "env-fallback-token", zap.NewNop())
		if _, err := dyn.ListTeams(context.Background()); err != nil {
			t.Fatalf("ListTeams: %v", err)
		}
		if gotAuth != "" {
			t.Fatalf("Authorization = %q: want no header at all (env fallback must not apply)", gotAuth)
		}
	})

	t.Run("env url uses env token", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Write([]byte(`{"teams":[]}`))
		}))
		defer srv.Close()

		db := &fakeDB{} // no row at all
		dyn := NewDynamicFleetClient(db, srv.URL, "env-fallback-token", zap.NewNop())
		if _, err := dyn.ListTeams(context.Background()); err != nil {
			t.Fatalf("ListTeams: %v", err)
		}
		if gotAuth != "Bearer env-fallback-token" {
			t.Fatalf("Authorization = %q", gotAuth)
		}
	})
}
