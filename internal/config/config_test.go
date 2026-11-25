package config

import (
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	hashA = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	hashB = "60303ae22b998861bce3b28f33eec1be758a213c86c93c076dbe9f558c11c752"
)

// fakeOpts serves env and files from maps.
func fakeOpts(env, files map[string]string) Options {
	return Options{
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile: func(p string) ([]byte, error) {
			if v, ok := files[p]; ok {
				return []byte(v), nil
			}
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
		},
	}
}

func validOpts() Options {
	return fakeOpts(
		map[string]string{"TEST_DATABASE_URL": "postgres://u:dbpass@db/llm"},
		map[string]string{"/run/secrets/anthropic_api_key": "sk-ant-secret\n"},
	)
}

func loadFile(t *testing.T, path string, o Options) (*Config, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return Parse(data, o)
}

func TestGoldenValid(t *testing.T) {
	cfg, err := loadFile(t, "testdata/valid.yaml", validOpts())
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Listen.Data != ":8443" || cfg.Listen.Admin != "[::1]:9090" || !cfg.Listen.TLS.Enabled() {
		t.Errorf("listen = %+v", cfg.Listen)
	}
	if cfg.Database.MaxConns != 8 || cfg.Database.URL.Secret().Reveal() != "postgres://u:dbpass@db/llm" {
		t.Errorf("database not resolved: %+v", cfg.Database)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d", len(cfg.Providers))
	}
	p := cfg.Providers[0]
	if p.AuthMode != AuthProxyKey || p.IdleTimeout.Std() != 90*time.Second || p.Credential.Ref.Secret().Reveal() != "sk-ant-secret" {
		t.Errorf("provider[0] = %+v", p)
	}
	if want := (core.Key{Name: "alice", SHA256: hashA, Project: "spyglass"}); cfg.Keys[0] != want {
		t.Errorf("keys[0] = %+v; want %+v", cfg.Keys[0], want)
	}
	if want := (core.Key{Name: "ci", SHA256: hashB, Disabled: true}); cfg.Keys[1] != want {
		t.Errorf("keys[1] = %+v; want %+v", cfg.Keys[1], want)
	}
	if got, want := cfg.Pricing[0].Price(), (core.Price{Input: 5_000_000, Output: 25_000_000, CacheRead: 500_000, CacheWrite5m: 6_250_000, CacheWrite1h: 10_000_000}); got != want {
		t.Errorf("price = %+v; want %+v", got, want)
	}
	if cfg.Retention.Calls.Std() != 90*24*time.Hour || cfg.Server.DrainTimeout.Std() != 2*time.Minute ||
		cfg.Server.MaxRequestBytes != 16<<20 || cfg.QueueCapacity != 500 || cfg.Log.Level != "debug" || cfg.Log.Format != "text" {
		t.Errorf("scalars = %+v %+v %d %+v", cfg.Retention, cfg.Server, cfg.QueueCapacity, cfg.Log)
	}

	got, err := json.MarshalIndent(cfg.View(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const golden = "testdata/valid.view.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("view differs from %s (run go test -update):\n%s", golden, got)
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := Parse([]byte(base), fakeOpts(map[string]string{"DB": "postgres://x", "KEY": "k"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen.Data != ":8080" || cfg.Listen.Admin != "127.0.0.1:9090" || cfg.Database.MaxConns != 20 ||
		cfg.Retention.Calls.Std() != 400*24*time.Hour || cfg.Server.DrainTimeout.Std() != 10*time.Minute ||
		cfg.Server.MaxRequestBytes != 32<<20 || cfg.QueueCapacity != 10000 || cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestExampleFile(t *testing.T) {
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	o := fakeOpts(
		nil,
		map[string]string{"/run/secrets/database_url": "postgres://x", "/run/secrets/anthropic_api_key": "k"},
	)
	cfg, err := Parse(data, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Keys) != 2 || len(cfg.Pricing) != 4 || len(cfg.Providers) != 1 {
		t.Errorf("example content = %d keys, %d pricing, %d providers", len(cfg.Keys), len(cfg.Pricing), len(cfg.Providers))
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "sha256:") && !strings.Contains(line, "# placeholder") {
			t.Errorf("hash line not marked as placeholder: %s", line)
		}
	}
}

// base is a small valid file the rejection cases edit.
const base = `
database: { url: { env: DB } }
providers:
  - name: anthropic
    kind: anthropic
    base_url: https://api.anthropic.com
    auth_mode: proxy_key
    credential: { env: KEY }
keys:
  - { name: a, sha256: ` + hashA + ` }
  - { name: b, sha256: ` + hashB + ` }
pricing:
  - { provider: anthropic, model: m, from: 2026-10-01, input: 1, output: 2 }
`

func baseOpts() Options {
	return fakeOpts(map[string]string{"DB": "postgres://x", "KEY": "k"}, nil)
}

func TestBaseIsValid(t *testing.T) {
	if _, err := Parse([]byte(base), baseOpts()); err != nil {
		t.Fatal(err)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name    string
		old     string // text of base to replace; empty appends new
		new     string
		env     map[string]string
		wantErr string
	}{
		// listeners
		{name: "admin all interfaces", new: "listen: { admin: ':9090' }", wantErr: "listen.admin"},
		{name: "admin wildcard host", new: "listen: { admin: '0.0.0.0:9090' }", wantErr: "not a loopback"},
		{name: "admin public ip", new: "listen: { admin: '10.0.0.5:9090' }", wantErr: "not a loopback"},
		{name: "admin hostname", new: "listen: { admin: 'example.com:9090' }", wantErr: "not a loopback"},
		{name: "admin no port", new: "listen: { admin: '127.0.0.1' }", wantErr: "host:port"},
		{name: "admin bad port", new: "listen: { admin: '127.0.0.1:99999' }", wantErr: "invalid port"},
		{name: "data bad addr", new: "listen: { data: 'nope' }", wantErr: "listen.data"},
		{name: "tls cert only", new: "listen: { tls: { cert_file: /c } }", wantErr: "listen.tls"},
		{name: "tls key only", new: "listen: { tls: { key_file: /k } }", wantErr: "listen.tls"},
		// keys
		{name: "duplicate key name", old: "name: b,", new: "name: a,", wantErr: `duplicate name "a"`},
		{name: "duplicate key hash", old: hashB, new: hashA, wantErr: "duplicate sha256"},
		{name: "short hash", old: hashB, new: hashB[:63], wantErr: "64 lowercase hex"},
		{name: "uppercase hash", old: hashB, new: strings.ToUpper(hashB), wantErr: "64 lowercase hex"},
		{name: "non hex hash", old: hashB, new: strings.Repeat("g", 64), wantErr: "64 lowercase hex"},
		{name: "empty key name", old: "name: b,", new: "name: '',", wantErr: "name is required"},
		{name: "key project too long", old: "name: b,", new: "project: " + strings.Repeat("p", 129) + ", name: b,", wantErr: "keys[1]: project must be"},
		{name: "key project not printable", old: "name: b,", new: `project: "a\tb", name: b,`, wantErr: "keys[1]: project must be"},
		// providers
		{name: "unknown kind", old: "kind: anthropic", new: "kind: gemini", wantErr: `unknown kind "gemini"`},
		{name: "unknown auth_mode", old: "auth_mode: proxy_key", new: "auth_mode: magic", wantErr: `unknown auth_mode "magic"`},
		{name: "auth_mode both", old: "auth_mode: proxy_key", new: "auth_mode: both", wantErr: `unknown auth_mode "both"`},
		{name: "bad base_url", old: "https://api.anthropic.com", new: "api.anthropic.com", wantErr: "base_url"},
		{name: "base_url with credentials", old: "https://api.anthropic.com", new: "https://u:p@api.anthropic.com", wantErr: "base_url"},
		{name: "two providers", old: "keys:", new: "  - { name: second, kind: anthropic, base_url: 'http://x', credential: { env: KEY } }\nkeys:", wantErr: "exactly one provider"},
		{name: "no providers", old: "providers:\n  - name: anthropic\n    kind: anthropic\n    base_url: https://api.anthropic.com\n    auth_mode: proxy_key\n    credential: { env: KEY }\n", new: "", wantErr: "exactly one provider"},
		{name: "openai_compatible kind", old: "kind: anthropic", new: "kind: openai_compatible", wantErr: "kind openai_compatible is v2"},
		{name: "headers_to_add", old: "kind: anthropic", new: "kind: anthropic\n    headers_to_add: { X-Org: o }", wantErr: "headers_to_add is v2"},
		{name: "missing credential", old: "    credential: { env: KEY }\n", new: "", wantErr: "credential is required"},
		{name: "credential none for anthropic", old: "{ env: KEY }", new: "none", wantErr: "credential none is v2"},
		{name: "inlined credential", old: "{ env: KEY }", new: "sk-ant-inline", wantErr: "secrets are never inlined"},
		{name: "credential unknown field", old: "{ env: KEY }", new: "{ env: KEY, value: x }", wantErr: "field value not found"},
		{name: "credential env and file", old: "{ env: KEY }", new: "{ env: KEY, file: /f }", wantErr: "only one of env and file"},
		{name: "credential empty map", old: "{ env: KEY }", new: "{}", wantErr: "credential is required"},
		// secrets
		{name: "unset secret env", env: map[string]string{"KEY": ""}, wantErr: "is empty"},
		{name: "missing secret env", old: "{ env: KEY }", new: "{ env: ABSENT }", wantErr: "ABSENT is not set"},
		{name: "unreadable secret file", old: "{ env: KEY }", new: "{ file: /no/such }", wantErr: "read secret file"},
		{name: "database url unset", old: "env: DB", new: "env: NOPE", wantErr: "database.url: environment variable NOPE is not set"},
		{name: "database url not a ref", old: "{ env: DB }", new: "postgres://x", wantErr: "secrets are never inlined"},
		// pricing
		{name: "overlapping pricing", old: "input: 1, output: 2 }\n", new: "input: 1, output: 2 }\n  - { provider: anthropic, model: m, from: 2026-10-01 }\n", wantErr: "overlaps pricing[0]"},
		{name: "pricing bad date", old: "from: 2026-10-01", new: "from: yesterday", wantErr: "invalid date"},
		{name: "pricing missing from", old: "from: 2026-10-01,", new: "", wantErr: "from is required"},
		{name: "pricing negative price", old: "input: 1", new: "input: -1", wantErr: "invalid price"},
		{name: "pricing price text", old: "input: 1", new: "input: cheap", wantErr: "invalid price"},
		{name: "pricing too many places", old: "input: 1", new: "input: 0.0000001", wantErr: "invalid price"},
		{name: "pricing unknown provider", old: "provider: anthropic, model", new: "provider: other, model", wantErr: `provider "other" is not declared`},
		{name: "pricing no model", old: "model: m,", new: "", wantErr: "model is required"},
		{name: "pricing unknown field", old: "output: 2", new: "output: 2, cache: 1", wantErr: "field cache not found"},
		// scalars
		{name: "bad duration", new: "retention: { calls: 400 }", wantErr: "invalid duration"},
		{name: "zero retention", new: "retention: { calls: 0d }", wantErr: "retention.calls"},
		{name: "bad size", new: "server: { max_request_bytes: 32MB }", wantErr: "invalid size"},
		{name: "zero drain", new: "server: { drain_timeout: 0s }", wantErr: "server.drain_timeout"},
		{name: "zero queue", new: "queue_capacity: 0", wantErr: "queue_capacity"},
		{name: "zero max_conns", old: "database: { url: { env: DB } }", new: "database: { url: { env: DB }, max_conns: 0 }", wantErr: "database.max_conns"},
		{name: "bad log level", new: "log: { level: loud }", wantErr: "log.level"},
		{name: "bad log format", new: "log: { format: xml }", wantErr: "log.format"},
		// structure
		{name: "unknown top-level field", new: "extra: 1", wantErr: "field extra not found"},
		{name: "unknown nested field", new: "server: { max_body: 1 }", wantErr: "field max_body not found"},
		{name: "unknown provider field", old: "kind: anthropic", new: "kind: anthropic\n    timeout: 5s", wantErr: "field timeout not found"},
		{name: "unknown key field", old: "name: b,", new: "name: b, plaintext: x,", wantErr: "field plaintext not found"},
		{name: "duplicate yaml key", new: "queue_capacity: 1\nqueue_capacity: 2", wantErr: "already defined"},
		{name: "two documents", new: "---\nqueue_capacity: 1", wantErr: "more than one YAML document"},
		{name: "wrong type", new: "queue_capacity: many", wantErr: "into int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := base
			switch {
			case tc.old != "":
				if !strings.Contains(text, tc.old) {
					t.Fatalf("base lacks %q", tc.old)
				}
				text = strings.Replace(text, tc.old, tc.new, 1)
			default:
				text += "\n" + tc.new + "\n"
			}
			env := map[string]string{"DB": "postgres://x", "KEY": "k"}
			for k, v := range tc.env {
				env[k] = v
			}
			cfg, err := Parse([]byte(text), fakeOpts(env, nil))
			if err == nil {
				t.Fatalf("accepted bad config: %+v", cfg)
			}
			if cfg != nil {
				t.Errorf("returned a config with the error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q; want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestRejectsAllProblemsAtOnce(t *testing.T) {
	text := strings.Replace(base, hashB, hashA, 1) + "\nqueue_capacity: 0\nlog: { level: loud }\n"
	_, err := Parse([]byte(text), baseOpts())
	if err == nil {
		t.Fatal("accepted bad config")
	}
	for _, want := range []string{"duplicate sha256", "queue_capacity", "log.level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestEnvOverrides(t *testing.T) {
	env := map[string]string{
		"DB": "postgres://x", "KEY": "k", "OTHER_KEY": "other",
		"SPYGLASS_LISTEN_DATA":              ":9999",
		"SPYGLASS_LISTEN_ADMIN":             "localhost:7000",
		"SPYGLASS_LISTEN_TLS_CERT_FILE":     "/c",
		"SPYGLASS_LISTEN_TLS_KEY_FILE":      "/k",
		"SPYGLASS_DATABASE_MAX_CONNS":       "3",
		"SPYGLASS_DATABASE_URL_ENV":         "OTHER_KEY",
		"SPYGLASS_RETENTION_CALLS":          "30d",
		"SPYGLASS_SERVER_DRAIN_TIMEOUT":     "45s",
		"SPYGLASS_SERVER_MAX_REQUEST_BYTES": "1MiB",
		"SPYGLASS_QUEUE_CAPACITY":           "77",
		"SPYGLASS_LOG_LEVEL":                "warn",
		"SPYGLASS_LOG_FORMAT":               "text",
	}
	cfg, err := Parse([]byte(base), fakeOpts(env, nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen.Data != ":9999" || cfg.Listen.Admin != "localhost:7000" || cfg.Listen.TLS.CertFile != "/c" || cfg.Listen.TLS.KeyFile != "/k" ||
		cfg.Database.MaxConns != 3 || cfg.Database.URL.Secret().Reveal() != "other" ||
		cfg.Retention.Calls.Std() != 30*24*time.Hour || cfg.Server.DrainTimeout.Std() != 45*time.Second ||
		cfg.Server.MaxRequestBytes != 1<<20 || cfg.QueueCapacity != 77 || cfg.Log.Level != "warn" || cfg.Log.Format != "text" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestEnvOverrideSwitchesSecretSource(t *testing.T) {
	env := map[string]string{"KEY": "k", "SPYGLASS_DATABASE_URL_FILE": "/run/secrets/db"}
	cfg, err := Parse([]byte(base), fakeOpts(env, map[string]string{"/run/secrets/db": "postgres://f\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Database.URL; got.Env != "" || got.File != "/run/secrets/db" || got.Secret().Reveal() != "postgres://f" {
		t.Errorf("database.url = %s, %q", got, got.Secret().Reveal())
	}

	env["SPYGLASS_DATABASE_URL_ENV"], env["DB"] = "DB", "postgres://x"
	if _, err := Parse([]byte(base), fakeOpts(env, map[string]string{"/run/secrets/db": "postgres://f\n"})); err == nil {
		t.Error("both env and file overrides accepted")
	}
}

func TestEnvOverrideIsValidated(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"non-loopback admin": {"SPYGLASS_LISTEN_ADMIN": "0.0.0.0:9090"},
		"bad duration":       {"SPYGLASS_SERVER_DRAIN_TIMEOUT": "soon"},
		"bad number":         {"SPYGLASS_QUEUE_CAPACITY": "lots"},
	} {
		t.Run(name, func(t *testing.T) {
			env["DB"], env["KEY"] = "x", "k"
			if _, err := Parse([]byte(base), fakeOpts(env, nil)); err == nil || !strings.Contains(err.Error(), "SPYGLASS") && name != "non-loopback admin" {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestEnvOverrideNames(t *testing.T) {
	var names []string
	walkLeaves(reflect.ValueOf(&Config{}).Elem(), []string{EnvPrefix}, func(n string, _ reflect.Value) { names = append(names, n) })
	got := strings.Join(names, " ")
	for _, want := range []string{
		"SPYGLASS_LISTEN_DATA", "SPYGLASS_LISTEN_ADMIN", "SPYGLASS_LISTEN_TLS_CERT_FILE", "SPYGLASS_LISTEN_TLS_KEY_FILE",
		"SPYGLASS_DATABASE_MAX_CONNS",
		"SPYGLASS_RETENTION_CALLS", "SPYGLASS_SERVER_DRAIN_TIMEOUT", "SPYGLASS_SERVER_MAX_REQUEST_BYTES",
		"SPYGLASS_QUEUE_CAPACITY", "SPYGLASS_LOG_LEVEL", "SPYGLASS_LOG_FORMAT",
	} {
		if !strings.Contains(got+" ", want+" ") {
			t.Errorf("no override variable %s in %s", want, got)
		}
	}
	if strings.Contains(got, "PROVIDERS") || strings.Contains(got, "KEYS") || strings.Contains(got, "PRICING") {
		t.Errorf("lists must not be overridable: %s", got)
	}
}

func TestPath(t *testing.T) {
	env := fakeOpts(map[string]string{EnvConfigPath: "/from/env.yaml"}, nil)
	if p, err := Path("/from/flag.yaml", env); err != nil || p != "/from/flag.yaml" {
		t.Errorf("flag wins: %q, %v", p, err)
	}
	if p, err := Path("", env); err != nil || p != "/from/env.yaml" {
		t.Errorf("env fallback: %q, %v", p, err)
	}
	if _, err := Path("", fakeOpts(nil, nil)); err == nil {
		t.Error("no path accepted")
	}
}

func TestLoadReadsFileAndSecretsAgain(t *testing.T) {
	files := map[string]string{"/etc/cfg.yaml": base, "/s": "v1\n"}
	files["/etc/cfg.yaml"] = strings.Replace(base, "{ env: KEY }", "{ file: /s }", 1)
	o := fakeOpts(map[string]string{"DB": "postgres://x"}, files)

	cfg, err := Load("/etc/cfg.yaml", o)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers[0].Credential.Ref.Secret().Reveal(); got != "v1" {
		t.Fatalf("secret = %q", got)
	}
	files["/s"] = "v2"
	cfg, err = Load("/etc/cfg.yaml", o)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers[0].Credential.Ref.Secret().Reveal(); got != "v2" {
		t.Errorf("secret after rotation = %q; want v2", got)
	}
	delete(files, "/s")
	if _, err := Load("/etc/cfg.yaml", o); err == nil {
		t.Error("load succeeded with an unreadable secret file")
	}
	if _, err := Load("/missing.yaml", o); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing config file: %v", err)
	}
}

func TestPassthroughProviderNeedsNoCredential(t *testing.T) {
	text := strings.Replace(base, "    credential: { env: KEY }\n", "", 1)
	text = strings.Replace(text, "auth_mode: proxy_key", "auth_mode: client_passthrough", 1)
	if _, err := Parse([]byte(text), fakeOpts(map[string]string{"DB": "x"}, nil)); err != nil {
		t.Fatal(err)
	}
}

func TestViewListsNamesNotHashes(t *testing.T) {
	cfg, err := Parse([]byte(base), baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	v := cfg.View()
	if len(v.Keys) != 2 || v.Keys[0].Name != "a" || v.Keys[1].Name != "b" {
		t.Errorf("keys = %+v", v.Keys)
	}
	v.Keys[0].Name = "changed"
	if cfg.Keys[0].Name != "a" {
		t.Error("View shares memory with Config")
	}
	b, _ := json.Marshal(cfg.View())
	for _, banned := range []string{hashA, hashB, "postgres://x", `"k"`} {
		if strings.Contains(string(b), banned) {
			t.Errorf("view leaks %q: %s", banned, b)
		}
	}
}

func TestPriceVersions(t *testing.T) {
	cfg, err := loadFile(t, "testdata/valid.yaml", validOpts())
	if err != nil {
		t.Fatal(err)
	}
	vs := cfg.PriceVersions()
	if len(vs) != len(cfg.Pricing) || len(vs) == 0 {
		t.Fatalf("price versions = %d, pricing = %d", len(vs), len(cfg.Pricing))
	}
	if got := vs[0]; got.Provider != cfg.Pricing[0].Provider || got.Version() != cfg.Pricing[0].From.String() || got.Price != cfg.Pricing[0].Price() {
		t.Errorf("price version = %+v", got)
	}
}
