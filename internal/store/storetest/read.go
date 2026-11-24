package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/store/postgres"
)

var (
	win  = core.CallFilter{From: Base, To: Base.Add(24 * time.Hour)}
	yes  = true
	no   = false
	page = func(limit int, after *core.Cursor) core.Page { return core.Page{After: after, Limit: limit} }
)

var readCases = []testCase{
	{"list_orders_by_ts_then_id_including_equal_ts", func(t *testing.T, e *env) {
		// Three timestamps, five calls each, with random ids inside each
		// timestamp, written out of order.
		var calls []core.CallRecord
		for _, sec := range []int{2, 0, 1} {
			ts := Base.Add(time.Duration(sec) * time.Second)
			for range 5 {
				calls = append(calls, e.gen.Call(e.gen.At(ts), WithID(core.NewIDAt(ts))))
			}
		}
		e.write(t, calls...)
		want := newestFirst(calls)
		for _, size := range []int{1, 2, 4, 7, 15, 100} {
			got := e.walk(t, win, size)
			if !slices.Equal(got, want) {
				t.Fatalf("page size %d: order differs\n got %v\nwant %v", size, got, want)
			}
		}
	}},
	{"list_pages_have_no_duplicates_or_gaps_while_inserting", func(t *testing.T, e *env) {
		g := e.gen
		first := g.Calls(20)
		e.write(t, first...)
		// Calls arrive between page reads, newer than every cursor and
		// sharing a timestamp with the oldest stored call.
		var seen []core.ID
		var after *core.Cursor
		for i := 0; ; i++ {
			rows, next, err := e.ListCalls(context.Background(), win, page(3, after))
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				seen = append(seen, r.ID)
			}
			e.write(t,
				g.Call(g.At(Base.Add(time.Hour+time.Duration(i)*time.Second))),
				g.Call(g.At(Base), WithID(core.NewIDAt(Base))),
			)
			if next == nil {
				break
			}
			after = next
		}
		// Every call stored before the walk is seen once, in order. Newer
		// calls are not seen, and nothing is seen twice.
		want := newestFirst(first)
		var got []core.ID
		for _, id := range seen {
			if slices.Contains(want, id) {
				got = append(got, id)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("gap or reorder\n got %v\nwant %v", got, want)
		}
		dup := map[core.ID]bool{}
		for _, id := range seen {
			if dup[id] {
				t.Fatalf("call %v listed twice", id)
			}
			dup[id] = true
		}
	}},
	{"list_time_range_is_half_open", func(t *testing.T, e *env) {
		g := e.gen
		at := func(d time.Duration) core.CallRecord { return g.Call(g.At(Base.Add(d))) }
		before, atFrom, inside, atTo := at(-time.Millisecond), at(0), at(time.Hour-time.Millisecond), at(time.Hour)
		e.write(t, before, atFrom, inside, atTo)
		f := core.CallFilter{From: Base, To: Base.Add(time.Hour)}
		got := e.walk(t, f, 10)
		if want := []core.ID{inside.ID, atFrom.ID}; !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		n, capped, err := e.CountCalls(context.Background(), f, 100)
		if err != nil || n != 2 || capped {
			t.Fatalf("count = %d capped %v err %v, want 2", n, capped, err)
		}
	}},
	{"list_rejects_an_unbounded_filter", func(t *testing.T, e *env) {
		ctx := context.Background()
		for _, f := range []core.CallFilter{{}, {From: Base}, {To: Base}, {From: Base, To: Base}} {
			if _, _, err := e.ListCalls(ctx, f, page(10, nil)); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("ListCalls(%+v) err = %v", f, err)
			}
			if _, _, err := e.CountCalls(ctx, f, 10); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("CountCalls(%+v) err = %v", f, err)
			}
		}
	}},
	{"list_limit_defaults_and_clamps", func(t *testing.T, e *env) {
		e.write(t, e.gen.Calls(postgres.MaxLimit+5)...)
		for _, c := range []struct{ limit, want int }{
			{0, postgres.DefaultLimit}, {-3, postgres.DefaultLimit}, {7, 7},
			{postgres.MaxLimit, postgres.MaxLimit}, {postgres.MaxLimit + 1, postgres.MaxLimit}, {1 << 30, postgres.MaxLimit},
		} {
			rows, next, err := e.ListCalls(context.Background(), win, page(c.limit, nil))
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != c.want || next == nil {
				t.Errorf("limit %d: %d rows, cursor %v; want %d rows and a cursor", c.limit, len(rows), next, c.want)
			}
		}
	}},
	{"list_last_page_has_no_cursor", func(t *testing.T, e *env) {
		e.write(t, e.gen.Calls(4)...)
		for _, limit := range []int{4, 5} {
			rows, next, err := e.ListCalls(context.Background(), win, page(limit, nil))
			if err != nil || len(rows) != 4 || next != nil {
				t.Errorf("limit %d: %d rows, cursor %v, err %v", limit, len(rows), next, err)
			}
		}
		rows, next, err := e.ListCalls(context.Background(), core.CallFilter{From: Base.Add(time.Hour), To: Base.Add(2 * time.Hour)}, page(4, nil))
		if err != nil || len(rows) != 0 || next != nil {
			t.Errorf("empty range: %d rows, cursor %v, err %v", len(rows), next, err)
		}
	}},
	{"count_is_exact_up_to_max_and_capped_above", func(t *testing.T, e *env) {
		e.write(t, e.gen.Calls(12)...)
		for _, c := range []struct {
			max, n int
			capped bool
		}{{20, 12, false}, {13, 12, false}, {12, 12, false}, {11, 11, true}, {1, 1, true}} {
			n, capped, err := e.CountCalls(context.Background(), win, c.max)
			if err != nil || n != c.n || capped != c.capped {
				t.Errorf("max %d: n=%d capped=%v err=%v, want n=%d capped=%v", c.max, n, capped, err, c.n, c.capped)
			}
		}
		if _, _, err := e.CountCalls(context.Background(), win, 0); !errors.Is(err, core.ErrInvalidFilter) {
			t.Errorf("max 0: err = %v", err)
		}
	}},
	{"list_and_count_apply_every_filter", func(t *testing.T, e *env) {
		calls := filterDataset(e.gen)
		e.write(t, calls...)
		tools := toolCalls(calls)
		touched := map[string]bool{}
		for _, c := range filterCases {
			t.Run(c.name, func(t *testing.T) {
				f := win
				c.set(&f)
				markSet(touched, f)
				var want []core.ID
				for _, r := range calls {
					if f.Match(r, tools) {
						want = append(want, r.ID)
					}
				}
				want = newestFirstIDs(calls, want)
				if len(want) == 0 || len(want) == len(calls) {
					t.Fatalf("filter selects %d of %d calls: the dataset does not exercise it", len(want), len(calls))
				}
				if got := e.walk(t, f, 3); !slices.Equal(got, want) {
					t.Errorf("list\n got %v\nwant %v", got, want)
				}
				n, capped, err := e.CountCalls(context.Background(), f, 100)
				if err != nil || n != len(want) || capped {
					t.Errorf("count = %d capped %v err %v, want %d", n, capped, err, len(want))
				}
			})
		}
		for field := range reflect.TypeFor[core.CallFilter]().Fields() {
			if name := field.Name; name != "From" && name != "To" && !touched[name] {
				t.Errorf("CallFilter.%s has no contract case", name)
			}
		}
	}},
	{"list_combines_filters_with_and", func(t *testing.T, e *env) {
		calls := filterDataset(e.gen)
		e.write(t, calls...)
		tools := toolCalls(calls)
		f := win
		f.Keys, f.Providers, f.Statuses = []string{"alice", "bob"}, []string{"anthropic"}, []int{200, 429}
		f.Streamed, f.MinLatency = &yes, time.Second
		var want []core.ID
		for _, r := range calls {
			if f.Match(r, tools) {
				want = append(want, r.ID)
			}
		}
		want = newestFirstIDs(calls, want)
		if len(want) == 0 {
			t.Fatal("dataset has no call for the combination")
		}
		if got := e.walk(t, f, 2); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	}},
	{"tool_error_matches_a_result_long_after_the_range", func(t *testing.T, e *env) {
		g := e.gen
		use := g.Call(g.At(Base), g.WithUse("toolu_late", "Bash"))
		late := g.Call(g.At(Base.Add(30*time.Hour)), g.WithResult("toolu_late", true))
		clean := g.Call(g.At(Base.Add(time.Minute)), g.WithUse("toolu_clean", "Bash"))
		cleanResult := g.Call(g.At(Base.Add(2*time.Minute)), g.WithResult("toolu_clean", false))
		calls := []core.CallRecord{use, late, clean, cleanResult}
		e.write(t, calls...)
		f := core.CallFilter{From: Base, To: Base.Add(time.Hour), ToolError: &yes}
		if !f.Match(use, toolCalls(calls)) {
			t.Fatal("Match, the reference, does not select the call")
		}
		if got, want := e.walk(t, f, 10), []core.ID{use.ID}; !slices.Equal(got, want) {
			t.Errorf("list got %v, want %v", got, want)
		}
		n, capped, err := e.CountCalls(context.Background(), f, 100)
		if err != nil || n != 1 || capped {
			t.Errorf("count = %d capped %v err %v, want 1", n, capped, err)
		}
	}},
	{"list_and_count_out_of_range_numbers_match_nothing", func(t *testing.T, e *env) {
		calls := e.gen.Calls(3)
		e.write(t, calls...)
		for _, set := range []func(*core.CallFilter){
			func(f *core.CallFilter) { f.Statuses = []int{70_000} },
			func(f *core.CallFilter) { f.Statuses = []int{70_000, 70_001} },
			func(f *core.CallFilter) { f.Statuses = []int{1<<32 + calls[0].Status} },
			func(f *core.CallFilter) { f.MinLatency = 30 * 24 * time.Hour },
			func(f *core.CallFilter) { f.MinCost = core.MicroUSD(1<<63 - 1) },
		} {
			f := win
			set(&f)
			for _, c := range calls {
				if f.Match(c, nil) {
					t.Fatalf("Match, the reference, selects a call for %+v", f)
				}
			}
			if got := e.walk(t, f, 10); len(got) != 0 {
				t.Errorf("%+v: list got %v, want none", f, got)
			}
			n, capped, err := e.CountCalls(context.Background(), f, 100)
			if err != nil || n != 0 || capped {
				t.Errorf("%+v: count = %d capped %v err %v, want 0", f, n, capped, err)
			}
		}
	}},
	{"list_row_has_summary_fields", func(t *testing.T, e *env) {
		c := fullRecord(e.gen)
		bare := e.gen.Call(e.gen.At(Base.Add(time.Minute)), func(c *core.CallRecord) {
			c.Client, c.Model, c.Status, c.StopReason, c.TTFT, c.Duration, c.Streamed = "", "", 0, "", 0, 0, false
			c.Cost = nil
		})
		e.write(t, c, bare)
		rows, _, err := e.ListCalls(context.Background(), win, page(10, nil))
		if err != nil || len(rows) != 2 {
			t.Fatalf("rows = %d, err %v", len(rows), err)
		}
		cost := *c.Cost
		wantFull := core.CallSummary{
			ID: c.ID, TS: c.TS, KeyName: c.KeyName, Client: c.Client, SessionKey: c.SessionKey, AgentID: c.AgentID,
			PromptID: c.PromptID, Project: c.Project, Provider: c.Provider, Model: c.Model, Status: c.Status, ErrorClass: c.ErrorClass, StopReason: c.StopReason,
			Streamed: c.Streamed, TTFT: c.TTFT, Duration: c.Duration, Usage: c.Usage, Cost: &cost, ToolCount: len(c.ToolUses),
			ContextCompacted: c.ContextCompacted,
		}
		wantBare := core.CallSummary{
			ID: bare.ID, TS: bare.TS, KeyName: bare.KeyName, Provider: bare.Provider, Usage: bare.Usage,
		}
		if !reflect.DeepEqual(rows[1], wantFull) {
			t.Errorf("full row\n got %+v\nwant %+v", rows[1], wantFull)
		}
		if !reflect.DeepEqual(rows[0], wantBare) {
			t.Errorf("bare row\n got %+v\nwant %+v", rows[0], wantBare)
		}
		if wantFull.ToolCount != 2 || wantFull.Cost == nil || *wantFull.Cost != 1_234_567 {
			t.Fatalf("test record is not what the case assumes: %+v", wantFull)
		}
	}},
	{"get_call_returns_the_record_with_its_tool_halves", func(t *testing.T, e *env) {
		c := fullRecord(e.gen)
		other := e.gen.Call(e.gen.WithUse("toolu_other", "Edit"))
		e.write(t, c, other)
		got, err := e.GetCall(context.Background(), c.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantRaw, gotRaw := decode(t, c.UsageRaw), decode(t, got.UsageRaw)
		if !reflect.DeepEqual(wantRaw, gotRaw) {
			t.Errorf("usage_raw = %s, want %s", got.UsageRaw, c.UsageRaw)
		}
		got.UsageRaw, c.UsageRaw = nil, nil
		if !reflect.DeepEqual(got.CallRecord, c) {
			t.Errorf("record\n got %+v\nwant %+v", got.CallRecord, c)
		}
		if len(got.ToolUses) != 2 || len(got.ToolResults) != 2 {
			t.Errorf("halves = %d uses and %d results, want 2 and 2", len(got.ToolUses), len(got.ToolResults))
		}
	}},
	{"get_call_unknown_fields_read_back_as_zero", func(t *testing.T, e *env) {
		c := e.gen.Call(func(c *core.CallRecord) {
			c.Client, c.ClientRaw, c.Model, c.Status, c.StopReason, c.EndedAt = "", "", "", 0, "", time.Time{}
			c.TTFB, c.TTFT, c.Duration, c.UsageRaw, c.Cost = 0, 0, 0, nil, nil
		})
		e.write(t, c)
		got, err := e.GetCall(context.Background(), c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.CallRecord, c) {
			t.Errorf("got %+v\nwant %+v", got.CallRecord, c)
		}
		if got.Cost != nil {
			t.Error("unknown cost read back as a value")
		}
	}},
	{"get_call_missing_is_not_found", func(t *testing.T, e *env) {
		e.write(t, e.gen.Call())
		if _, err := e.GetCall(context.Background(), core.NewID()); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}},
}

// walk lists every page of the filter and returns the ids in the order seen.
func (e *env) walk(t *testing.T, f core.CallFilter, size int) []core.ID {
	t.Helper()
	var ids []core.ID
	var after *core.Cursor
	for range 10_000 {
		rows, next, err := e.ListCalls(context.Background(), f, page(size, after))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > size {
			t.Fatalf("page of %d rows for limit %d", len(rows), size)
		}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		if next == nil {
			return ids
		}
		if len(rows) == 0 {
			t.Fatal("empty page with a cursor")
		}
		after = next
	}
	t.Fatal("pagination does not end")
	return nil
}

// newestFirst returns the ids of calls ordered by (ts, id) descending.
func newestFirst(calls []core.CallRecord) []core.ID {
	ids := make([]core.ID, len(calls))
	for i, c := range calls {
		ids[i] = c.ID
	}
	return newestFirstIDs(calls, ids)
}

// newestFirstIDs orders the chosen ids by (ts, id) descending.
func newestFirstIDs(calls []core.CallRecord, ids []core.ID) []core.ID {
	ts := map[core.ID]time.Time{}
	for _, c := range calls {
		ts[c.ID] = c.TS
	}
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b core.ID) int {
		if c := ts[b].Compare(ts[a]); c != 0 {
			return c
		}
		return slices.Compare(b[:], a[:])
	})
	return out
}

func decode(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("usage_raw %q: %v", raw, err)
	}
	return v
}

// toolCalls pairs the tool halves of the calls the way the reader does: a use
// with the result that has the same tool call id.
func toolCalls(calls []core.CallRecord) []core.ToolCall {
	results := map[string]core.ToolResult{}
	for _, c := range calls {
		for _, r := range c.ToolResults {
			results[r.ToolCallID] = r
		}
	}
	var out []core.ToolCall
	for _, c := range calls {
		for _, u := range c.ToolUses {
			tc := core.ToolCall{ToolCallID: u.ToolCallID, Name: u.Name, Source: u.Source, UseCallID: c.ID, UseTS: c.TS, Status: core.ToolCallUseOnly}
			if r, ok := results[u.ToolCallID]; ok {
				tc.Status, tc.IsError = core.ToolCallPaired, &r.IsError
			}
			out = append(out, tc)
		}
	}
	return out
}

// fullRecord is a call with every field set, two tool uses and two results.
func fullRecord(g *Gen) core.CallRecord {
	ts := Base.Add(30 * time.Second).Add(123456 * time.Microsecond)
	return core.CallRecord{
		ID: g.ID(ts), TS: ts,
		KeyName: "alice", Client: "claude-code", ClientRaw: "claude-cli/2.1.0 (external, cli)", ClientRequestID: "req-client-1",
		SessionKey: "sess-1", AgentID: "agent-1", ParentAgentID: "agent-0", AgentType: "subagent", PromptID: "prompt-1",
		Project: "spyglass", RequestClass: "main", Compaction: true, ContextCompacted: true, TraceID: "trace-1",
		Family: "anthropic", Endpoint: "/v1/messages", AuthMode: "client_passthrough", Provider: "anthropic",
		Model: "claude-opus-5-5", UpstreamRequestID: "req_up_1",
		Status: 529, ErrorClass: "upstream_overloaded", ErrorType: "overloaded_error", StopReason: "max_tokens", Streamed: true,
		TTFB: 120 * time.Millisecond, TTFT: 340 * time.Millisecond, Duration: 2500 * time.Millisecond,
		EndedAt: ts.Add(2500 * time.Millisecond),
		Usage: core.Usage{
			InputUncached: 11, CacheRead: 22, CacheWrite5m: 33, CacheWrite1h: 44, Output: 55, Reasoning: 5,
		},
		UsageRaw: json.RawMessage(`{"input_tokens":11,"output_tokens":55}`), UsageEstimated: true, ExtractionPartial: true,
		Cost: new(core.MicroUSD(1_234_567)), PricingVersion: "2026-10-01", PrevToolDurations: "1200,40",
		ToolUses: []core.ToolUse{
			{Source: core.ToolSourceClient, ToolCallID: "toolu_a", Name: "Bash", ArgBytes: 71},
			{Source: core.ToolSourceServer, ToolCallID: "srvtoolu_b", Name: "web_search", ArgBytes: 12},
		},
		ToolResults: []core.ToolResult{
			{Source: core.ToolSourceClient, ToolCallID: "toolu_x", IsError: true, ResultBytes: 300},
			{Source: core.ToolSourceClient, ToolCallID: "toolu_y", IsError: false, ResultBytes: 0},
		},
	}
}

// filterDataset is twelve calls, ten seconds apart, that differ in every
// filtered field so that each filter case selects some but not all of them.
func filterDataset(g *Gen) []core.CallRecord {
	mut := func(i int, f func(*core.CallRecord)) core.CallRecord {
		return g.Call(g.At(Base.Add(time.Duration(i)*10*time.Second)), f)
	}
	return []core.CallRecord{
		mut(0, func(c *core.CallRecord) {
			c.SessionKey, c.AgentID, c.AgentType, c.PromptID, c.RequestClass = "s1", "a1", "main", "p1", "main"
			c.Project = "billing"
			c.Cost = new(core.MicroUSD(1000))
			g.WithUse("toolu_1", "Bash")(c)
		}),
		mut(1, func(c *core.CallRecord) {
			c.KeyName, c.Client, c.Status, c.ErrorClass, c.Streamed = "bob", "", 429, "rate_limited", false
			c.SessionKey, c.AgentID, c.AgentType, c.RequestClass = "s1", "a2", "subagent", "main"
			c.Duration = 90 * time.Second
			g.WithResult("toolu_1", true)(c)
			g.WithUse("toolu_2", "Grep")(c)
		}),
		mut(2, func(c *core.CallRecord) {
			c.Provider, c.Model, c.Endpoint = "other", "claude-sonnet-5-5", "/v1/messages/count_tokens"
			c.SessionKey, c.PromptID, c.RequestClass = "s2", "p2", "compaction"
			c.Project = "billing"
			c.Cost = new(core.MicroUSD(5000))
			g.WithResult("toolu_2", false)(c)
			g.WithUse("toolu_3", "Bash")(c)
			g.WithUse("toolu_4", "Edit")(c)
		}),
		mut(3, func(c *core.CallRecord) {
			c.KeyName, c.Model, c.Status = "carol", "claude-haiku-5-5", 500
			c.ErrorClass = "upstream_error"
			c.Duration = 2 * time.Second
			g.WithResult("toolu_3", true)(c)
		}),
		mut(4, func(c *core.CallRecord) {
			c.Client, c.Streamed = "sdk", false
			c.Project = "web"
			c.Cost = new(core.MicroUSD(0))
			c.Duration = time.Second
			g.WithUse("toolu_5", "Read")(c)
		}),
		mut(5, func(c *core.CallRecord) {
			c.Status, c.ErrorClass, c.Duration = 0, "client_cancelled", 0
			c.Cost = new(core.MicroUSD(999_999))
		}),
		mut(6, func(c *core.CallRecord) { c.AgentType, c.RequestClass = "subagent", "auxiliary" }),
		mut(7, func(c *core.CallRecord) { g.WithResult("toolu_4", false)(c) }),
		mut(8, func(c *core.CallRecord) { c.SessionKey, c.AgentID = "s2", "a1" }),
		mut(9, func(c *core.CallRecord) {}),
		mut(10, func(c *core.CallRecord) { c.Duration = 1500 * time.Millisecond }),
		mut(11, func(c *core.CallRecord) { c.Model = "" }),
	}
}

var filterCases = []struct {
	name string
	set  func(*core.CallFilter)
}{
	{"keys", func(f *core.CallFilter) { f.Keys = []string{"bob", "carol"} }},
	{"clients", func(f *core.CallFilter) { f.Clients = []string{"sdk", "claude-code"} }},
	{"clients_unknown", func(f *core.CallFilter) { f.Clients = []string{""} }},
	{"projects", func(f *core.CallFilter) { f.Projects = []string{"web", "billing"} }},
	{"projects_one", func(f *core.CallFilter) { f.Projects = []string{"web"} }},
	{"projects_unknown", func(f *core.CallFilter) { f.Projects = []string{""} }},
	{"providers", func(f *core.CallFilter) { f.Providers = []string{"other"} }},
	{"models", func(f *core.CallFilter) { f.Models = []string{"claude-sonnet-5-5", "claude-haiku-5-5"} }},
	{"models_unknown", func(f *core.CallFilter) { f.Models = []string{""} }},
	{"endpoints", func(f *core.CallFilter) { f.Endpoints = []string{"/v1/messages/count_tokens"} }},
	{"streamed_true", func(f *core.CallFilter) { f.Streamed = &yes }},
	{"streamed_false", func(f *core.CallFilter) { f.Streamed = &no }},
	{"statuses", func(f *core.CallFilter) { f.Statuses = []int{429, 500} }},
	{"statuses_unknown", func(f *core.CallFilter) { f.Statuses = []int{0} }},
	{"error_classes", func(f *core.CallFilter) { f.ErrorClasses = []string{"rate_limited", "upstream_error"} }},
	{"error_classes_none", func(f *core.CallFilter) { f.ErrorClasses = []string{""} }},
	{"session_key", func(f *core.CallFilter) { f.SessionKey = "s1" }},
	{"agent_id", func(f *core.CallFilter) { f.AgentID = "a1" }},
	{"agent_types", func(f *core.CallFilter) { f.AgentTypes = []string{"subagent"} }},
	{"prompt_id", func(f *core.CallFilter) { f.PromptID = "p2" }},
	{"request_classes", func(f *core.CallFilter) { f.RequestClasses = []string{"compaction", "auxiliary"} }},
	{"tools", func(f *core.CallFilter) { f.Tools = []string{"Bash", "Read"} }},
	{"tools_one", func(f *core.CallFilter) { f.Tools = []string{"Edit"} }},
	{"tool_error_true", func(f *core.CallFilter) { f.ToolError = &yes }},
	{"tool_error_false", func(f *core.CallFilter) { f.ToolError = &no }},
	{"tools_and_tool_error", func(f *core.CallFilter) { f.Tools, f.ToolError = []string{"Bash"}, &yes }},
	{"min_latency", func(f *core.CallFilter) { f.MinLatency = 1500 * time.Millisecond }},
	{"min_cost", func(f *core.CallFilter) { f.MinCost = 1000 }},
}

// markSet records the names of the non-zero fields of f other than the range.
func markSet(touched map[string]bool, f core.CallFilter) {
	v := reflect.ValueOf(f)
	for field, val := range v.Fields() {
		if name := field.Name; name != "From" && name != "To" && !val.IsZero() {
			touched[name] = true
		}
	}
}
