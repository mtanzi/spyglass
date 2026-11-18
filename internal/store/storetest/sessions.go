package storetest

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// sessionWin is the time range of the session cases: the first hour after Base.
var sessionWin = core.SessionFilter{From: Base, To: Base.Add(time.Hour)}

var sessionCases = []testCase{
	{"sessions_group_calls_by_session_key_and_skip_calls_without_one", func(t *testing.T, e *env) {
		calls := sessionDataset(e.gen)
		e.write(t, calls...)
		got := e.walkSessions(t, sessionWin, 10)
		want := refSessions(calls, sessionWin)
		if len(want) != 3 {
			t.Fatalf("dataset has %d sessions in range, want 3", len(want))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("sessions\n got %+v\nwant %+v", got, want)
		}
		// Spot checks, so the reference cannot drift with the code.
		byKey := map[string]core.SessionSummary{}
		for _, s := range got {
			byKey[s.SessionKey] = s
		}
		s1 := byKey["s1"]
		if s1.Calls != 5 || s1.Errors != 1 || s1.AgentCount != 2 || s1.KeyName != "alice" || s1.Client != "claude-code" ||
			!slices.Equal(s1.Models, []string{"claude-opus-5-5", "claude-sonnet-5-5"}) ||
			s1.FirstTS != Base || s1.LastTS != Base.Add(40*time.Second) || s1.Cost == nil || *s1.Cost != 3000 {
			t.Errorf("s1 = %+v", s1)
		}
		if s2 := byKey["s2"]; s2.Cost != nil || s2.Client != "sdk" || s2.AgentCount != 0 {
			t.Errorf("s2 = %+v, want no known cost, client sdk and no agents", s2)
		}
	}},
	{"sessions_count_unpriced_calls_and_sum_only_priced_ones", func(t *testing.T, e *env) {
		g := e.gen
		at := func(d time.Duration, k string, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(d)), WithSession(k)}, opts...)...)
		}
		calls := []core.CallRecord{
			at(0, "mixed", WithCost(1000)), at(time.Second, "mixed", WithoutCost()),
			at(2*time.Second, "mixed", WithCost(2500)), at(3*time.Second, "mixed", WithoutCost()),
			at(0, "priced", WithCost(700)), at(time.Second, "priced", WithCost(300)),
			at(0, "none", WithoutCost()),
		}
		e.write(t, calls...)
		got := e.walkSessions(t, sessionWin, 10)
		if want := refSessions(calls, sessionWin); !reflect.DeepEqual(got, want) {
			t.Fatalf("sessions\n got %+v\nwant %+v", got, want)
		}
		byKey := map[string]core.SessionSummary{}
		for _, s := range got {
			byKey[s.SessionKey] = s
		}
		if s := byKey["mixed"]; s.UnpricedCalls != 2 || s.Cost == nil || *s.Cost != 3500 {
			t.Errorf("mixed = %+v, want 2 unpriced calls and a cost of the priced calls only", s)
		}
		if s := byKey["priced"]; s.UnpricedCalls != 0 || s.Cost == nil || *s.Cost != 1000 {
			t.Errorf("priced = %+v, want 0 unpriced calls and cost 1000", s)
		}
		if s := byKey["none"]; s.UnpricedCalls != 1 || s.Cost != nil {
			t.Errorf("none = %+v, want 1 unpriced call and no cost", s)
		}
		d, err := e.GetSession(context.Background(), "mixed")
		if err != nil {
			t.Fatal(err)
		}
		if d.UnpricedCalls != 2 || d.Cost == nil || *d.Cost != 3500 {
			t.Errorf("detail summary = %+v, want 2 unpriced calls and cost 3500", d.SessionSummary)
		}
	}},
	{"sessions_count_only_calls_in_a_half_open_range", func(t *testing.T, e *env) {
		g := e.gen
		at := func(d time.Duration) core.CallRecord { return g.Call(g.At(Base.Add(d)), WithSession("s")) }
		calls := []core.CallRecord{at(-time.Millisecond), at(0), at(time.Hour - time.Millisecond), at(time.Hour)}
		e.write(t, calls...)
		got, _, err := e.ListSessions(context.Background(), sessionWin, page(10, nil))
		if err != nil || len(got) != 1 {
			t.Fatalf("sessions = %+v, err %v", got, err)
		}
		if got[0].Calls != 2 || got[0].FirstTS != Base || got[0].LastTS != Base.Add(time.Hour-time.Millisecond) {
			t.Errorf("session = %+v, want the two calls inside the range", got[0])
		}
		f := sessionWin
		f.From, f.To = Base.Add(2*time.Hour), Base.Add(3*time.Hour)
		if got, next, err := e.ListSessions(context.Background(), f, page(10, nil)); err != nil || len(got) != 0 || next != nil {
			t.Errorf("empty range: %+v, cursor %v, err %v", got, next, err)
		}
	}},
	{"sessions_count_prompts_and_tool_calls_and_end_at_the_latest_call_end", func(t *testing.T, e *env) {
		g := e.gen
		at := func(d time.Duration, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(d)), WithSession("s")}, opts...)...)
		}
		noEnd := func(c *core.CallRecord) { c.EndedAt = time.Time{} }
		calls := []core.CallRecord{
			at(0, WithPrompt("p1"), g.WithUse("toolu_1", "Bash")),
			// Ends after the start of the newest call: the session ends here.
			at(10*time.Second, WithPrompt("p1"), WithDuration(30*time.Second), g.WithResult("toolu_1", false), g.WithUse("toolu_2", "Read")),
			at(20*time.Second, WithPrompt("p2"), g.WithServerTool("toolu_3", "web_search", false), g.WithResult("toolu_9", true), noEnd),
			at(25 * time.Second),
			// Outside the range: neither its prompt nor its tool call counts.
			at(2*time.Hour, WithPrompt("p3"), g.WithUse("toolu_4", "Bash")),
			g.Call(g.At(Base.Add(5*time.Second)), WithSession("open"), noEnd),
		}
		e.write(t, calls...)
		got := e.walkSessions(t, sessionWin, 10)
		if want := refSessions(calls, sessionWin); !reflect.DeepEqual(got, want) {
			t.Fatalf("sessions\n got %+v\nwant %+v", got, want)
		}
		byKey := map[string]core.SessionSummary{}
		for _, s := range got {
			byKey[s.SessionKey] = s
		}
		if s := byKey["s"]; s.PromptCount != 2 || s.ToolCallCount != 4 || s.EndedAt != Base.Add(40*time.Second) || s.LastTS != Base.Add(25*time.Second) {
			t.Errorf("s = %+v, want 2 prompts, 4 tool calls and the end of the second call", s)
		}
		if s := byKey["open"]; s.PromptCount != 0 || s.ToolCallCount != 0 || s.EndedAt != Base.Add(5*time.Second) {
			t.Errorf("open = %+v, want no prompts, no tool calls and the start of its call as end", s)
		}
		d, err := e.GetSession(context.Background(), "s")
		if err != nil {
			t.Fatal(err)
		}
		if d.PromptCount != 3 || d.ToolCallCount != 5 || d.EndedAt != Base.Add(2*time.Hour+1500*time.Millisecond) {
			t.Errorf("detail summary = %+v, want every call of the session", d.SessionSummary)
		}
	}},
	{"sessions_list_orders_by_newest_call_and_pages_without_gaps_or_duplicates", func(t *testing.T, e *env) {
		g := e.gen
		// Four sessions whose newest calls share a timestamp, plus interleaved older calls.
		var calls []core.CallRecord
		for _, k := range []string{"s1", "s2", "s3", "s4"} {
			calls = append(calls,
				g.Call(g.At(Base.Add(time.Minute)), WithSession(k)),
				g.Call(g.At(Base.Add(time.Second)), WithSession(k)))
		}
		calls = append(calls, g.Call(g.At(Base.Add(2*time.Minute)), WithSession("s5")))
		e.write(t, calls...)
		want := refSessions(calls, sessionWin)
		for _, size := range []int{1, 2, 3, 100} {
			if got := e.walkSessions(t, sessionWin, size); !reflect.DeepEqual(got, want) {
				t.Fatalf("page size %d\n got %+v\nwant %+v", size, got, want)
			}
		}
		if want[0].SessionKey != "s5" {
			t.Errorf("first session %q, want the one with the newest call", want[0].SessionKey)
		}
	}},
	{"sessions_list_applies_every_filter", func(t *testing.T, e *env) {
		calls := sessionDataset(e.gen)
		e.write(t, calls...)
		touched := map[string]bool{}
		for _, c := range sessionFilterCases {
			t.Run(c.name, func(t *testing.T) {
				f := sessionWin
				c.set(&f)
				for field, val := range reflect.ValueOf(f).Fields() {
					if name := field.Name; name != "From" && name != "To" && !val.IsZero() {
						touched[name] = true
					}
				}
				want := refSessions(calls, f)
				if len(want) == 0 || len(want) == len(refSessions(calls, sessionWin)) {
					t.Fatalf("filter selects %d sessions: the dataset does not exercise it", len(want))
				}
				if got := e.walkSessions(t, f, 1); !reflect.DeepEqual(got, want) {
					t.Errorf("got %+v\nwant %+v", got, want)
				}
			})
		}
		for field := range reflect.TypeFor[core.SessionFilter]().Fields() {
			if name := field.Name; name != "From" && name != "To" && !touched[name] {
				t.Errorf("SessionFilter.%s has no contract case", name)
			}
		}
	}},
	{"sessions_filter_selects_by_any_call_but_summarizes_all_calls", func(t *testing.T, e *env) {
		calls := sessionDataset(e.gen)
		e.write(t, calls...)
		f := sessionWin
		f.Models = []string{"claude-sonnet-5-5"}
		got := e.walkSessions(t, f, 10)
		if len(got) != 1 || got[0].SessionKey != "s1" || got[0].Calls != 5 ||
			!slices.Equal(got[0].Models, []string{"claude-opus-5-5", "claude-sonnet-5-5"}) {
			t.Fatalf("got %+v, want s1 with all five calls and both models", got)
		}
	}},
	{"sessions_list_rejects_an_unbounded_filter", func(t *testing.T, e *env) {
		for _, f := range []core.SessionFilter{{}, {From: Base}, {To: Base}, {From: Base, To: Base}} {
			if _, _, err := e.ListSessions(context.Background(), f, page(10, nil)); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("ListSessions(%+v) err = %v", f, err)
			}
		}
	}},
	{"session_detail_has_summary_agents_prompts_and_calls", func(t *testing.T, e *env) {
		calls := sessionDataset(e.gen)
		e.write(t, calls...)
		got, err := e.GetSession(context.Background(), "s1")
		if err != nil {
			t.Fatal(err)
		}
		// The detail reaches every call of the session, not a time range.
		all := refSessions(calls, core.SessionFilter{From: Base.Add(-time.Hour), To: Base.Add(10 * time.Hour), SessionKey: "s1"})
		if len(all) != 1 || all[0].Calls != 6 {
			t.Fatalf("reference = %+v", all)
		}
		if !reflect.DeepEqual(got.SessionSummary, all[0]) {
			t.Errorf("summary\n got %+v\nwant %+v", got.SessionSummary, all[0])
		}
		wantAgents := []core.AgentNode{
			{AgentID: "a1", AgentType: "subagent", Calls: 2},
			{AgentID: "a2", ParentAgentID: "a1", AgentType: "subagent", Calls: 3},
		}
		if !reflect.DeepEqual(got.Agents, wantAgents) {
			t.Errorf("agents\n got %+v\nwant %+v", got.Agents, wantAgents)
		}
		if got.AgentCount != len(got.Agents) {
			t.Errorf("AgentCount %d, %d agent nodes", got.AgentCount, len(got.Agents))
		}
		if want := refAgents(calls, "s1"); !reflect.DeepEqual(got.Agents, want) {
			t.Errorf("agents differ from the call rows\n got %+v\nwant %+v", got.Agents, want)
		}
		cost := func(m core.MicroUSD) *core.MicroUSD { return &m }
		p := func(id string, sec, n int, cost *core.MicroUSD, calls ...core.CallRecord) core.PromptSummary {
			var u core.Usage
			for _, c := range calls {
				u.InputUncached += c.Usage.InputUncached
				u.Output += c.Usage.Output
			}
			return core.PromptSummary{PromptID: id, FirstTS: Base.Add(time.Duration(sec) * time.Second), Calls: n, Usage: u, Cost: cost}
		}
		wantPrompts := []core.PromptSummary{
			p("p3", 7200, 1, cost(500), calls[5]),
			p("p2", 30, 2, nil, calls[3], calls[4]),
			p("p1", 0, 3, cost(3000), calls[0], calls[1], calls[2]),
		}
		if !reflect.DeepEqual(got.Prompts, wantPrompts) {
			t.Errorf("prompts\n got %+v\nwant %+v", got.Prompts, wantPrompts)
		}
		// The calls are the session's rows of the call log, in its order.
		rows, _, err := e.ListCalls(context.Background(), core.CallFilter{
			From: Base.Add(-time.Hour), To: Base.Add(10 * time.Hour), SessionKey: "s1",
		}, page(100, nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 6 || !reflect.DeepEqual(got.Calls, rows) {
			t.Errorf("calls\n got %+v\nwant %+v", got.Calls, rows)
		}
	}},
	{"session_agent_tree_matches_the_call_rows", func(t *testing.T, e *env) {
		g := e.gen
		var calls []core.CallRecord
		add := func(id, parent, typ string, n int) {
			for range n {
				calls = append(calls, g.Call(WithSession("s"), WithAgent(id, parent, typ)))
			}
		}
		// A main agent with no id, two subagents under it, and a subagent of a subagent.
		calls = append(calls, g.Call(WithSession("s")), g.Call(WithSession("s")))
		add("a1", "", "subagent", 2)
		add("a2", "", "subagent", 1)
		add("a3", "a1", "subagent", 3)
		add("a1", "", "subagent", 1)
		// The same agent id with another parent or type is another node.
		add("a3", "a2", "workflow", 1)
		calls = append(calls, g.Call(WithSession("other"), WithAgent("zz", "", "subagent")))
		e.write(t, calls...)
		got, err := e.GetSession(context.Background(), "s")
		if err != nil {
			t.Fatal(err)
		}
		want := []core.AgentNode{
			{AgentID: "a1", AgentType: "subagent", Calls: 3},
			{AgentID: "a2", AgentType: "subagent", Calls: 1},
			{AgentID: "a3", ParentAgentID: "a1", AgentType: "subagent", Calls: 3},
			{AgentID: "a3", ParentAgentID: "a2", AgentType: "workflow", Calls: 1},
		}
		if !reflect.DeepEqual(got.Agents, want) || !reflect.DeepEqual(got.Agents, refAgents(calls, "s")) {
			t.Errorf("agents\n got %+v\nwant %+v", got.Agents, want)
		}
		if got.AgentCount != 4 || got.Calls[0].SessionKey != "s" || len(got.Calls) != 10 {
			t.Errorf("AgentCount %d with %d calls", got.AgentCount, len(got.Calls))
		}
	}},
	{"session_text_order_is_bytewise", func(t *testing.T, e *env) {
		// Mixed case and punctuation sort differently by byte and by glibc
		// locale ("B" < "_a" < "b" against "_a" < "b" < "B"). The store orders
		// text bytewise (COLLATE "C"), whatever the server's locale. The calls
		// share one timestamp, so the text decides the order of agents and prompts.
		g := e.gen
		at := g.At(Base)
		e.write(t,
			g.Call(at, WithSession("m"), WithModel("claude-opus4"), WithAgent("b", "", "subagent"), WithPrompt("p1")),
			g.Call(at, WithSession("m"), WithModel("claude-opus-4-8"), WithAgent("B", "", "subagent"), WithPrompt("P2")),
			g.Call(at, WithSession("m"), WithModel("Claude-x"), WithAgent("_a", "", "subagent"), WithPrompt("_p")),
		)
		got, err := e.GetSession(context.Background(), "m")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"Claude-x", "claude-opus-4-8", "claude-opus4"}; !slices.Equal(got.Models, want) {
			t.Errorf("models %q, want %q", got.Models, want)
		}
		var agents, prompts []string
		for _, a := range got.Agents {
			agents = append(agents, a.AgentID)
		}
		for _, p := range got.Prompts {
			prompts = append(prompts, p.PromptID)
		}
		if want := []string{"B", "_a", "b"}; !slices.Equal(agents, want) {
			t.Errorf("agents %q, want %q", agents, want)
		}
		if want := []string{"P2", "_p", "p1"}; !slices.Equal(prompts, want) {
			t.Errorf("prompts %q, want %q", prompts, want)
		}
		list := e.walkSessions(t, sessionWin, 10)
		if len(list) != 1 || !slices.Equal(list[0].Models, got.Models) {
			t.Errorf("session list %+v, want one session with the same models", list)
		}
	}},
	{"session_detail_missing_is_not_found", func(t *testing.T, e *env) {
		e.write(t, e.gen.Call(), e.gen.Call(WithSession("s1")))
		for _, key := range []string{"nope", "", "S1"} {
			if _, err := e.GetSession(context.Background(), key); !errors.Is(err, core.ErrNotFound) {
				t.Errorf("GetSession(%q) err = %v, want ErrNotFound", key, err)
			}
		}
	}},
}

// sessionDataset is thirteen calls: session s1 with six calls (agents, two
// prompts and a third prompt outside the first hour), session s2 with two,
// session s3 with one, and four calls without a session.
func sessionDataset(g *Gen) []core.CallRecord {
	at := func(sec int, opts ...CallOpt) core.CallRecord {
		return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second))}, opts...)...)
	}
	s1 := func(opts ...CallOpt) []CallOpt { return append([]CallOpt{WithSession("s1")}, opts...) }
	return []core.CallRecord{
		at(0, s1(WithPrompt("p1"), WithCost(1000))...),
		at(10, s1(WithPrompt("p1"), WithAgent("a1", "", "subagent"), WithModel("claude-sonnet-5-5"), WithError(429, "rate_limited"), WithCost(2000))...),
		at(20, s1(WithPrompt("p1"), WithAgent("a2", "a1", "subagent"))...),
		at(30, s1(WithPrompt("p2"), WithAgent("a1", "", "subagent"))...),
		at(40, s1(WithPrompt("p2"), WithAgent("a2", "a1", "subagent"))...),
		at(7200, s1(WithPrompt("p3"), WithAgent("a2", "a1", "subagent"), WithCost(500))...),
		at(5, WithSession("s2"), WithKey("bob"), func(c *core.CallRecord) { c.Client, c.Provider = "sdk", "other" }),
		at(50, WithSession("s2"), WithKey("bob"), func(c *core.CallRecord) { c.Client, c.Provider = "sdk", "other" }, WithError(500, "upstream_5xx")),
		at(25, WithSession("s3"), WithKey("carol"), WithModel("claude-haiku-5-5"), WithCost(9000)),
		at(15),
		at(45),
		at(46, WithAgent("a1", "", "subagent")),
		at(47, WithPrompt("p1")),
	}
}

var sessionFilterCases = []struct {
	name string
	set  func(*core.SessionFilter)
}{
	{"keys", func(f *core.SessionFilter) { f.Keys = []string{"bob", "carol"} }},
	{"clients", func(f *core.SessionFilter) { f.Clients = []string{"sdk"} }},
	{"providers", func(f *core.SessionFilter) { f.Providers = []string{"other"} }},
	{"models", func(f *core.SessionFilter) { f.Models = []string{"claude-haiku-5-5", "claude-sonnet-5-5"} }},
	{"error_classes", func(f *core.SessionFilter) { f.ErrorClasses = []string{"rate_limited", "upstream_5xx"} }},
	{"session_key", func(f *core.SessionFilter) { f.SessionKey = "s3" }},
	{"min_cost", func(f *core.SessionFilter) { f.MinCost = 5000 }},
}

func (e *env) walkSessions(t *testing.T, f core.SessionFilter, size int) []core.SessionSummary {
	t.Helper()
	var out []core.SessionSummary
	var after *core.Cursor
	for range 10_000 {
		rows, next, err := e.ListSessions(context.Background(), f, page(size, after))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > size {
			t.Fatalf("page of %d rows for limit %d", len(rows), size)
		}
		out = append(out, rows...)
		if next == nil {
			return out
		}
		if len(rows) == 0 {
			t.Fatal("empty page with a cursor")
		}
		after = next
	}
	t.Fatal("pagination does not end")
	return nil
}

// refSessions is the reference for ListSessions: it groups the calls in Go.
// A session is listed when one of its calls in the range satisfies the call
// fields of f; its summary counts all its calls in the range.
func refSessions(calls []core.CallRecord, f core.SessionFilter) []core.SessionSummary {
	type group struct {
		calls []core.CallRecord
		hit   bool
	}
	groups := map[string]*group{}
	for _, c := range calls {
		if c.SessionKey == "" || c.TS.Before(f.From) || !c.TS.Before(f.To) {
			continue
		}
		g := groups[c.SessionKey]
		if g == nil {
			g = &group{}
			groups[c.SessionKey] = g
		}
		g.calls = append(g.calls, c)
		cf := core.CallFilter{
			Keys: f.Keys, Clients: f.Clients, Providers: f.Providers, Models: f.Models,
			ErrorClasses: f.ErrorClasses, SessionKey: f.SessionKey,
		}
		g.hit = g.hit || cf.Match(c, nil)
	}
	type listed struct {
		s    core.SessionSummary
		last core.CallRecord
	}
	type toolTotal struct {
		time      time.Duration
		estimated bool
	}
	toolTime := map[string]toolTotal{}
	for _, tc := range refTools(calls, core.ToolCallFilter{From: f.From, To: f.To}, time.Now()) {
		if tc.DurationSource == "" {
			continue
		}
		tt := toolTime[tc.SessionKey]
		tt.time += tc.Duration
		tt.estimated = tt.estimated || tc.DurationSource == core.DurationEstimated
		toolTime[tc.SessionKey] = tt
	}
	var out []listed
	for key, g := range groups {
		if !g.hit {
			continue
		}
		slices.SortFunc(g.calls, func(a, b core.CallRecord) int { return compareCalls(a, b) })
		s := core.SessionSummary{
			SessionKey: key, KeyName: g.calls[0].KeyName, FirstTS: g.calls[0].TS, LastTS: g.calls[len(g.calls)-1].TS,
			Calls: len(g.calls),
		}
		models := map[string]bool{}
		agents := map[[3]string]bool{}
		prompts := map[string]bool{}
		tools := map[string]bool{}
		var total core.MicroUSD
		known := false
		for _, c := range g.calls {
			if s.Client == "" {
				s.Client = c.Client
			}
			if c.ErrorClass != "" {
				s.Errors++
			}
			if c.AgentID != "" {
				agents[[3]string{c.AgentID, c.ParentAgentID, c.AgentType}] = true
			}
			if c.Model != "" {
				models[c.Model] = true
			}
			if c.PromptID != "" {
				prompts[c.PromptID] = true
			}
			for _, u := range c.ToolUses {
				tools[u.ToolCallID] = true
			}
			for _, r := range c.ToolResults {
				tools[r.ToolCallID] = true
			}
			end := c.EndedAt
			if end.IsZero() {
				end = c.TS
			}
			if end.After(s.EndedAt) {
				s.EndedAt = end
			}
			s.LLMTime += c.Duration
			s.Usage.InputUncached += c.Usage.InputUncached
			s.Usage.CacheRead += c.Usage.CacheRead
			s.Usage.CacheWrite5m += c.Usage.CacheWrite5m
			s.Usage.CacheWrite1h += c.Usage.CacheWrite1h
			s.Usage.Output += c.Usage.Output
			s.Usage.Reasoning += c.Usage.Reasoning
			if c.Cost != nil {
				total, known = total+*c.Cost, true
			} else {
				s.UnpricedCalls++
			}
		}
		s.AgentCount, s.PromptCount, s.ToolCallCount = len(agents), len(prompts), len(tools)
		s.ToolTime, s.ToolTimeEstimated = toolTime[key].time, toolTime[key].estimated
		for m := range models {
			s.Models = append(s.Models, m)
		}
		slices.Sort(s.Models)
		if known {
			s.Cost = &total
		}
		if f.MinCost > 0 && (s.Cost == nil || *s.Cost < f.MinCost) {
			continue
		}
		out = append(out, listed{s, g.calls[len(g.calls)-1]})
	}
	slices.SortFunc(out, func(a, b listed) int { return compareCalls(b.last, a.last) })
	sessions := make([]core.SessionSummary, len(out))
	for i, l := range out {
		sessions[i] = l.s
	}
	return sessions
}

// compareCalls orders calls by (ts, id) ascending.
func compareCalls(a, b core.CallRecord) int {
	if c := a.TS.Compare(b.TS); c != 0 {
		return c
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}

// refAgents is the reference agent tree of a session: the distinct
// (agent_id, parent_agent_id, agent_type) of its calls with an agent id, with
// the number of calls of each, in the order of their first call.
func refAgents(calls []core.CallRecord, session string) []core.AgentNode {
	var mine []core.CallRecord
	for _, c := range calls {
		if c.SessionKey == session && c.AgentID != "" {
			mine = append(mine, c)
		}
	}
	slices.SortFunc(mine, compareCalls)
	var out []core.AgentNode
	for _, c := range mine {
		i := slices.IndexFunc(out, func(n core.AgentNode) bool {
			return n.AgentID == c.AgentID && n.ParentAgentID == c.ParentAgentID && n.AgentType == c.AgentType
		})
		if i < 0 {
			out = append(out, core.AgentNode{AgentID: c.AgentID, ParentAgentID: c.ParentAgentID, AgentType: c.AgentType})
			i = len(out) - 1
		}
		out[i].Calls++
	}
	return out
}
