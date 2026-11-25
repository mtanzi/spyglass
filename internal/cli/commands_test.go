package cli

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"
)

const detailJSON = `{"id":"` + id1 + `","ts":"2026-11-13T14:10:00Z","key":"alice","client":"claude-code","client_raw":"claude-cli/2.1","client_request_id":"r1",
 "session_key":"fm:7f3a","agent_id":"main","parent_agent_id":"","agent_type":"","prompt_id":"p1","request_class":"main","project":"spyglass","compaction":false,
 "context_compacted":false,"trace_id":"","family":"anthropic","endpoint":"/v1/messages","auth_mode":"client_passthrough","provider":"claude-max",
 "model":"claude-opus-4-8","upstream_request_id":"req_1","status":200,"error_class":"","error_type":"","stop_reason":"end_turn","streamed":true,
 "ttfb_ms":300,"ttft_ms":420,"duration_ms":3100,"ended_at":"2026-11-13T14:10:03Z",
 "usage":{"input_uncached":10,"cache_read":200,"cache_write_5m":30,"cache_write_1h":4,"output":55,"reasoning":5},"usage_raw":{"input_tokens":10},
 "usage_estimated":false,"extraction_partial":false,"cost_micro_usd":1234500,"pricing_version":"2026-10-01","prev_tool_durations":"",
 "tool_uses":[{"source":"client","tool_call_id":"toolu_1","name":"Bash","arg_bytes":42}],
 "tool_results":[{"source":"client","tool_call_id":"toolu_0","is_error":true,"result_bytes":7}]}`

func sessionDetailJSON() string {
	return `{"summary":` + sessionJSON("team/a:1", 3, 1, "1234500", 1) + `,
 "agents":[{"agent_id":"main","parent_agent_id":"","agent_type":"","calls":2},{"agent_id":"sub1","parent_agent_id":"main","agent_type":"Explore","calls":1}],
 "prompts":[{"prompt_id":"p1","first_ts":"2026-11-13T09:00:00Z","calls":3,"usage":{"input_uncached":1,"cache_read":2,"cache_write_5m":3,"cache_write_1h":4,"output":5,"reasoning":0},"cost_micro_usd":null}],
 "calls":[` + callJSON(id1, "2026-11-13T14:10:00Z", "claude-opus-4-8", "claude-max", 200, "", "1234500") + `,` +
		callJSON(id2, "2026-11-13T14:05:00Z", "claude-opus-4-8", "claude-max", 200, "", "null") + `]}`
}

var homeSessions = series("session",
	bucket(day, "fm:7f3a", 188, 2, 1000, 500, 9812000, 0, 5000, "0.5"),
	bucket(day, "fm:21c0", 95, 0, 1000, 500, 4200700, 14, 5000, "0.5"),
	bucket(day, "team/a:1", 40, 3, 1000, 500, 1900500, 0, 5000, "null"),
	bucket(day, "", 5, 0, 1000, 500, 100, 0, 5000, "null"))

var homeTotal = series("", bucket(day, "", 412, 9, 3120000, 210000, 18421300, 14, 9400, "0.8013"))

func TestHomeViewMakesTwoDayGrainRequestsOverTheUTCDay(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/usage": homeRoute(homeTotal, homeSessions)})
	out, code := f.cli()
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	want(t, out, `
window:
  from: "2026-11-13T00:00:00Z"
  to: "2026-11-13T14:20:30Z"
  default: false
today:
  calls: 412
  errors: 9
  error_rate: 0.0218
  cost_usd: 18.4213
  unpriced_calls: 14
  tokens_in: 3120000
  tokens_out: 210000
  cache_hit_ratio: 0.8013
  latency_p95_ms: 9400
top_sessions[3]{session_key,calls,cost_usd,unpriced_calls}:
  "fm:7f3a",188,9.812,0
  "fm:21c0",95,4.2007,14
  "team/a:1",40,1.9005,0
calls_without_session: 5
notes[1]:
  - "14 calls have no price: cost_usd leaves them out, and unknown is not zero"
help[5]:
  - "spyglass sessions show fm:7f3a"
  - "spyglass sessions show fm:21c0"
  - "spyglass calls list --from 2026-11-13T00:00:00Z --to 2026-11-13T14:20:30Z --fields id,model,provider,cost_usd   # unpriced calls show cost_usd: unpriced"
  - "spyglass calls list --from 2026-11-13T00:00:00Z --to 2026-11-13T14:20:30Z --fields id,model,status,error_class"
  - spyglass config
`)
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("the home view made %d requests, want 2 usage requests and no /admin/config: %v", len(reqs), reqs)
	}
	for i, wantGroup := range []string{"", "session"} {
		r := reqs[i]
		if r.Path != "/admin/usage" || r.Query.Get("grain") != "day" || r.Query.Get("group_by") != wantGroup ||
			r.Query.Get("from") != "2026-11-13T00:00:00Z" || r.Query.Get("to") != "2026-11-13T14:20:30Z" {
			t.Errorf("request %d = %s %v, want /admin/usage over the UTC day with grain=day and group_by=%q", i, r.Path, r.Query, wantGroup)
		}
	}
}

func TestHomeViewRanksByCostAndKeepsUnpricedSessionsHonest(t *testing.T) {
	// Input order is not cost order; a session with only unpriced calls has cost 0.
	sessions := series("session",
		bucket(day, "cheap", 5, 0, 1, 1, 10000, 0, 1, "null"),
		bucket(day, "none", 3, 0, 1, 1, 0, 3, 1, "null"),
		bucket(day, "dear", 5, 0, 1, 1, 5000000, 0, 1, "null"))
	f := newFakeAdmin(t, map[string]route{"/admin/usage": homeRoute(homeTotal, sessions)})
	out, _ := f.cli()
	for _, w := range []string{"dear,5,5,0\n  cheap,5,0.01,0\n  none,3,unpriced,3"} {
		if !strings.Contains(out, w) {
			t.Errorf("ranking missing %q in:\n%s", w, out)
		}
	}
}

func TestHomeViewEmptyDay(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/usage": homeRoute(series(""), series("session"))})
	out, code := f.cli()
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	want(t, out, `
window:
  from: "2026-11-13T00:00:00Z"
  to: "2026-11-13T14:20:30Z"
  default: false
today:
  calls: 0
top_sessions[0]:
notes[1]:
  - "No calls between 2026-11-13T00:00:00Z to 2026-11-13T14:20:30Z UTC. A new UTC day starts empty; use spyglass calls list --since 24h for a longer window"
help[1]:
  - spyglass config
`)
}

func TestCallsListDefaultsAndParameters(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/calls": {schema: "CallList", body: callList3(`"eyJ0cyI6"`, 134, false)},
		"/admin/config": {schema: "ConfigView", body: configJSON}})
	out, code := f.cli("calls", "list", "--limit", "3", "--model", "claude-opus-4-8", "--model", "claude-sonnet-5-5",
		"--status", "200", "--status=429", "--streamed", "--cost-micro-usd-gte", "1000", "--error-class", "rate_limited")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	want(t, out, `
window:
  from: "2026-11-12T14:20:30Z"
  to: "2026-11-13T14:20:30Z"
  default: true
filters:
  model: "claude-opus-4-8,claude-sonnet-5-5"
  streamed: true
  status: "200,429"
  error_class: rate_limited
  cost_micro_usd_gte: 1000
count: 134
showing: 3
next_page: true
calls[3]{id,model,status,cost_usd}:
  0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b,claude-opus-4-8,200,1.2345
  0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c,claude-sonnet-5-5,429,unpriced
  0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5d,claude-opus-4-8,200,9.99
notes[1]:
  - "1 of the shown calls: cost_usd is unpriced because no price matched, and unknown is not zero"
help[3]:
  - spyglass calls show 0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b
  - "spyglass calls list --cost-micro-usd-gte=1000 --error-class=rate_limited --limit=3 --model=claude-opus-4-8 --model=claude-sonnet-5-5 --status=200 --status=429 --streamed=true --from 2026-11-12T14:20:30Z --to 2026-11-13T14:20:30Z --cursor eyJ0cyI6"
  - spyglass describe
`)
	var call request
	for _, r := range f.requests() {
		if r.Path == "/admin/calls" {
			call = r
		}
	}
	q := call.Query
	if q.Get("limit") != "3" || q.Get("from") != "2026-11-12T14:20:30Z" || q.Get("to") != "2026-11-13T14:20:30Z" ||
		strings.Join(q["model"], ",") != "claude-opus-4-8,claude-sonnet-5-5" || strings.Join(q["status"], ",") != "200,429" ||
		q.Get("streamed") != "true" || q.Get("cost_micro_usd_gte") != "1000" || q.Get("error_class") != "rate_limited" {
		t.Errorf("request query = %v", q)
	}
	if got := call.Query.Get("cursor"); got != "" {
		t.Errorf("first page sent cursor %q", got)
	}
}

func TestCallsListNextPageCommandRoundTrips(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/calls": {schema: "CallList", body: callList3(`"abc"`, 9, false)},
		"/admin/config": {schema: "ConfigView", body: configJSON}})
	out, _ := f.cli("calls", "list", "--since", "2h", "--model", "x y")
	var next string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "--cursor") {
			next = strings.Trim(strings.TrimPrefix(strings.TrimSpace(l), "- "), `"`)
		}
	}
	if !strings.Contains(next, "--model='x y'") || !strings.Contains(next, "--from 2026-11-13T12:20:30Z --to 2026-11-13T14:20:30Z") {
		t.Fatalf("next-page line = %q: it must keep the filters and pin the window", next)
	}
}

func TestCallsListCappedCountAndAPIEquivalent(t *testing.T) {
	list := `{"calls":[` + callJSON(id1, "2026-11-13T14:10:00Z", "claude-opus-4-8", "claude-max", 200, "", "1234500") + `,` +
		callJSON(id2, "2026-11-13T14:05:00Z", "claude-opus-4-8", "anthropic", 200, "", "10000") + `],"next_cursor":null,"count":10000,"count_capped":true}`
	f := newFakeAdmin(t, map[string]route{"/admin/calls": {schema: "CallList", body: list}, "/admin/config": {schema: "ConfigView", body: configJSON}})
	out, code := f.cli("calls", "list")
	if code != 0 {
		t.Fatal(out)
	}
	want(t, out, `
window:
  from: "2026-11-12T14:20:30Z"
  to: "2026-11-13T14:20:30Z"
  default: true
count: 10000+
showing: 2
next_page: false
calls[2]{id,model,status,cost_usd,api_equivalent}:
  0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b,claude-opus-4-8,200,1.2345,true
  0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c,claude-opus-4-8,200,0.01,false
notes[1]:
  - "cost_usd of a row with api_equivalent true is an API-equivalent price, not a bill (client_passthrough provider, ADR 0052)"
help[2]:
  - spyglass calls show 0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b
  - spyglass describe
`)
}

func TestCallsListFieldsSelectDeclaredColumns(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/calls": {schema: "CallList", body: callList3("null", 3, false)},
		"/admin/config": {schema: "ConfigView", body: configJSON}})
	out, code := f.cli("calls", "list", "--fields", "ts,tokens_in,tokens_out,duration_ms,error_class")
	if code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(out, "calls[3]{ts,tokens_in,tokens_out,duration_ms,error_class}:\n  \"2026-11-13T14:10:00Z\",244,55,3100,\"\"\n") {
		t.Errorf("--fields output:\n%s", out)
	}
	out, code = f.cli("calls", "list", "--fields", "nope")
	if code != 2 || !strings.Contains(out, "error: usage") || !strings.Contains(out, `is not a field`) || !strings.Contains(out, "cost_usd") {
		t.Errorf("unknown field: code=%d\n%s", code, out)
	}
}

func TestEmptyStatesNameWindowAndFilters(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{
		"/admin/calls":    {schema: "CallList", body: `{"calls":[],"next_cursor":null,"count":0,"count_capped":false}`},
		"/admin/sessions": {schema: "SessionList", body: `{"sessions":[],"next_cursor":null}`},
	})
	out, code := f.cli("calls", "list", "--model", "ghost", "--since", "1h")
	if code != 0 {
		t.Fatal(out)
	}
	want(t, out, `
window:
  from: "2026-11-13T13:20:30Z"
  to: "2026-11-13T14:20:30Z"
  default: false
filters:
  model: ghost
count: 0
showing: 0
next_page: false
calls[0]:
empty: "No calls from 2026-11-13T13:20:30Z to 2026-11-13T14:20:30Z UTC with filters model=ghost."
help[2]:
  - spyglass calls list --since 7d
  - spyglass describe
`)
	out, _ = f.cli("sessions", "list")
	want(t, out, `
window:
  from: "2026-11-12T14:20:30Z"
  to: "2026-11-13T14:20:30Z"
  default: true
showing: 0
next_page: false
sessions[0]:
empty: "No sessions from 2026-11-12T14:20:30Z to 2026-11-13T14:20:30Z UTC with no filters."
help[1]:
  - spyglass sessions list --since 7d
`)
}

func TestCallsShow(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/calls/" + id1: {schema: "CallDetail", body: detailJSON}})
	out, code := f.cli("calls", "show", id1)
	if code != 0 {
		t.Fatal(out)
	}
	for _, w := range []string{
		"id: " + id1, "auth_mode: client_passthrough", "tokens_in: 244", "cache_write_1h_tokens: 4",
		`usage_raw: "{\"input_tokens\":10}"`, "cost_usd: 1.2345", "api_equivalent: true", "ended_at: \"2026-11-13T14:10:03Z\"",
		"tool_uses[1]{name,source,tool_call_id,arg_bytes}:\n  Bash,client,toolu_1,42",
		"tool_results[1]{tool_call_id,source,is_error,result_bytes}:\n  toolu_0,client,true,7",
		"API-equivalent price, not a bill", "spyglass sessions show fm:7f3a",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("calls show lacks %q", w)
		}
	}
	for _, r := range f.requests() {
		if r.Path == "/admin/config" {
			t.Error("calls show must read auth_mode from the call, not request /admin/config")
		}
	}
}

func TestCallsShowErrors(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{
		"/admin/calls/" + id2:    {status: 404, schema: "ErrorResponse", body: `{"error":{"type":"not_found","message":"no such call"}}`},
		"/admin/calls/not-an-id": {status: 400, schema: "ErrorResponse", body: `{"error":{"type":"invalid_id","message":"not a call id"}}`},
	})
	out, code := f.cli("calls", "show", id2)
	want(t, out, `
error: not_found
message: no such call
help[1]:
  - spyglass describe
`)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	out, code = f.cli("calls", "show", "not-an-id")
	if code != 1 || !strings.HasPrefix(out, "error: invalid_id\n") {
		t.Errorf("invalid id: code=%d\n%s", code, out)
	}
	if _, code := f.cli("calls", "show"); code != 2 {
		t.Errorf("missing id: code = %d, want 2", code)
	}
}

func TestSessionsList(t *testing.T) {
	body := `{"sessions":[` + sessionJSON("fm:7f3a", 188, 2, "9812000", 0) + `,` + sessionJSON("fm:21c0", 95, 0, "4200700", 14) + `,` +
		sessionJSON("lone", 3, 0, "null", 3) + `],"next_cursor":"eyJ0cyI6"}`
	f := newFakeAdmin(t, map[string]route{"/admin/sessions": {schema: "SessionList", body: body}})
	out, code := f.cli("sessions", "list", "--limit", "3", "--client", "claude-code")
	if code != 0 {
		t.Fatal(out)
	}
	want(t, out, `
window:
  from: "2026-11-12T14:20:30Z"
  to: "2026-11-13T14:20:30Z"
  default: true
filters:
  client: claude-code
showing: 3
next_page: true
sessions[3]{session_key,calls,errors,cost_usd}:
  "fm:7f3a",188,2,9.812
  "fm:21c0",95,0,4.2007
  lone,3,0,unpriced
notes[2]:
  - "1 of the shown sessions: cost_usd sums the priced calls only; add unpriced_calls to --fields for the count left out"
  - "1 of the shown sessions: cost_usd is unpriced because no call has a price, and unknown is not zero"
help[2]:
  - "spyglass sessions list --client=claude-code --limit=3 --from 2026-11-12T14:20:30Z --to 2026-11-13T14:20:30Z --cursor eyJ0cyI6"
  - "spyglass sessions show fm:7f3a"
`)
	out, _ = f.cli("sessions", "list", "--fields", "session_key,unpriced_calls")
	if !strings.Contains(out, "sessions[3]{session_key,unpriced_calls}:\n  \"fm:7f3a\",0\n  \"fm:21c0\",14\n  lone,3\n") {
		t.Errorf("--fields unpriced_calls:\n%s", out)
	}
	if reqs := f.requests(); reqs[0].Query.Get("limit") != "3" || reqs[0].Query["client"][0] != "claude-code" {
		t.Errorf("query = %v", reqs[0].Query)
	}
}

func TestSessionsShowEncodesTheKeyAndTruncates(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{
		"/admin/sessions/team%2Fa%3A1": {schema: "SessionDetail", body: sessionDetailJSON()},
		"/admin/config":                {schema: "ConfigView", body: configJSON},
	})
	out, code := f.cli("sessions", "show", "team/a:1", "--limit", "1")
	if code != 0 {
		t.Fatal(out)
	}
	for _, w := range []string{
		"session:\n  session_key: \"team/a:1\"\n", "  unpriced_calls: 1\n", "  cost_usd: 1.2345\n",
		"  models: claude-opus-4-8;claude-haiku-5-5\n",
		"agents[2]{agent_id,parent_agent_id,agent_type,calls}:\n  main,\"\",\"\",2\n  sub1,main,Explore,1\n",
		"prompts[1]{prompt_id,first_ts,calls,tokens_in,tokens_out,cost_usd}:\n  p1,\"2026-11-13T09:00:00Z\",3,10,5,unpriced\n",
		"calls[1]{id,model,status,cost_usd,api_equivalent}:\n  " + id1 + ",claude-opus-4-8,200,1.2345,true\n",
		"totals:\n  agents: 2\n  prompts: 1\n  calls: 2\n  showing_prompts: 1\n  showing_calls: 1\n",
		"cost_usd sums the priced calls only; 1 call is left out (unpriced_calls)",
		"showing the first 1 of 2 calls in API order",
		"spyglass calls list --session team/a:1 --from 2026-11-13T09:00:00Z --to 2026-11-13T14:10:01Z",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("sessions show lacks %q:\n%s", w, out)
		}
	}
	if got := f.requests()[0].Path; got != "/admin/sessions/team%2Fa%3A1" {
		t.Errorf("path = %q, want the key percent-encoded", got)
	}
	if code := func() int { _, c := f.cli("sessions", "show"); return c }(); code != 2 {
		t.Errorf("missing key: code = %d, want 2", code)
	}
}

func TestSessionsShowNotFound(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/sessions/nope": {status: 404, schema: "ErrorResponse", body: `{"error":{"type":"not_found","message":"no such session"}}`}})
	out, code := f.cli("sessions", "show", "nope")
	if code != 1 || !strings.HasPrefix(out, "error: not_found\nmessage: no such session\n") {
		t.Errorf("code=%d\n%s", code, out)
	}
}

func TestInvalidParameterErrorKeepsTheAPIType(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/calls": {status: 400, schema: "ErrorResponse",
		body: `{"error":{"type":"invalid_parameter","message":"cursor is bad","parameter":"cursor"}}`}})
	out, code := f.cli("calls", "list", "--cursor", "zzz")
	if code != 1 || !strings.Contains(out, "error: invalid_parameter\n") || !strings.Contains(out, "parameter: cursor\n") {
		t.Errorf("code=%d\n%s", code, out)
	}
}

func TestConfig(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/config": {schema: "ConfigView", body: configJSON}})
	out, code := f.cli("config")
	if code != 0 {
		t.Fatal(out)
	}
	want(t, out, `
providers[2]{name,kind,auth_mode,base_url}:
  anthropic,anthropic,proxy_key,"https://api.anthropic.com"
  claude-max,anthropic,client_passthrough,"https://api.anthropic.com"
pricing[1]{provider,model,from,input_usd_per_mtok,output_usd_per_mtok,cache_read_usd_per_mtok,cache_write_5m_usd_per_mtok,cache_write_1h_usd_per_mtok}:
  anthropic,claude-opus-4-8,2026-10-01,15,75,1.5,18.75,30
keys[2]{name,disabled}:
  alice,false
  bob,true
help[2]:
  - spyglass describe
  - spyglass calls list --since 24h
`)
}

func TestDescribeIsDrivenByTheColumnTables(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/config": {schema: "ConfigView", body: configJSON}})
	out, code := f.cli("describe")
	if code != 0 {
		t.Fatal(out)
	}
	for _, tb := range allTables() {
		key := strings.ReplaceAll(tb.ID, ".", "_") + "_fields[" // one table per row type
		if !strings.Contains(out, key) {
			t.Errorf("describe lacks the table %s", tb.ID)
		}
		for _, c := range tb.Cols {
			if !strings.Contains(out, "\n  "+c.Name+",") {
				t.Errorf("describe lacks field %s of %s", c.Name, tb.ID)
			}
		}
	}
	for _, w := range []string{"never_stored[4]:", "request and response bodies", "the current UTC day", "last 24 hours",
		"providers[2]{name,kind,auth_mode}:", "keys[2]{name,disabled}:", "unknown is not zero"} {
		if !strings.Contains(out, w) {
			t.Errorf("describe lacks %q", w)
		}
	}
}

func TestUsageErrorsExitTwoOnStdout(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{})
	cases := [][]string{
		{"nope"},
		{"calls"},
		{"calls", "nope"},
		{"sessions", "list", "--nope"},
		{"calls", "list", "--limit", "0"},
		{"calls", "list", "--limit", "501"},
		{"calls", "list", "--since", "1w"},
		{"calls", "list", "--since", "1h", "--from", "2026-11-13T00:00:00Z"},
		{"calls", "list", "--from", "yesterday"},
		{"calls", "list", "--status", "x"},
		{"calls", "list", "--latency-ms-gte", "-1"},
		{"calls", "list", "--streamed=maybe"},
		{"calls", "list", "extra"},
		{"calls", "list", "--from", "2026-11-13T10:00:00Z", "--to", "2026-11-13T09:00:00Z"},
		{"config", "--now"},
		{"describe", "extra"},
		{"sessions", "show", "a", "b"},
	}
	for _, args := range cases {
		out, code := f.cli(args...)
		if code != 2 || !strings.HasPrefix(out, "error: usage\nmessage: ") || !strings.Contains(out, "help[") {
			t.Errorf("%v: code=%d\n%s", args, code, out)
		}
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("a usage error made %d requests", n)
	}
}

func TestEverySubcommandHasHelpWithAnExample(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{})
	for _, args := range [][]string{{"describe"}, {"config"}, {"sessions", "list"}, {"sessions", "show"}, {"calls", "list"}, {"calls", "show"}} {
		for _, flag := range []string{"--help", "-h"} {
			out, code := f.cli(append(append([]string{}, args...), flag)...)
			if code != 0 || !strings.HasPrefix(out, "usage: spyglass "+strings.Join(args, " ")) || !strings.Contains(out, "examples:\n  spyglass ") {
				t.Errorf("%v %s: code=%d\n%s", args, flag, code, out)
			}
		}
	}
	out, code := f.cli("--help")
	if code != 0 || !strings.Contains(out, "examples:") || !strings.Contains(out, "SPYGLASS_ADMIN_URL") {
		t.Errorf("top help: code=%d\n%s", code, out)
	}
	if len(f.requests()) != 0 {
		t.Error("help made a request")
	}
}

func TestUnreachableServerNamesTheURLAndTheVariable(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{})
	url := f.srv.URL
	f.srv.Close()
	out, code := runCLI(url, "calls", "list")
	if code != 1 || !strings.HasPrefix(out, "error: unreachable\nmessage: ") ||
		!strings.Contains(out, url) || !strings.Contains(out, "SPYGLASS_ADMIN_URL") {
		t.Errorf("code=%d\n%s", code, out)
	}
	// The default is the default listen.admin address; no request is made, since
	// a real server may listen there.
	if c, err := newAdminClient(func(string) string { return "" }); err != nil || c.base != DefaultAdminURL || DefaultAdminURL != "http://127.0.0.1:9090" {
		t.Errorf("default client = %+v, %v", c, err)
	}
	if c, _ := newAdminClient(func(string) string { return "http://example.test:9090/" }); c.base != "http://example.test:9090" {
		t.Errorf("trailing slash kept: %q", c.base)
	}
	out, code = runCLI("ftp://nowhere", "config")
	if code != 1 || !strings.HasPrefix(out, "error: invalid_admin_url\n") {
		t.Errorf("bad URL: code=%d\n%s", code, out)
	}
}

func TestBadResponses(t *testing.T) {
	f := newFakeAdmin(t, map[string]route{"/admin/config": {body: "<html>not the api</html>"}, "/admin/calls": {status: 502, body: "bad gateway"}})
	out, code := f.cli("config")
	if code != 1 || !strings.HasPrefix(out, "error: bad_response\n") {
		t.Errorf("code=%d\n%s", code, out)
	}
	out, code = f.cli("calls", "list")
	if code != 1 || !strings.HasPrefix(out, "error: bad_response\n") || !strings.Contains(out, "502") {
		t.Errorf("code=%d\n%s", code, out)
	}
}

func TestSinceUnits(t *testing.T) {
	ok := map[string]string{"30m": "30m0s", "24h": "24h0m0s", "7d": "168h0m0s", "1d": "24h0m0s", "90m": "1h30m0s"}
	for in, want := range ok {
		d, err := parseSince(in)
		if err != nil || d.String() != want {
			t.Errorf("parseSince(%q) = %v, %v; want %s", in, d, err, want)
		}
	}
	for _, in := range []string{"", "5", "m", "1w", "1.5h", "-5m", "0d", "1h30m", " 1h", "1H", "1s"} {
		if _, err := parseSince(in); err == nil {
			t.Errorf("parseSince(%q) succeeded, want an error", in)
		}
	}
	// --since sets from = now - duration, resolved against the command clock.
	f := newFakeAdmin(t, map[string]route{"/admin/calls": {schema: "CallList", body: `{"calls":[],"next_cursor":null,"count":0,"count_capped":false}`}})
	f.cli("calls", "list", "--since", "7d")
	q := f.requests()[0].Query
	if q.Get("from") != "2026-11-06T14:20:30Z" || q.Get("to") != "2026-11-13T14:20:30Z" {
		t.Errorf("--since 7d sent %v", q)
	}
}

func TestWindowFlagsFromAndTo(t *testing.T) {
	w := windowFlags{from: "2026-11-01T00:00:00Z"}
	got, err := w.resolve(testNow)
	if err != nil || got.Default || fmtTime(got.From) != "2026-11-01T00:00:00Z" || fmtTime(got.To) != "2026-11-13T14:20:30Z" {
		t.Errorf("--from only: %+v, %v", got, err)
	}
	w = windowFlags{to: "2026-11-02T00:00:00Z"}
	got, err = w.resolve(testNow)
	if err != nil || fmtTime(got.From) != "2026-11-01T00:00:00Z" || got.Default {
		t.Errorf("--to only: from is 24h before to: %+v, %v", got, err)
	}
	w = windowFlags{}
	if got, _ := w.resolve(testNow); !got.Default {
		t.Error("no flags: the window is the default")
	}
}

// The window to is exclusive and printed in whole seconds. When the clock is
// part way into a second, a call recorded earlier in that second must still be
// inside a window that ends at now, so to is the next whole second, not the
// current one.
func TestWindowEndingNowCoversTheCurrentSecond(t *testing.T) {
	now := testNow.Add(240 * time.Millisecond) // a call at 14:20:30.200 is before now
	w := windowFlags{}
	got, err := w.resolve(now)
	if err != nil || fmtTime(got.To) != "2026-11-13T14:20:31Z" || fmtTime(got.From) != "2026-11-12T14:20:31Z" {
		t.Errorf("default window at %s: %+v, %v; want to 14:20:31", now.Format(time.RFC3339Nano), got, err)
	}

	f := newFakeAdmin(t, map[string]route{"/admin/usage": homeRoute(homeTotal, homeSessions)})
	var out bytes.Buffer
	env := func(k string) string {
		if k == adminURLVar {
			return f.srv.URL
		}
		return ""
	}
	if code := run(nil, &out, env, func() time.Time { return now }); code != 0 || len(f.requests()) != 2 {
		t.Fatalf("home view: code=%d, %d requests\n%s", code, len(f.requests()), out.String())
	}
	for _, r := range f.requests() {
		if r.Query.Get("from") != "2026-11-13T00:00:00Z" || r.Query.Get("to") != "2026-11-13T14:20:31Z" {
			t.Errorf("home view at %s sent %v; want from 00:00:00 and to 14:20:31", now.Format(time.RFC3339Nano), r.Query)
		}
	}
}

func TestCutText(t *testing.T) {
	if got := cutText("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := cutText(strings.Repeat("é", 12), 10); got != strings.Repeat("é", 10)+"... (cut at 10 characters)" {
		t.Errorf("got %q", got)
	}
}

func TestCostConversion(t *testing.T) {
	cases := map[int64]float64{0: 0, 1: 0, 49: 0, 50: 0.0001, 1234500: 1.2345, 18421300: 18.4213, 9812000: 9.812}
	for micro, want := range cases {
		if got := usdFromMicro(micro); got != want {
			t.Errorf("usdFromMicro(%d) = %v, want %v", micro, got, want)
		}
	}
	if costUSD(nil) != "unpriced" {
		t.Error("a null cost must print unpriced, not 0")
	}
	zero := int64(0)
	if costUSD(&zero) != 0.0 {
		t.Error("a known zero cost prints 0")
	}
}

func TestEscapeSegment(t *testing.T) {
	if got := escapeSegment("team/a:1 é"); got != "team%2Fa%3A1%20%C3%A9" {
		t.Errorf("got %q", got)
	}
	if got, _ := url.PathUnescape(escapeSegment("a/b")); got != "a/b" {
		t.Errorf("round trip: %q", got)
	}
}
