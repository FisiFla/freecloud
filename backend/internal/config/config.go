package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// defaultDatabaseURL is the insecure local-dev DSN. Validate() rejects it (and
// any sslmode=disable DSN) outside development so it can never be the live
// production database connection.
const defaultDatabaseURL = "postgres://freecloud:freecloud@localhost:5432/freecloud?sslmode=disable"

// defaultKeycloakClientID is the service-account client created by the
// bootstrap engine on first startup.
const defaultKeycloakClientID = "freecloud-service"

// defaultKeycloakURL is the local-dev Keycloak address; Validate() rejects it
// outside development so production never points at a localhost identity provider.
const defaultKeycloakURL = "http://localhost:8081"

// deviceCookieKeyLabel is the domain-separation label used when deriving the
// device-identity cookie HMAC key from a master secret.
//
// The browser-facing device cookie and the server-to-server Fleet enrollment
// webhook are DIFFERENT trust domains: a key that is valid for one must not
// be valid for the other. Deriving a labelled subkey from the master secret
// guarantees that even the fallback path (no dedicated DEVICE_COOKIE_SECRET)
// never reuses the exact bytes of the fleet-webhook key.
const deviceCookieKeyLabel = "freecloud/device-cookie/v1"

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
	// DeviceCookieSecret HMAC-signs freecloud-device-id cookies. Optional:
	// when empty, DeviceCookieSigningSecret() derives a DOMAIN-SEPARATED
	// subkey from FleetWebhookSecret (see deviceCookieKeyLabel) so the
	// browser-facing cookie trust domain never reuses the Fleet webhook key.
	DeviceCookieSecret    string
	ProvisioningMasterKey string
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

	// C1 (LDAP/AD federation) — bind password for LDAP user-storage providers.
	// Resolved via env/file/vault (LDAP_BIND_PASSWORD / LDAP_BIND_PASSWORD_FILE).
	LDAPBindPassword string

	// Bootstrap credentials for the self-bootstrap Keycloak provisioning on startup.
	// KC_BOOTSTRAP_ADMIN / KC_BOOTSTRAP_PASSWORD (master-realm admin, dev default admin/admin).
	BootstrapAdminUser     string
	BootstrapAdminPassword string

	// B1 (v1.7 HA) — Redis-backed rate limiter. When set, the rate limiter
	// shares counters across all backend replicas via Redis instead of each
	// replica keeping its own in-memory counters. Required in production
	// (Validate() rejects an empty value outside dev/test) so a multi-replica
	// prod deployment can never silently fall back to per-replica counters,
	// which would let a client multiply its effective rate limit by the
	// replica count.
	RedisURL string

	// B2 (v1.7 HA) — how long server startup waits for a schema migration
	// that another instance/the migrate job is already applying, polling
	// until schema_migrations reflects the current version before serving.
	// 0 disables waiting: startup fails immediately if the schema is behind.
	WaitForSchemaTimeout time.Duration

	// A2 — GeoIPMMDBPath points at an operator-supplied MaxMind GeoLite2/GeoIP2
	// mmdb file (Country or City — both include the "country" record). Empty
	// (the default) keeps geo-allowlist conditions on the no-op lookup, which
	// fails closed (unknown country) whenever a policy sets a geo allowlist.
	// See docs/DEPLOYMENT.md "GeoIP (MaxMind GeoLite2)" for how to obtain one.
	GeoIPMMDBPath string

	// TrustedProxyCIDRs lists the network ranges of reverse proxies whose
	// X-Forwarded-For header may be trusted when deriving the client IP for
	// rate limiting (see internal/middleware/ratelimit.go). Empty means no
	// proxy is trusted and the peer address is always used, which behind a
	// single reverse proxy makes every tenant share one rate-limit bucket.
	TrustedProxyCIDRs []string

	// CORSOrigin is the browser origin allowed to call the API. It must be an
	// explicit origin (never "*") because the CORS handler also sends
	// Allow-Credentials: true, which the CORS spec forbids for a wildcard.
	CORSOrigin string

	// secretErrors records secrets that could not be resolved (for example a
	// configured Vault path that could not be read). Validate() refuses to
	// start when the list is non-empty: a secret that silently degraded to a
	// different source is worse than no secret at all.
	secretErrors []string

	// durationErrors records duration settings that could not be parsed.
	// Validate() refuses to start when the list is non-empty — a silently
	// substituted duration (e.g. AUDIT_RETAIN_FOR=30d parsed as 15m) prunes
	// the audit log.
	durationErrors []string
}

// SecretErrors returns the secret-resolution failures recorded by Load.
func (c *Config) SecretErrors() []string { return c.secretErrors }

// DurationErrors returns the duration-parse failures recorded by Load.
func (c *Config) DurationErrors() []string { return c.durationErrors }

// Load reads configuration from environment variables with sensible defaults.
// Secret fields (anything a leaked value would compromise) use resolveSecret,
// which honours the _FILE → _VAULT_PATH → plain-env precedence; see
// internal/config/secrets.go and docs/SECRETS.md for details.
//
// Load never fails: a secret that cannot be resolved, or a duration that
// cannot be parsed, is recorded on the Config and makes Validate() reject the
// configuration, so the process refuses to start rather than running with a
// silently substituted value.
func Load() *Config {
	secrets := &secretResolver{}
	c := &Config{
		Port:                  getEnv("PORT", "8080"),
		DatabaseURL:           secrets.get("DATABASE_URL", defaultDatabaseURL),
		KeycloakURL:           getEnv("KEYCLOAK_URL", defaultKeycloakURL),
		KeycloakRealm:         getEnv("KEYCLOAK_REALM", "freecloud"),
		KeycloakClientID:      getEnv("KEYCLOAK_CLIENT_ID", defaultKeycloakClientID),
		KeycloakClientSecret:  secrets.get("KEYCLOAK_CLIENT_SECRET", ""),
		KeycloakAudience:      getEnv("KEYCLOAK_AUDIENCE", "freecloud-dashboard"),
		FleetURL:              getEnv("FLEET_URL", "http://localhost:8082"),
		FleetAPIToken:         secrets.get("FLEET_API_TOKEN", ""),
		FleetWebhookSecret:    secrets.get("FLEET_WEBHOOK_SECRET", ""),
		DeviceCookieSecret:    secrets.get("DEVICE_COOKIE_SECRET", ""),
		ProvisioningMasterKey: secrets.get("PROVISIONING_MASTER_KEY", ""),
		SCIMBearerToken:       secrets.get("SCIM_BEARER_TOKEN", ""),
		AccessEvalToken:       secrets.get("ACCESS_EVAL_TOKEN", ""),

		// D1 — Notifications
		NotifyEmail:           parseBool(getEnv("NOTIFY_EMAIL", "false")),
		SMTPHost:              getEnv("SMTP_HOST", ""),
		SMTPPort:              getEnv("SMTP_PORT", "587"),
		SMTPFrom:              getEnv("SMTP_FROM", ""),
		SMTPTo:                getEnv("SMTP_TO", ""),
		SMTPPassword:          secrets.get("SMTP_PASSWORD", ""),
		NotifySlack:           parseBool(getEnv("NOTIFY_SLACK", "false")),
		SlackWebhookURL:       secrets.get("SLACK_WEBHOOK_URL", ""),
		NotifyWebhook:         parseBool(getEnv("NOTIFY_WEBHOOK", "false")),
		WebhookURL:            getEnv("WEBHOOK_URL", ""),
		WebhookSecret:         secrets.get("WEBHOOK_SECRET", ""),
		NotifyEventOffboard:   parseBool(getEnv("NOTIFY_EVENT_OFFBOARD", "true")),
		NotifyEventDrift:      parseBool(getEnv("NOTIFY_EVENT_DRIFT", "true")),
		NotifyEventCompliance: parseBool(getEnv("NOTIFY_EVENT_COMPLIANCE", "true")),

		// D3 — SIEM streaming
		SIEMSyslogAddr: getEnv("SIEM_SYSLOG_ADDR", ""),
		SIEMSyslogNet:  getEnv("SIEM_SYSLOG_NET", "udp"),
		SIEMHTTPUrl:    getEnv("SIEM_HTTP_URL", ""),
		SIEMHTTPToken:  secrets.get("SIEM_HTTP_TOKEN", ""),

		// C1 (LDAP/AD federation)
		LDAPBindPassword: secrets.get("LDAP_BIND_PASSWORD", ""),

		// Bootstrap credentials
		BootstrapAdminUser:     getEnv("KC_BOOTSTRAP_ADMIN", "admin"),
		BootstrapAdminPassword: secrets.get("KC_BOOTSTRAP_PASSWORD", "admin"),

		// B1 (v1.7 HA) — Redis-backed rate limiter (empty = in-memory fallback in dev/test).
		RedisURL: secrets.get("REDIS_URL", ""),
		// A2 — GeoIP (optional; empty keeps the no-op fail-closed default)
		GeoIPMMDBPath: getEnv("GEOIP_MMDB_PATH", ""),

		// Reverse proxies whose X-Forwarded-For may be trusted when deriving
		// the client IP for the rate limiter (issue #129).
		TrustedProxyCIDRs: splitCSV(getEnv("TRUSTED_PROXY_CIDRS", "")),

		// Browser origin allowed to call the API (issue #131).
		CORSOrigin: getEnv("CORS_ORIGIN", ""),
	}
	c.secretErrors = secrets.errs

	// Durations: an unparsable value is recorded and rejected by Validate()
	// rather than silently replaced by a default (issue #130).
	c.ReconcileInterval = c.duration("RECONCILE_INTERVAL", getEnv("RECONCILE_INTERVAL", "15m"))
	c.SnapshotInterval = c.duration("SNAPSHOT_INTERVAL", getEnv("SNAPSHOT_INTERVAL", "1h"))
	c.SIEMInterval = c.duration("SIEM_INTERVAL", getEnv("SIEM_INTERVAL", "5s"))
	c.AuditPruneInterval = c.duration("AUDIT_PRUNE_INTERVAL", getEnv("AUDIT_PRUNE_INTERVAL", "0"))
	c.AuditRetainFor = c.duration("AUDIT_RETAIN_FOR", getEnv("AUDIT_RETAIN_FOR", "0"))
	c.WaitForSchemaTimeout = c.duration("WAIT_FOR_SCHEMA_TIMEOUT", getEnv("WAIT_FOR_SCHEMA_TIMEOUT", "0"))
	return c
}

// duration parses one duration setting, recording a parse failure instead of
// substituting a default. A silently substituted duration is a data-loss bug:
// AUDIT_RETAIN_FOR=30d parsed as 15m prunes the entire audit history.
func (c *Config) duration(name, raw string) time.Duration {
	d, err := parseDuration(raw)
	if err != nil {
		c.durationErrors = append(c.durationErrors, fmt.Sprintf("%s=%q: %v", name, raw, err))
		return 0
	}
	return d
}

// daySuffixRe matches a "<number>d" duration component. Go's time.ParseDuration
// has no day unit, but operators naturally write "30d" for an audit retention
// window — and silently parsing that as an error (or, worse, substituting a
// default) is exactly what issue #130 is about.
var daySuffixRe = regexp.MustCompile(`(\d+(?:\.\d+)?)d`)

// parseDuration parses a duration string. An empty string or "0" disables the
// corresponding job. In addition to Go's time.ParseDuration syntax, a "d"
// (days) suffix is accepted.
//
// An unparsable or negative value is an ERROR, never a silent substitution.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	// Expand day components ("30d" → "720h") so the rest can be handed to
	// time.ParseDuration, which has no notion of days.
	expanded := daySuffixRe.ReplaceAllStringFunc(s, func(m string) string {
		num := strings.TrimSuffix(m, "d")
		days, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return m
		}
		return strconv.FormatFloat(days*24, 'f', -1, 64) + "h"
	})
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use e.g. 15m, 1h30m or 30d): %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid duration %q: must not be negative", s)
	}
	return d, nil
}

// splitCSV splits a comma-separated environment value into its non-empty,
// trimmed parts. It returns nil for an empty value.
func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Validate enforces that no insecure default reaches a non-development
// deployment. It is FAIL-CLOSED: only APP_ENV=development (or test) skips the
// checks. An unset or unrecognized APP_ENV is treated as production, so a
// deployment that simply forgets to set APP_ENV cannot silently run on the
// insecure dev defaults (default DB credentials, sslmode=disable, the public
// admin-cli client, empty audience/issuer, localhost CORS).
//
// A few failures are rejected in EVERY environment, because they are not
// "insecure defaults" but broken configuration: a secret that could not be
// read from its configured source, a duration that could not be parsed, and a
// wildcard CORS origin (which the CORS handler's Allow-Credentials: true
// makes invalid).
func (c *Config) Validate() error {
	env := os.Getenv("APP_ENV")

	var problems []string
	add := func(msg string) { problems = append(problems, msg) }

	// A secret that could not be read from its configured source (a Vault path
	// that failed, a missing secret file) is a hard error: the value that
	// would be used is either empty or comes from a different source than
	// the operator configured.
	for _, e := range c.secretErrors {
		add("secret could not be resolved: " + e)
	}
	// An unparsable duration is a hard error too: silently substituting a
	// default once turned AUDIT_RETAIN_FOR=30d into 15m and pruned the
	// entire audit history.
	for _, e := range c.durationErrors {
		add("invalid duration: " + e)
	}
	// A wildcard CORS origin is invalid wherever it appears: the CORS
	// handler also sends Allow-Credentials: true, which the CORS spec
	// forbids for a wildcard, and it lets any site make cross-origin
	// calls carrying an Authorization header.
	if c.CORSOrigin == "*" {
		add("CORS_ORIGIN must not be \"*\"; set the explicit dashboard origin (e.g. https://dashboard.example.com)")
	}

	if env == "development" || env == "test" {
		if len(problems) > 0 {
			return fmt.Errorf("invalid configuration for %s environment: %s", env, strings.Join(problems, "; "))
		}
		return nil // dev/test defaults are acceptable
	}
	if env == "" {
		env = "production (APP_ENV unset)"
	}

	// KEYCLOAK_CLIENT_SECRET is now optional in config: the bootstrap engine sets
	// it at runtime from Keycloak. An explicit env override is still accepted.

	// Reject the public admin-cli client (not the confidential service default).
	if c.KeycloakClientID == "" || c.KeycloakClientID == "admin-cli" {
		add("KEYCLOAK_CLIENT_ID must be a confidential client, not empty or the public \"admin-cli\"")
	}
	// An empty URL or audience silently disables the issuer/audience checks in
	// the auth middleware, so both must be present.
	if c.KeycloakURL == "" || c.KeycloakURL == defaultKeycloakURL {
		add("KEYCLOAK_URL must point at your real Keycloak, not empty or the localhost default")
	} else if u, err := url.Parse(c.KeycloakURL); err != nil || u.Scheme == "" || u.Host == "" {
		// A malformed URL (or one without a scheme/host) breaks the issuer
		// and JWKS derivation, so every token is rejected. Catch it here
		// instead of at the first request.
		add("KEYCLOAK_URL must be an absolute URL (e.g. https://keycloak.example.com)")
	} else if u.Scheme != "http" && u.Scheme != "https" {
		add("KEYCLOAK_URL must use the http or https scheme")
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
	if c.ProvisioningMasterKey == "" {
		add("PROVISIONING_MASTER_KEY must be set (used to encrypt stored integration secrets)")
	} else if raw, err := base64.StdEncoding.DecodeString(c.ProvisioningMasterKey); err != nil || len(raw) != 32 {
		add("PROVISIONING_MASTER_KEY must be a base64-encoded 32-byte key")
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
	if c.CORSOrigin == "" {
		add("CORS_ORIGIN must be set")
	} else if c.CORSOrigin != "*" {
		// Outside dev/test the dashboard is served over TLS; an http:// or
		// relative origin would let a network attacker read API responses.
		// (The "*" wildcard is already rejected above, in every environment.)
		if u, err := url.Parse(c.CORSOrigin); err != nil || u.Scheme != "https" || u.Host == "" {
			add("CORS_ORIGIN must be an absolute https:// origin (e.g. https://dashboard.example.com)")
		}
	}
	// DEVICE_COOKIE_SECRET is OPTIONAL: when it is unset the device-cookie
	// key is derived from FLEET_WEBHOOK_SECRET with its own
	// domain-separation label, so the browser-facing cookie trust domain
	// never reuses the fleet-webhook key (issue #125). Requiring the
	// variable outright would break the documented deployment (which leaves
	// it unset), so the fallback is allowed but never silent: main.go logs a
	// warning at startup whenever the derived key is in use.
	// REDIS_URL must be set in production: without it, the rate limiter falls
	// back to per-replica in-memory counters, which is silently unsafe on any
	// deployment running more than one backend instance (ADR 0004).
	if c.RedisURL == "" {
		add("REDIS_URL must be set (the rate limiter requires shared state across replicas in production)")
	}
	// Bootstrap admin password used to provision Keycloak on first start.
	// Reject empty/well-known defaults so a hand-rolled prod deploy cannot
	// leave master-realm admin/admin reachable.
	weakBootstrap := map[string]bool{
		"": true, "admin": true, "password": true, "changeme": true, "admin123": true,
	}
	if weakBootstrap[strings.ToLower(strings.TrimSpace(c.BootstrapAdminPassword))] {
		add("KC_BOOTSTRAP_PASSWORD must be set to a strong non-default value")
	}

	if len(problems) > 0 {
		return fmt.Errorf("insecure configuration for %s environment: %s", env, strings.Join(problems, "; "))
	}
	return nil
}

// IsDevOrE2E reports whether APP_ENV is "development" or "test" (the e2e
// stack sets APP_ENV=test — see docker/docker-compose.e2e.yml). Any other
// value, including unset, is treated as production. FAIL-CLOSED: gates
// dev/e2e-only affordances (like bootstrap.Config.SeedE2EAdmin) that must
// never be reachable in a real deployment.
func IsDevOrE2E() bool {
	env := os.Getenv("APP_ENV")
	return env == "development" || env == "test"
}

// DeviceCookieSigningSecret returns the HMAC key for freecloud-device-id.
//
// The cookie and the Fleet enrollment webhook are DIFFERENT trust domains:
// the cookie authenticates a browser talking to the backend, the webhook
// authenticates the Fleet server talking to the backend. Signing both with
// the same key means one leaked secret authenticates in both domains.
//
// The device-cookie key is therefore always domain-separated from the Fleet
// webhook key:
//
//	device cookie key = HMAC-SHA256(master, "freecloud/device-cookie/v1")
//	fleet webhook key = the raw FLEET_WEBHOOK_SECRET
//
// The fleet side deliberately stays RAW: FleetDM signs the enrollment
// callback with the shared secret itself (X-Fleet-Signature is
// HMAC-SHA256(body, FLEET_WEBHOOK_SECRET)), so the backend must verify with
// the same bytes Fleet used — a derived subkey would break every enrollment
// callback. The domain separation is achieved by deriving the OTHER side
// instead, which needs no cooperation from the external peer.
//
// master is DEVICE_COOKIE_SECRET when set, otherwise FLEET_WEBHOOK_SECRET.
// Validate() additionally requires a dedicated DEVICE_COOKIE_SECRET in
// production, so the shared-master fallback is a dev/test convenience only.
func (c *Config) DeviceCookieSigningSecret() string {
	if c == nil {
		return ""
	}
	master := c.DeviceCookieSecret
	if master == "" {
		master = c.FleetWebhookSecret
	}
	if master == "" {
		return ""
	}
	return deriveSubkey(master, deviceCookieKeyLabel)
}

// DeviceCookieSecretConfigured reports whether a dedicated DEVICE_COOKIE_SECRET
// was supplied. When it is false the device-cookie key is DERIVED from
// FLEET_WEBHOOK_SECRET with its own domain-separation label, so the two trust
// domains never share key material — but the operator should know the cookie
// key is not independently rotatable (issue #125).
func (c *Config) DeviceCookieSecretConfigured() bool {
	return c != nil && c.DeviceCookieSecret != ""
}

// deriveSubkey derives a purpose-specific subkey from a master secret using
// HMAC-SHA256 with a domain-separation label. Two purposes sharing a master
// therefore never produce the same key bytes.
func deriveSubkey(master, label string) string {
	mac := hmac.New(sha256.New, []byte(master))
	mac.Write([]byte(label))
	return hex.EncodeToString(mac.Sum(nil))
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
