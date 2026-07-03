package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

func signFleet(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestEnrollmentCallbackRejectsBadSignature(t *testing.T) {
	h := setupTestHandler(t)
	h.SetFleetWebhookSecret("topsecret")
	body := []byte(`{"enrollment_token":"t","host_id":"h"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", "sha256=deadbeef")
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad signature, got %d: %s", rec.Code, rec.Body.String())
	}
}

// With no configured secret the callback must reject everything (fail closed),
// even a request that carries a syntactically valid-looking signature.
func TestEnrollmentCallbackFailsClosedWithoutSecret(t *testing.T) {
	h := setupTestHandler(t)
	body := []byte(`{"enrollment_token":"t","host_id":"h"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet("anything", body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when no webhook secret is configured, got %d", rec.Code)
	}
}

func TestEnrollmentCallbackUnknownToken(t *testing.T) {
	const secret = "topsecret"
	db := &fakeDB{
		beginFn: func(ctx context.Context) (pgx.Tx, error) {
			return &fakeTx{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}}, nil
		},
	}
	// After the atomic consume finds no row, default fakeDB.QueryRow returns
	// ErrNoRows, so the handler classifies it as an unknown token.
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"missing","host_id":"host-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEnrollmentCallbackUsedToken(t *testing.T) {
	const secret = "topsecret"
	usedAt := time.Now()
	expiresAt := time.Now().Add(time.Hour)
	db := &fakeDB{
		beginFn: func(ctx context.Context) (pgx.Tx, error) {
			return &fakeTx{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}}, nil
		},
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(**time.Time)) = &usedAt
				*(dest[1].(*time.Time)) = expiresAt
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"used","host_id":"host-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 for used token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEnrollmentCallbackExpiredToken(t *testing.T) {
	const secret = "topsecret"
	expiresAt := time.Now().Add(-time.Hour)
	db := &fakeDB{
		beginFn: func(ctx context.Context) (pgx.Tx, error) {
			return &fakeTx{queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}}, nil
		},
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(**time.Time)) = nil
				*(dest[1].(*time.Time)) = expiresAt
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"expired","host_id":"host-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusGone {
		t.Errorf("expected 410 for expired token, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestEnrollmentCallbackLooksUpByHash proves M3: the token consumption
// UPDATE looks up by sha256 hash, never the plaintext value presented by
// the (in production, untrusted-until-verified) Fleet callback body.
func TestEnrollmentCallbackLooksUpByHash(t *testing.T) {
	const secret = "topsecret"
	const plaintext = "plain-token-value"
	var gotArg string
	db := &fakeDB{
		beginFn: func(ctx context.Context) (pgx.Tx, error) {
			return &fakeTx{
				queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
					if strings.Contains(sql, "UPDATE enrollment_tokens") && len(args) > 0 {
						gotArg, _ = args[0].(string)
					}
					return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
				},
			}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"` + plaintext + `","host_id":"host-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if gotArg == "" {
		t.Fatal("expected the token consumption UPDATE to run")
	}
	if gotArg == plaintext {
		t.Fatal("enrollment callback looked up the plaintext token, not its hash")
	}
	if gotArg != enrollmentTokenHash(plaintext) {
		t.Errorf("expected lookup by sha256 hash %q, got %q", enrollmentTokenHash(plaintext), gotArg)
	}
}

// TestEnrollmentCallbackSetsDeviceOrgFromToken proves C2: linkEnrolledDevice
// resolves the org_id the token was issued for and sets it EXPLICITLY on
// the devices INSERT/ON CONFLICT UPDATE, instead of letting it default to
// the Default Organization regardless of which org actually onboarded the
// user — the hole that let a Default-Org admin reach any tenant's devices.
func TestEnrollmentCallbackSetsDeviceOrgFromToken(t *testing.T) {
	const secret = "topsecret"
	const wantOrg = "aaaaaaaa-0000-0000-0000-0000000000aa"
	var insertedOrgID string
	var insertSQL string
	db := &fakeDB{
		beginFn: func(ctx context.Context) (pgx.Tx, error) {
			return &fakeTx{
				queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
					if strings.Contains(sql, "UPDATE enrollment_tokens") {
						return fakeRow{scanFn: func(dest ...any) error {
							*(dest[0].(*string)) = "user-1"
							*(dest[1].(*string)) = wantOrg
							return nil
						}}
					}
					return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
				},
				execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
					if strings.Contains(sql, "INSERT INTO devices") {
						insertSQL = sql
						if len(args) > 0 {
							insertedOrgID, _ = args[len(args)-1].(string)
						}
					}
					return pgconn.CommandTag{}, nil
				},
				commitFn: func(ctx context.Context) error { return nil },
			}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"tok","host_id":"host-99","hostname":"mac","os_version":"15"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if insertSQL == "" {
		t.Fatal("expected an INSERT INTO devices to run")
	}
	if !strings.Contains(insertSQL, "org_id") {
		t.Fatalf("expected the devices INSERT to set org_id explicitly, got SQL: %s", insertSQL)
	}
	if insertedOrgID != wantOrg {
		t.Errorf("expected device org_id=%s (the token's org), got %q", wantOrg, insertedOrgID)
	}
}

func TestEnrollmentCallbackMissingFields(t *testing.T) {
	const secret = "topsecret"
	h := NewHandler(&fakeDB{}, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"","host_id":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing fields, got %d", rec.Code)
	}
}
