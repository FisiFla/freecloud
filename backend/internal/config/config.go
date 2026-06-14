package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	Port                 string
	DatabaseURL          string
	KeycloakURL          string
	KeycloakRealm        string
	KeycloakClientID     string
	KeycloakClientSecret string
	KeycloakAudience     string
	FleetURL             string
	FleetAPIToken        string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Port:                 getEnv("PORT", "8080"),
		DatabaseURL:          getEnv("DATABASE_URL", "postgres://freecloud:freecloud@localhost:5432/freecloud?sslmode=disable"),
		KeycloakURL:          getEnv("KEYCLOAK_URL", "http://localhost:8081"),
		KeycloakRealm:        getEnv("KEYCLOAK_REALM", "freecloud"),
		KeycloakClientID:     getEnv("KEYCLOAK_CLIENT_ID", "admin-cli"),
		KeycloakClientSecret: getEnv("KEYCLOAK_CLIENT_SECRET", ""),
		KeycloakAudience:     getEnv("KEYCLOAK_AUDIENCE", "freecloud-dashboard"),
		FleetURL:             getEnv("FLEET_URL", "http://localhost:8082"),
		FleetAPIToken:        getEnv("FLEET_API_TOKEN", ""),
	}
}

// Validate checks that required configuration is set for non-development environments.
func (c *Config) Validate() error {
	env := os.Getenv("APP_ENV")
	if env == "" || env == "development" {
		return nil // dev defaults are acceptable
	}

	if c.KeycloakClientSecret == "" {
		return fmt.Errorf("KEYCLOAK_CLIENT_SECRET is required in %s environment", env)
	}
	if c.FleetAPIToken == "" {
		return fmt.Errorf("FLEET_API_TOKEN is required in %s environment", env)
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required in %s environment", env)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// RedactDSN returns a copy of the database URL with the password component
// masked, so it is safe to log. Non-URL strings (e.g. "host=... user=...")
// are returned with a generic "(redacted)" marker if they appear to contain
// a password.
func RedactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	// libpq connection URI form: postgres://user:pass@host:port/db?...
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, hasPw := u.User.Password(); hasPw {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
			return u.String()
		}
		return u.String()
	}
	// libpq keyword/value form
	if strings.Contains(dsn, "password=") {
		return "(redacted: keyword/value DSN contains password)"
	}
	return dsn
}
