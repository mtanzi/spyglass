package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/mtanzi/spyglass/internal/fakeupstream"
)

// The end-to-end tests of ADR 0038. Each starts the real wiring against the
// fake upstream and a test Postgres (they skip without one, like the contract
// suite), uses anthropic-sdk-go as the client, and reads the result through
// the admin API. Run them alone with `make e2e`.

// A streamed and a non-streamed call each end up as a row with the usage the
// fixture carries, and the upstream sees the provider credential, not the
// virtual key.
func TestE2EStreamedAndNonStreamedCallsBecomeRows(t *testing.T) {
	e := newE2E(t)
	chat, unsplit := e2eFixture(t, "short_chat"), e2eFixture(t, "cache_write_unsplit")
	e.Upstream.Enqueue(fakeupstream.Reply{Fixture: chat, Split: 11}, fakeupstream.Reply{Fixture: unsplit})

	c := e.DefaultClient()
	msg, err := e.Stream(c, chat)
	if err != nil {
		t.Fatalf("streamed call: %v", err)
	}
	if got := msg.Content[0].Text; !strings.Contains(got, "Hello, Zaphod") {
		t.Errorf("client got text %q, want the fixture's reply", got)
	}
	if _, err := e.Send(c, unsplit); err != nil {
		t.Fatalf("non-streamed call: %v", err)
	}

	calls := e.WaitCalls(2)
	if s := calls[0]; !s.Streamed || s.Key != e2eDefaultKey || s.Status != 200 || s.Model != "claude-opus-4-8" ||
		s.Usage.InputUncached != 25 || s.Usage.Output != 12 || s.StopReason != "end_turn" || s.CostMicroUSD == nil {
		t.Errorf("streamed row = %+v, want key alice, 200, 25 in / 12 out, end_turn and a cost", s)
	}
	if n := calls[1]; n.Streamed || n.Status != 200 || n.Usage.CacheWrite5m != 40 {
		t.Errorf("non-streamed row = %+v, want 40 cache-write tokens", n)
	}

	for _, r := range e.Upstream.Requests() {
		if got := r.Header.Get("X-Api-Key"); got != e2eProviderSecret {
			t.Errorf("upstream saw x-api-key %q, want the provider credential", got)
		}
		if strings.Contains(r.Header.Get("Authorization"), e.Key(e2eDefaultKey)) {
			t.Error("the virtual key reached the upstream")
		}
	}
}

// A client command reads the real admin API the suite started, through the real
// wiring: the call the proxy recorded shows up in `spyglass calls list`, and
// the home view answers for the current UTC day (ADR 0055).
func TestE2ECLICallsListReadsTheRealAdminAPI(t *testing.T) {
	e := newE2E(t)
	chat := e2eFixture(t, "short_chat")
	e.Upstream.Enqueue(fakeupstream.Reply{Fixture: chat})
	if _, err := e.Stream(e.DefaultClient(), chat); err != nil {
		t.Fatal(err)
	}
	call := e.WaitCalls(1)[0]

	t.Setenv("SPYGLASS_ADMIN_URL", e.AdminURL())
	var out, errb bytes.Buffer
	if code := run([]string{"calls", "list"}, &out, &errb); code != 0 {
		t.Fatalf("calls list: code=%d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errb.String())
	}
	for _, want := range []string{"count: 1\n", "showing: 1\n", "calls[1]{id,model,status,cost_usd}:\n  " + call.ID + ",claude-opus-4-8,200,"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("calls list output lacks %q:\n%s", want, out.String())
		}
	}
	if errb.Len() != 0 {
		t.Errorf("a client command wrote to stderr: %q", errb.String())
	}

	out.Reset()
	if code := run(nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "today:\n") || !strings.Contains(out.String(), "top_sessions[") {
		t.Errorf("home view: code=%d\n%s", code, out.String())
	}

	out.Reset()
	if code := run([]string{"calls", "show", call.ID}, &out, &errb); code != 0 || !strings.Contains(out.String(), "id: "+call.ID+"\n") {
		t.Errorf("calls show: code=%d\n%s", code, out.String())
	}
}

// The use half comes from one call's response and the result half from a later
// call's request; the API pairs them when it reads.
func TestE2EToolHalvesPairAtRead(t *testing.T) {
	e := newE2E(t)
	uses, results := e2eFixture(t, "parallel_tool_calls"), e2eFixture(t, "tool_results_next_request")
	e.Upstream.Enqueue(fakeupstream.Reply{Fixture: uses}, fakeupstream.Reply{Fixture: results})

	c := e.DefaultClient(option.WithHeader("X-Claude-Code-Session-Id", "sess-pair"))
	if _, err := e.Stream(c, uses); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Send(c, results); err != nil {
		t.Fatal(err)
	}
	calls := e.WaitCalls(2)
	useCall, resultCall := calls[0].ID, calls[1].ID

	want := map[string]string{"toolu_01A": "client", "toolu_01B": "client", "srvtoolu_01C": "server"}
	got := map[string]e2eToolCall{}
	for _, tc := range e.ToolCalls("sess-pair") {
		got[tc.ToolCallID] = tc
	}
	for id, source := range want {
		tc, ok := got[id]
		if !ok {
			t.Errorf("tool call %s missing from the tool-call list", id)
			continue
		}
		if tc.Status != "paired" || tc.Source != source || tc.UseCallID == nil || tc.ResultCallID == nil {
			t.Errorf("tool call %s = %+v, want a paired %s call", id, tc, source)
			continue
		}
		if *tc.UseCallID != useCall {
			t.Errorf("tool call %s: use call %s, want the first call %s", id, *tc.UseCallID, useCall)
		}
		wantResult := resultCall
		if source == "server" {
			wantResult = useCall // a server tool returns inside the same response
		}
		if *tc.ResultCallID != wantResult {
			t.Errorf("tool call %s: result call %s, want %s", id, *tc.ResultCallID, wantResult)
		}
	}
}

// A key the keyring does not know gets an Anthropic-shaped 401 and never
// reaches the upstream.
func TestE2EUnknownKeyGets401(t *testing.T) {
	e := newE2E(t)
	chat := e2eFixture(t, "short_chat")

	_, err := e.Send(e.Client("lp_not_a_key"), chat)
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want a 401 API error", err)
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(apiErr.RawJSON()), &body); err != nil || body.Type != "error" || body.Error.Type != "authentication_error" {
		t.Errorf("401 body = %s (err %v), want an authentication_error", apiErr.RawJSON(), err)
	}
	if n := len(e.Upstream.Requests()); n != 0 {
		t.Errorf("upstream saw %d requests for a rejected key, want 0", n)
	}

	req, _ := http.NewRequest("POST", e.DataURL()+"/v1/messages", bytes.NewReader(chat.Request))
	resp, err := http.DefaultClient.Do(req) // no key at all
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("request with no key = %d, want 401", resp.StatusCode)
	}
}

// An error from the upstream reaches the client with its status, its headers
// and its exact body, and the call is recorded with that status.
func TestE2EUpstreamErrorsPassThroughUnchanged(t *testing.T) {
	e := newE2E(t)
	tests := []struct {
		fixture string
		status  int
	}{
		{"error_429", 429},
		{"error_529", 529},
		{"error_429", 503}, // the same body at a 5xx status
		{"error_429", 500},
	}
	for _, tt := range tests {
		f := e2eFixture(t, tt.fixture)
		e.Upstream.Enqueue(fakeupstream.Reply{Fixture: f, Status: tt.status, Headers: map[string]string{"Retry-After": "7", "Request-Id": "req_err"}})
	}
	for i, tt := range tests {
		f := e2eFixture(t, tt.fixture)
		req, _ := http.NewRequest("POST", e.DataURL()+"/v1/messages", bytes.NewReader(f.Request))
		req.Header.Set("Authorization", "Bearer "+e.Key(e2eDefaultKey))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tt.status || !bytes.Equal(body, f.Response) {
			t.Errorf("call %d: status %d body %q, want %d and the fixture body unchanged", i, resp.StatusCode, body, tt.status)
		}
		if resp.Header.Get("Retry-After") != "7" || resp.Header.Get("Request-Id") != "req_err" {
			t.Errorf("call %d: headers %v, want the upstream's Retry-After and Request-Id", i, resp.Header)
		}
	}
	calls := e.WaitCalls(len(tests))
	for i, tt := range tests {
		if calls[i].Status != tt.status {
			t.Errorf("row %d status = %d, want %d", i, calls[i].Status, tt.status)
		}
	}

	// The SDK sees the same error as a typed one.
	e.Upstream.Enqueue(fakeupstream.Reply{Fixture: e2eFixture(t, "error_529")})
	_, err := e.Send(e.DefaultClient(), e2eFixture(t, "error_529"))
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 529 {
		t.Errorf("SDK error = %v, want a 529 API error", err)
	}
}

// A client that leaves mid-stream is recorded as a client abort and its request
// is logged.
func TestE2EClientAbortIsLogged(t *testing.T) {
	e := newE2E(t)
	chat := e2eFixture(t, "short_chat")
	e.Upstream.Enqueue(fakeupstream.Reply{Fixture: chat, Delay: 400 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	cl := e.DefaultClient()
	s := cl.Messages.NewStreaming(ctx, params(t, chat))
	if !s.Next() {
		t.Fatalf("stream gave no event: %v", s.Err())
	}
	cancel() // the client walks away with the stream still open
	_ = s.Close()

	calls := e.WaitCalls(1)
	if calls[0].ErrorClass != "client_abort" {
		t.Errorf("error_class = %q, want client_abort", calls[0].ErrorClass)
	}
	if !logHasRequest(e.Logs.String(), calls[0].ID) {
		t.Errorf("no access-log line for call %s in:\n%s", calls[0].ID, e.Logs.String())
	}
}

// logHasRequest reports whether the JSON log has a request line for call id.
func logHasRequest(logs, id string) bool {
	for _, line := range strings.Split(logs, "\n") {
		var l struct {
			Msg    string `json:"msg"`
			CallID string `json:"call_id"`
			Path   string `json:"path"`
		}
		if json.Unmarshal([]byte(line), &l) == nil && l.Msg == "request" && l.CallID == id && l.Path == "/v1/messages" {
			return true
		}
	}
	return false
}

// A stream the upstream cuts is recorded as an upstream failure with the usage
// estimated, and an error event inside a stream keeps its place in the row.
func TestE2EUpstreamStreamFailures(t *testing.T) {
	e := newE2E(t)
	chat, midstream := e2eFixture(t, "short_chat"), e2eFixture(t, "midstream_error")
	e.Upstream.Enqueue(
		fakeupstream.Reply{Fixture: chat, AbortAfter: 3},
		fakeupstream.Reply{Fixture: midstream, Pings: true},
	)
	c := e.DefaultClient()
	if _, err := e.Stream(c, chat); err == nil {
		t.Error("a cut stream ended without an error at the client")
	}
	_, _ = e.Stream(c, midstream)

	calls := e.WaitCalls(2)
	if calls[0].ErrorClass != "upstream_5xx" || calls[0].Status != 200 {
		t.Errorf("cut stream row = status %d class %q, want 200 and upstream_5xx", calls[0].Status, calls[0].ErrorClass)
	}
	if d := e.Detail(calls[1].ID); d.ErrorType != "overloaded_error" || d.ErrorClass != "upstream_5xx" {
		t.Errorf("mid-stream error row = type %q class %q, want overloaded_error and upstream_5xx", d.ErrorType, d.ErrorClass)
	}
}

// With Postgres gone the data plane still forwards and still answers its own
// health endpoints; the calls made meanwhile are recorded once the database is
// back.
func TestE2EPostgresOutageDoesNotStopDataPlane(t *testing.T) {
	e := newE2E(t)
	chat := e2eFixture(t, "short_chat")
	e.Upstream.Enqueue(fakeupstream.Reply{Fixture: chat}, fakeupstream.Reply{Fixture: chat}, fakeupstream.Reply{Fixture: chat})
	c := e.DefaultClient()

	if _, err := e.Stream(c, chat); err != nil {
		t.Fatal(err)
	}
	e.WaitCalls(1)

	e.PG.Cut()
	for i := 0; i < 2; i++ {
		if _, err := e.Stream(c, chat); err != nil {
			t.Fatalf("call %d during the outage: %v", i+1, err)
		}
	}
	if code, _ := e.AdminStatus("/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("admin /readyz = %d during the outage, want 503", code)
	}
	if code, _ := e.AdminStatus("/admin/calls"); code == http.StatusOK {
		t.Error("the call log answered 200 with the database gone")
	}
	resp, err := http.Get(e.DataURL() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("data /healthz = %d during the outage, want 200", resp.StatusCode)
	}

	e.PG.Restore()
	if calls := e.WaitCalls(3); len(calls) != 3 {
		t.Errorf("%d rows after the outage, want 3: the recorder holds a batch until Postgres is back", len(calls))
	}
}

// Header sets from the fixture file: a call without hint headers records only
// the session and agent; a call with them records the whole correlation set.
func TestE2EHintHeaders(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "claude-code-headers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sets map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sets); err != nil {
		t.Fatal(err)
	}
	headers := func(name string) []option.RequestOption {
		var h map[string]string
		if err := json.Unmarshal(sets[name], &h); err != nil {
			t.Fatalf("header set %s: %v", name, err)
		}
		var opts []option.RequestOption
		for k, v := range h {
			if strings.EqualFold(k, "Authorization") { // the test's own key goes there
				continue
			}
			opts = append(opts, option.WithHeader(k, v))
		}
		return opts
	}

	e := newE2E(t)
	chat := e2eFixture(t, "short_chat")
	e.Upstream.Enqueue(fakeupstream.Reply{Fixture: chat}, fakeupstream.Reply{Fixture: chat})
	c := e.DefaultClient()
	if _, err := e.Stream(c, chat, headers("without_hint_headers")...); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Stream(c, chat, headers("with_hint_headers")...); err != nil {
		t.Fatal(err)
	}
	calls := e.WaitCalls(2)

	plain, hinted := e.Detail(calls[0].ID), e.Detail(calls[1].ID)
	if plain.SessionKey != "6f1c2d3e-8a4b-4c5d-9e0f-1a2b3c4d5e6f" || plain.AgentID != "agent-0001" || plain.Client != "claude-cli" {
		t.Errorf("without hints: session %q agent %q client %q", plain.SessionKey, plain.AgentID, plain.Client)
	}
	if plain.ParentAgentID != "" || plain.AgentType != "" || plain.PromptID != "" || plain.RequestClass != "" || plain.TraceID != "" || plain.ContextCompacted {
		t.Errorf("without hints the row holds hint fields: %+v", plain)
	}
	if hinted.AgentID != "agent-0002" || hinted.ParentAgentID != "agent-0001" || hinted.AgentType != "Explore" ||
		hinted.PromptID != "prompt-0042" || hinted.RequestClass != "subagent" || hinted.Compaction || !hinted.ContextCompacted ||
		hinted.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || hinted.PrevToolDurations != `{"Bash":1200,"Read":35}` {
		t.Errorf("with hints the row = %+v, want the full correlation set", hinted)
	}
}

// The no-bodies check: a unique marker sits in the prompt, the response and the
// tool arguments of two calls, and in the parent directories of the working
// directory of a third, and shows up nowhere the proxy stores, logs, counts or
// serves. Of the working directory only the final name, the project, is kept.
func TestE2ENoBodiesAnywhere(t *testing.T) {
	var b [8]byte
	_, _ = rand.Read(b[:])
	marker := "E2EMARKER" + hex.EncodeToString(b[:])

	e := newE2E(t)
	uses := withMarker(e2eFixture(t, "parallel_tool_calls"), marker)
	results := withMarker(e2eFixture(t, "tool_results_next_request"), marker)
	inDir := withMarker(e2eFixture(t, "claude_code_project"), marker)
	e.Upstream.Enqueue(fakeupstream.Reply{Fixture: uses, Split: 13}, fakeupstream.Reply{Fixture: results}, fakeupstream.Reply{Fixture: inDir})

	// The marker is in every body: the test would prove nothing otherwise.
	for name, body := range map[string][]byte{"use request": uses.Request, "use response": uses.Response, "result request": results.Request,
		"working directory request": inDir.Request} {
		if !bytes.Contains(body, []byte(marker)) {
			t.Fatalf("%s carries no marker", name)
		}
	}
	for _, field := range []string{`"system"`, `"text"`, `"partial_json"`} {
		if !bytes.Contains(uses.Request, []byte(field)) && !bytes.Contains(uses.Response, []byte(field)) {
			t.Fatalf("fixtures have no %s to hold the marker", field)
		}
	}

	c := e.DefaultClient(option.WithHeader("X-Claude-Code-Session-Id", "sess-nobodies"))
	msg, err := e.Stream(c, uses)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Content[0].Text, marker) {
		t.Errorf("the client did not get the marked response: %q", msg.Content[0].Text)
	}
	if _, err := e.Send(c, results); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Stream(c, inDir); err != nil {
		t.Fatal(err)
	}
	for i, r := range e.Upstream.Requests() {
		if !bytes.Contains(r.Body, []byte(marker)) {
			t.Fatalf("upstream request %d lost the marker: the proxy must forward bodies unchanged", i)
		}
	}
	calls := e.WaitCalls(3)
	if got := e.Detail(calls[2].ID).Project; got != "spyglass-demo" {
		t.Errorf("project of the call from the working directory = %q, want its final directory name", got)
	}

	// Every admin endpoint, with the call details and the session behind them.
	var apiBodies []string
	paths := []string{"/admin/calls?limit=200", "/admin/sessions", "/admin/sessions/sess-nobodies", "/admin/tool-calls?limit=100",
		"/admin/tool-calls/summary", "/admin/usage?group_by=model&grain=hour", "/admin/config", "/admin/openapi.json", "/metrics"}
	for _, c := range calls {
		paths = append(paths, "/admin/calls/"+c.ID)
	}
	for _, p := range paths {
		apiBodies = append(apiBodies, p+": "+string(e.Admin(p, nil)))
	}
	e.Stop() // the shutdown logs too

	sinks := map[string][]string{
		"a table value":              e.StoredText(),
		"a log line":                 strings.Split(e.Logs.String(), "\n"),
		"an API or metrics response": apiBodies,
	}
	for name, values := range sinks {
		if len(values) == 0 {
			t.Errorf("scanned no %s", name)
		}
		for _, v := range values {
			if strings.Contains(v, marker) {
				t.Errorf("the marker is in %s: %.300s", name, v)
			}
		}
	}
	// The pieces the marker stands for are as absent as the marker.
	for _, v := range e.StoredText() {
		for _, frag := range []string{"Zaphod", "secret agent", "a.txt", "b.txt", "-parent", "other-dir"} {
			if strings.Contains(v, frag) {
				t.Errorf("a table value holds body text %q: %.300s", frag, v)
			}
		}
	}
}
