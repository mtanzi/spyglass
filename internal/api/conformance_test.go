package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/core/coretest"
)

// spec is the served OpenAPI document with a compiler for its schemas. OpenAPI
// 3.1 schemas are JSON Schema 2020-12, so the document is a schema resource and
// "#/components/schemas/X" references resolve inside it.
type spec struct {
	doc map[string]any
	c   *jsonschema.Compiler
}

func loadSpec(t *testing.T) *spec {
	t.Helper()
	raw, err := specJSON()
	if err != nil {
		t.Fatal(err)
	}
	docAny, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("openapi.json", docAny); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return &spec{doc: doc, c: c}
}

// responseSchema returns the JSON pointer of the schema of a response, or ""
// when the operation documents no such status.
func (s *spec) responseSchema(t *testing.T, path, status string) string {
	t.Helper()
	get := func(m any, keys ...string) any {
		for _, k := range keys {
			mm, ok := m.(map[string]any)
			if !ok {
				return nil
			}
			m = mm[k]
		}
		return m
	}
	resp := get(s.doc, "paths", path, "get", "responses", status)
	base := "openapi.json#/paths/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(path) + "/get/responses/" + status
	if ref, ok := get(resp, "$ref").(string); ok { // #/components/responses/Name
		resp = get(s.doc, strings.Split(strings.TrimPrefix(ref, "#/"), "/")...)
		base = "openapi.json" + ref
	}
	if get(resp, "content", "application/json", "schema") == nil {
		return ""
	}
	return base + "/content/application~1json/schema"
}

// check validates one response against the schema its operation documents.
func (s *spec) check(t *testing.T, path string, status int, body []byte) {
	t.Helper()
	ptr := s.responseSchema(t, path, strconv.Itoa(status))
	if ptr == "" {
		t.Errorf("%s answers %d, which the OpenAPI document does not describe", path, status)
		return
	}
	sch, err := s.c.Compile(ptr)
	if err != nil {
		t.Fatalf("compile %s: %v", ptr, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		t.Errorf("%s %d: body is not JSON: %v", path, status, err)
		return
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("%s %d does not match the OpenAPI schema: %v\n%s", path, status, err, body)
	}
}

func TestResponsesConformToOpenAPI(t *testing.T) {
	s := loadSpec(t)

	cost := core.MicroUSD(1234)
	id := core.NewIDAt(testNow.Add(-time.Hour))
	next := core.Cursor{TS: testNow.Add(-time.Hour), ID: id}
	full := core.CallRecord{
		ID: id, TS: testNow, KeyName: "alice", Client: "claude-code", ClientRaw: "claude-cli/2.1", ClientRequestID: "r1",
		SessionKey: "s", AgentID: "a", ParentAgentID: "p", AgentType: "Explore", PromptID: "pr", RequestClass: "main",
		Compaction: true, ContextCompacted: true, TraceID: "t", Family: "anthropic", Endpoint: "/v1/messages",
		AuthMode: "proxy_key", Provider: "anthropic", Model: "claude-opus-4-8", UpstreamRequestID: "req_1",
		Status: 200, ErrorClass: "", ErrorType: "", StopReason: "end_turn", Streamed: true,
		TTFB: time.Second, TTFT: 2 * time.Second, Duration: 3 * time.Second, EndedAt: testNow.Add(3 * time.Second),
		Usage:    core.Usage{InputUncached: 1, CacheRead: 2, CacheWrite5m: 3, CacheWrite1h: 4, Output: 5, Reasoning: 1},
		UsageRaw: json.RawMessage(`{"input_tokens":1}`), UsageEstimated: true, ExtractionPartial: true,
		Cost: &cost, PricingVersion: "2026-10-01", PrevToolDurations: "{}",
		ToolUses:    []core.ToolUse{{Source: core.ToolSourceServer, ToolCallID: "toolu_1", Name: "Bash", ArgBytes: 9}},
		ToolResults: []core.ToolResult{{Source: core.ToolSourceClient, ToolCallID: "toolu_0", IsError: true, ResultBytes: 3}},
	}
	bare := core.CallRecord{ID: core.NewID()}
	rd := &coretest.Reader{
		Calls: []core.CallSummary{
			{ID: id, TS: testNow, KeyName: "alice", Status: 200, Cost: &cost, ToolCount: 1, ContextCompacted: true,
				Usage: core.Usage{Output: 5}, TTFT: time.Second, Duration: 2 * time.Second, Streamed: true},
			{ID: core.NewID(), TS: testNow, Status: 529},
		},
		Count: 10000, Capped: true, Next: &next,
		Details: map[core.ID]core.CallDetail{id: {CallRecord: full}, bare.ID: {CallRecord: bare}},
	}
	view := config.View{
		Providers: []config.ProviderView{{Name: "anthropic", Kind: "anthropic", BaseURL: "https://api.anthropic.com", AuthMode: "proxy_key", IdleTimeout: config.Duration(90 * time.Second)}},
		Pricing:   []config.PricingView{{Provider: "anthropic", Model: "m", From: config.Date(testNow), Price: config.PriceView{Input: 5_000_000}}},
		Keys:      []config.KeyView{{Name: "alice"}, {Name: "ci", Disabled: true}},
	}
	rd.Sessions = []core.SessionSummary{
		{SessionKey: "s", KeyName: "alice", Client: "claude-code", FirstTS: testNow, LastTS: testNow, EndedAt: testNow.Add(time.Second),
			Calls: 2, PromptCount: 1, ToolCallCount: 1, Errors: 1, AgentCount: 2, Models: []string{"m"}, Usage: core.Usage{Output: 5}, Cost: &cost},
		{SessionKey: "bare"},
	}
	rd.SessionMap = map[string]core.SessionDetail{
		"team/a:1": {
			SessionSummary: rd.Sessions[0],
			Agents:         []core.AgentNode{{AgentID: "a", AgentType: "Explore", Calls: 1}, {AgentID: "b", ParentAgentID: "a", Calls: 1}},
			Prompts:        []core.PromptSummary{{PromptID: "p", FirstTS: testNow, Calls: 2, Usage: core.Usage{Output: 5}, Cost: &cost}},
			Calls:          rd.Calls,
		},
		"bare": {SessionSummary: rd.Sessions[1]},
	}
	yes := true
	rd.ToolCalls = []core.ToolCall{
		{ToolCallID: "toolu_1", Name: "Bash", Source: core.ToolSourceClient, Status: core.ToolCallPaired, SessionKey: "s", AgentID: "a",
			UseCallID: id, ResultCallID: bare.ID, UseTS: testNow, ResultTS: testNow.Add(time.Second), Duration: time.Second,
			DurationSource: core.DurationHeader, IsError: &yes, ArgBytes: 3, ResultBytes: 4},
		{ToolCallID: "toolu_2", Name: "Read", Source: core.ToolSourceServer, Status: core.ToolCallPending, UseCallID: id, UseTS: testNow},
		{ToolCallID: "toolu_3", Source: core.ToolSourceClient, Status: core.ToolCallResultOnly, ResultCallID: bare.ID, ResultTS: testNow, IsError: &yes},
	}
	rd.ToolStats = []core.ToolStat{
		{Name: "Bash", AgentType: "Explore", Count: 4, Errors: 1, DurationP50: time.Second, DurationP95: 2 * time.Second, TotalDuration: 9 * time.Second},
		{Name: "Read"},
	}
	rd.Buckets = []core.UsageBucket{
		{Start: testNow, Group: "alice", Calls: 3, Errors: 1, Usage: core.Usage{InputUncached: 5, CacheRead: 5, Output: 2}, Cost: 99,
			UnpricedCalls: 1, EstimatedCalls: 1, NoUsageCalls: 1, LatencyP50: time.Second, LatencyP95: 2 * time.Second, LatencyP99: 3 * time.Second},
		{Start: testNow.Add(time.Hour), Calls: 1},
	}
	h := newTestMux(t, rd, view)

	samples := []struct {
		target string
		path   string // the path template of the document
		status int
	}{
		{"/admin/calls", "/admin/calls", 200},
		{"/admin/calls?model=a&status=429&streamed=true&tool=Bash&tool_error=true&latency_ms_gte=60000&endpoint=/v1/messages&limit=10", "/admin/calls", 200},
		{"/admin/calls/" + id.String(), "/admin/calls/{id}", 200},
		{"/admin/calls/" + bare.ID.String(), "/admin/calls/{id}", 200},
		{"/admin/config", "/admin/config", 200},
		{"/admin/sessions", "/admin/sessions", 200},
		{"/admin/sessions?model=a&key=alice&cost_micro_usd_gte=5&session=s&limit=5", "/admin/sessions", 200},
		{"/admin/sessions/team%2Fa%3A1", "/admin/sessions/{key}", 200},
		{"/admin/sessions/bare", "/admin/sessions/{key}", 200},
		{"/admin/sessions/missing", "/admin/sessions/{key}", 404},
		{"/admin/sessions?nope=1", "/admin/sessions", 400},
		{"/admin/tool-calls", "/admin/tool-calls", 200},
		{"/admin/tool-calls?session=s&tool=Bash&source=client&status=paired&status=use_only&is_error=true&duration_ms_gte=5", "/admin/tool-calls", 200},
		{"/admin/tool-calls?status=nope", "/admin/tool-calls", 400},
		{"/admin/tool-calls?cursor=zzz", "/admin/tool-calls", 400},
		{"/admin/tool-calls/summary", "/admin/tool-calls/summary", 200},
		{"/admin/tool-calls/summary?limit=5", "/admin/tool-calls/summary", 400},
		{"/admin/usage", "/admin/usage", 200},
		{"/admin/usage?group_by=model&grain=day&status=529&tool=Bash", "/admin/usage", 200},
		{"/admin/usage?group_by=nope", "/admin/usage", 400},
		{"/admin/usage?grain=week", "/admin/usage", 400},
		{"/admin/usage/prompts", "/admin/usage/prompts", 200},
		{"/admin/usage/prompts?limit=5&model=a&status=529&session=s", "/admin/usage/prompts", 200},
		{"/admin/usage/prompts?limit=0", "/admin/usage/prompts", 400},
		{"/admin/usage/prompts?limit=501", "/admin/usage/prompts", 400},
		{"/admin/usage/prompts?cursor=zzz", "/admin/usage/prompts", 400},
		{"/admin/usage/prompts?group_by=model", "/admin/usage/prompts", 400},
		{"/admin/calls?nope=1", "/admin/calls", 400},
		{"/admin/calls?cursor=zzz", "/admin/calls", 400},
		{"/admin/calls/not-an-id", "/admin/calls/{id}", 400},
		{"/admin/calls/" + core.NewID().String(), "/admin/calls/{id}", 404},
	}
	for _, sm := range samples {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", sm.target, nil))
		if rr.Code != sm.status {
			t.Errorf("%s: status %d, want %d\n%s", sm.target, rr.Code, sm.status, rr.Body)
			continue
		}
		s.check(t, sm.path, rr.Code, rr.Body.Bytes())
	}

	failing := newTestMux(t, &coretest.Reader{Err: errBoom}, view)
	for _, sm := range []struct{ target, path string }{
		{"/admin/calls", "/admin/calls"},
		{"/admin/calls/" + id.String(), "/admin/calls/{id}"},
		{"/admin/sessions", "/admin/sessions"},
		{"/admin/sessions/bare", "/admin/sessions/{key}"},
		{"/admin/tool-calls", "/admin/tool-calls"},
		{"/admin/tool-calls/summary", "/admin/tool-calls/summary"},
		{"/admin/usage", "/admin/usage"},
		{"/admin/usage/prompts", "/admin/usage/prompts"},
	} {
		rr := httptest.NewRecorder()
		failing.ServeHTTP(rr, httptest.NewRequest("GET", sm.target, nil))
		s.check(t, sm.path, rr.Code, rr.Body.Bytes())
	}
}

// TestSpecQueryParametersMatchParser fails when the OpenAPI document and the
// parameter parsers drift apart: every documented parameter must be one the
// parser accepts, and every parameter the parser accepts must be documented.
func TestSpecQueryParametersMatchParser(t *testing.T) {
	s := loadSpec(t)
	params := s.doc["components"].(map[string]any)["parameters"].(map[string]any)
	for path, known := range map[string]map[string]bool{
		"/admin/calls":              knownCallParams,
		"/admin/sessions":           knownSessionParams,
		"/admin/tool-calls":         knownToolCallParams,
		"/admin/tool-calls/summary": knownToolSummaryParams,
		"/admin/usage":              knownUsageParams,
		"/admin/usage/prompts":      knownPromptCostParams,
		"/admin/stream":             knownStreamParams,
	} {
		op := s.doc["paths"].(map[string]any)[path].(map[string]any)["get"].(map[string]any)
		var documented, parser []string
		for _, p := range op["parameters"].([]any) {
			ref := p.(map[string]any)["$ref"].(string)
			def := params[strings.TrimPrefix(ref, "#/components/parameters/")].(map[string]any)
			documented = append(documented, def["name"].(string))
		}
		for name := range known {
			parser = append(parser, name)
		}
		slices.Sort(documented)
		slices.Sort(parser)
		if !slices.Equal(documented, parser) {
			t.Errorf("%s: documented parameters %v\nparser parameters     %v", path, documented, parser)
		}
	}
}

// TestSpecEnumsMatchWhitelists keeps the documented value lists and the
// whitelists of the parser in step.
func TestSpecEnumsMatchWhitelists(t *testing.T) {
	s := loadSpec(t)
	params := s.doc["components"].(map[string]any)["parameters"].(map[string]any)
	enum := func(name string) []string {
		sch := params[name].(map[string]any)["schema"].(map[string]any)
		if items, ok := sch["items"].(map[string]any); ok {
			sch = items
		}
		var out []string
		for _, v := range sch["enum"].([]any) {
			out = append(out, v.(string))
		}
		return out
	}
	var groups, grains, sources []string
	for _, g := range usageGroups {
		groups = append(groups, string(g))
	}
	for _, g := range usageGrains {
		grains = append(grains, string(g))
	}
	for _, g := range toolSources {
		sources = append(sources, string(g))
	}
	for name, want := range map[string][]string{"GroupBy": groups, "Grain": grains, "ToolSource": sources} {
		if got := enum(name); !slices.Equal(got, want) {
			t.Errorf("%s: document %v, parser %v", name, got, want)
		}
	}
	var statuses []string
	for _, st := range toolStatuses {
		statuses = append(statuses, string(st))
	}
	schema := s.doc["components"].(map[string]any)["schemas"].(map[string]any)["ToolCallStatus"].(map[string]any)
	var got []string
	for _, v := range schema["enum"].([]any) {
		got = append(got, v.(string))
	}
	slices.Sort(got)
	slices.Sort(statuses)
	if !slices.Equal(got, statuses) {
		t.Errorf("ToolCallStatus: document %v, parser %v", got, statuses)
	}
}

func TestSpecRoutesMatchMux(t *testing.T) {
	s := loadSpec(t)
	paths := s.doc["paths"].(map[string]any)
	want := []string{
		"/admin/calls", "/admin/calls/{id}", "/admin/config", "/admin/openapi.json", "/admin/sessions",
		"/admin/sessions/{key}", "/admin/stream", "/admin/tool-calls", "/admin/tool-calls/summary", "/admin/usage",
		"/admin/usage/prompts",
	}
	for _, p := range want {
		if _, ok := paths[p]; !ok {
			t.Errorf("OpenAPI document lacks %s", p)
		}
	}
	if len(paths) != len(want) {
		t.Errorf("document has %d paths, want %d", len(paths), len(want))
	}
}

// TestSpecIsWellFormed is the local half of spec validation (CI also lints the
// file with a pinned OpenAPI linter): the version, every $ref target, every
// component schema and the operation ids.
func TestSpecIsWellFormed(t *testing.T) {
	s := loadSpec(t)
	if s.doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", s.doc["openapi"])
	}

	var refs []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if r, ok := x["$ref"].(string); ok {
				refs = append(refs, r)
			}
			for k, c := range x {
				if c == nil {
					t.Errorf("key %q has a null value: a flow mapping broke on a comma or colon", k)
				}
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(s.doc)
	if len(refs) == 0 {
		t.Fatal("no $ref found: the walk is broken")
	}
	for _, r := range refs {
		var cur any = s.doc
		for _, seg := range strings.Split(strings.TrimPrefix(r, "#/"), "/") {
			m, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = m[seg]
		}
		if !strings.HasPrefix(r, "#/") || cur == nil {
			t.Errorf("$ref %s does not resolve", r)
		}
	}

	schemas := s.doc["components"].(map[string]any)["schemas"].(map[string]any)
	for name := range schemas {
		if _, err := s.c.Compile("openapi.json#/components/schemas/" + name); err != nil {
			t.Errorf("schema %s: %v", name, err)
		}
	}

	ids := map[string]string{}
	for path, item := range s.doc["paths"].(map[string]any) {
		op := item.(map[string]any)["get"].(map[string]any)
		id, _ := op["operationId"].(string)
		if id == "" || ids[id] != "" {
			t.Errorf("%s: operationId %q is empty or repeated (also %s)", path, id, ids[id])
		}
		ids[id] = path
		if _, ok := op["responses"].(map[string]any)["200"]; !ok {
			t.Errorf("%s has no 200 response", path)
		}
	}
}
