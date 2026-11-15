// Failing-first tests for the provisioning secret-envelope hardening
// (security lane L2, issue 9): versioned ciphertexts with per-purpose AAD,
// legacy decryption, and previous-key rotation with opportunistic
// re-encryption signalling.
package handlers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"strings"
	"testing"
)

func testMasterKeyB64(t *testing.T, seed byte) string {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// setMasterKeys installs current/previous keys for the test and restores the
// unset state afterwards. It also clears the env fallbacks so the test is
// hermetic.
func setMasterKeys(t *testing.T, current, previous string) {
	t.Helper()
	t.Setenv("PROVISIONING_MASTER_KEY", "")
	t.Setenv("PROVISIONING_MASTER_KEY_PREVIOUS", "")
	t.Setenv("PROVISIONING_MASTER_KEY_PREVIOUS_FILE", "")
	SetProvisioningMasterKey(current)
	SetProvisioningMasterKeyPrevious(previous)
	t.Cleanup(func() {
		SetProvisioningMasterKey("")
		SetProvisioningMasterKeyPrevious("")
	})
}

// legacySeal encrypts exactly like the pre-rotation code did: AES-256-GCM
// with nil AAD, base64(nonce||ciphertext), no envelope.
func legacySeal(t *testing.T, keyB64, plaintext string) string {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("new GCM: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))
}

func TestEncryptSecret_RoundTripsWithPurpose(t *testing.T) {
	setMasterKeys(t, testMasterKeyB64(t, 1), "")
	for _, purpose := range []string{SecretPurposeConnectorToken, SecretPurposeFleetToken, SecretPurposeSMTPPassword} {
		ct, err := encryptSecret(purpose, "s3cret-value")
		if err != nil {
			t.Fatalf("encryptSecret(%s): %v", purpose, err)
		}
		if !strings.HasPrefix(ct, "v1:") {
			t.Fatalf("encryptSecret(%s) = %q, want a v1 envelope", purpose, ct)
		}
		pt, rotated, err := decryptSecret(purpose, ct)
		if err != nil {
			t.Fatalf("decryptSecret(%s): %v", purpose, err)
		}
		if rotated {
			t.Fatalf("decryptSecret(%s): rotated=true for a current-key ciphertext", purpose)
		}
		if pt != "s3cret-value" {
			t.Fatalf("decryptSecret(%s) = %q", purpose, pt)
		}
	}
}

func TestDecryptSecret_WrongPurposeFails(t *testing.T) {
	setMasterKeys(t, testMasterKeyB64(t, 2), "")
	ct, err := encryptSecret(SecretPurposeSMTPPassword, "pw")
	if err != nil {
		t.Fatalf("encryptSecret: %v", err)
	}
	if _, _, err := decryptSecret(SecretPurposeFleetToken, ct); err == nil {
		t.Fatal("decrypting with the wrong purpose must fail (AAD binding)")
	}
}

func TestDecryptSecret_LegacyFormatStillDecrypts(t *testing.T) {
	key := testMasterKeyB64(t, 3)
	setMasterKeys(t, key, "")
	legacy := legacySeal(t, key, "old-secret")

	pt, rotated, err := decryptSecret(SecretPurposeConnectorToken, legacy)
	if err != nil {
		t.Fatalf("legacy ciphertext must still decrypt: %v", err)
	}
	if pt != "old-secret" || rotated {
		t.Fatalf("legacy decrypt = %q rotated=%v", pt, rotated)
	}
	// The legacy wrapper decrypts both formats.
	if _, err := decryptProvisioningToken(legacy); err != nil {
		t.Fatalf("decryptProvisioningToken(legacy): %v", err)
	}
	fresh, err := encryptProvisioningToken("new-secret")
	if err != nil {
		t.Fatalf("encryptProvisioningToken: %v", err)
	}
	if pt, err := decryptProvisioningToken(fresh); err != nil || pt != "new-secret" {
		t.Fatalf("wrapper roundtrip = %q err=%v", pt, err)
	}
}

func TestDecryptSecret_PreviousKeyRotation(t *testing.T) {
	current := testMasterKeyB64(t, 4)
	previous := testMasterKeyB64(t, 5)
	// Ciphertext sealed under the previous key (as stored before rotation).
	old := legacySeal(t, previous, "rotated-secret")

	setMasterKeys(t, current, previous)
	pt, rotated, err := decryptSecret(SecretPurposeFleetToken, old)
	if err != nil {
		t.Fatalf("decrypt with previous key: %v", err)
	}
	if pt != "rotated-secret" {
		t.Fatalf("plaintext = %q", pt)
	}
	if !rotated {
		t.Fatal("decrypt-with-previous must report rotated=true for opportunistic re-encryption")
	}

	// Without the previous key the same ciphertext no longer decrypts.
	setMasterKeys(t, current, "")
	if _, _, err := decryptSecret(SecretPurposeFleetToken, old); err == nil {
		t.Fatal("expected failure without the previous key")
	}
}

func TestProvisioningMasterKeyPrevious_FileForm(t *testing.T) {
	current := testMasterKeyB64(t, 6)
	previous := testMasterKeyB64(t, 7)
	old := legacySeal(t, previous, "file-secret")

	path := t.TempDir() + "/prev.key"
	if err := os.WriteFile(path, []byte(previous+"\n"), 0600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	t.Setenv("PROVISIONING_MASTER_KEY", "")
	t.Setenv("PROVISIONING_MASTER_KEY_PREVIOUS", "")
	t.Setenv("PROVISIONING_MASTER_KEY_PREVIOUS_FILE", path)
	SetProvisioningMasterKey(current)
	SetProvisioningMasterKeyPrevious("")
	t.Cleanup(func() {
		SetProvisioningMasterKey("")
		SetProvisioningMasterKeyPrevious("")
	})

	pt, rotated, err := decryptSecret(SecretPurposeSMTPPassword, old)
	if err != nil {
		t.Fatalf("decrypt via _FILE previous key: %v", err)
	}
	if pt != "file-secret" || !rotated {
		t.Fatalf("plaintext = %q rotated=%v", pt, rotated)
	}
}
