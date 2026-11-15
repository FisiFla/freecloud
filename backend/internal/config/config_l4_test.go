package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Security-fix lane L4: whitespace-only secrets must not pass production
// validation, DATABASE_URL must carry an explicit strong sslmode, and the
// Redis password must be injectable from REDIS_PASSWORD(_FILE).

// Every required secret / required value that Validate() gates in production
// must treat a blank (whitespace-only) value as unset — " ", "\n" and "\t"
// included — not as a configured secret.
func TestL4ValidateProductionRejectsBlankSecrets(t *testing.T) {
	required := []string{
		"FLEET_API_TOKEN",
		"FLEET_WEBHOOK_SECRET",
		"PROVISIONING_MASTER_KEY",
		"SCIM_BEARER_TOKEN",
		"ACCESS_EVAL_TOKEN",
		"KC_BOOTSTRAP_PASSWORD",
		"REDIS_URL",
		"CORS_ORIGIN",
		"KEYCLOAK_AUDIENCE",
		"DATABASE_URL",
	}
	blanks := []string{" ", "\n", "\t", "  \t\n "}
	for _, key := range required {
		for _, blank := range blanks {
			t.Run(key+"/"+strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(blank, " ", "sp"), "\n", "nl"), "\t", "tab"), func(t *testing.T) {
				setSecureProdEnv(t)
				t.Setenv(key, blank)
				cfg := Load()
				if err := cfg.Validate(); err == nil {
					t.Errorf("expected error for %s=%q in production, got nil", key, blank)
				}
			})
		}
	}
}

// A _FILE whose content is blank (after trimming) is a misconfiguration and
// must be an error — the value degrades to empty and Validate() refuses to
// start — instead of silently running with a whitespace secret.
func TestL4ResolveSecretBlankFileIsError(t *testing.T) {
	blanks := []string{" ", "\n", "\t", "  \t\n ", "\n\n"}
	for _, blank := range blanks {
		dir := t.TempDir()
		p := filepath.Join(dir, "secret")
		if err := os.WriteFile(p, []byte(blank), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MY_SECRET", "env-value-that-must-not-be-used")
		t.Setenv("MY_SECRET_FILE", p)

		got, err := resolveSecret("MY_SECRET", "")
		if err == nil {
			t.Errorf("blank %q: expected an error, got nil (value %q)", blank, got)
		}
		if got != "" {
			t.Errorf("blank %q: expected empty value, got %q", blank, got)
		}
	}
}

// Trailing newlines in secret files keep working: "value\n" resolves to
// "value" with no error (documents the existing TrimRight behaviour).
func TestL4ResolveSecretTrailingNewlineStillWorks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret")
	if err := os.WriteFile(p, []byte("file-secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MY_SECRET_FILE", p)
	got, err := resolveSecret("MY_SECRET", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file-secret-value" {
		t.Errorf("expected 'file-secret-value', got %q", got)
	}
}

// Exception: docker/init-secrets/generate.sh deliberately mirrors unset
// operator-managed keys as empty (newline-only) files. Those must keep
// meaning "not configured" — ("", nil) — not a boot failure. For
// FLEET_API_TOKEN the "must be set" Validate() check still fires; that is
// the documented fast failure, not a resolution error.
func TestL4ResolveSecretBlankFileAllowedForMirroredOptional(t *testing.T) {
	for _, key := range []string{
		"FLEET_API_TOKEN",
		"SLACK_ALERT_WEBHOOK_URL",
		"SLACK_WEBHOOK_URL",
		"DEVICE_COOKIE_SECRET",
		"LDAP_BIND_PASSWORD",
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, "secret")
		// Exactly what generate.sh writes for an unset mirrored key:
		// printf '%s\n' "".
		if err := os.WriteFile(p, []byte("\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, "")
		t.Setenv(key+"_FILE", p)
		got, err := resolveSecret(key, "")
		if err != nil {
			t.Errorf("%s: blank mirrored file must not error, got %v", key, err)
		}
		if got != "" {
			t.Errorf("%s: blank mirrored file must resolve to empty, got %q", key, got)
		}
	}
}

// End-to-end through Load: empty mirrored files for the optional
// integrations keep them "not configured" without recording secret errors,
// so the production boot is unaffected.
func TestL4LoadEmptyMirroredOptionalsStayUnconfigured(t *testing.T) {
	setSecureProdEnv(t)
	for _, key := range []string{"SLACK_WEBHOOK_URL", "DEVICE_COOKIE_SECRET", "LDAP_BIND_PASSWORD"} {
		p := filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(p, []byte("\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key+"_FILE", p)
	}
	cfg := Load()
	if len(cfg.SecretErrors()) != 0 {
		t.Fatalf("empty mirrored optionals must record no errors, got %v", cfg.SecretErrors())
	}
	if cfg.SlackWebhookURL != "" || cfg.DeviceCookieSecret != "" || cfg.LDAPBindPassword != "" {
		t.Errorf("empty mirrored optionals must resolve empty, got slack=%q device=%q ldap=%q",
			cfg.SlackWebhookURL, cfg.DeviceCookieSecret, cfg.LDAPBindPassword)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("empty mirrored optionals must not fail Validate, got %v", err)
	}
}

// A blank DATABASE_PASSWORD_FILE is a misconfiguration: the DSN degrades to
// empty and Validate() refuses to start.
func TestL4DatabasePasswordFileBlankIsError(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("DATABASE_URL", "postgres://freecloud@db.internal:5432/freecloud?sslmode=require")
	pwFile := filepath.Join(t.TempDir(), "postgres_password")
	if err := os.WriteFile(pwFile, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_PASSWORD_FILE", pwFile)
	cfg := Load()
	if cfg.DatabaseURL != "" {
		t.Errorf("expected DATABASE_URL to degrade to empty, got %q", cfg.DatabaseURL)
	}
	if len(cfg.SecretErrors()) == 0 {
		t.Error("expected a recorded secret error, got none")
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected Validate() to reject a blank DATABASE_PASSWORD_FILE, got nil")
	}
}

// A blank dedicated device-cookie secret counts as unconfigured (derived-key
// fallback + startup warning), not as key material.
func TestL4BlankDeviceCookieSecretCountsAsUnconfigured(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("FLEET_WEBHOOK_SECRET", "fleet-secret")
	t.Setenv("DEVICE_COOKIE_SECRET", "  ")
	cfg := Load()
	if cfg.DeviceCookieSecretConfigured() {
		t.Error("expected DeviceCookieSecretConfigured() to be false for a blank secret")
	}
	if got := cfg.DeviceCookieSigningSecret(); got == "  " || got == "" {
		t.Errorf("expected a derived key from the fleet master, got %q", got)
	}
}

// A blank setup token counts as unset (fail closed: production then refuses
// first-admin provisioning until a real token is configured).
func TestL4BlankSetupTokenCountsAsUnset(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	cfg := &Config{SetupToken: "  "}
	if got := cfg.EffectiveSetupToken(); got != "" {
		t.Errorf("expected blank setup token to be inert, got %q", got)
	}
}

// Production requires an explicit strong sslmode on DATABASE_URL — require,
// verify-ca or verify-full — parsed from the DSN (URL form and key=value
// form, case-insensitive). Missing, weak or unknown values are rejected.
func TestL4ValidateProductionDatabaseSSLMode(t *testing.T) {
	cases := []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{"url require", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=require", false},
		{"url verify-ca", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=verify-ca", false},
		{"url verify-full", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=verify-full", false},
		{"url case-insensitive", "postgres://app:s3cret@db.internal:5432/freecloud?SSLMODE=Require", false},
		{"url missing", "postgres://app:s3cret@db.internal:5432/freecloud", true},
		{"url disable", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=disable", true},
		{"url allow", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=allow", true},
		{"url prefer", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=prefer", true},
		{"url unknown", "postgres://app:s3cret@db.internal:5432/freecloud?sslmode=sometimes", true},
		{"url weak among params", "postgres://app@db.internal:5432/freecloud?connect_timeout=5&sslmode=prefer", true},
		{"url sslmode-lookalike in password is not a param", "postgres://app:sslmode%3Ddisable@db.internal:5432/freecloud?sslmode=require", false},
		{"kv require", "host=db.internal user=app password=s3cret dbname=freecloud sslmode=require", false},
		{"kv verify-full", "host=db.internal user=app dbname=freecloud sslmode=verify-full", false},
		{"kv case-insensitive", "host=db.internal user=app dbname=freecloud SSLMODE=Verify-CA", false},
		{"kv quoted", "host=db.internal user=app dbname=freecloud sslmode='require'", false},
		{"kv missing", "host=db.internal user=app dbname=freecloud", true},
		{"kv disable", "host=db.internal user=app dbname=freecloud sslmode=disable", true},
		{"kv allow", "host=db.internal user=app dbname=freecloud sslmode=allow", true},
		{"kv prefer", "host=db.internal user=app dbname=freecloud sslmode=prefer", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setSecureProdEnv(t)
			t.Setenv("DATABASE_URL", c.dsn)
			cfg := Load()
			err := cfg.Validate()
			if c.wantErr && err == nil {
				t.Errorf("expected error for DSN %q, got nil", c.dsn)
			}
			if !c.wantErr && err != nil {
				t.Errorf("expected no error for DSN %q, got %v", c.dsn, err)
			}
		})
	}
}

// REDIS_PASSWORD / REDIS_PASSWORD_FILE contract: a non-blank value is
// injected (URL-escaped) into a password-less REDIS_URL at config load; a
// URL that already carries a password is left alone; a blank/absent value
// changes nothing and records no error.
func TestL4RedisPasswordInjection(t *testing.T) {
	writeFile := func(t *testing.T, content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "redis_password")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	redisPassword := func(t *testing.T, rawurl string) (string, bool) {
		t.Helper()
		u, err := url.Parse(rawurl)
		if err != nil {
			t.Fatalf("REDIS_URL does not parse: %v (%q)", err, rawurl)
		}
		return u.User.Password()
	}

	t.Run("plain env injected", func(t *testing.T) {
		setSecureProdEnv(t)
		t.Setenv("REDIS_URL", "redis://redis:6379/0")
		t.Setenv("REDIS_PASSWORD", "s3cret")
		cfg := Load()
		if len(cfg.SecretErrors()) != 0 {
			t.Fatalf("unexpected secret errors: %v", cfg.SecretErrors())
		}
		got, ok := redisPassword(t, cfg.RedisURL)
		if !ok || got != "s3cret" {
			t.Errorf("expected injected password, got %q (present=%v) in %q", got, ok, cfg.RedisURL)
		}
	})

	t.Run("file injected", func(t *testing.T) {
		setSecureProdEnv(t)
		t.Setenv("REDIS_URL", "redis://redis:6379/0")
		t.Setenv("REDIS_PASSWORD_FILE", writeFile(t, "file-s3cret\n"))
		cfg := Load()
		if len(cfg.SecretErrors()) != 0 {
			t.Fatalf("unexpected secret errors: %v", cfg.SecretErrors())
		}
		got, ok := redisPassword(t, cfg.RedisURL)
		if !ok || got != "file-s3cret" {
			t.Errorf("expected injected file password, got %q (present=%v) in %q", got, ok, cfg.RedisURL)
		}
	})

	t.Run("existing URL password wins", func(t *testing.T) {
		setSecureProdEnv(t)
		t.Setenv("REDIS_URL", "redis://:url-password@redis:6379/0")
		t.Setenv("REDIS_PASSWORD", "env-password")
		cfg := Load()
		got, ok := redisPassword(t, cfg.RedisURL)
		if !ok || got != "url-password" {
			t.Errorf("expected URL password to be kept, got %q (present=%v) in %q", got, ok, cfg.RedisURL)
		}
	})

	t.Run("blank absent changes nothing", func(t *testing.T) {
		setSecureProdEnv(t)
		const plain = "redis://redis:6379/0"
		t.Setenv("REDIS_URL", plain)
		t.Setenv("REDIS_PASSWORD", "  ")
		cfg := Load()
		if cfg.RedisURL != plain {
			t.Errorf("blank password must leave REDIS_URL unchanged, got %q", cfg.RedisURL)
		}
		if len(cfg.SecretErrors()) != 0 {
			t.Errorf("blank password must record no error, got %v", cfg.SecretErrors())
		}
	})

	t.Run("blank file changes nothing", func(t *testing.T) {
		setSecureProdEnv(t)
		const plain = "redis://redis:6379/0"
		t.Setenv("REDIS_URL", plain)
		t.Setenv("REDIS_PASSWORD_FILE", writeFile(t, "\n"))
		cfg := Load()
		if cfg.RedisURL != plain {
			t.Errorf("blank password file must leave REDIS_URL unchanged, got %q", cfg.RedisURL)
		}
		if len(cfg.SecretErrors()) != 0 {
			t.Errorf("blank password file must record no error, got %v", cfg.SecretErrors())
		}
	})

	t.Run("absent changes nothing", func(t *testing.T) {
		setSecureProdEnv(t)
		const plain = "redis://redis:6379/0"
		t.Setenv("REDIS_URL", plain)
		t.Setenv("REDIS_PASSWORD", "")
		t.Setenv("REDIS_PASSWORD_FILE", "")
		cfg := Load()
		if cfg.RedisURL != plain {
			t.Errorf("absent password must leave REDIS_URL unchanged, got %q", cfg.RedisURL)
		}
	})

	t.Run("special chars escaped", func(t *testing.T) {
		setSecureProdEnv(t)
		t.Setenv("REDIS_URL", "redis://redis:6379/0")
		const pw = `p@ss:w/rd?x#y%z`
		t.Setenv("REDIS_PASSWORD", pw)
		cfg := Load()
		got, ok := redisPassword(t, cfg.RedisURL)
		if !ok || got != pw {
			t.Errorf("expected escaped round-trip of %q, got %q (present=%v) in %q", pw, got, ok, cfg.RedisURL)
		}
	})

	t.Run("unparseable URL with password is a resolution error", func(t *testing.T) {
		setSecureProdEnv(t)
		t.Setenv("REDIS_URL", "redis://[::1")
		t.Setenv("REDIS_PASSWORD", "s3cret")
		cfg := Load()
		if len(cfg.SecretErrors()) == 0 {
			t.Error("expected a recorded secret error for an unparseable REDIS_URL with a password, got none")
		}
	})
}
