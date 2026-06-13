package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	const validListen = "127.0.0.1:3000"
	const validDBPath = "/var/lib/skra/skra.db"

	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		wantProblem []string
		want        Config
	}{
		{
			name: "all present, secure true",
			env:  map[string]string{envListen: validListen, envDBPath: validDBPath, envCookieSecure: "true"},
			want: Config{Listen: validListen, DBPath: validDBPath, CookieSecure: true},
		},
		{
			name: "secure false",
			env:  map[string]string{envListen: validListen, envDBPath: validDBPath, envCookieSecure: "false"},
			want: Config{Listen: validListen, DBPath: validDBPath, CookieSecure: false},
		},
		{
			name:        "missing listen",
			env:         map[string]string{envDBPath: validDBPath, envCookieSecure: "true"},
			wantErr:     true,
			wantProblem: []string{envListen},
		},
		{
			name:        "missing cookie secure",
			env:         map[string]string{envListen: validListen, envDBPath: validDBPath},
			wantErr:     true,
			wantProblem: []string{envCookieSecure},
		},
		{
			name:        "malformed cookie secure",
			env:         map[string]string{envListen: validListen, envDBPath: validDBPath, envCookieSecure: "yes"},
			wantErr:     true,
			wantProblem: []string{envCookieSecure},
		},
		{
			name:        "all missing reports all",
			env:         map[string]string{},
			wantErr:     true,
			wantProblem: []string{envListen, envDBPath, envCookieSecure},
		},
		{
			name: "values are trimmed",
			env:  map[string]string{envListen: "  " + validListen + "  ", envDBPath: " " + validDBPath + " ", envCookieSecure: "true"},
			want: Config{Listen: validListen, DBPath: validDBPath, CookieSecure: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envListen, "")
			t.Setenv(envDBPath, "")
			t.Setenv(envCookieSecure, "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			got, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load() expected error, got nil")
				}
				for _, key := range tc.wantProblem {
					if !strings.Contains(err.Error(), key) {
						t.Errorf("error %q does not mention %q", err, key)
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
