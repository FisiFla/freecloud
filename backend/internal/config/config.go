package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// defaultDatabaseURL is the insecure local-dev DSN. Validate() rejects it (and
// any sslmode=disable DSN) outside development so it can never be the live
// production database connection.
const defaultDatabaseURL = "postgres://freecloud:freecloud@localhost:5432/freecloud?sslmode=disable"

// defaultKeycloakClientID is Keycloak's built-in public client. It cannot
// perform a confidential client-credentials grant with a secret, so Validate()
// rejects it in production.
const defaultKeycloakClientID = "admin-cli"

// defaultKeycloakURL is the local-dev Keycloak address; Validate() rejects it
// outside development so production never points at a localhost identity provider.
const defaultKeycloakURL = "http://localhost:8081"

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
	FleetWebhookSecret   string
	// SCIMBearerToken authenticates inbound SCIM 2.0 provisioning requests.
	// Must be a high-entropy secret (e.g. 32+ random bytes hex-encoded).
	// Required in production — Validate() rejects an empty value outside dev/test.
	SCIMBearerToken      string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Port:                 getEnv("PORT", "8080"),
		DatabaseURL:          getEnv("DATABASE_URL", defaultDatabaseURL),
		KeycloakURL:          getEnv("KEYCLOAK_URL", defaultKeycloakURL),
		KeycloakRealm:        getEnv("KEYCLOAK_REALM", "freecloud"),
		KeycloakClientID:     getEnv("KEYCLOAK_CLIENT_ID", defaultKeycloakClientID),
		KeycloakClientSecret: getEnv("KEYCLOAK_CLIENT_SECRET", ""),
		KeycloakAudience:     getEnv("KEYCLOAK_AUDIENCE", "freecloud-dashboard"),
		FleetURL:             getEnv("FLEET_URL", "http://localhost:8082"),
		FleetAPIToken:        getEnv("FLEET_API_TOKEN", ""),
		FleetWebhookSecret:   getEnv("FLEET_WEBHOOK_SECRET", ""),
		SCIMBearerToken:      getEnv("SCIM_BEARER_TOKEN", ""),
	}
}

// Validate enforces that no insecure default reaches a non-development
// deployment. It is FAIL-CLOSED: only APP_ENV=development (or test) skips the
// checks. An unset or unrecognized APP_ENV is treated as production, so a
// deployment that simply forgets to set APP_ENV cannot silently run on the
// insecure dev defaults (default DB credentials, sslmode=disable, the public
// admin-cli client, empty audience/issuer, localhost CORS).
func (c *Config) Validate() error {
	env := os.Getenv("APP_ENV")
	if env == "development" || env == "test" {
		return nil // dev/test defaults are acceptable
	}
	if env == "" {
		env = "production (APP_ENV unset)"
	}

	var problems []string
	add := func(msg string) { problems = append(problems, msg) }

	if c.KeycloakClientSecret == "" {
		add("KEYCLOAK_CLIENT_SECRET must be set")
	}
	if c.KeycloakClientID == "" || c.KeycloakClientID == defaultKeycloakClientID {
		add("KEYCLOAK_CLIENT_ID must be a confidential client, not the default \"admin-cli\"")
	}
	// An empty URL or audience silently disables the issuer/audience checks in
	// the auth middleware, so both must be present.
	if c.KeycloakURL == "" || c.KeycloakURL == defaultKeycloakURL {
		add("KEYCLOAK_URL must point at your real Keycloak, not empty or the localhost default")
	}
	if c.KeycloakAudience == "" {
		add("KEYCLOAK_AUDIENCE must be set (an empty value disables JWT audience validation)")
	}
	if c.FleetAPIToken == "" {
		add("FLEET_API_TOKEN must be set")
	}
	if c.FleetWebhookSecret == "" {
		add("FLEET_WEBHOOK_SECRET must be set (used to authenticate Fleet enrollment callbacks)")
	}
	if c.SCIMBearerToken == "" {
		add("SCIM_BEARER_TOKEN must be set (used to authenticate inbound SCIM provisioning requests)")
	}
	if c.DatabaseURL == "" || c.DatabaseURL == defaultDatabaseURL {
		add("DATABASE_URL must be set to a real database, not the insecure default")
	} else if strings.Contains(c.DatabaseURL, "sslmode=disable") {
		add("DATABASE_URL must not use sslmode=disable; require TLS to the database")
	}
	// CORS_ORIGIN must be set explicitly outside dev so credentials are never
	// silently allowed from the localhost default.
	if os.Getenv("CORS_ORIGIN") == "" {
		add("CORS_ORIGIN must be set")
	}

	if len(problems) > 0 {
		return fmt.Errorf("insecure configuration for %s environment: %s", env, strings.Join(problems, "; "))
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
