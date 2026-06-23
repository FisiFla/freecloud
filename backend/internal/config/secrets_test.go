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

	got := resolveSecret("MY_SECRET", "default")
	if got != "file-secret-value" {
		t.Errorf("expected 'file-secret-value', got %q", got)
	}
}

func TestResolveSecretFromEnv(t *testing.T) {
	t.Setenv("MY_SECRET", "env-value")
	// Ensure no _FILE override.
	t.Setenv("MY_SECRET_FILE", "")

	got := resolveSecret("MY_SECRET", "default")
	if got != "env-value" {
		t.Errorf("expected 'env-value', got %q", got)
	}
}

func TestResolveSecretFallsBackToDefault(t *testing.T) {
	t.Setenv("MY_SECRET", "")
	t.Setenv("MY_SECRET_FILE", "")

	got := resolveSecret("MY_SECRET", "fallback")
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

	got := resolveSecret("MY_SECRET", "")
	if got != "from-file" {
		t.Errorf("_FILE must take precedence over plain env: got %q", got)
	}
}

func TestResolveSecretMissingFileReturnsEmpty(t *testing.T) {
	t.Setenv("MY_SECRET_FILE", "/nonexistent/path/secret")
	t.Setenv("MY_SECRET", "env-fallback")

	// A missing file returns "" (Validate() then catches it in production).
	got := resolveSecret("MY_SECRET", "")
	if got != "" {
		t.Errorf("missing _FILE should return empty string, got %q", got)
	}
}

// TestProductionValidateAcceptsEmptyKeycloakSecret verifies that Validate() does
// NOT reject a missing KEYCLOAK_CLIENT_SECRET: the bootstrap engine sets it at
// runtime from Keycloak, so the operator is not required to provide it.
func TestProductionValidateAcceptsEmptyKeycloakSecret(t *testing.T) {
	setSecureProdEnv(t)
	t.Setenv("KEYCLOAK_CLIENT_SECRET", "")
	t.Setenv("KEYCLOAK_CLIENT_SECRET_FILE", "")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Errorf("KEYCLOAK_CLIENT_SECRET is self-managed by bootstrap; Validate() must accept empty, got: %v", err)
	}
}
