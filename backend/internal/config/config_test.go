package config

import (
	"testing"
	"time"
)

// setSecureProdEnv sets a complete, secure production configuration via the
// environment. Individual tests then override one variable to an insecure
// value to prove Validate() rejects it.
func setSecureProdEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=require")
	t.Setenv("KEYCLOAK_URL", "https://kc.example.com")
	t.Setenv("KEYCLOAK_CLIENT_ID", "freecloud-service")
	t.Setenv("KEYCLOAK_CLIENT_SECRET", "kc-secret")
	t.Setenv("KEYCLOAK_AUDIENCE", "freecloud-dashboard")
	t.Setenv("FLEET_API_TOKEN", "fleet-token")
	t.Setenv("FLEET_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("DEVICE_COOKIE_SECRET", "device-cookie-secret")
	t.Setenv("PROVISIONING_MASTER_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	t.Setenv("CORS_ORIGIN", "https://dashboard.example.com")
	t.Setenv("SCIM_BEARER_TOKEN", "scim-secret-token")
	t.Setenv("ACCESS_EVAL_TOKEN", "access-eval-secret")
	t.Setenv("REDIS_URL", "redis://redis:6379/0")
	t.Setenv("KC_BOOTSTRAP_PASSWORD", "prod-bootstrap-s3cret!")
}

func TestValidateDevelopmentExplicit(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error in development, got %v", err)
	}
}

func TestValidateTestEnv(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error in test env, got %v", err)
	}
}

// Unset APP_ENV must FAIL CLOSED: it is treated as production, so the insecure
// dev defaults (default DSN, admin-cli, missing secrets) must be rejected.
func TestValidateUnsetAppEnvFailsClosed(t *testing.T) {
	t.Setenv("APP_ENV", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when APP_ENV is unset and config is the insecure default, got nil")
	}
}

func TestValidateProductionAllSecure(t *testing.T) {
	setSecureProdEnv(t)
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error with a full secure prod config, got %v", err)
	}
}

func TestValidateProductionRejectsWeakBootstrapPassword(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("KC_BOOTSTRAP_PASSWORD", "admin")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for default KC_BOOTSTRAP_PASSWORD in production, got nil")
	}
}

func TestValidateProductionRejectsDefaultDSN(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("DATABASE_URL", defaultDatabaseURL)
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for the insecure default DATABASE_URL in production, got nil")
	}
}

func TestValidateProductionRejectsSSLModeDisable(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("DATABASE_URL", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=disable")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for sslmode=disable DATABASE_URL in production, got nil")
	}
}

func TestValidateProductionRejectsAdminCliClient(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("KEYCLOAK_CLIENT_ID", "admin-cli")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for default admin-cli KEYCLOAK_CLIENT_ID in production, got nil")
	}
}

func TestValidateProductionRejectsLocalhostKeycloak(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("KEYCLOAK_URL", defaultKeycloakURL)
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for localhost-default KEYCLOAK_URL in production, got nil")
	}
}

func TestValidateProductionKeycloakSecretIsOptional(t *testing.T) {
	// KEYCLOAK_CLIENT_SECRET is optional: the bootstrap engine sets it at runtime.
	// Validate() must not reject a missing secret — the operator may rely on bootstrap.
	setSecureProdEnv(t)
	t.Setenv("KEYCLOAK_CLIENT_SECRET", "")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("KEYCLOAK_CLIENT_SECRET is self-managed by bootstrap; Validate() must accept an empty value, got: %v", err)
	}
}

func TestValidateProductionMissingFleetToken(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("FLEET_API_TOKEN", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing FLEET_API_TOKEN in production, got nil")
	}
}

func TestValidateProductionMissingWebhookSecret(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("FLEET_WEBHOOK_SECRET", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing FLEET_WEBHOOK_SECRET in production, got nil")
	}
}

func TestValidateProductionMissingProvisioningMasterKey(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("PROVISIONING_MASTER_KEY", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing PROVISIONING_MASTER_KEY in production, got nil")
	}
}

func TestValidateProductionInvalidProvisioningMasterKey(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("PROVISIONING_MASTER_KEY", "not-a-32-byte-base64-key")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid PROVISIONING_MASTER_KEY in production, got nil")
	}
}

func TestValidateProductionMissingCORSOrigin(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("CORS_ORIGIN", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing CORS_ORIGIN in production, got nil")
	}
}

func TestValidateProductionMissingSCIMBearerToken(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("SCIM_BEARER_TOKEN", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing SCIM_BEARER_TOKEN in production, got nil")
	}
}

func TestValidateProductionMissingAccessEvalToken(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("ACCESS_EVAL_TOKEN", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing ACCESS_EVAL_TOKEN in production, got nil")
	}
}

// The device-cookie key is always domain-separated from the fleet-webhook
// key, even when both derive from the same master secret: the two trust
// domains (browser→backend cookie vs. Fleet→backend webhook) must never
// share key material (issue #125).
func TestDeviceCookieSigningSecretIsDomainSeparated(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("FLEET_WEBHOOK_SECRET", "fleet-secret")
	t.Setenv("DEVICE_COOKIE_SECRET", "")
	cfg := Load()

	got := cfg.DeviceCookieSigningSecret()
	if got == "" {
		t.Fatal("expected a derived device-cookie key")
	}
	if got == "fleet-secret" {
		t.Error("device-cookie key must not be the raw fleet webhook secret")
	}
	// The fleet webhook key stays RAW: FleetDM signs the enrollment callback
	// with the shared secret itself, so a derived subkey would break it.
	if cfg.FleetWebhookSecret != "fleet-secret" {
		t.Errorf("fleet webhook secret must stay raw, got %q", cfg.FleetWebhookSecret)
	}
	// Deriving the same master with the same label is deterministic, so a
	// restart does not invalidate every issued cookie.
	if cfg.DeviceCookieSigningSecret() != got {
		t.Error("device-cookie key derivation must be deterministic")
	}
}

// With a dedicated DEVICE_COOKIE_SECRET the key is derived from THAT
// secret, so the fleet-webhook master is never involved.
func TestDeviceCookieSigningSecretPrefersDedicatedSecret(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("FLEET_WEBHOOK_SECRET", "fleet-secret")
	t.Setenv("DEVICE_COOKIE_SECRET", "cookie-only")
	cfg := Load()

	got := cfg.DeviceCookieSigningSecret()
	if got == "" {
		t.Fatal("expected a device-cookie key")
	}
	if got == "cookie-only" {
		t.Error("device-cookie key must be domain-separated from its master too")
	}
	if got == "fleet-secret" {
		t.Error("device-cookie key must never equal the fleet webhook secret")
	}
}

// The device-cookie key is derived from FLEET_WEBHOOK_SECRET when no
// dedicated DEVICE_COOKIE_SECRET is set. The derivation is domain-separated,
// so the two trust domains never share key material (issue #125) — but the
// operator should know the cookie key is not independently rotatable.
func TestDeviceCookieSecretConfigured(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("FLEET_WEBHOOK_SECRET", "fleet-secret")
	t.Setenv("DEVICE_COOKIE_SECRET", "")
	cfg := Load()
	if cfg.DeviceCookieSecretConfigured() {
		t.Error("expected DeviceCookieSecretConfigured() to be false without DEVICE_COOKIE_SECRET")
	}
	t.Setenv("DEVICE_COOKIE_SECRET", "cookie-only")
	cfg = Load()
	if !cfg.DeviceCookieSecretConfigured() {
		t.Error("expected DeviceCookieSecretConfigured() to be true with DEVICE_COOKIE_SECRET set")
	}
}

// A wildcard CORS origin is invalid everywhere: the CORS handler also sends
// Allow-Credentials: true, which the spec forbids for a wildcard.
func TestValidateRejectsWildcardCORSOrigin(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("CORS_ORIGIN", "*")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for CORS_ORIGIN=* , got nil")
	}
}

// The wildcard is rejected in dev/test too — it is invalid in every
// environment, not just production.
func TestValidateRejectsWildcardCORSOriginInDev(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CORS_ORIGIN", "*")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for CORS_ORIGIN=* in development, got nil")
	}
}

// Outside dev/test the dashboard origin must be an absolute https URL.
func TestValidateRejectsNonHTTPSCORSOrigin(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("CORS_ORIGIN", "http://dashboard.example.com")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for a non-https CORS_ORIGIN in production, got nil")
	}
}

// An unparsable AUDIT_RETAIN_FOR must fail validation: silently substituting
// 15m pruned the entire audit history (issue #130).
func TestValidateRejectsUnparsableAuditRetention(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("AUDIT_RETAIN_FOR", "30 days")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for an unparsable AUDIT_RETAIN_FOR, got nil")
	}
}

// The day suffix operators naturally use must parse.
func TestParseDurationAcceptsDaySuffix(t *testing.T) {
	d, err := parseDuration("30d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := 30 * 24 * time.Hour; d != want {
		t.Errorf("expected %v, got %v", want, d)
	}
}

func TestParseDurationRejectsGarbage(t *testing.T) {
	for _, s := range []string{"30 days", "abc", "-5m", "15x"} {
		if _, err := parseDuration(s); err == nil {
			t.Errorf("expected an error for %q", s)
		}
	}
}

func TestParseDurationZeroAndEmpty(t *testing.T) {
	for _, s := range []string{"", "0", "  "} {
		d, err := parseDuration(s)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", s, err)
		}
		if d != 0 {
			t.Errorf("%q: expected 0, got %v", s, d)
		}
	}
}

// A malformed KEYCLOAK_URL breaks issuer/JWKS derivation, so every token is
// rejected — catch it at startup instead of at the first request.
func TestValidateRejectsMalformedKeycloakURL(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("KEYCLOAK_URL", "not-a-url")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for a malformed KEYCLOAK_URL, got nil")
	}
}

// B1 (v1.7 HA): REDIS_URL must be required in production so a multi-replica
// deployment can never silently fall back to per-replica in-memory rate
// limiting (see docs/adr/0004-multi-instance-ha.md).
func TestValidateProductionMissingRedisURL(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("REDIS_URL", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing REDIS_URL in production, got nil")
	}
}

// TestIsDevOrE2E_FailsClosed proves the A1 e2e-admin gate (config.IsDevOrE2E)
// only opens for development/test and treats everything else — including an
// unset APP_ENV — as production. bootstrap.Config.SeedE2EAdmin is only ever
// meant to be honoured behind this check.
func TestIsDevOrE2E_FailsClosed(t *testing.T) {
	cases := []struct {
		appEnv string
		want   bool
	}{
		{"development", true},
		{"test", true},
		{"production", false},
		{"staging", false},
		{"", false},
	}
	for _, c := range cases {
		t.Setenv("APP_ENV", c.appEnv)
		if got := IsDevOrE2E(); got != c.want {
			t.Errorf("IsDevOrE2E() with APP_ENV=%q: got %v, want %v", c.appEnv, got, c.want)
		}
	}
}
