package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSecretFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret")
	if err := os.WriteFile(p, []byte("file-secret-value\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MY_SECRET", "env-value")
	t.Setenv("MY_SECRET_FILE", p)

	got, err := resolveSecret("MY_SECRET", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file-secret-value" {
		t.Errorf("expected 'file-secret-value', got %q", got)
	}
}

func TestResolveSecretFromEnv(t *testing.T) {
	t.Setenv("MY_SECRET", "env-value")
	// Ensure no _FILE override.
	t.Setenv("MY_SECRET_FILE", "")

	got, err := resolveSecret("MY_SECRET", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "env-value" {
		t.Errorf("expected 'env-value', got %q", got)
	}
}

func TestResolveSecretFallsBackToDefault(t *testing.T) {
	t.Setenv("MY_SECRET", "")
	t.Setenv("MY_SECRET_FILE", "")

	got, err := resolveSecret("MY_SECRET", "fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fallback" {
		t.Errorf("expected 'fallback', got %q", got)
	}
}

func TestResolveSecretFilePrecedenceOverEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret")
	if err := os.WriteFile(p, []byte("from-file"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MY_SECRET", "from-env")
	t.Setenv("MY_SECRET_FILE", p)

	got, err := resolveSecret("MY_SECRET", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-file" {
		t.Errorf("_FILE must take precedence over plain env: got %q", got)
	}
}

// A configured-but-unreadable _FILE is a hard error: the value must NOT
// silently fall back to the plain env var, which may hold a stale secret.
func TestResolveSecretMissingFileIsAnError(t *testing.T) {
	t.Setenv("MY_SECRET_FILE", "/nonexistent/path/secret")
	t.Setenv("MY_SECRET", "env-fallback")

	got, err := resolveSecret("MY_SECRET", "")
	if err == nil {
		t.Fatal("expected an error for an unreadable _FILE path")
	}
	if got != "" {
		t.Errorf("missing _FILE should return an empty string, got %q", got)
	}
}

// A configured Vault path that cannot be read is a hard error: the plain env
// var must NOT be used as a fallback (issue #126).
func TestResolveSecretVaultFailureDoesNotFallBackToEnv(t *testing.T) {
	t.Setenv("MY_SECRET_VAULT_PATH", "secret/data/my-secret")
	t.Setenv("MY_SECRET", "env-fallback")
	// VAULT_ADDR/VAULT_TOKEN are unset, so the Vault read must fail.
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")

	got, err := resolveSecret("MY_SECRET", "")
	if err == nil {
		t.Fatal("expected an error when the configured Vault path cannot be read")
	}
	if got != "" {
		t.Errorf("a failed Vault read must not fall back to the env var, got %q", got)
	}
}

// A configured Vault path that fails to read is recorded by the resolver so
// Validate() can refuse to start the process.
func TestLoadRecordsVaultFailure(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("MY_SECRET_VAULT_PATH", "secret/data/my-secret")
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")

	r := &secretResolver{}
	if v := r.get("MY_SECRET", ""); v != "" {
		t.Errorf("expected empty value, got %q", v)
	}
	if len(r.errs) != 1 {
		t.Fatalf("expected 1 recorded error, got %d: %v", len(r.errs), r.errs)
	}
}

// ProductionValidateAcceptsEmptyKeycloakSecret verifies that Validate() does
// NOT reject a missing KEYCLOAK_CLIENT_SECRET: the bootstrap engine sets it
// at runtime from Keycloak, so the operator is not required to provide it.
func TestProductionValidateAcceptsEmptyKeycloakSecret(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("KEYCLOAK_CLIENT_SECRET", "")
	t.Setenv("KEYCLOAK_CLIENT_SECRET_FILE", "")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("KEYCLOAK_CLIENT_SECRET is self-managed by bootstrap; Validate() must accept an empty value, got: %v", err)
	}
}
