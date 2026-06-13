package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	const (
		validListen   = "127.0.0.1:3000"
		validDBPath   = "/var/lib/skra/skra.db"
		validExternal = "https://contacts.example.com"
		validKey      = "0123456789abcdef0123456789abcdef" // 32 chars
	)
	base := func() map[string]string {
		return map[string]string{
			envListen:       validListen,
			envDBPath:       validDBPath,
			envCookieSecure: "true",
			envExternalURL:  validExternal,
			envSessionKey:   validKey,
		}
	}
	without := func(key string) map[string]string {
		m := base()
		delete(m, key)
		return m
	}
	with := func(key, val string) map[string]string {
		m := base()
		m[key] = val
		return m
	}

	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		wantProblem []string
		want        Config
	}{
		{
			name: "all present",
			env:  base(),
			want: Config{Listen: validListen, DBPath: validDBPath, CookieSecure: true, ExternalURL: validExternal, SessionKey: validKey},
		},
		{name: "missing listen", env: without(envListen), wantErr: true, wantProblem: []string{envListen}},
		{name: "missing db path", env: without(envDBPath), wantErr: true, wantProblem: []string{envDBPath}},
		{name: "missing cookie secure", env: without(envCookieSecure), wantErr: true, wantProblem: []string{envCookieSecure}},
		{name: "malformed cookie secure", env: with(envCookieSecure, "yes"), wantErr: true, wantProblem: []string{envCookieSecure}},
		{name: "missing external url", env: without(envExternalURL), wantErr: true, wantProblem: []string{envExternalURL}},
		{name: "external url without scheme", env: with(envExternalURL, "contacts.example.com"), wantErr: true, wantProblem: []string{envExternalURL}},
		{name: "missing session key", env: without(envSessionKey), wantErr: true, wantProblem: []string{envSessionKey}},
		{name: "short session key", env: with(envSessionKey, "tooshort"), wantErr: true, wantProblem: []string{envSessionKey}},
		{
			name: "external url trailing slash trimmed",
			env:  with(envExternalURL, validExternal+"/"),
			want: Config{Listen: validListen, DBPath: validDBPath, CookieSecure: true, ExternalURL: validExternal, SessionKey: validKey},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{envListen, envDBPath, envCookieSecure, envExternalURL, envSessionKey} {
				t.Setenv(k, "")
			}
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
