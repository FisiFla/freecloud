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
// An empty _FILE path falls through to Vault / plain env; a non-empty path
// whose file cannot be read is a fatal error so the server fails closed.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var vaultHTTPClient = &http.Client{Timeout: 5 * time.Second}

// resolveSecret returns the secret value for the given environment variable key.
// It follows the _FILE → _VAULT_PATH → plain-env precedence documented above.
func resolveSecret(key, fallback string) string {
	// 1. _FILE
	if filePath := os.Getenv(key + "_FILE"); filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			// Fail loudly so misconfigured _FILE paths are visible at startup.
			// We return the empty string here; Validate() will catch it.
			return ""
		}
		return strings.TrimRight(string(data), "\n\r")
	}

	// 2. _VAULT_PATH
	if vaultPath := os.Getenv(key + "_VAULT_PATH"); vaultPath != "" {
		if val, err := vaultRead(vaultPath); err == nil {
			return val
		}
		// If Vault read fails, fall through to plain env — Validate() will
		// reject an empty/placeholder value in production.
	}

	// 3. Plain env var.
	return getEnv(key, fallback)
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
