package config

import (
	"testing"
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
	t.Setenv("PROVISIONING_MASTER_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	t.Setenv("CORS_ORIGIN", "https://dashboard.example.com")
	t.Setenv("SCIM_BEARER_TOKEN", "scim-secret-token")
	t.Setenv("ACCESS_EVAL_TOKEN", "access-eval-secret")
	t.Setenv("REDIS_URL", "redis://redis:6379/0")
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
