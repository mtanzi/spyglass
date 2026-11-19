package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/keyring"
)

const plaintextPrefix = "plaintext key (shown once, give it to the client and keep it nowhere else): "

var keyRE = regexp.MustCompile(`^lp_[0-9A-Za-z]{43}$`)

func keygen(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(append([]string{"keygen"}, args...), &out, &errb)
	return out.String(), errb.String(), code
}

func plaintext(t *testing.T, out string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if k, ok := strings.CutPrefix(l, plaintextPrefix); ok {
			return k
		}
	}
	t.Fatalf("no plaintext line in %q", out)
	return ""
}

func TestKeygenFormatGolden(t *testing.T) {
	zero := bytes.NewReader(make([]byte, keyBytes))
	key, err := newKey(zero)
	if err != nil {
		t.Fatal(err)
	}
	if want := "lp_" + strings.Repeat("0", 43); key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
	got, err := formatKeygen("fm:task-1", key)
	if err != nil {
		t.Fatal(err)
	}
	want := plaintextPrefix + key + "\n\n" +
		"keys entry (add under keys: in the config):\n" +
		"- name: fm:task-1\n  sha256: " + core.HashKey(key) + "\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

func TestKeygenMaxValueFitsWidth(t *testing.T) {
	ff := bytes.NewReader(bytes.Repeat([]byte{0xff}, keyBytes))
	key, err := newKey(ff)
	if err != nil {
		t.Fatal(err)
	}
	if !keyRE.MatchString(key) {
		t.Fatalf("key %q does not match %s", key, keyRE)
	}
}

func TestKeygenHashMatchesPrintedKey(t *testing.T) {
	out, _, code := keygen(t, "--name", "marco-laptop")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	key := plaintext(t, out)
	if !keyRE.MatchString(key) {
		t.Fatalf("key %q malformed", key)
	}
	if want := "sha256: " + core.HashKey(key); !strings.Contains(out, want) {
		t.Fatalf("output lacks %q:\n%s", want, out)
	}
}

func TestKeygenRunsDiffer(t *testing.T) {
	a, _, _ := keygen(t, "--name", "a")
	b, _, _ := keygen(t, "--name", "a")
	if plaintext(t, a) == plaintext(t, b) {
		t.Fatal("two runs printed the same key")
	}
}

func TestKeygenEntryPassesConfigValidation(t *testing.T) {
	out, _, _ := keygen(t, "--name", "fm:task-1")
	_, entry, ok := strings.Cut(out, "config):\n")
	if !ok {
		t.Fatalf("no entry in %q", out)
	}
	cfg := "database: { url: { env: DB } }\nproviders:\n  - { name: a, kind: anthropic, base_url: 'https://x', auth_mode: client_passthrough }\nkeys:\n" + entry
	opts := config.Options{
		LookupEnv: func(k string) (string, bool) { return "postgres://x", k == "DB" },
		BuildKeys: func(k []core.Key) (config.KeyIndex, error) { return keyring.New(k) }}
	c, err := config.Parse([]byte(cfg), opts)
	if err != nil {
		t.Fatalf("config rejected entry: %v\n%s", err, cfg)
	}
	if len(c.Keys) != 1 || c.Keys[0].Name != "fm:task-1" {
		t.Fatalf("keys = %+v", c.Keys)
	}
}

func TestKeygenRejectsBadName(t *testing.T) {
	for _, name := range []string{"", "a b", "a\tb", " a"} {
		out, errOut, code := keygen(t, "--name", name)
		if code != 2 || out != "" || errOut == "" {
			t.Errorf("name %q: code=%d stdout=%q stderr=%q", name, code, out, errOut)
		}
	}
	if _, _, code := keygen(t); code != 2 {
		t.Errorf("missing --name: code=%d", code)
	}
}

func TestKeygenPlaintextNotOnStderr(t *testing.T) {
	out, errOut, _ := keygen(t, "--name", "x")
	if strings.Contains(errOut, plaintext(t, out)) {
		t.Fatal("plaintext on stderr")
	}
}

func TestVersion(t *testing.T) {
	old := version
	version = "v1.2.3"
	t.Cleanup(func() { version = old })
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || out.String() != "v1.2.3\n" {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestDispatchUnknownAndEmpty(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"nope"}, &out, &errb); code != 2 {
		t.Errorf("unknown: code=%d", code)
	}
	if !strings.Contains(errb.String(), "usage: spyglass") {
		t.Errorf("unknown command: stderr = %q, want the usage text", errb.String())
	}
}

// With no arguments spyglass runs the home view, a client command (ADR 0055).
// It reads the admin API, so a server that is not there is a structured error
// on stdout with exit 1, not usage on stderr with exit 2.
func TestDispatchNoArgsRunsTheHomeView(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	t.Setenv("SPYGLASS_ADMIN_URL", url)
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 1 {
		t.Errorf("no args: code=%d, want 1 (admin API unreachable)", code)
	}
	if !strings.HasPrefix(out.String(), "error: unreachable\n") || !strings.Contains(out.String(), url) {
		t.Errorf("no args: stdout = %q, want a structured unreachable error naming %s", out.String(), url)
	}
	if errb.Len() != 0 {
		t.Errorf("no args: stderr = %q, want nothing", errb.String())
	}
}

// The client commands dispatch to cli.Run; serve and keygen keep stderr.
func TestDispatchClientCommands(t *testing.T) {
	t.Setenv("SPYGLASS_ADMIN_URL", "http://127.0.0.1:1") // never reached: usage errors stop first
	for _, args := range [][]string{{"calls"}, {"sessions", "nope"}, {"config", "--nope"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 || !strings.HasPrefix(out.String(), "error: usage\n") || errb.Len() != 0 {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, out.String(), errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"--help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "examples:") {
		t.Errorf("--help: code=%d\n%s", code, out.String())
	}
}
