package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

func TestAssignAppCompensatesWhenAssignmentInsertFails(t *testing.T) {
	const appID = "app-123"
	const keycloakClientID = "kc-client-123"
	const userID = "00000000-0000-0000-0000-000000000099"

	db := &fakeDB{
		queryRowFn: ownershipFoundQueryRowFn(func(_ context.Context, sql string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = keycloakClientID
				return nil
			}}
		}),
		beginFn: func(context.Context) (pgx.Tx, error) {
			return &fakeTx{
				execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
					return pgconn.CommandTag{}, errors.New("db insert failed")
				},
			}, nil
		},
	}

	assignCalled := false
	unassignCalled := false
	kc := &fakeKeycloak{
		assignUserToClientFn: func(_ context.Context, gotUserID, gotClientID string) error {
			assignCalled = true
			if gotUserID != userID || gotClientID != keycloakClientID {
				t.Fatalf("assign got user=%q client=%q", gotUserID, gotClientID)
			}
			return nil
		},
		unassignUserFromClientFn: func(_ context.Context, gotUserID, gotClientID string) error {
			unassignCalled = true
			if gotUserID != userID || gotClientID != keycloakClientID {
				t.Fatalf("unassign got user=%q client=%q", gotUserID, gotClientID)
			}
			return nil
		},
	}

	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/"+appID+"/assign", bytes.NewBufferString(`{"userId":"`+userID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("appId", appID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	req = req.WithContext(ctx)
	req = withOrgContext(req)
	rec := httptest.NewRecorder()

	h.AssignApp(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if !assignCalled {
		t.Fatal("expected Keycloak assignment to be attempted")
	}
	if !unassignCalled {
		t.Fatal("expected failed DB insert to compensate Keycloak assignment")
	}
}

// TestValidateRedirectURI locks down the exact attack cases the redirect-URI
// validator is meant to block, so it cannot regress.
func TestValidateRedirectURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		// Allowed.
		{"https production", "https://example.com/callback", false},
		{"https with port", "https://app.example.com:8443/cb", false},
		{"localhost http", "http://localhost:3000/callback", false},
		{"loopback http", "http://127.0.0.1:3000/callback", false},
		{"localhost no port", "http://localhost/callback", false},

		// Deceptive localhost variants (must FAIL).
		{"localhost subdomain trick", "http://localhost.evil.com/callback", true},
		{"localhost prefix trick", "http://localhost-evil.com/callback", true},
		{"evil using localhost path", "http://evil.com/localhost", true},

		// Bad ports.
		{"non-numeric port", "http://localhost:bad/callback", true},
		{"negative port", "http://localhost:-1/callback", true},

		// Relative / schemeless (must FAIL — used for open-redirect abuse).
		{"relative path", "/relative/callback", true},
		{"protocol-relative", "//evil.com/callback", true},
		{"bare host no scheme", "example.com/callback", true},

		// Wrong schemes.
		{"ftp scheme", "ftp://example.com/callback", true},
		{"javascript scheme", "javascript:alert(1)", true},
		{"file scheme", "file:///etc/passwd", true},

		// Empty / malformed.
		{"empty string", "", true},
		{"only scheme", "https://", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRedirectURI(tt.uri)
			if tt.wantErr && err == nil {
				t.Errorf("validateRedirectURI(%q): expected error, got nil", tt.uri)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateRedirectURI(%q): expected no error, got %v", tt.uri, err)
			}
		})
	}
}
