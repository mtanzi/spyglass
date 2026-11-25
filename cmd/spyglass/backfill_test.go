package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/store/postgres"
	"github.com/mtanzi/spyglass/internal/store/postgres/pgtest"
)

func backfillCall(key, session, project string) core.CallRecord {
	ts := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	return core.CallRecord{
		ID: core.NewIDAt(ts), TS: ts, KeyName: key, SessionKey: session, Project: project,
		Family: "anthropic", Endpoint: "/v1/messages", AuthMode: "proxy_key", Provider: "anthropic",
	}
}

// The command reads keys[].project from the config file, fills the calls of
// that key, spreads a session's one project, and a second run changes nothing.
func TestBackfillProjectsCommand(t *testing.T) {
	url := pgtest.NewDatabase(t)
	t.Setenv("BACKFILL_TEST_DB", url)
	t.Setenv("BACKFILL_TEST_CRED", "provider-secret")
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := fmt.Sprintf(`
database: { url: { env: BACKFILL_TEST_DB } }
providers:
  - { name: anthropic, kind: anthropic, base_url: "https://api.anthropic.com", auth_mode: proxy_key, credential: { env: BACKFILL_TEST_CRED } }
keys:
  - { name: "fm:task-1", sha256: %q, project: spyglass }
  - { name: plain, sha256: %q }
`, core.HashKey("lp_one"), core.HashKey("lp_two"))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	st, err := openStore(ctx, postgres.Config{URL: url}, testLogger(), defaultStartupRetry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	calls := []core.CallRecord{
		backfillCall("fm:task-1", "", ""),
		backfillCall("plain", "s", "alpha"),
		backfillCall("plain", "s", ""),
		backfillCall("plain", "", ""),
	}
	if err := st.WriteBatch(ctx, calls); err != nil {
		t.Fatal(err)
	}

	runCmd := func() string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := run([]string{"backfill-projects", "--config", cfgPath}, &out, &errb); code != 0 {
			t.Fatalf("exit %d, stderr %q", code, errb.String())
		}
		return out.String()
	}
	if got, want := runCmd(), "1 calls got the project of their key, 1 calls got the project of their session\n"; got != want {
		t.Errorf("first run printed %q, want %q", got, want)
	}
	var got []string
	for _, c := range calls {
		d, err := st.GetCall(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, d.Project)
	}
	if want := "spyglass,alpha,alpha,"; strings.Join(got, ",") != want {
		t.Errorf("projects = %q, want %q", strings.Join(got, ","), want)
	}
	if got, want := runCmd(), "0 calls got the project of their key, 0 calls got the project of their session\n"; got != want {
		t.Errorf("second run printed %q, want %q", got, want)
	}
}

func TestBackfillProjectsRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{"backfill-projects", "extra"}, {"backfill-projects", "--nope"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("run(%v) = %d, want 2; stderr %q", args, code, errb.String())
		}
	}
}
