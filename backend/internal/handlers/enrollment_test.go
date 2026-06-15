package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
