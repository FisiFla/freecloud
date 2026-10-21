package handlers

import (
	"bytes"
	"encoding/base64"
	"testing"
)

// withProvisioningMasterKey injects a key for the duration of the test and
// restores whatever was there before. An empty string means "not injected",
// which makes the lookup fall back to the environment variable.
func withProvisioningMasterKey(t *testing.T, keyB64 string) {
	t.Helper()
	previous, _ := provisioningMasterKeyB64.Load().(string)
	SetProvisioningMasterKey(keyB64)
	t.Cleanup(func() {
		SetProvisioningMasterKey(previous)
	})
}

// TestProvisioningMasterKeyUsesInjectedValue is the regression test for the
// issue where the handler read os.Getenv("PROVISIONING_MASTER_KEY") directly
// instead of the value resolved by config (which supports _FILE and
// _VAULT_PATH). A deployment that delivered the key as a mounted file passed
// config.Validate() and then failed every encrypt/decrypt at runtime.
func TestProvisioningMasterKeyUsesInjectedValue(t *testing.T) {
	// The key exists only as a mounted file in the scenario this fixes, so the
	// plain environment variable is deliberately empty.
	t.Setenv("PROVISIONING_MASTER_KEY", "")
	t.Setenv("APP_ENV", "production")

	raw := bytes.Repeat([]byte{0xA5}, 32)
	injected := base64.StdEncoding.EncodeToString(raw)
	withProvisioningMasterKey(t, injected)

	key, err := provisioningMasterKey()
	if err != nil {
		t.Fatalf("provisioningMasterKey() error = %v, want nil", err)
	}
	if key == nil {
		t.Fatal("provisioningMasterKey() returned nil key, want the injected key")
	}
	if !bytes.Equal(key, raw) {
		t.Fatalf("provisioningMasterKey() = %x, want the injected key %x", key, raw)
	}
}

// TestProvisioningMasterKeyInjectedWinsOverEnvironment pins the precedence:
// the injected (resolved) key is authoritative, the plain env var is only a
// fallback for dev/test and unwired callers.
func TestProvisioningMasterKeyInjectedWinsOverEnvironment(t *testing.T) {
	envRaw := bytes.Repeat([]byte{0x11}, 32)
	t.Setenv("PROVISIONING_MASTER_KEY", base64.StdEncoding.EncodeToString(envRaw))
	t.Setenv("APP_ENV", "production")

	injectedRaw := bytes.Repeat([]byte{0x22}, 32)
	withProvisioningMasterKey(t, base64.StdEncoding.EncodeToString(injectedRaw))

	key, err := provisioningMasterKey()
	if err != nil {
		t.Fatalf("provisioningMasterKey() error = %v, want nil", err)
	}
	if !bytes.Equal(key, injectedRaw) {
		t.Fatalf("provisioningMasterKey() = %x, want the injected key %x", key, injectedRaw)
	}
}

// TestProvisioningMasterKeyFallsBackToEnvironment keeps development, tests and
// any caller that never wires the setter working.
func TestProvisioningMasterKeyFallsBackToEnvironment(t *testing.T) {
	envRaw := bytes.Repeat([]byte{0x33}, 32)
	t.Setenv("PROVISIONING_MASTER_KEY", base64.StdEncoding.EncodeToString(envRaw))
	t.Setenv("APP_ENV", "production")

	withProvisioningMasterKey(t, "")

	key, err := provisioningMasterKey()
	if err != nil {
		t.Fatalf("provisioningMasterKey() error = %v, want nil", err)
	}
	if !bytes.Equal(key, envRaw) {
		t.Fatalf("provisioningMasterKey() = %x, want the environment key %x", key, envRaw)
	}
}

// TestProvisioningMasterKeyFailsClosedOutsideDevWithNoKey proves that a
// production deployment with neither an injected nor an environment key still
// refuses to encrypt rather than silently falling back to plaintext.
func TestProvisioningMasterKeyFailsClosedOutsideDevWithNoKey(t *testing.T) {
	t.Setenv("PROVISIONING_MASTER_KEY", "")
	t.Setenv("APP_ENV", "production")

	withProvisioningMasterKey(t, "")

	key, err := provisioningMasterKey()
	if err == nil {
		t.Fatalf("provisioningMasterKey() error = nil, want an error (key=%v)", key)
	}
	if key != nil {
		t.Fatalf("provisioningMasterKey() key = %x, want nil", key)
	}
}

// TestProvisioningTokenRoundTripWithInjectedKey exercises the actual
// encrypt/decrypt path with the injected key.
func TestProvisioningTokenRoundTripWithInjectedKey(t *testing.T) {
	t.Setenv("PROVISIONING_MASTER_KEY", "")
	t.Setenv("APP_ENV", "production")

	raw := bytes.Repeat([]byte{0x5A}, 32)
	withProvisioningMasterKey(t, base64.StdEncoding.EncodeToString(raw))

	enc, err := encryptProvisioningToken("scim-token")
	if err != nil {
		t.Fatalf("encryptProvisioningToken() error = %v", err)
	}
	dec, err := decryptProvisioningToken(enc)
	if err != nil {
		t.Fatalf("decryptProvisioningToken() error = %v", err)
	}
	if dec != "scim-token" {
		t.Fatalf("decryptProvisioningToken() = %q, want %q", dec, "scim-token")
	}
}

// TestProvisioningMasterKeyRejectsMalformedInjectedKey keeps the validation
// applied to the resolved key.
func TestProvisioningMasterKeyRejectsMalformedInjectedKey(t *testing.T) {
	t.Setenv("PROVISIONING_MASTER_KEY", "")
	t.Setenv("APP_ENV", "production")

	withProvisioningMasterKey(t, "not-base64!!")

	if _, err := provisioningMasterKey(); err == nil {
		t.Fatal("provisioningMasterKey() error = nil, want an error for a malformed key")
	}
}
