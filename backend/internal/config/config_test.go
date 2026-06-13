package config

import (
	"testing"
)

func TestValidateDevelopment(t *testing.T) {
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error in dev, got %v", err)
	}
}

func TestValidateDevelopmentExplicit(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error in development, got %v", err)
	}
}

func TestValidateProductionMissingKeycloakSecret(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("KEYCLOAK_CLIENT_SECRET", "")
	t.Setenv("FLEET_API_TOKEN", "some-token")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing KEYCLOAK_CLIENT_SECRET in production, got nil")
	}
}

func TestValidateProductionMissingFleetToken(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("KEYCLOAK_CLIENT_SECRET", "some-secret")
	t.Setenv("FLEET_API_TOKEN", "")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing FLEET_API_TOKEN in production, got nil")
	}
}

func TestValidateProductionAllSet(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("KEYCLOAK_CLIENT_SECRET", "some-secret")
	t.Setenv("FLEET_API_TOKEN", "some-token")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error with all secrets set, got %v", err)
	}
}
