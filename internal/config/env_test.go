package config

import (
	"strings"
	"testing"
)

// The LLM_PROXY_ variables of release 0.1.0 are no longer read (ADR 0054).
// Each one is ignored, whether or not its SPYGLASS_ name is set.
func TestLegacyEnvOverridesAreNotHonoured(t *testing.T) {
	env := map[string]string{
		"DB": "postgres://x", "KEY": "k",
		"LLM_PROXY_LISTEN_DATA":          ":9999",
		"LLM_PROXY_SERVER_DRAIN_TIMEOUT": "45s",
		"LLM_PROXY_LOG_LEVEL":            "warn",
		"LLM_PROXY_DATABASE_URL_FILE":    "/run/secrets/db",
	}
	want, err := Parse([]byte(base), fakeOpts(map[string]string{"DB": "postgres://x", "KEY": "k"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse([]byte(base), fakeOpts(env, map[string]string{"/run/secrets/db": "postgres://f\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen.Data != want.Listen.Data || cfg.Server.DrainTimeout != want.Server.DrainTimeout ||
		cfg.Log.Level != want.Log.Level || cfg.Database.URL.File != "" ||
		cfg.Database.URL.Secret().Reveal() != "postgres://x" {
		t.Errorf("a LLM_PROXY_ variable was applied: %+v", cfg)
	}
}

// A malformed LLM_PROXY_ value is not even parsed, so it cannot fail the load.
func TestLegacyEnvIsNotValidated(t *testing.T) {
	env := map[string]string{"DB": "x", "KEY": "k", "LLM_PROXY_QUEUE_CAPACITY": "lots"}
	if _, err := Parse([]byte(base), fakeOpts(env, nil)); err != nil {
		t.Errorf("err = %v, want the legacy variable ignored", err)
	}
}

// The SPYGLASS_ names still override the file.
func TestSpyglassEnvOverridesWin(t *testing.T) {
	env := map[string]string{
		"DB": "postgres://x", "KEY": "k", "OTHER": "other",
		"SPYGLASS_LOG_LEVEL":          "warn",
		"LLM_PROXY_LOG_LEVEL":         "debug",
		"SPYGLASS_DATABASE_URL_ENV":   "OTHER",
		"LLM_PROXY_DATABASE_URL_FILE": "/run/secrets/db",
	}
	cfg, err := Parse([]byte(base), fakeOpts(env, nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("log.level = %q, want the SPYGLASS_ value", cfg.Log.Level)
	}
	if r := cfg.Database.URL; r.Env != "OTHER" || r.File != "" || r.Secret().Reveal() != "other" {
		t.Errorf("database.url = %s, want the SPYGLASS_ part only", r)
	}
}

// A secret reference to a SPYGLASS_ variable no longer falls back to its
// LLM_PROXY_ name: the variable is reported as not set.
func TestSecretRefDoesNotFallBackToLegacyName(t *testing.T) {
	text := strings.Replace(base, "{ env: DB }", "{ env: SPYGLASS_DATABASE_URL }", 1)
	_, err := Parse([]byte(text), fakeOpts(map[string]string{"KEY": "k", "LLM_PROXY_DATABASE_URL": "postgres://old"}, nil))
	if err == nil || !strings.Contains(err.Error(), "SPYGLASS_DATABASE_URL") {
		t.Errorf("err = %v, want it to name SPYGLASS_DATABASE_URL as not set", err)
	}
	cfg, err := Parse([]byte(text), fakeOpts(map[string]string{"KEY": "k", "SPYGLASS_DATABASE_URL": "postgres://new"}, nil))
	if err != nil || cfg.Database.URL.Secret().Reveal() != "postgres://new" {
		t.Errorf("SPYGLASS_ name: err %v", err)
	}
}

func TestPathIgnoresLegacyConfig(t *testing.T) {
	if p, err := Path("", fakeOpts(map[string]string{"SPYGLASS_CONFIG": "/new.yaml"}, nil)); err != nil || p != "/new.yaml" {
		t.Errorf("SPYGLASS_CONFIG: %q, %v", p, err)
	}
	// LLM_PROXY_CONFIG of 0.1.0 is no longer read.
	_, err := Path("", fakeOpts(map[string]string{"LLM_PROXY_CONFIG": "/old.yaml"}, nil))
	if err == nil || !strings.Contains(err.Error(), "SPYGLASS_CONFIG") {
		t.Errorf("err = %v, want the no-path error naming SPYGLASS_CONFIG", err)
	}
}
