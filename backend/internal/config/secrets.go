package config

// resolveSecret implements the three-tier secret resolution:
//
//  1. If <key>_FILE is set, read and return the file contents (Docker / K8s
//     secrets volume convention).
//  2. If <key>_VAULT_PATH is set together with VAULT_ADDR and VAULT_TOKEN,
//     fetch the KV secret from HashiCorp Vault (KV v2, field "value").
//  3. Fall back to the plain environment variable <key>.
//
// Precedence: _FILE > _VAULT_PATH > plain env var.
//
// A configured-but-unreadable source is a HARD FAILURE: the plain env var is
// NOT used as a fallback. A Vault outage or a revoked token must not silently
// downgrade the secret source (and it must not leave the secret empty
// either) — the caller records the error and Validate() refuses to start.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

var vaultHTTPClient = &http.Client{Timeout: 5 * time.Second}

// resolveSecret returns the secret value for the given environment variable key.
// It follows the _FILE → _VAULT_PATH → plain-env precedence documented above.
//
// The returned error is non-nil when a CONFIGURED source could not be read;
// the returned value is then empty (never a value from a different source).
func resolveSecret(key, fallback string) (string, error) {
	// 1. _FILE
	if filePath := os.Getenv(key + "_FILE"); filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			// Fail closed: a misconfigured _FILE path must not silently fall
			// back to the plain env var (which may hold a stale value).
			return "", fmt.Errorf("reading %s_FILE (%s): %w", key, filePath, err)
		}
		return strings.TrimRight(string(data), "\n\r"), nil
	}

	// 2. _VAULT_PATH
	if vaultPath := os.Getenv(key + "_VAULT_PATH"); vaultPath != "" {
		val, err := vaultRead(vaultPath)
		if err != nil {
			// Fail closed: a Vault read failure must NOT fall through to the
			// plain env var — that is a silent downgrade of the secret source.
			return "", fmt.Errorf("reading %s from vault path %q: %w", key, vaultPath, err)
		}
		return val, nil
	}

	// 3. Plain env var.
	return getEnv(key, fallback), nil
}

// secretResolver resolves secrets and records the failures so Validate() can
// refuse to start the process. It is the fail-closed counterpart of the old
// "fall through to the env var" behaviour.
type secretResolver struct {
	errs []string
}

// get resolves one secret, recording (and loudly logging) any failure.
func (r *secretResolver) get(key, fallback string) string {
	value, err := resolveSecret(key, fallback)
	if err != nil {
		r.errs = append(r.errs, err.Error())
		zap.L().Error("config: secret could not be resolved from its configured source",
			zap.String("key", key), zap.Error(err))
		return ""
	}
	return value
}

// vaultRead fetches the "value" field from a HashiCorp Vault KV v2 secret at
// path. Requires VAULT_ADDR and VAULT_TOKEN in the environment.
func vaultRead(path string) (string, error) {
	addr := os.Getenv("VAULT_ADDR")
	token := os.Getenv("VAULT_TOKEN")
	if addr == "" || token == "" {
		return "", fmt.Errorf("VAULT_ADDR or VAULT_TOKEN not set")
	}

	// KV v2 API: GET <addr>/v1/<path>
	url := strings.TrimRight(addr, "/") + "/v1/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := vaultHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vault returned %d for path %q", resp.StatusCode, path)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read vault body: %w", err)
	}

	// KV v2 response: {"data":{"data":{"value":"..."},...},...}
	// KV v1 response: {"data":{"value":"..."},...}
	// Try both.
	var kv2 struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &kv2); err == nil {
		if v, ok := kv2.Data.Data["value"]; ok {
			return v, nil
		}
	}

	var kv1 struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &kv1); err == nil {
		if v, ok := kv1.Data["value"]; ok {
			return v, nil
		}
	}

	return "", fmt.Errorf("vault secret at %q has no 'value' field", path)
}
