package main

import (
	"testing"
)

// Security-fix lane L4: CREATE_DEMO_USER=true must be honoured only in
// dev/e2e, exactly like E2E_SEED_ADMIN — never in production.
func TestL4ShouldCreateDemoUser(t *testing.T) {
	cases := []struct {
		name   string
		appEnv string
		envVal string
		want   bool
	}{
		{"production true ignored", "production", "true", false},
		{"production empty ignored", "production", "", false},
		{"unset env true ignored", "", "true", false},
		{"development true honoured", "development", "true", true},
		{"development empty off", "development", "", false},
		{"test true honoured", "test", "true", true},
		// APP_ENV=e2e is NOT covered by IsDevOrE2E (same as E2E_SEED_ADMIN).
		{"e2e true ignored", "e2e", "true", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("APP_ENV", c.appEnv)
			t.Setenv("CREATE_DEMO_USER", c.envVal)
			if got := shouldCreateDemoUser(); got != c.want {
				t.Errorf("APP_ENV=%q CREATE_DEMO_USER=%q: got %v, want %v", c.appEnv, c.envVal, got, c.want)
			}
		})
	}
}
