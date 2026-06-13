package config

import (
	"os"
	"testing"
)

func TestValidateDevelopment(t *testing.T) {
	os.Unsetenv("APP_ENV")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error in dev, got %v", err)
	}
}

func TestValidateDevelopmentExplicit(t *testing.T) {
	os.Setenv("APP_ENV", "development")
	defer os.Unsetenv("APP_ENV")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error in development, got %v", err)
	}
}

func TestValidateProductionMissingKeycloakSecret(t *testing.T) {
	os.Setenv("APP_ENV", "production")
	os.Setenv("KEYCLOAK_CLIENT_SECRET", "")
	os.Setenv("FLEET_API_TOKEN", "some-token")
	defer os.Unsetenv("APP_ENV")
	defer os.Unsetenv("FLEET_API_TOKEN")
	cfg := Load()
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing KEYCLOAK_CLIENT_SECRET in production, got nil")
	}
}

func TestValidateProductionMissingFleetToken(t *testing.T) {
	os.Setenv("APP_ENV", "production")
	os.Setenv("KEYCLOAK_CLIENT_SECRET", "some-secret")
	os.Setenv("FLEET_API_TOKEN", "")
	defer os.Unsetenv("APP_ENV")
	defer os.Unsetenv("KEYCLOAK_CLIENT_SECRET")
	cfg := Load()
	// Need to set the fields directly since Load() reads env at call time
	cfg.FleetAPIToken = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing FLEET_API_TOKEN in production, got nil")
	}
}

func TestValidateProductionAllSet(t *testing.T) {
	os.Setenv("APP_ENV", "production")
	os.Setenv("KEYCLOAK_CLIENT_SECRET", "some-secret")
	os.Setenv("FLEET_API_TOKEN", "some-token")
	defer os.Unsetenv("APP_ENV")
	defer os.Unsetenv("KEYCLOAK_CLIENT_SECRET")
	defer os.Unsetenv("FLEET_API_TOKEN")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error with all secrets set, got %v", err)
	}
}
