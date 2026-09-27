package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadBotDSN(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		env     string
		want    string
		wantErr bool
	}{
		{name: "file", config: `{"db_connection_string": "postgres://file"}`, want: "postgres://file"},
		{
			name:   "env overrides file",
			config: `{"db_connection_string": "<SEAL>"}`,
			env:    "postgres://env",
			want:   "postgres://env",
		},
		{
			name:   "unknown keys",
			config: `{"db_connection_string": "postgres://file", "renamed_away": 1}`,
			want:   "postgres://file",
		},
		{name: "missing", config: `{"website": "chaturbate"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bot-config.json")
			if err := os.WriteFile(path, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			// Empty clears the developer's own export, which viper would otherwise read.
			t.Setenv("XRN_DB_CONNECTION_STRING", tt.env)
			got, err := readBotDSN(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("dsn = %q, want %q", got, tt.want)
			}
		})
	}
}
