package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/core/coretest"
)

// viewSpy wraps the canned reader and records what the view handlers pass in.
type viewSpy struct {
	*coretest.Reader
	sessionFilter core.SessionFilter
	toolFilter    core.ToolCallFilter
	usageQuery    core.UsageQuery
	promptQuery   core.PromptCostQuery
	page          core.Page
	sessionKey    string
	calls         int
}

func (s *viewSpy) ListSessions(ctx context.Context, f core.SessionFilter, p core.Page) ([]core.SessionSummary, *core.Cursor, error) {
	s.sessionFilter, s.page = f, p
	s.calls++
	return s.Reader.ListSessions(ctx, f, p)
}

func (s *viewSpy) GetSession(ctx context.Context, key string) (core.SessionDetail, error) {
	s.sessionKey = key
	s.calls++
	return s.Reader.GetSession(ctx, key)
}

func (s *viewSpy) ListToolCalls(ctx context.Context, f core.ToolCallFilter, p core.Page) ([]core.ToolCall, *core.Cursor, error) {
	s.toolFilter, s.page = f, p
	s.calls++
	return s.Reader.ListToolCalls(ctx, f, p)
}

func (s *viewSpy) ToolSummary(ctx context.Context, f core.ToolCallFilter) ([]core.ToolStat, error) {
	s.toolFilter = f
	s.calls++
	return s.Reader.ToolSummary(ctx, f)
}

func (s *viewSpy) Usage(ctx context.Context, q core.UsageQuery) ([]core.UsageBucket, error) {
	s.usageQuery = q
	s.calls++
	return s.Reader.Usage(ctx, q)
}

func TestListSessions(t *testing.T) {
	next := core.Cursor{TS: testNow.Add(-time.Hour), ID: core.NewID()}
	rd := &viewSpy{Reader: &coretest.Reader{
		Sessions: []core.SessionSummary{
			{SessionKey: "s1", KeyName: "alice", Client: "claude-code", FirstTS: testNow.Add(-2 * time.Hour), LastTS: testNow.Add(-time.Hour),
				EndedAt: testNow.Add(-time.Hour + 3*time.Second), Calls: 4, PromptCount: 3, ToolCallCount: 5, Errors: 1, AgentCount: 2,
				Models: []string{"m1", "m2"}, Usage: core.Usage{Output: 9}, Cost: cost(77)},
			{SessionKey: "s2", Calls: 1},
		},
		Next: &next,
	}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/sessions?model=m1&limit=2")
	if rr.Code != 200 {
		t.Fatalf("status = %d\n%s", rr.Code, rr.Body)
	}
	body := decode[sessionListJSON](t, rr)
	if len(body.Sessions) != 2 || body.NextCursor == nil {
		t.Fatalf("body = %+v", body)
	}
	if c, err := decodeCursor(*body.NextCursor); err != nil || c.ID != next.ID || !c.TS.Equal(next.TS) {
		t.Errorf("next_cursor = %q: %+v %v", *body.NextCursor, c, err)
	}
	s := body.Sessions[0]
	if s.SessionKey != "s1" || s.Key != "alice" || s.Calls != 4 || s.Prompts != 3 || s.ToolCalls != 5 ||
		!s.EndedAt.Equal(testNow.Add(-time.Hour+3*time.Second)) || s.Errors != 1 || s.AgentCount != 2 || len(s.Models) != 2 ||
		s.Usage.Output != 9 || s.CostMicroUSD == nil || *s.CostMicroUSD != 77 {
		t.Errorf("session = %+v", s)
	}
	if body.Sessions[1].CostMicroUSD != nil || body.Sessions[1].Models == nil {
		t.Errorf("unknown cost must stay null and models must be an array: %+v", body.Sessions[1])
	}
	if f := rd.sessionFilter; len(f.Models) != 1 || !f.To.Equal(testNow) || !f.From.Equal(testNow.Add(-24*time.Hour)) || rd.page.Limit != 2 {
		t.Errorf("filter %+v page %+v", f, rd.page)
	}
}

func TestListSessionsEmptyAndCursor(t *testing.T) {
	rr := get(newTestMux(t, &coretest.Reader{}, config.View{}), "/admin/sessions")
	if !strings.Contains(rr.Body.String(), `"sessions":[]`) || !strings.Contains(rr.Body.String(), `"next_cursor":null`) {
		t.Errorf("empty page must carry [] and null: %s", rr.Body)
	}
	c := core.Cursor{TS: testNow.Add(-3 * time.Hour), ID: core.NewID()}
	rd := &viewSpy{Reader: &coretest.Reader{}}
	rr = get(newTestMux(t, rd, config.View{}), "/admin/sessions?cursor="+encodeCursor(c))
	if rr.Code != 200 || rd.page.After == nil || rd.page.After.ID != c.ID || !rd.page.After.TS.Equal(c.TS) {
		t.Errorf("code %d after %+v", rr.Code, rd.page.After)
	}
}

func TestGetSessionKeyEncoding(t *testing.T) {
	keys := []string{"plain", "a:b", "team/a", "a b", "100%", "x?y#z", "é", "a%2Fb", "../etc"}
	sessions := map[string]core.SessionDetail{}
	for _, k := range keys {
		sessions[k] = core.SessionDetail{SessionSummary: core.SessionSummary{SessionKey: k, Calls: 1}}
	}
	rd := &viewSpy{Reader: &coretest.Reader{SessionMap: sessions}}
	h := newTestMux(t, rd, config.View{})
	for _, k := range keys {
		// url.PathEscape leaves ':' alone and escapes '/', which is how a client
		// must send a key that holds a slash.
		for _, esc := range []string{url.PathEscape(k), url.QueryEscape(k)} {
			rr := get(h, "/admin/sessions/"+strings.ReplaceAll(esc, "+", "%20"))
			if rr.Code != 200 {
				t.Errorf("key %q as %q: status %d\n%s", k, esc, rr.Code, rr.Body)
				continue
			}
			if rd.sessionKey != k {
				t.Errorf("key %q as %q reached the reader as %q", k, esc, rd.sessionKey)
			}
			if got := decode[sessionDetailJSON](t, rr).Summary.SessionKey; got != k {
				t.Errorf("key %q came back as %q", k, got)
			}
		}
	}
}

func TestGetSessionDetail(t *testing.T) {
	call := core.CallSummary{ID: core.NewID(), TS: testNow, Status: 200, ToolCount: 1}
	d := core.SessionDetail{
		SessionSummary: core.SessionSummary{SessionKey: "s", Calls: 1, AgentCount: 2},
		Agents:         []core.AgentNode{{AgentID: "a", AgentType: "main", Calls: 1}, {AgentID: "b", ParentAgentID: "a", Calls: 0}},
		Prompts:        []core.PromptSummary{{PromptID: "p1", FirstTS: testNow, Calls: 1, Usage: core.Usage{Output: 3}, Cost: cost(4)}},
		Calls:          []core.CallSummary{call},
	}
	h := newTestMux(t, &coretest.Reader{SessionMap: map[string]core.SessionDetail{"s": d}}, config.View{})
	rr := get(h, "/admin/sessions/s")
	if rr.Code != 200 {
		t.Fatalf("status = %d\n%s", rr.Code, rr.Body)
	}
	got := decode[sessionDetailJSON](t, rr)
	if got.Summary.Calls != 1 || len(got.Agents) != 2 || got.Agents[1].ParentAgentID != "a" || len(got.Prompts) != 1 ||
		got.Prompts[0].CostMicroUSD == nil || *got.Prompts[0].CostMicroUSD != 4 || len(got.Calls) != 1 || got.Calls[0].ID != call.ID.String() {
		t.Errorf("detail = %+v", got)
	}
	rr = get(h, "/admin/sessions/missing")
	if rr.Code != 404 || decode[errorBody](t, rr).Error.Type != ErrNotFound {
		t.Errorf("%d %s", rr.Code, rr.Body)
	}
}

func TestListToolCalls(t *testing.T) {
	useID, resID := core.NewIDAt(testNow), core.NewIDAt(testNow.Add(time.Second))
	next := core.Cursor{TS: testNow, ID: useID, Idx: 3}
	yes := true
	rd := &viewSpy{Reader: &coretest.Reader{
		ToolCalls: []core.ToolCall{
			{ToolCallID: "toolu_1", Name: "Bash", Source: core.ToolSourceClient, Status: core.ToolCallPaired, SessionKey: "s", AgentID: "a",
				UseCallID: useID, ResultCallID: resID, UseTS: testNow, ResultTS: testNow.Add(2500 * time.Millisecond),
				Duration: 2500 * time.Millisecond, DurationSource: core.DurationHeader, IsError: &yes, ArgBytes: 10, ResultBytes: 20},
			{ToolCallID: "toolu_2", Name: "Read", Source: core.ToolSourceClient, Status: core.ToolCallUseOnly, UseCallID: useID, UseTS: testNow},
			{ToolCallID: "toolu_3", Source: core.ToolSourceClient, Status: core.ToolCallResultOnly, ResultCallID: resID, ResultTS: testNow, ResultBytes: 5},
			{ToolCallID: "toolu_4", Name: "Edit", Source: core.ToolSourceClient, Status: core.ToolCallPaired, UseCallID: useID, ResultCallID: resID,
				UseTS: testNow, ResultTS: testNow, Duration: 0, DurationSource: core.DurationEstimated},
		},
		Next: &next,
	}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/tool-calls?session=s&tool=Bash&status=paired&limit=4")
	if rr.Code != 200 {
		t.Fatalf("status = %d\n%s", rr.Code, rr.Body)
	}
	body := decode[toolCallListJSON](t, rr)
	if len(body.ToolCalls) != 4 || body.NextCursor == nil {
		t.Fatalf("body = %+v", body)
	}
	if c, err := decodeCursor(*body.NextCursor); err != nil || c != (core.Cursor{TS: next.TS, ID: useID, Idx: 3}) {
		t.Errorf("next_cursor = %q: %+v %v", *body.NextCursor, c, err)
	}
	paired := body.ToolCalls[0]
	if paired.Name != "Bash" || paired.Status != core.ToolCallPaired || paired.UseCallID == nil || *paired.UseCallID != useID.String() ||
		paired.ResultCallID == nil || paired.DurationMs == nil || *paired.DurationMs != 2500 ||
		paired.DurationSource == nil || *paired.DurationSource != core.DurationHeader || paired.IsError == nil || !*paired.IsError {
		t.Errorf("paired = %+v", paired)
	}
	useOnly, resultOnly, zero := body.ToolCalls[1], body.ToolCalls[2], body.ToolCalls[3]
	if useOnly.ResultCallID != nil || useOnly.ResultTS != nil || useOnly.DurationMs != nil || useOnly.DurationSource != nil || useOnly.IsError != nil {
		t.Errorf("use_only must read null for the missing half: %+v", useOnly)
	}
	if resultOnly.UseCallID != nil || resultOnly.UseTS != nil || resultOnly.ResultTS == nil || resultOnly.DurationMs != nil {
		t.Errorf("result_only must read null for the missing half: %+v", resultOnly)
	}
	if zero.DurationMs == nil || *zero.DurationMs != 0 || zero.DurationSource == nil || *zero.DurationSource != core.DurationEstimated {
		t.Errorf("a known zero duration is not null: %+v", zero)
	}
	f := rd.toolFilter
	if f.SessionKey != "s" || len(f.Tools) != 1 || len(f.Statuses) != 1 || !f.To.Equal(testNow) || rd.page.Limit != 4 {
		t.Errorf("filter %+v page %+v", f, rd.page)
	}
}

func TestListToolCallsCursorKeepsIndex(t *testing.T) {
	c := core.Cursor{TS: testNow.Add(-time.Hour), ID: core.NewID(), Idx: 5}
	rd := &viewSpy{Reader: &coretest.Reader{}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/tool-calls?cursor="+encodeCursor(c))
	if rr.Code != 200 || rd.page.After == nil || *rd.page.After != (core.Cursor{TS: c.TS, ID: c.ID, Idx: 5}) {
		t.Errorf("code %d after %+v", rr.Code, rd.page.After)
	}
	if !strings.Contains(rr.Body.String(), `"tool_calls":[]`) {
		t.Errorf("empty page must carry []: %s", rr.Body)
	}
}

func TestToolSummary(t *testing.T) {
	rd := &viewSpy{Reader: &coretest.Reader{ToolStats: []core.ToolStat{
		{Name: "Bash", AgentType: "Explore", Count: 4, Errors: 1, DurationP50: 1500 * time.Millisecond, DurationP95: 3 * time.Second, TotalDuration: 8 * time.Second},
		{Name: "Read"},
	}}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/tool-calls/summary?session=s&tool=Bash")
	if rr.Code != 200 {
		t.Fatalf("status = %d\n%s", rr.Code, rr.Body)
	}
	body := decode[toolSummaryJSON](t, rr)
	if len(body.Tools) != 2 {
		t.Fatalf("body = %+v", body)
	}
	b := body.Tools[0]
	if b.Name != "Bash" || b.AgentType != "Explore" || b.Count != 4 || b.Errors != 1 || b.ErrorRate != 0.25 ||
		b.DurationP50Ms != 1500 || b.DurationP95Ms != 3000 || b.TotalDurationMs != 8000 {
		t.Errorf("stat = %+v", b)
	}
	if body.Tools[1].ErrorRate != 0 {
		t.Errorf("an empty row has error rate 0: %+v", body.Tools[1])
	}
	if rd.toolFilter.SessionKey != "s" || !rd.toolFilter.From.Equal(testNow.Add(-24*time.Hour)) {
		t.Errorf("filter = %+v", rd.toolFilter)
	}
	// "summary" is its own route, not a tool call id or a session key.
	rr = get(newTestMux(t, &coretest.Reader{}, config.View{}), "/admin/tool-calls/summary")
	if !strings.Contains(rr.Body.String(), `"tools":[]`) {
		t.Errorf("empty summary must carry []: %s", rr.Body)
	}
}

func TestGetUsage(t *testing.T) {
	rd := &viewSpy{Reader: &coretest.Reader{Buckets: []core.UsageBucket{
		{Start: testNow, Group: "claude-opus-4-8", Calls: 3, Errors: 1, Usage: core.Usage{InputUncached: 10, CacheRead: 30, Output: 5}, Cost: 99,
			UnpricedCalls: 1, EstimatedCalls: 2, NoUsageCalls: 1, LatencyP50: time.Second, LatencyP95: 2 * time.Second, LatencyP99: 3 * time.Second},
		{Start: testNow.Add(time.Hour), Calls: 1},
	}}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/usage?group_by=model&grain=day&status=529")
	if rr.Code != 200 {
		t.Fatalf("status = %d\n%s", rr.Code, rr.Body)
	}
	body := decode[usageSeriesJSON](t, rr)
	if body.GroupBy == nil || *body.GroupBy != core.GroupModel || body.Grain != core.GrainDay || len(body.Buckets) != 2 {
		t.Fatalf("body = %+v", body)
	}
	b := body.Buckets[0]
	if b.Group != "claude-opus-4-8" || b.Calls != 3 || b.CostMicroUSD != 99 || b.UnpricedCalls != 1 || b.EstimatedCalls != 2 ||
		b.NoUsageCalls != 1 || b.LatencyP95Ms != 2000 || b.LatencyP99Ms != 3000 || b.CacheHitRatio == nil || *b.CacheHitRatio != 0.75 {
		t.Errorf("bucket = %+v", b)
	}
	if body.Buckets[1].CacheHitRatio != nil {
		t.Errorf("no input tokens means no ratio, not zero: %+v", body.Buckets[1])
	}
	q := rd.usageQuery
	if q.GroupBy != core.GroupModel || q.Grain != core.GrainDay || len(q.Filter.Statuses) != 1 || !q.Filter.To.Equal(testNow) {
		t.Errorf("query = %+v", q)
	}

	rr = get(newTestMux(t, &coretest.Reader{}, config.View{}), "/admin/usage")
	if !strings.Contains(rr.Body.String(), `"group_by":null`) || !strings.Contains(rr.Body.String(), `"grain":"hour"`) ||
		!strings.Contains(rr.Body.String(), `"buckets":[]`) {
		t.Errorf("defaults: %s", rr.Body)
	}
}

func (s *viewSpy) PromptCosts(ctx context.Context, q core.PromptCostQuery) (core.PromptCosts, error) {
	s.promptQuery = q
	s.calls++
	return s.Reader.PromptCosts(ctx, q)
}

func TestGetPromptCosts(t *testing.T) {
	cost := core.MicroUSD(1_500)
	rd := &viewSpy{Reader: &coretest.Reader{Prompts: core.PromptCosts{
		Prompts: []core.PromptCost{
			{PromptID: "p1", SessionKey: "s1", FirstTS: testNow, Calls: 3, Cost: &cost, UnpricedCalls: 1, TokensIn: 40, APIEquivalent: true},
			{PromptID: "p2", FirstTS: testNow, Calls: 1, UnpricedCalls: 1},
		},
		PromptCount: 7, CallsWithoutPrompt: 4,
	}}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/usage/prompts?limit=2&status=529")
	if rr.Code != 200 {
		t.Fatalf("status = %d\n%s", rr.Code, rr.Body)
	}
	body := decode[promptCostsJSON](t, rr)
	if body.PromptCount != 7 || body.CallsWithoutPrompt != 4 || len(body.Prompts) != 2 {
		t.Fatalf("body = %+v", body)
	}
	if p := body.Prompts[0]; p.PromptID != "p1" || p.SessionKey != "s1" || p.Calls != 3 || p.CostMicroUSD == nil || *p.CostMicroUSD != 1500 ||
		p.UnpricedCalls != 1 || p.TokensIn != 40 || !p.APIEquivalent {
		t.Errorf("prompt 0 = %+v", p)
	}
	if p := body.Prompts[1]; p.CostMicroUSD != nil || p.APIEquivalent {
		t.Errorf("an all-unpriced prompt has a null cost, not zero: %+v", p)
	}
	if !strings.Contains(rr.Body.String(), `"cost_micro_usd":null`) {
		t.Errorf("unknown cost must be null on the wire: %s", rr.Body)
	}
	q := rd.promptQuery
	if q.Limit != 2 || len(q.Filter.Statuses) != 1 || !q.Filter.To.Equal(testNow) {
		t.Errorf("query = %+v", q)
	}

	rd = &viewSpy{Reader: &coretest.Reader{}}
	rr = get(newTestMux(t, rd, config.View{}), "/admin/usage/prompts")
	if !strings.Contains(rr.Body.String(), `"prompts":[]`) || rd.promptQuery.Limit != DefaultLimit {
		t.Errorf("defaults: %s, limit %d", rr.Body, rd.promptQuery.Limit)
	}
}

func TestViewEndpointErrors(t *testing.T) {
	boom := errors.New("pq: connection to 10.0.0.5 refused")
	tests := []struct {
		name   string
		reader core.CallReader
		target string
		status int
		typ    ErrorType
		param  string
	}{
		{"sessions unknown parameter", &coretest.Reader{}, "/admin/sessions?nope=1", 400, ErrInvalidParameter, "nope"},
		{"sessions bad cursor", &coretest.Reader{}, "/admin/sessions?cursor=zzz", 400, ErrInvalidCursor, "cursor"},
		{"sessions store failure", &coretest.Reader{Err: boom}, "/admin/sessions", 500, ErrInternal, ""},
		{"sessions no reader", nil, "/admin/sessions", 503, ErrInternal, ""},
		{"session store failure", &coretest.Reader{Err: boom}, "/admin/sessions/s", 500, ErrInternal, ""},
		{"session no reader", nil, "/admin/sessions/s", 503, ErrInternal, ""},
		{"session not found", &coretest.Reader{}, "/admin/sessions/s", 404, ErrNotFound, ""},
		{"session reader refuses the filter", &coretest.Reader{Err: core.ErrInvalidFilter}, "/admin/sessions", 400, ErrInvalidParameter, ""},
		{"tool calls bad status", &coretest.Reader{}, "/admin/tool-calls?status=open", 400, ErrInvalidParameter, "status"},
		{"tool calls bad cursor", &coretest.Reader{}, "/admin/tool-calls?cursor=zzz", 400, ErrInvalidCursor, "cursor"},
		{"tool calls store failure", &coretest.Reader{Err: boom}, "/admin/tool-calls", 500, ErrInternal, ""},
		{"tool calls no reader", nil, "/admin/tool-calls", 503, ErrInternal, ""},
		{"summary rejects a page", &coretest.Reader{}, "/admin/tool-calls/summary?limit=5", 400, ErrInvalidParameter, "limit"},
		{"summary store failure", &coretest.Reader{Err: boom}, "/admin/tool-calls/summary", 500, ErrInternal, ""},
		{"summary no reader", nil, "/admin/tool-calls/summary", 503, ErrInternal, ""},
		{"usage bad group_by", &coretest.Reader{}, "/admin/usage?group_by=prompt", 400, ErrInvalidParameter, "group_by"},
		{"usage bad grain", &coretest.Reader{}, "/admin/usage?grain=week", 400, ErrInvalidParameter, "grain"},
		{"usage store failure", &coretest.Reader{Err: boom}, "/admin/usage", 500, ErrInternal, ""},
		{"usage no reader", nil, "/admin/usage", 503, ErrInternal, ""},
		{"prompt costs bad limit", &coretest.Reader{}, "/admin/usage/prompts?limit=0", 400, ErrInvalidParameter, "limit"},
		{"prompt costs limit above the maximum", &coretest.Reader{}, "/admin/usage/prompts?limit=501", 400, ErrInvalidParameter, "limit"},
		{"prompt costs rejects a cursor", &coretest.Reader{}, "/admin/usage/prompts?cursor=x", 400, ErrInvalidParameter, "cursor"},
		{"prompt costs rejects group_by", &coretest.Reader{}, "/admin/usage/prompts?group_by=key", 400, ErrInvalidParameter, "group_by"},
		{"prompt costs store failure", &coretest.Reader{Err: boom}, "/admin/usage/prompts", 500, ErrInternal, ""},
		{"prompt costs no reader", nil, "/admin/usage/prompts", 503, ErrInternal, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := get(newTestMux(t, tc.reader, config.View{}), tc.target)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d\n%s", rr.Code, tc.status, rr.Body)
			}
			e := decode[errorBody](t, rr).Error
			if e.Type != tc.typ || e.Parameter != tc.param {
				t.Errorf("error = %q %q, want %q %q", e.Type, e.Parameter, tc.typ, tc.param)
			}
			if strings.Contains(rr.Body.String(), "10.0.0.5") {
				t.Errorf("store error text leaked: %s", rr.Body)
			}
		})
	}
}

func TestBadParametersNeverReachTheReader(t *testing.T) {
	rd := &viewSpy{Reader: &coretest.Reader{}}
	h := newTestMux(t, rd, config.View{})
	for _, target := range []string{
		"/admin/sessions?nope=1", "/admin/tool-calls?status=x", "/admin/tool-calls/summary?limit=1", "/admin/usage?group_by=x", "/admin/usage?grain=x",
	} {
		if rr := get(h, target); rr.Code != 400 {
			t.Errorf("%s: status %d", target, rr.Code)
		}
	}
	if rd.calls != 0 {
		t.Errorf("reader called %d times for refused requests", rd.calls)
	}
}

func TestViewRoutesAreGETOnly(t *testing.T) {
	h := newTestMux(t, &coretest.Reader{}, config.View{})
	for _, target := range []string{"/admin/sessions", "/admin/sessions/s", "/admin/tool-calls", "/admin/tool-calls/summary", "/admin/usage"} {
		rr := httptestPost(h, target)
		if rr.Code == 200 {
			t.Errorf("POST %s answered 200", target)
		}
	}
}

func httptestPost(h http.Handler, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, target, nil))
	return rr
}
