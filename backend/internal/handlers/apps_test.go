package handlers

import "testing"

// TestValidateRedirectURI locks down the exact attack cases the redirect-URI
// validator is meant to block, so it cannot regress.
func TestValidateRedirectURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		// Allowed.
		{"https production", "https://example.com/callback", false},
		{"https with port", "https://app.example.com:8443/cb", false},
		{"localhost http", "http://localhost:3000/callback", false},
		{"loopback http", "http://127.0.0.1:3000/callback", false},
		{"localhost no port", "http://localhost/callback", false},

		// Deceptive localhost variants (must FAIL).
		{"localhost subdomain trick", "http://localhost.evil.com/callback", true},
		{"localhost prefix trick", "http://localhost-evil.com/callback", true},
		{"evil using localhost path", "http://evil.com/localhost", true},

		// Bad ports.
		{"non-numeric port", "http://localhost:bad/callback", true},
		{"negative port", "http://localhost:-1/callback", true},

		// Relative / schemeless (must FAIL — used for open-redirect abuse).
		{"relative path", "/relative/callback", true},
		{"protocol-relative", "//evil.com/callback", true},
		{"bare host no scheme", "example.com/callback", true},

		// Wrong schemes.
		{"ftp scheme", "ftp://example.com/callback", true},
		{"javascript scheme", "javascript:alert(1)", true},
		{"file scheme", "file:///etc/passwd", true},

		// Empty / malformed.
		{"empty string", "", true},
		{"only scheme", "https://", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRedirectURI(tt.uri)
			if tt.wantErr && err == nil {
				t.Errorf("validateRedirectURI(%q): expected error, got nil", tt.uri)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateRedirectURI(%q): expected no error, got %v", tt.uri, err)
			}
		})
	}
}
