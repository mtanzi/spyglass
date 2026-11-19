package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/keyring"
	"github.com/mtanzi/spyglass/internal/store/postgres"
)

var update = flag.Bool("update", false, "rewrite testdata/metrics.golden")

const testClientKey = "lp_alice_secret"

// loadedHolder is a config holder with one provider pointing at upstream.
func loadedHolder(t *testing.T, upstream string) *config.Holder {
	t.Helper()
	yaml := fmt.Sprintf(`
database: { url: { env: DB } }
providers:
  - name: anthropic
    kind: anthropic
    base_url: %s
    auth_mode: proxy_key
    credential: { env: PROVKEY }
keys:
  - name: alice
    sha256: %s
`, upstream, core.HashKey(testClientKey))
	h := config.NewHolder("cfg.yaml", config.Options{
		ReadFile: func(string) ([]byte, error) { return []byte(yaml), nil },
		LookupEnv: func(k string) (string, bool) {
			if k == "DB" {
				return "postgres://x", true
			}
			return "provider-secret", k == "PROVKEY"
		},
		BuildKeys: func(keys []core.Key) (config.KeyIndex, error) { return keyring.New(keys) },
	}, nil)
	if err := h.Load(); err != nil {
		t.Fatal(err)
	}
	return h
}

// downStore opens a store whose database refuses connections. Open connects
// lazily, so it succeeds and Ping fails.
func downStore(t *testing.T) *postgres.Store {
	t.Helper()
	s, err := postgres.Open(context.Background(), postgres.Config{URL: "postgres://nobody@127.0.0.1:1/none?connect_timeout=1&sslmode=disable"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func status(t *testing.T, h http.Handler, path string) int {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	return rr.Code
}

// With the store down, the data plane stays ready and the admin plane is not.
func TestReadinessWhileStoreIsDown(t *testing.T) {
	store := downStore(t)
	if err := store.Ping(context.Background()); err == nil {
		t.Fatal("ping of a refused database succeeded")
	}
	s, err := testServers(testConfig(), loadedHolder(t, "http://127.0.0.1:1"), store.Ping)
	if err != nil {
		t.Fatal(err)
	}
	if c := status(t, s.Data.Handler, "/readyz"); c != http.StatusOK {
		t.Errorf("data /readyz = %d, want 200: it must not depend on Postgres", c)
	}
	if c := status(t, s.Admin.Handler, "/readyz"); c != http.StatusServiceUnavailable {
		t.Errorf("admin /readyz = %d, want 503 with the store down", c)
	}
	for name, h := range map[string]http.Handler{"data": s.Data.Handler, "admin": s.Admin.Handler} {
		if c := status(t, h, "/healthz"); c != http.StatusOK {
			t.Errorf("%s /healthz = %d, want 200", name, c)
		}
	}
}

func TestAdminReadyWhenStoreAnswers(t *testing.T) {
	s, err := testServers(testConfig(), loadedHolder(t, "http://127.0.0.1:1"),
		func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if c := status(t, s.Admin.Handler, "/readyz"); c != http.StatusOK {
		t.Errorf("admin /readyz = %d, want 200", c)
	}
}

func TestAdminNotReadyWithoutStore(t *testing.T) {
	s, err := testServers(testConfig(), loadedHolder(t, "http://127.0.0.1:1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := status(t, s.Admin.Handler, "/readyz"); c != http.StatusServiceUnavailable {
		t.Errorf("admin /readyz with no Ping = %d, want 503", c)
	}
}

// Both /readyz endpoints flip to not-ready when shutdown starts, while
// /healthz keeps saying alive.
func TestReadinessFlipsOnShutdown(t *testing.T) {
	s, err := testServers(testConfig(), loadedHolder(t, "http://127.0.0.1:1"),
		func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	planes := map[string]http.Handler{"data": s.Data.Handler, "admin": s.Admin.Handler}
	for name, h := range planes {
		if c := status(t, h, "/readyz"); c != http.StatusOK {
			t.Fatalf("%s /readyz before shutdown = %d, want 200", name, c)
		}
	}
	s.Drain.Start()
	for name, h := range planes {
		if c := status(t, h, "/readyz"); c != http.StatusServiceUnavailable {
			t.Errorf("%s /readyz during shutdown = %d, want 503", name, c)
		}
		if c := status(t, h, "/healthz"); c != http.StatusOK {
			t.Errorf("%s /healthz during shutdown = %d, want 200", name, c)
		}
	}
}

// discardStore is a store that drops every record it is given.
type discardStore struct{ *postgres.Store }

func (discardStore) WriteBatch(context.Context, []core.CallRecord) error { return nil }

// TestMetricsGolden pins the names, types, help and label names of every
// metric ADR 0026 lists. A change to the list is a change to the dashboards and
// alerts that use it: review the diff. Rewrite with -update.
func TestMetricsGolden(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(upstream.Close)

	holder := loadedHolder(t, upstream.URL)
	// A real store whose database is down, with its writer replaced so the
	// forwarded call below writes nothing.
	a, err := buildApp(holder, discardStore{downStore(t)}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.abort)
	s := a.servers

	// One forwarded call primes the labelled series.
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+testClientKey)
	rr := httptest.NewRecorder()
	s.Data.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("forwarded call = %d: %s", rr.Code, rr.Body)
	}

	fams, err := a.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := metricsGolden(fams)
	const path = "testdata/metrics.golden"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("metrics differ from %s (run go test ./cmd/spyglass -run TestMetricsGolden -update and review):\n--- got\n%s--- want\n%s", path, got, want)
	}

	// /metrics serves the same registry over HTTP.
	mrr := httptest.NewRecorder()
	s.Admin.Handler.ServeHTTP(mrr, httptest.NewRequest("GET", "/metrics", nil))
	if mrr.Code != http.StatusOK || !strings.Contains(mrr.Body.String(), "spyglass_requests_total{") {
		t.Errorf("/metrics = %d, body lacks spyglass_requests_total", mrr.Code)
	}
}

// metricsGolden renders the spyglass_ families: name, type, label names and help.
func metricsGolden(fams []*dto.MetricFamily) string {
	var lines []string
	for _, f := range fams {
		if !strings.HasPrefix(f.GetName(), "spyglass_") {
			continue
		}
		set := map[string]bool{}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				set[l.GetName()] = true
			}
		}
		labels := make([]string, 0, len(set))
		for l := range set {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		lines = append(lines, fmt.Sprintf("%s %s {%s}\n    %s\n",
			f.GetName(), strings.ToLower(f.GetType().String()), strings.Join(labels, ","), f.GetHelp()))
	}
	sort.Strings(lines)
	return strings.Join(lines, "")
}
