package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
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
	SCIMBearerToken string

	// AccessEvalToken authenticates POST /api/v1/access/evaluate calls from the
	// Keycloak authenticator SPI (or any other service that needs to gate SSO on
	// device posture). Must be a high-entropy secret. Required in production.
	AccessEvalToken string

	// Reconciliation job (D1 / FCEXP-21)
	ReconcileInterval time.Duration // 0 means disabled

	// D1 — Event notifications (FCEX2-17)
	NotifyEmail     bool
	SMTPHost        string
	SMTPPort        string
	SMTPFrom        string
	SMTPTo          string // comma-separated
	SMTPPassword    string
	NotifySlack     bool
	SlackWebhookURL string
	NotifyWebhook   bool
	WebhookURL      string
	WebhookSecret   string
	// Per-event toggles (all default true)
	NotifyEventOffboard   bool
	NotifyEventDrift      bool
	NotifyEventCompliance bool

	// D2 — Analytics snapshot job (FCEX2-18)
	SnapshotInterval time.Duration // 0 means disabled

	// D3 — SIEM streaming (FCEX2-19)
	SIEMSyslogAddr string
	SIEMSyslogNet  string
	SIEMHTTPUrl    string
	SIEMHTTPToken  string
	SIEMInterval   time.Duration // 0 means disabled

	// C1 (FCEX3-13) — Audit retention (C2 / FCEX3-14)
	// AuditPruneInterval controls how often the pruner runs. 0 = disabled.
	// AuditRetainFor is the retention window. 0 = keep forever.
	AuditPruneInterval time.Duration
	AuditRetainFor     time.Duration
}

// Load reads configuration from environment variables with sensible defaults.
// Secret fields (anything a leaked value would compromise) use resolveSecret,
// which honours the _FILE → _VAULT_PATH → plain-env precedence; see
// internal/config/secrets.go and docs/SECRETS.md for details.
func Load() *Config {
	return &Config{
		Port:                 getEnv("PORT", "8080"),
		DatabaseURL:          resolveSecret("DATABASE_URL", defaultDatabaseURL),
		KeycloakURL:          getEnv("KEYCLOAK_URL", defaultKeycloakURL),
		KeycloakRealm:        getEnv("KEYCLOAK_REALM", "freecloud"),
		KeycloakClientID:     getEnv("KEYCLOAK_CLIENT_ID", defaultKeycloakClientID),
		KeycloakClientSecret: resolveSecret("KEYCLOAK_CLIENT_SECRET", ""),
		KeycloakAudience:     getEnv("KEYCLOAK_AUDIENCE", "freecloud-dashboard"),
		FleetURL:             getEnv("FLEET_URL", "http://localhost:8082"),
		FleetAPIToken:        resolveSecret("FLEET_API_TOKEN", ""),
		FleetWebhookSecret:   resolveSecret("FLEET_WEBHOOK_SECRET", ""),
		SCIMBearerToken:      resolveSecret("SCIM_BEARER_TOKEN", ""),
		AccessEvalToken:      resolveSecret("ACCESS_EVAL_TOKEN", ""),
		ReconcileInterval:    parseDuration(getEnv("RECONCILE_INTERVAL", "15m")),

		// D1 — Notifications
		NotifyEmail:           parseBool(getEnv("NOTIFY_EMAIL", "false")),
		SMTPHost:              getEnv("SMTP_HOST", ""),
		SMTPPort:              getEnv("SMTP_PORT", "587"),
		SMTPFrom:              getEnv("SMTP_FROM", ""),
		SMTPTo:                getEnv("SMTP_TO", ""),
		SMTPPassword:          resolveSecret("SMTP_PASSWORD", ""),
		NotifySlack:           parseBool(getEnv("NOTIFY_SLACK", "false")),
		SlackWebhookURL:       resolveSecret("SLACK_WEBHOOK_URL", ""),
		NotifyWebhook:         parseBool(getEnv("NOTIFY_WEBHOOK", "false")),
		WebhookURL:            getEnv("WEBHOOK_URL", ""),
		WebhookSecret:         resolveSecret("WEBHOOK_SECRET", ""),
		NotifyEventOffboard:   parseBool(getEnv("NOTIFY_EVENT_OFFBOARD", "true")),
		NotifyEventDrift:      parseBool(getEnv("NOTIFY_EVENT_DRIFT", "true")),
		NotifyEventCompliance: parseBool(getEnv("NOTIFY_EVENT_COMPLIANCE", "true")),

		// D2 — Snapshot job
		SnapshotInterval: parseDuration(getEnv("SNAPSHOT_INTERVAL", "1h")),

		// D3 — SIEM streaming
		SIEMSyslogAddr: getEnv("SIEM_SYSLOG_ADDR", ""),
		SIEMSyslogNet:  getEnv("SIEM_SYSLOG_NET", "udp"),
		SIEMHTTPUrl:    getEnv("SIEM_HTTP_URL", ""),
		SIEMHTTPToken:  resolveSecret("SIEM_HTTP_TOKEN", ""),
		SIEMInterval:   parseDuration(getEnv("SIEM_INTERVAL", "5s")),

		// C1 — Audit retention
		AuditPruneInterval: parseDuration(getEnv("AUDIT_PRUNE_INTERVAL", "0")),
		AuditRetainFor:     parseDuration(getEnv("AUDIT_RETAIN_FOR", "0")),
	}
}

// parseDuration parses a duration string. An empty string or "0" disables the
// reconciliation job. Invalid values fall back to the sane default of 15 minutes
// rather than panicking at startup.
func parseDuration(s string) time.Duration {
	if s == "" || s == "0" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 15 * time.Minute
	}
	return d
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
	if c.AccessEvalToken == "" {
		add("ACCESS_EVAL_TOKEN must be set (used to authenticate access evaluation requests)")
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

// parseBool parses a boolean environment variable. Returns false on invalid values.
func parseBool(s string) bool {
	return s == "true" || s == "1" || s == "yes"
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
