// Package httpx provides tiny HTTP helpers shared across outbound client
// packages (provisioning connectors, bootstrap). Kept dependency-free.
package httpx

import (
	"fmt"
	"io"
)

// MaxResponseBytes is the default cap for ReadAllBounded: 10 MiB, far beyond
// any legitimate API/connector payload while preventing a compromised or
// misconfigured upstream from exhausting backend memory.
const MaxResponseBytes = 10 << 20 // 10 MiB

// ReadAllBounded reads r up to limit bytes (default MaxResponseBytes when
// limit <= 0) and errors if the stream is larger. Use it everywhere a remote
// response body is buffered — io.ReadAll alone is unbounded.
func ReadAllBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = MaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response too large (> %d bytes)", limit)
	}
	return body, nil
}
