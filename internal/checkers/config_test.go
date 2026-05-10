package checkers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bcmk/siren/v2/lib/cmdlib"
)

// TestReadCheckerConfigEnvOverride checks the XRN_-prefixed env wiring in
// readCheckerConfig: a value absent from the file, an override of a file
// value, and an override of a field on the squashed BaseCheckerConfig.
func TestReadCheckerConfigEnvOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stripchat-checker.json")
	const body = `{
	"timeout_seconds": 10,
	"min_request_interval_ms": 100,
	"users_online_endpoint": "https://example.test/online"
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name            string
		extraEnv        map[string]string
		wantUserID      cmdlib.Secret
		wantOnlineURL   string
		wantTimeoutSecs int
	}{
		{
			name:            "env supplies a key absent from the file",
			wantUserID:      "secret-from-env",
			wantOnlineURL:   "https://example.test/online",
			wantTimeoutSecs: 10,
		},
		{
			name:            "env overrides a file value",
			extraEnv:        map[string]string{"XRN_USERS_ONLINE_ENDPOINT": "https://override.test/online"},
			wantUserID:      "secret-from-env",
			wantOnlineURL:   "https://override.test/online",
			wantTimeoutSecs: 10,
		},
		{
			name:            "env overrides a squashed base field",
			extraEnv:        map[string]string{"XRN_TIMEOUT_SECONDS": "99"},
			wantUserID:      "secret-from-env",
			wantOnlineURL:   "https://example.test/online",
			wantTimeoutSecs: 99,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XRN_USER_ID", "secret-from-env")
			for k, v := range tt.extraEnv {
				t.Setenv(k, v)
			}
			cfg := &StripchatCheckerConfig{}
			if err := readCheckerConfig(cfg, "stripchat", path); err != nil {
				t.Fatalf("readCheckerConfig: %v", err)
			}
			if cfg.UserID != tt.wantUserID {
				t.Errorf("UserID = %q, want %q", cfg.UserID, tt.wantUserID)
			}
			if cfg.UsersOnlineEndpoint != tt.wantOnlineURL {
				t.Errorf("UsersOnlineEndpoint = %q, want %q", cfg.UsersOnlineEndpoint, tt.wantOnlineURL)
			}
			if cfg.TimeoutSeconds != tt.wantTimeoutSecs {
				t.Errorf("TimeoutSeconds = %d, want %d", cfg.TimeoutSeconds, tt.wantTimeoutSecs)
			}
		})
	}
}
