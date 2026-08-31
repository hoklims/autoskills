package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, raw string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(), "config.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProviderCompatibility(t *testing.T) {
	unsetEnv(t, "AUTOSKILLS_PROVIDER")
	t.Setenv("AUTOSKILLS_MODEL", "")
	t.Setenv("AUTOSKILLS_ENDPOINT", "")
	t.Setenv("AUTOSKILLS_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	for _, tc := range []struct {
		name  string
		raw   string
		want  string
		model string
	}{
		{name: "legacy", raw: `{"endpoint":"http://localhost:11434/v1","model":"local"}`, want: "http", model: "local"},
		{name: "explicit http", raw: `{"provider":"http","endpoint":"https://example.com/v1","api_key":"key","model":"m"}`, want: "http", model: "m"},
		{name: "codex", raw: `{"provider":"codex"}`, want: "codex", model: ""},
		{name: "claude", raw: `{"provider":"claude"}`, want: "claude", model: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeConfig(t, tc.raw)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Provider != tc.want {
				t.Fatalf("provider = %q, want %q", cfg.Provider, tc.want)
			}
			if cfg.Model != tc.model {
				t.Fatalf("model = %q, want %q", cfg.Model, tc.model)
			}
		})
	}
}

func TestLoadRejectsUnknownProvider(t *testing.T) {
	unsetEnv(t, "AUTOSKILLS_PROVIDER")
	writeConfig(t, `{"provider":"other"}`)
	if _, err := Load(); err == nil {
		t.Fatal("unknown provider must fail")
	}
}

func TestLoadRejectsExplicitlyInvalidProvider(t *testing.T) {
	unsetEnv(t, "AUTOSKILLS_PROVIDER")
	for _, raw := range []string{
		`{"provider":null}`,
		`{"provider":""}`,
		`{"provider":" "}`,
		`{"provider":42}`,
		`{"Provider":"other"}`,
		`{"PROVIDER":null}`,
		`{"Provider":""}`,
		`{"Provider":"http","PROVIDER":"other"}`,
		`{"provider":"other","provider":"http"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			writeConfig(t, raw)
			if _, err := Load(); err == nil {
				t.Fatalf("provider in %s must fail", raw)
			}
		})
	}
}

func TestLoadRejectsInvalidFileProviderBeforeEnvironmentOverride(t *testing.T) {
	t.Setenv("AUTOSKILLS_PROVIDER", "codex")
	for _, raw := range []string{`{"provider":null}`, `{"Provider":"other"}`} {
		t.Run(raw, func(t *testing.T) {
			writeConfig(t, raw)
			if _, err := Load(); err == nil {
				t.Fatal("invalid file provider must fail before environment override")
			}
		})
	}
}

func TestLoadAcceptsValidProviderEnvironmentOverride(t *testing.T) {
	t.Setenv("AUTOSKILLS_PROVIDER", "claude")
	writeConfig(t, `{"provider":"http"}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "claude" {
		t.Fatalf("provider = %q", cfg.Provider)
	}
}

func TestLoadRejectsInvalidProviderEnvironment(t *testing.T) {
	writeConfig(t, `{}`)
	for _, value := range []string{"", " ", "other"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("AUTOSKILLS_PROVIDER", value)
			if _, err := Load(); err == nil {
				t.Fatalf("provider environment %q must fail", value)
			}
		})
	}
}
