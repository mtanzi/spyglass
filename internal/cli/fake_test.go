package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/mtanzi/spyglass/api"
)

// testNow is the clock of every command test.
var testNow = time.Date(2026, 11, 13, 14, 20, 30, 0, time.UTC)

// spec is api/openapi.yaml as a JSON Schema resource, the way
// internal/api/conformance_test.go reads it: OpenAPI 3.1 schemas are JSON
// Schema 2020-12, so "#/components/schemas/X" resolves inside the document.
type spec struct {
	doc map[string]any
	c   *jsonschema.Compiler
}

func loadSpec(t testing.TB) *spec {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(api.OpenAPIYAML, &doc); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("openapi.json", inst); err != nil {
		t.Fatal(err)
	}
	return &spec{doc: doc, c: c}
}

// validate checks a body against a named schema of components.schemas.
func (s *spec) validate(schema string, body []byte) error {
	sch, err := s.c.Compile("openapi.json#/components/schemas/" + schema)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("body is not JSON: %w", err)
	}
	return sch.Validate(inst)
}

// route is one canned response of the fake admin server. Its body is checked
// against the OpenAPI schema it names every time it is served, so a fixture
// that drifts from the contract fails the test that uses it.
type route struct {
	status int
	schema string
	body   string
	// pick, when set, chooses the body from the request (the home view reads
	// /admin/usage twice with different group_by).
	pick func(url.Values) string
}

type request struct {
	Path  string // as sent, percent-encoding kept
	Query url.Values
}

// fakeAdmin is an httptest.Server that stands in for the admin API.
type fakeAdmin struct {
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []request
	routes map[string]route
}

func newFakeAdmin(t *testing.T, routes map[string]route) *fakeAdmin {
	t.Helper()
	sp := loadSpec(t)
	f := &fakeAdmin{routes: routes}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, request{Path: r.URL.EscapedPath(), Query: r.URL.Query()})
		f.mu.Unlock()
		if r.Method != http.MethodGet {
			t.Errorf("the CLI sent %s %s: it must only GET", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the CLI sent an Authorization header, but the admin API has no sign-in in v1")
		}
		rt, ok := f.routes[r.URL.EscapedPath()]
		if !ok {
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
			return
		}
		body := rt.body
		if rt.pick != nil {
			body = rt.pick(r.URL.Query())
		}
		if rt.schema != "" {
			if err := sp.validate(rt.schema, []byte(body)); err != nil {
				t.Errorf("fixture for %s does not match schema %s: %v", r.URL.EscapedPath(), rt.schema, err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if rt.status != 0 {
			w.WriteHeader(rt.status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAdmin) requests() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]request(nil), f.reqs...)
}

// cli runs one command against the fake and returns stdout and the exit code.
func (f *fakeAdmin) cli(args ...string) (string, int) {
	return runCLI(f.srv.URL, args...)
}

func runCLI(adminURL string, args ...string) (string, int) {
	var out bytes.Buffer
	env := func(k string) string {
		if k == adminURLVar {
			return adminURL
		}
		return ""
	}
	code := run(args, &out, env, func() time.Time { return testNow })
	return out.String(), code
}

// want compares output with a golden string, trimming one trailing newline.
func want(t *testing.T, got, golden string) {
	t.Helper()
	golden = strings.TrimPrefix(golden, "\n")
	if got != golden {
		t.Errorf("output differs.\n--- got ---\n%s--- want ---\n%s", got, golden)
	}
}

// ---- fixtures: JSON the real API could return --------------------------------

func bucket(start, group string, calls, errs, in, out, costMicro, unpriced int, p95 int, hit string) string {
	return fmt.Sprintf(`{"start":%q,"group":%q,"calls":%d,"errors":%d,
 "usage":{"input_uncached":%d,"cache_read":0,"cache_write_5m":0,"cache_write_1h":0,"output":%d,"reasoning":0},
 "cost_micro_usd":%d,"unpriced_calls":%d,"estimated_calls":0,"no_usage_calls":0,
 "latency_p50_ms":100,"latency_p95_ms":%d,"latency_p99_ms":%d,"cache_hit_ratio":%s}`,
		start, group, calls, errs, in, out, costMicro, unpriced, p95, p95*2, hit)
}

func series(groupBy string, buckets ...string) string {
	gb := "null"
	if groupBy != "" {
		gb = `"` + groupBy + `"`
	}
	return fmt.Sprintf(`{"group_by":%s,"grain":"day","buckets":[%s]}`, gb, strings.Join(buckets, ","))
}

const day = "2026-11-13T00:00:00Z"

// homeRoute serves the ungrouped total, or the session buckets for group_by.
func homeRoute(total string, sessions string) route {
	return route{schema: "UsageSeries", pick: func(q url.Values) string {
		if q.Get("group_by") == "session" {
			return sessions
		}
		return total
	}}
}

func callJSON(id, ts, model, provider string, status int, errClass string, cost string) string {
	return fmt.Sprintf(`{"id":%q,"ts":%q,"key":"alice","client":"claude-code","session_key":"fm:7f3a","agent_id":"main",
 "provider":%q,"model":%q,"status":%d,"error_class":%q,"stop_reason":"end_turn","streamed":true,"ttft_ms":420,"duration_ms":3100,
 "usage":{"input_uncached":10,"cache_read":200,"cache_write_5m":30,"cache_write_1h":4,"output":55,"reasoning":5},
 "cost_micro_usd":%s,"tool_count":2,"context_compacted":false,"future_field":"ignored"}`,
		id, ts, provider, model, status, errClass, cost)
}

const (
	id1 = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	id2 = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c"
	id3 = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5d"
)

func callList3(next string, count int, capped bool) string {
	return fmt.Sprintf(`{"calls":[%s,%s,%s],"next_cursor":%s,"count":%d,"count_capped":%t}`,
		callJSON(id1, "2026-11-13T14:10:00Z", "claude-opus-4-8", "anthropic", 200, "", "1234500"),
		callJSON(id2, "2026-11-13T14:05:00Z", "claude-sonnet-5-5", "anthropic", 429, "rate_limited", "null"),
		callJSON(id3, "2026-11-13T14:00:00Z", "claude-opus-4-8", "anthropic", 200, "", "9990000"),
		next, count, capped)
}

const configJSON = `{"providers":[
 {"name":"anthropic","kind":"anthropic","base_url":"https://api.anthropic.com","auth_mode":"proxy_key","idle_timeout":"10m"},
 {"name":"claude-max","kind":"anthropic","base_url":"https://api.anthropic.com","auth_mode":"client_passthrough","idle_timeout":"10m"}],
 "pricing":[{"provider":"anthropic","model":"claude-opus-4-8","from":"2026-10-01",
  "price":{"input":15000000,"output":75000000,"cache_read":1500000,"cache_write_5m":18750000,"cache_write_1h":30000000}}],
 "keys":[{"name":"alice","disabled":false},{"name":"bob","disabled":true}]}`

func sessionJSON(key string, calls, errs int, cost string, unpriced int) string {
	return fmt.Sprintf(`{"session_key":%q,"key":"alice","client":"claude-code","first_ts":"2026-11-13T09:00:00Z","last_ts":"2026-11-13T14:10:00Z",
 "ended_at":"2026-11-13T14:10:03Z","calls":%d,"prompts":4,"tool_calls":17,"errors":%d,"agent_count":2,"models":["claude-opus-4-8","claude-haiku-5-5"],
 "usage":{"input_uncached":100,"cache_read":2000,"cache_write_5m":300,"cache_write_1h":40,"output":550,"reasoning":50},
 "cost_micro_usd":%s,"unpriced_calls":%d,"llm_ms":42000,"tool_ms":28000,"tool_ms_estimated":false}`, key, calls, errs, cost, unpriced)
}
