package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		wantMissing []string
		want        Config
	}{
		{
			name: "all present",
			env:  map[string]string{envListen: "127.0.0.1:3000", envDBPath: "/var/lib/skra/skra.db"},
			want: Config{Listen: "127.0.0.1:3000", DBPath: "/var/lib/skra/skra.db"},
		},
		{
			name:        "missing listen",
			env:         map[string]string{envDBPath: "/var/lib/skra/skra.db"},
			wantErr:     true,
			wantMissing: []string{envListen},
		},
		{
			name:        "missing db path",
			env:         map[string]string{envListen: "127.0.0.1:3000"},
			wantErr:     true,
			wantMissing: []string{envDBPath},
		},
		{
			name:        "blank values treated as missing",
			env:         map[string]string{envListen: "   ", envDBPath: ""},
			wantErr:     true,
			wantMissing: []string{envListen, envDBPath},
		},
		{
			name:        "all missing",
			env:         map[string]string{},
			wantErr:     true,
			wantMissing: []string{envListen, envDBPath},
		},
		{
			name: "values are trimmed",
			env:  map[string]string{envListen: "  127.0.0.1:3000  ", envDBPath: " ./skra.db "},
			want: Config{Listen: "127.0.0.1:3000", DBPath: "./skra.db"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envListen, "")
			t.Setenv(envDBPath, "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			got, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load() expected error, got nil")
				}
				for _, key := range tc.wantMissing {
					if !strings.Contains(err.Error(), key) {
						t.Errorf("error %q does not mention missing key %q", err, key)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Load() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
