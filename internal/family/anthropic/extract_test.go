package anthropic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/mtanzi/spyglass/internal/core"
)

const fixturesDir = "../../../testdata/fixtures"

// fixtureMeta is the part of an exchange the fixture files cannot show: what
// the proxy saw around the bodies.
type fixtureMeta struct {
	Endpoint   string `json:"endpoint"`
	Status     int    `json:"status"`
	Streamed   bool   `json:"streamed"`
	ErrorClass string `json:"error_class"`
	TTFBMs     int    `json:"ttfb_ms"`
	TTFTMs     int    `json:"ttft_ms"`
	DurationMs int    `json:"duration_ms"`
}

type fixture struct {
	name     string
	meta     fixtureMeta
	request  []byte
	response []byte
}

func loadFixtures(t testing.TB) []fixture {
	t.Helper()
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(fixturesDir, e.Name())
		f := fixture{name: e.Name()}
		read := func(name string) []byte {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		if err := json.Unmarshal(read("meta.json"), &f.meta); err != nil {
			t.Fatalf("%s: meta.json: %v", e.Name(), err)
		}
		f.request = read("request.json")
		if f.meta.Streamed {
			f.response = read("response.sse")
		} else {
			f.response = read("response.json")
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		t.Fatal("no fixtures")
	}
	return out
}

var fixtureStart = time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

// baseRecord is the record the proxy has built when the response body closes:
// header fields, status and timings are set, extraction has not run.
func (f fixture) baseRecord() core.CallRecord {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	id, _ := core.ParseID("0198a000-0000-7000-8000-000000000001")
	return core.CallRecord{
		ID: id, TS: fixtureStart,
		KeyName: "alice", Client: "claude-cli", ClientRaw: "claude-cli/2.1.290 (external, cli)",
		SessionKey: "sess-1", AgentID: "agent-1", AgentType: "main", PromptID: "prompt-1",
		Family: "anthropic", Endpoint: f.meta.Endpoint, AuthMode: "proxy_key", Provider: "anthropic",
		UpstreamRequestID: "req_01",
		Status:            f.meta.Status, ErrorClass: f.meta.ErrorClass, Streamed: f.meta.Streamed,
		TTFB: ms(f.meta.TTFBMs), TTFT: ms(f.meta.TTFTMs), Duration: ms(f.meta.DurationMs),
		EndedAt: fixtureStart.Add(ms(f.meta.DurationMs)),
	}
}

// extract runs the scanner and the extractor over the fixture, as the tap does.
func (f fixture) extract() core.CallRecord {
	s := NewScanner(f.meta.Streamed)
	s.Write(f.response)
	rec := f.baseRecord()
	Extractor{}.Extract(&rec, f.request, s.Facts())
	return rec
}

func recordJSON(t testing.TB, rec core.CallRecord) []byte {
	t.Helper()
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// TestFixtureGoldens compares the call record of each fixture exchange with
// its golden. Regenerate with `make goldens` after a deliberate rule change,
// and read the diff: a wrong rule cannot be recomputed for past rows.
func TestFixtureGoldens(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			got := recordJSON(t, f.extract())
			path := filepath.Join(fixturesDir, f.name, "record.golden.json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("record mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// TestFixtureFacts pins the rules the goldens show, so a regenerated golden
// cannot hide a broken rule.
func TestFixtureFacts(t *testing.T) {
	rec := map[string]core.CallRecord{}
	for _, f := range loadFixtures(t) {
		rec[f.name] = f.extract()
	}
	tests := []struct {
		name string
		want func(core.CallRecord) string // empty when the record is right
	}{
		{"short_chat", func(r core.CallRecord) string {
			return check(r.Model == "claude-opus-4-8" && r.StopReason == "end_turn" && r.Usage.Output == 12 &&
				r.Usage.InputUncached == 25 && !r.UsageEstimated && !r.ExtractionPartial && r.ErrorClass == "", r)
		}},
		{"long_thinking", func(r core.CallRecord) string {
			return check(r.Usage.Output == 2048 && r.Usage.Reasoning == 0 && r.TTFT == 9*time.Second, r)
		}},
		{"parallel_tool_calls", func(r core.CallRecord) string {
			u := r.ToolUses
			return check(len(u) == 3 && u[0].Source == core.ToolSourceClient && u[1].ToolCallID == "toolu_01B" &&
				u[2].Source == core.ToolSourceServer && u[2].Name == "web_search" && len(r.ToolResults) == 1 &&
				r.ToolResults[0].Source == core.ToolSourceServer && r.ToolResults[0].ToolCallID == "srvtoolu_01C" &&
				!r.ToolResults[0].IsError && r.ToolResults[0].ResultBytes > 0, r)
		}},
		{"server_tool_error", func(r core.CallRecord) string {
			x := r.ToolResults
			return check(r.Status == 200 && r.ErrorClass == "" && len(r.ToolUses) == 2 && len(x) == 2 &&
				x[0].Source == core.ToolSourceServer && x[0].ToolCallID == "srvtoolu_01A" && x[0].IsError && x[0].ResultBytes > 0 &&
				x[1].Source == core.ToolSourceServer && x[1].ToolCallID == "srvtoolu_01B" && x[1].IsError && x[1].ResultBytes > 0, r)
		}},
		{"tool_results_next_request", func(r core.CallRecord) string {
			x := r.ToolResults
			// Only the trailing user messages count; the older result was recorded by its own call.
			return check(len(x) == 2 && x[0].ToolCallID == "toolu_01A" && !x[0].IsError &&
				x[0].ResultBytes == len("FAKEPROMPT-file-a-contents-héllo") &&
				x[1].ToolCallID == "toolu_01B" && x[1].IsError && x[1].ResultBytes > len("FAKEPROMPT-err-text") &&
				len(r.ToolUses) == 0, r)
		}},
		{"cache_read_write", func(r core.CallRecord) string {
			return check(r.Usage == core.Usage{InputUncached: 10, CacheRead: 1500, CacheWrite5m: 200, CacheWrite1h: 500, Output: 20}, r)
		}},
		{"cache_write_unsplit", func(r core.CallRecord) string {
			return check(r.Usage.CacheWrite5m == 40 && r.Usage.CacheWrite1h == 0, r)
		}},
		{"error_429", func(r core.CallRecord) string {
			return check(r.ErrorType == "rate_limit_error" && r.ErrorClass == "" && r.Status == 429 && r.UsageRaw == nil && r.Usage == core.Usage{}, r)
		}},
		{"error_529", func(r core.CallRecord) string {
			return check(r.ErrorType == "overloaded_error" && r.Status == 529, r)
		}},
		{"midstream_error", func(r core.CallRecord) string {
			return check(r.Status == 200 && r.ErrorType == "overloaded_error" && r.ErrorClass == "" && r.UsageEstimated, r)
		}},
		{"abort", func(r core.CallRecord) string {
			return check(r.ErrorClass == "client_abort" && r.UsageEstimated && !r.ExtractionPartial &&
				len(r.ToolUses) == 1 && r.ToolUses[0].ArgBytes > 0 && r.StopReason == "", r)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, ok := rec[tt.name]
			if !ok {
				t.Fatalf("no fixture %q", tt.name)
			}
			if msg := tt.want(r); msg != "" {
				t.Error(msg)
			}
		})
	}
}

func check(ok bool, r core.CallRecord) string {
	if ok {
		return ""
	}
	b, _ := json.Marshal(r)
	return "unexpected record: " + string(b)
}

// TestFakePromptScan is the property test of ADR 0014: no text from a request
// or a response reaches a record field. It collects every string value of the
// fixture bodies, except the values a record may carry by design (names, ids,
// types and the model), and wants none of them anywhere in the record JSON.
func TestFakePromptScan(t *testing.T) {
	allowedKeys := map[string]bool{
		"type": true, "id": true, "name": true, "model": true, "tool_use_id": true, "role": true,
		"stop_reason": true, "service_tier": true, "media_type": true, "request_id": true,
	}
	for _, f := range loadFixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			var texts []string
			collect := func(raw []byte) {
				var v any
				if json.Unmarshal(raw, &v) == nil {
					walkStrings(v, "", allowedKeys, &texts)
				}
			}
			collect(f.request)
			if f.meta.Streamed {
				for _, line := range strings.Split(string(f.response), "\n") {
					if data, ok := strings.CutPrefix(line, "data: "); ok {
						collect([]byte(data))
					}
				}
			} else {
				collect(f.response)
			}
			if len(texts) == 0 {
				t.Fatal("fixture has no text to scan for")
			}
			record := string(recordJSON(t, f.extract()))
			for _, s := range texts {
				if len(s) >= 4 && strings.Contains(record, s) {
					t.Errorf("record holds input text %q", s)
				}
			}
			if strings.Contains(record, "FAKEPROMPT") {
				t.Errorf("record holds a FAKEPROMPT marker")
			}
		})
	}
}

func walkStrings(v any, key string, skip map[string]bool, out *[]string) {
	switch x := v.(type) {
	case string:
		if !skip[key] {
			*out = append(*out, x)
		}
	case []any:
		for _, e := range x {
			walkStrings(e, key, skip, out)
		}
	case map[string]any:
		for k, e := range x {
			walkStrings(e, k, skip, out)
		}
	}
}

func TestNormalizeUsage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want core.Usage
	}{
		{"plain", `{"input_tokens":10,"output_tokens":5}`,
			core.Usage{InputUncached: 10, Output: 5}},
		{"cache read", `{"input_tokens":3,"cache_read_input_tokens":900,"output_tokens":5}`,
			core.Usage{InputUncached: 3, CacheRead: 900, Output: 5}},
		{"write split 5m and 1h", `{"input_tokens":1,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":200},"output_tokens":2}`,
			core.Usage{InputUncached: 1, CacheWrite5m: 100, CacheWrite1h: 200, Output: 2}},
		{"write 1h only", `{"cache_creation_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":50}}`,
			core.Usage{CacheWrite1h: 50}},
		{"write unsplit is 5m", `{"cache_creation_input_tokens":40,"output_tokens":9}`,
			core.Usage{CacheWrite5m: 40, Output: 9}},
		{"split short of the total", `{"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":150}}`,
			core.Usage{CacheWrite5m: 150, CacheWrite1h: 150}},
		{"split without a total", `{"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":8}}`,
			core.Usage{CacheWrite5m: 7, CacheWrite1h: 8}},
		{"output keeps thinking, reasoning unreported", `{"input_tokens":4,"output_tokens":4096}`,
			core.Usage{InputUncached: 4, Output: 4096}},
		{"negative is not a count", `{"input_tokens":-5,"output_tokens":2}`,
			core.Usage{Output: 2}},
		{"nothing", `{}`, core.Usage{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var u Usage
			setUsage(&u, gjson.Parse(tt.in))
			if got := NormalizeUsage(u); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestErrorClassOf(t *testing.T) {
	tests := map[string]core.ErrorClass{
		"overloaded_error": core.ErrorClassUpstream5xx, "api_error": core.ErrorClassUpstream5xx,
		"rate_limit_error": core.ErrorClassRateLimited, "authentication_error": core.ErrorClassAuth,
		"permission_error": core.ErrorClassAuth, "timeout_error": core.ErrorClassTimeout,
		"invalid_request_error": core.ErrorClassInvalidRequest, "not_found_error": core.ErrorClassInvalidRequest,
		"something_new": core.ErrorClassUpstream5xx,
	}
	for in, want := range tests {
		if got := ErrorClassOf(in); got != want {
			t.Errorf("ErrorClassOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMalformedRequestIsNotAPanic feeds hostile and broken bodies to the
// extractor. Each yields a record with null fields: no model, no tool halves,
// and, for a body that is not JSON, the partial flag.
func TestMalformedRequestIsNotAPanic(t *testing.T) {
	bodies := map[string]string{
		"not json":          "this is not json {",
		"truncated":         `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"tool_res`,
		"binary":            "\x00\x01\xff\xfe",
		"wrong top level":   `[1,2,3]`,
		"wrong model type":  `{"model":42,"messages":"nope"}`,
		"messages not list": `{"model":"m","messages":{"role":"user"}}`,
		"content not list":  `{"messages":[{"role":"user","content":7}]}`,
		"blocks not object": `{"messages":[{"role":"user","content":[1,"a",null,[]]}]}`,
		"result no id":      `{"messages":[{"role":"user","content":[{"type":"tool_result"}]}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			rec := core.CallRecord{Endpoint: "/v1/messages", Status: 200}
			Extractor{}.Extract(&rec, []byte(body), Facts{})
			if rec.Model != "" && name != "messages not list" {
				t.Errorf("model = %q, want none", rec.Model)
			}
			if rec.Usage != (core.Usage{}) || rec.UsageRaw != nil || rec.StopReason != "" || len(rec.ToolUses) != 0 {
				t.Errorf("response fields set from no response: %+v", rec)
			}
			if name == "not json" || name == "truncated" || name == "binary" {
				if !rec.ExtractionPartial || len(rec.ToolResults) != 0 {
					t.Errorf("partial = %v, results = %v", rec.ExtractionPartial, rec.ToolResults)
				}
			}
			for _, r := range rec.ToolResults {
				if r.ToolCallID != "" || r.ResultBytes != 0 || r.IsError {
					t.Errorf("result from a broken block: %+v", r)
				}
			}
		})
	}
	t.Run("empty body", func(t *testing.T) {
		rec := core.CallRecord{Endpoint: "/v1/models"}
		Extractor{}.Extract(&rec, nil, Facts{})
		if !reflect.DeepEqual(rec, core.CallRecord{Endpoint: "/v1/models"}) {
			t.Errorf("record changed: %+v", rec)
		}
	})
	t.Run("long model is dropped", func(t *testing.T) {
		rec := core.CallRecord{}
		ExtractRequest(&rec, []byte(`{"model":"`+strings.Repeat("a", maxFieldLen+1)+`"}`))
		if rec.Model != "" {
			t.Errorf("model kept at %d bytes", len(rec.Model))
		}
	})
	t.Run("count_tokens has no tool halves", func(t *testing.T) {
		rec := core.CallRecord{Endpoint: "/v1/messages/count_tokens"}
		Extractor{}.Extract(&rec, []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"x"}]}]}`),
			Facts{ToolUses: []core.ToolUse{{ToolCallID: "toolu_2"}}})
		if rec.Model != "m" || len(rec.ToolResults) != 0 || len(rec.ToolUses) != 0 {
			t.Errorf("record = %+v", rec)
		}
	})
}

func TestRequestToolResultsTrailingUserRun(t *testing.T) {
	result := func(id string) string {
		return `{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"FAKEPROMPT"}]}`
	}
	assistant := `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]}`
	text := `{"role":"user","content":[{"type":"text","text":"FAKEPROMPT"}]}`
	tests := []struct {
		name     string
		messages []string
		want     []string
	}{
		{"result then text", []string{assistant, result("toolu_1"), text}, []string{"toolu_1"}},
		{"text then result", []string{assistant, text, result("toolu_1")}, []string{"toolu_1"}},
		{"results in two messages", []string{assistant, result("toolu_1"), result("toolu_2")}, []string{"toolu_1", "toolu_2"}},
		{"resent history stays out", []string{result("toolu_0"), assistant, result("toolu_1"), text}, []string{"toolu_1"}},
		{"assistant last", []string{result("toolu_0"), assistant}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := core.CallRecord{Endpoint: "/v1/messages"}
			ExtractRequest(&rec, []byte(`{"messages":[`+strings.Join(tt.messages, ",")+`]}`))
			var got []string
			for _, r := range rec.ToolResults {
				got = append(got, r.ToolCallID)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("results = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUntrustedStringsAreNotKept: a kept string field holds a JSON string within
// the bound or nothing. gjson would otherwise render an object as its raw text.
func TestUntrustedStringsAreNotKept(t *testing.T) {
	long := `"` + strings.Repeat("a", maxFieldLen+1) + `"`
	bad := map[string]string{
		"object": `{"x":"FAKEPROMPT"}`, "array": `["FAKEPROMPT"]`, "number": `42`, "bool": `true`, "too long": long,
	}
	for name, v := range bad {
		t.Run(name, func(t *testing.T) {
			rec := core.CallRecord{Endpoint: "/v1/messages"}
			ExtractRequest(&rec, []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":`+v+`,"content":"x"}]}]}`))
			if len(rec.ToolResults) != 1 || rec.ToolResults[0].ToolCallID != "" {
				t.Errorf("request tool results = %+v", rec.ToolResults)
			}

			blocks := `{"type":"tool_use","id":` + v + `,"name":` + v + `,"input":{}},` +
				`{"type":"web_search_tool_result","tool_use_id":` + v + `,"content":[]}`
			body, _ := scanChunks(false, []byte(`{"type":"message","stop_reason":`+v+`,"content":[`+blocks+`]}`))
			stream, _ := scanChunks(true, []byte(
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":`+v+`,"name":`+v+`,"input":{}}}`+"\n\n"+
					`data: {"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":`+v+`,"content":[]}}`+"\n\n"+
					`data: {"type":"message_delta","delta":{"stop_reason":`+v+`}}`+"\n\n"+
					`data: {"type":"error","error":{"type":`+v+`}}`+"\n\n"))
			for scanner, f := range map[string]Facts{"body": body, "stream": stream} {
				if len(f.ToolUses) != 1 || f.ToolUses[0].ToolCallID != "" || f.ToolUses[0].Name != "" {
					t.Errorf("%s tool uses = %+v", scanner, f.ToolUses)
				}
				if len(f.ToolResults) != 1 || f.ToolResults[0].ToolCallID != "" {
					t.Errorf("%s tool results = %+v", scanner, f.ToolResults)
				}
				if f.StopReason != "" || f.ErrorType != "" {
					t.Errorf("%s stop reason %q, error type %q", scanner, f.StopReason, f.ErrorType)
				}
			}
		})
	}
	t.Run("error body type", func(t *testing.T) {
		f, _ := scanChunks(false, []byte(`{"type":"error","error":{"type":{"x":"FAKEPROMPT"}}}`))
		if f.ErrorType != "" {
			t.Errorf("error type = %q", f.ErrorType)
		}
	})
	t.Run("is_error must be a JSON true", func(t *testing.T) {
		rec := core.CallRecord{Endpoint: "/v1/messages"}
		ExtractRequest(&rec, []byte(`{"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"a","is_error":"true"},
			{"type":"tool_result","tool_use_id":"b","is_error":true}]}]}`))
		if len(rec.ToolResults) != 2 || rec.ToolResults[0].IsError || !rec.ToolResults[1].IsError {
			t.Errorf("tool results = %+v", rec.ToolResults)
		}
	})
}

func TestServerToolResultsInBody(t *testing.T) {
	f, _ := scanChunks(false, []byte(`{"type":"message","content":[
		{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}},
		{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","title":"t"}]},
		{"type":"server_tool_use","id":"srvtoolu_2","name":"code_execution","input":{}},
		{"type":"code_execution_tool_result","tool_use_id":"srvtoolu_2","content":{"type":"code_execution_tool_result_error","error_code":"unavailable"}},
		{"type":"mcp_tool_result","tool_use_id":"mcptoolu_3","is_error":true,"content":[]}]}`))
	want := []core.ToolResult{
		{Source: core.ToolSourceServer, ToolCallID: "srvtoolu_1", ResultBytes: len(`[{"type":"web_search_result","title":"t"}]`)},
		{Source: core.ToolSourceServer, ToolCallID: "srvtoolu_2", IsError: true,
			ResultBytes: len(`{"type":"code_execution_tool_result_error","error_code":"unavailable"}`)},
	}
	if !reflect.DeepEqual(f.ToolResults, want) {
		t.Errorf("results = %+v, want %+v", f.ToolResults, want)
	}
}

func TestToolHalfOrderAndEndpoint(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"x"}]}]}`)
	facts := Facts{ToolResults: []core.ToolResult{{Source: core.ToolSourceServer, ToolCallID: "srvtoolu_2"}}}

	rec := core.CallRecord{Endpoint: "/v1/messages"}
	Extractor{}.Extract(&rec, body, facts)
	if len(rec.ToolResults) != 2 || rec.ToolResults[0].ToolCallID != "toolu_1" || rec.ToolResults[1].ToolCallID != "srvtoolu_2" {
		t.Errorf("results = %+v", rec.ToolResults)
	}

	rec = core.CallRecord{Endpoint: "/v1/messages/count_tokens"}
	Extractor{}.Extract(&rec, body, facts)
	if len(rec.ToolResults) != 0 {
		t.Errorf("count_tokens results = %+v", rec.ToolResults)
	}
}

// A Claude Code request names its working directory in the system prompt. The
// project is the final directory name and never the path (ADR 0059).
func TestExtractProject(t *testing.T) {
	env := func(line string) string { return "# Environment\n - " + line + "\n - Platform: linux" }
	block := func(text string) string {
		b, _ := json.Marshal(text)
		return `{"type":"text","text":` + string(b) + `}`
	}
	str := func(text string) string {
		b, _ := json.Marshal(text)
		return `{"model":"m","system":` + string(b) + `}`
	}
	blocks := func(texts ...string) string {
		var parts []string
		for _, s := range texts {
			parts = append(parts, block(s))
		}
		return `{"model":"m","system":[` + strings.Join(parts, ",") + `]}`
	}
	pad := strings.Repeat("x", 40<<10)

	tests := []struct {
		name string
		body string
		want string
	}{
		{"string system", str(env("Primary working directory: /home/ann/code/spyglass")), "spyglass"},
		{"text blocks", blocks("You are an agent.", env("Primary working directory: /home/ann/code/spyglass")), "spyglass"},
		{"older env block", str("<env>\nWorking directory: /srv/app/billing\nPlatform: linux\n</env>"), "billing"},
		{"trailing slash", str(env("Primary working directory: /home/ann/code/spyglass/")), "spyglass"},
		{"windows path", str(env(`Primary working directory: C:\Users\ann\code\spyglass`)), "spyglass"},
		{"windows drive root", str(env(`Primary working directory: C:\`)), ""},
		{"unix root", str(env("Primary working directory: /")), ""},
		{"name with a space", str(env("Primary working directory: /home/ann/my project")), "my project"},
		{"additional directories are not the project", str("# Environment\n - Additional working directories:\n  - /home/ann/other\n"), ""},
		{"primary wins over a later line", str(env("Primary working directory: /a/first") + "\n - Working directory: /a/second"), "first"},
		{"no match", str("You are a helpful assistant."), ""},
		{"no system field", `{"model":"m","messages":[]}`, ""},
		{"system of another type", `{"model":"m","system":42}`, ""},
		{"name over 128 characters", str(env("Primary working directory: /a/" + strings.Repeat("n", 129))), ""},
		{"name of 128 characters", str(env("Primary working directory: /a/" + strings.Repeat("n", 128))), strings.Repeat("n", 128)},
		{"control character in the name", str(env("Primary working directory: /a/bad\x07name")), ""},
		{"line after the first 32 KiB", str(pad + "\n" + env("Primary working directory: /a/late")), ""},
		{"line before the 32 KiB bound", str(env("Primary working directory: /a/early") + "\n" + pad), "early"},
		{"line cut by the 32 KiB bound", str(pad[:32<<10-30] + "\n - Primary working directory: /a/projec" + pad), ""},
		{"block after the first 32 KiB", blocks(pad, env("Primary working directory: /a/late")), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := core.CallRecord{Endpoint: "/v1/messages"}
			ExtractRequest(&rec, []byte(tt.body))
			if rec.Project != tt.want {
				t.Errorf("project = %q, want %q", rec.Project, tt.want)
			}
			if rec.ExtractionPartial {
				t.Error("detection marked the record partial")
			}
		})
	}
}

// A project the header or the key already set is never replaced by detection.
func TestExtractProjectKeepsAnEarlierProject(t *testing.T) {
	body := `{"model":"m","system":"# Environment\n - Primary working directory: /a/detected\n"}`
	rec := core.CallRecord{Endpoint: "/v1/messages", Project: "from-header"}
	ExtractRequest(&rec, []byte(body))
	if rec.Project != "from-header" {
		t.Errorf("project = %q, want from-header", rec.Project)
	}
}

// A body that is not JSON gives no project, and the request still succeeds.
func TestExtractProjectOfABrokenBody(t *testing.T) {
	rec := core.CallRecord{Endpoint: "/v1/messages"}
	ExtractRequest(&rec, []byte(`{"system": "Primary working directory: /a/x`))
	if rec.Project != "" || !rec.ExtractionPartial {
		t.Errorf("project %q partial %v, want none and partial (a body that is not JSON)", rec.Project, rec.ExtractionPartial)
	}
}

// The environment block of the Claude Code fixture gives the directory name,
// not the path. It carries no git remote, so no repository name is read.
func TestFixtureProject(t *testing.T) {
	for _, f := range loadFixtures(t) {
		want := ""
		if f.name == "claude_code_project" {
			want = "spyglass-demo"
		}
		if got := f.extract().Project; got != want {
			t.Errorf("%s: project = %q, want %q", f.name, got, want)
		}
	}
}
