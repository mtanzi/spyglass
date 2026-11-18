package storetest

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// toolWin is the time range of the tool-call cases: the first hour after Base.
var toolWin = core.ToolCallFilter{From: Base, To: Base.Add(time.Hour)}

// tuse is a client tool use with a chosen argument size.
func tuse(id, name string, argBytes int) CallOpt {
	return func(c *core.CallRecord) {
		c.ToolUses = append(c.ToolUses, core.ToolUse{
			Source: core.ToolSourceClient, ToolCallID: id, Name: name, ArgBytes: argBytes,
		})
	}
}

// tres is a client tool result with a chosen size.
func tres(id string, isError bool, resultBytes int) CallOpt {
	return func(c *core.CallRecord) {
		c.ToolResults = append(c.ToolResults, core.ToolResult{
			Source: core.ToolSourceClient, ToolCallID: id, IsError: isError, ResultBytes: resultBytes,
		})
	}
}

var toolCases = []testCase{
	{"tool_use_then_its_result_resolves", func(t *testing.T, e *env) {
		g := e.gen
		use := g.Call(g.At(Base), WithSession("s1"), WithAgent("a1", "", "main"), tuse("toolu_1", "Bash", 120))
		res := g.Call(g.At(Base.Add(30*time.Second)), WithSession("s1"), tres("toolu_1", true, 3400))
		e.write(t, use, res)
		want := core.ToolCall{
			ToolCallID: "toolu_1", Name: "Bash", Source: core.ToolSourceClient, Status: core.ToolCallPaired,
			SessionKey: "s1", AgentID: "a1", UseCallID: use.ID, ResultCallID: res.ID,
			UseTS: use.EndedAt, ResultTS: res.TS,
			Duration: res.TS.Sub(use.EndedAt), DurationSource: core.DurationEstimated,
			IsError: &yes, ArgBytes: 120, ResultBytes: 3400,
		}
		e.wantTools(t, toolWin, want)
	}},
	{"tool_result_before_its_use_resolves_once_the_use_arrives", func(t *testing.T, e *env) {
		g := e.gen
		use := g.Call(g.At(Base), WithSession("s1"), tuse("toolu_1", "Bash", 120))
		res := g.Call(g.At(Base.Add(30*time.Second)), WithSession("s1"), tres("toolu_1", false, 80))
		e.write(t, res)
		e.wantTools(t, toolWin, core.ToolCall{
			ToolCallID: "toolu_1", Source: core.ToolSourceClient, Status: core.ToolCallResultOnly,
			SessionKey: "s1", ResultCallID: res.ID, ResultTS: res.TS, IsError: &no, ResultBytes: 80,
		})
		e.write(t, use)
		e.wantTools(t, toolWin, core.ToolCall{
			ToolCallID: "toolu_1", Name: "Bash", Source: core.ToolSourceClient, Status: core.ToolCallPaired,
			SessionKey: "s1", UseCallID: use.ID, ResultCallID: res.ID, UseTS: use.EndedAt, ResultTS: res.TS,
			Duration: res.TS.Sub(use.EndedAt), DurationSource: core.DurationEstimated,
			IsError: &no, ArgBytes: 120, ResultBytes: 80,
		})
	}},
	{"tool_pairing_ignores_the_session_key", func(t *testing.T, e *env) {
		g := e.gen
		use := g.Call(g.At(Base), WithSession("s1"), tuse("toolu_1", "Bash", 1))
		res := g.Call(g.At(Base.Add(time.Second)), WithSession("s2"), tres("toolu_1", false, 1))
		bare := g.Call(g.At(Base.Add(2*time.Second)), tuse("toolu_2", "Read", 1))
		bareRes := g.Call(g.At(Base.Add(3*time.Second)), WithSession("s3"), tres("toolu_2", false, 1))
		e.write(t, use, res, bare, bareRes)
		got := e.listTools(t, toolWin, 10)
		if len(got) != 2 || got[0].Status != core.ToolCallPaired || got[1].Status != core.ToolCallPaired {
			t.Fatalf("got %+v, want two paired tool calls", got)
		}
		if got[1].SessionKey != "s1" || got[0].SessionKey != "" {
			t.Errorf("sessions %q and %q, want the use's: s1 and none", got[1].SessionKey, got[0].SessionKey)
		}
	}},
	{"tool_use_without_result_is_pending_then_use_only_after_one_hour", func(t *testing.T, e *env) {
		g := e.gen
		now := time.Now().Truncate(time.Millisecond)
		// The use is timed at the end of its call, 1.5 s after the start.
		young := g.Call(g.At(now.Add(-10*time.Minute)), tuse("toolu_young", "Bash", 1))
		old := g.Call(g.At(now.Add(-core.ToolIdleAfter-10*time.Minute)), tuse("toolu_old", "Bash", 1))
		e.write(t, young, old)
		f := core.ToolCallFilter{From: now.Add(-3 * time.Hour), To: now.Add(time.Hour)}
		got := e.listTools(t, f, 10)
		if len(got) != 2 || got[0].ToolCallID != "toolu_young" || got[1].ToolCallID != "toolu_old" {
			t.Fatalf("got %+v", got)
		}
		if got[0].Status != core.ToolCallPending || got[1].Status != core.ToolCallUseOnly {
			t.Errorf("statuses %q and %q, want pending and use_only", got[0].Status, got[1].Status)
		}
		for _, tc := range got {
			if tc.ResultCallID != (core.ID{}) || !tc.ResultTS.IsZero() || tc.IsError != nil ||
				tc.Duration != 0 || tc.DurationSource != "" || tc.ResultBytes != 0 {
				t.Errorf("unmatched use carries result fields: %+v", tc)
			}
		}
		f.Statuses = []core.ToolCallStatus{core.ToolCallUseOnly}
		if got := e.listTools(t, f, 10); len(got) != 1 || got[0].ToolCallID != "toolu_old" {
			t.Errorf("use_only filter got %+v", got)
		}
	}},
	{"tool_result_up_to_24_hours_past_to_still_pairs", func(t *testing.T, e *env) {
		g := e.gen
		to := toolWin.To
		late := func(id string, at time.Time) (core.CallRecord, core.CallRecord) {
			return g.Call(g.At(Base.Add(59*time.Minute)), tuse(id, "Bash", 1)), g.Call(g.At(at), tres(id, false, 1))
		}
		u1, r1 := late("toolu_23h", to.Add(23*time.Hour))
		u2, r2 := late("toolu_edge", to.Add(core.ToolResultLookahead-time.Millisecond))
		u3, r3 := late("toolu_24h", to.Add(core.ToolResultLookahead))
		u4, r4 := late("toolu_25h", to.Add(25*time.Hour))
		e.write(t, u1, r1, u2, r2, u3, r3, u4, r4)
		status := map[string]core.ToolCallStatus{}
		for _, tc := range e.listTools(t, toolWin, 10) {
			status[tc.ToolCallID] = tc.Status
		}
		want := map[string]core.ToolCallStatus{
			"toolu_23h": core.ToolCallPaired, "toolu_edge": core.ToolCallPaired,
			"toolu_24h": core.ToolCallUseOnly, "toolu_25h": core.ToolCallUseOnly,
		}
		if !reflect.DeepEqual(status, want) {
			t.Fatalf("statuses %v, want %v", status, want)
		}
		// A wider range reaches the later results.
		wide := toolWin
		wide.To = to.Add(2 * time.Hour)
		for _, tc := range e.listTools(t, wide, 10) {
			if tc.Status != core.ToolCallPaired {
				t.Errorf("%s is %s in a range that ends %v after the use", tc.ToolCallID, tc.Status, wide.To.Sub(tc.UseTS))
			}
		}
	}},
	{"tool_result_without_a_use_is_result_only_in_its_own_range", func(t *testing.T, e *env) {
		g := e.gen
		orphan := g.Call(g.At(Base.Add(time.Minute)), tres("toolu_orphan", true, 9))
		again := g.Call(g.At(Base.Add(2*time.Minute)), tres("toolu_orphan", true, 9))
		// The use of this result is long before the range, so the result is not an orphan.
		early := g.Call(g.At(Base.Add(-48*time.Hour)), tuse("toolu_early", "Bash", 1))
		answer := g.Call(g.At(Base.Add(3*time.Minute)), tres("toolu_early", false, 1))
		outside := g.Call(g.At(Base.Add(2*time.Hour)), tres("toolu_outside", false, 1))
		e.write(t, orphan, again, early, answer, outside)
		// One orphan, however often its result was sent; the other results are not orphans here.
		e.wantTools(t, toolWin, core.ToolCall{
			ToolCallID: "toolu_orphan", Source: core.ToolSourceClient, Status: core.ToolCallResultOnly,
			ResultCallID: orphan.ID, ResultTS: orphan.TS, IsError: &yes, ResultBytes: 9,
		})
		got := e.listTools(t, core.ToolCallFilter{From: Base.Add(2 * time.Hour), To: Base.Add(3 * time.Hour)}, 10)
		if len(got) != 1 || got[0].ToolCallID != "toolu_outside" || got[0].Status != core.ToolCallResultOnly {
			t.Errorf("later range got %+v", got)
		}
	}},
	{"tool_duplicate_results_pair_with_the_earliest", func(t *testing.T, e *env) {
		g := e.gen
		use := g.Call(g.At(Base), tuse("toolu_1", "Bash", 1))
		second := g.Call(g.At(Base.Add(50*time.Second)), tres("toolu_1", true, 1))
		first := g.Call(g.At(Base.Add(10*time.Second)), tres("toolu_1", false, 1))
		e.write(t, use, second, first)
		got := e.listTools(t, toolWin, 10)
		if len(got) != 1 || got[0].ResultCallID != first.ID || got[0].IsError == nil || *got[0].IsError {
			t.Fatalf("got %+v, want one tool call paired with the earliest result", got)
		}
	}},
	{"tool_duration_is_the_header_value_or_the_estimated_gap", func(t *testing.T, e *env) {
		g := e.gen
		// Five tools in one response, answered together: two Bash uses run in
		// parallel, and one tool name the header percent-encodes. The header
		// reports all but Grep, one entry per tool call, in order.
		use := g.Call(g.At(Base),
			tuse("toolu_b1", "Bash", 1), tuse("toolu_r", "Read", 1), tuse("toolu_b2", "Bash", 1),
			tuse("toolu_g", "Grep", 1), tuse("toolu_m", "mcp tool;x=1,%", 1))
		res := g.Call(g.At(Base.Add(10*time.Second)),
			tres("toolu_b1", false, 1), tres("toolu_r", false, 1), tres("toolu_b2", false, 1),
			tres("toolu_g", false, 1), tres("toolu_m", false, 1),
			WithPrevToolDurations("Bash=1200;Read=40;Bash=310;mcp%20tool%3Bx%3D1%2C%25=77"))
		e.write(t, use, res)
		type dur struct {
			d   time.Duration
			src core.DurationSource
		}
		got := map[string]dur{}
		for _, tc := range e.listTools(t, toolWin, 10) {
			got[tc.ToolCallID] = dur{tc.Duration, tc.DurationSource}
		}
		gap := res.TS.Sub(use.EndedAt)
		want := map[string]dur{
			"toolu_b1": {1200 * time.Millisecond, core.DurationHeader},
			"toolu_r":  {40 * time.Millisecond, core.DurationHeader},
			"toolu_b2": {310 * time.Millisecond, core.DurationHeader},
			"toolu_g":  {gap, core.DurationEstimated},
			"toolu_m":  {77 * time.Millisecond, core.DurationHeader},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("durations %v, want %v", got, want)
		}
		if ref := refTools([]core.CallRecord{use, res}, toolWin, time.Now()); !reflect.DeepEqual(ref, e.listTools(t, toolWin, 10)) {
			t.Fatalf("reference disagrees: %+v", ref)
		}
	}},
	{"tool_duration_header_entries_go_to_uses_of_their_name_in_order", func(t *testing.T, e *env) {
		g := e.gen
		// Three Bash uses over two responses, all answered by one call. The
		// header has two Bash entries and a malformed one between them: the
		// third use has no entry and falls back to the gap.
		u1 := g.Call(g.At(Base), tuse("toolu_1", "Bash", 1), tuse("toolu_2", "Bash", 1))
		u2 := g.Call(g.At(Base.Add(time.Second)), tuse("toolu_3", "Bash", 1))
		res := g.Call(g.At(Base.Add(10*time.Second)),
			tres("toolu_3", false, 1), tres("toolu_2", false, 1), tres("toolu_1", false, 1),
			WithPrevToolDurations("Bash=500;Bash=x;Bash=600"))
		e.write(t, res, u2, u1)
		got := map[string]core.DurationSource{}
		ms := map[string]time.Duration{}
		for _, tc := range e.listTools(t, toolWin, 10) {
			got[tc.ToolCallID], ms[tc.ToolCallID] = tc.DurationSource, tc.Duration
		}
		want := map[string]core.DurationSource{"toolu_1": core.DurationHeader, "toolu_2": core.DurationHeader, "toolu_3": core.DurationEstimated}
		if !reflect.DeepEqual(got, want) || ms["toolu_1"] != 500*time.Millisecond || ms["toolu_2"] != 600*time.Millisecond ||
			ms["toolu_3"] != res.TS.Sub(u2.EndedAt) {
			t.Fatalf("sources %v durations %v", got, ms)
		}
	}},
	{"tool_duration_ignores_malformed_header_entries", func(t *testing.T, e *env) {
		g := e.gen
		for i, header := range []string{
			`not a header`, `{"Bash":1200}`, `Bash`, `Bash=`, `=1200`, `Read=5`, `Bash=-5`, `Bash=1.5`,
			`Bash=12x`, `Bash=1=2`, `Ba%7=5`, `Ba%ZZh=5`, `Bash =5`, `Bash=31536000001`, `Bash=99999999999999999999999`,
		} {
			id := "toolu_" + string(rune('a'+i))
			start := Base.Add(time.Duration(i) * time.Minute)
			e.write(t,
				g.Call(g.At(start), tuse(id, "Bash", 1)),
				g.Call(g.At(start.Add(5*time.Second)), tres(id, false, 1), WithPrevToolDurations(header)))
		}
		got := e.listTools(t, core.ToolCallFilter{From: Base, To: Base.Add(time.Hour)}, 50)
		if len(got) != 15 {
			t.Fatalf("%d tool calls, want 15", len(got))
		}
		for _, tc := range got {
			if tc.DurationSource != core.DurationEstimated || tc.Duration != 3500*time.Millisecond {
				t.Errorf("%s: %v from %q, want the 3.5 s gap", tc.ToolCallID, tc.Duration, tc.DurationSource)
			}
		}
	}},
	{"tool_server_side_tool_is_complete_inside_one_call", func(t *testing.T, e *env) {
		g := e.gen
		c := g.Call(g.At(Base), WithSession("s1"), g.WithServerTool("srvtoolu_1", "web_search", true),
			WithPrevToolDurations("web_search=999"))
		e.write(t, c)
		e.wantTools(t, toolWin, core.ToolCall{
			ToolCallID: "srvtoolu_1", Name: "web_search", Source: core.ToolSourceServer, Status: core.ToolCallPaired,
			SessionKey: "s1", UseCallID: c.ID, ResultCallID: c.ID, UseTS: c.EndedAt, ResultTS: c.EndedAt,
			DurationSource: core.DurationEstimated, IsError: &yes,
			ArgBytes: len(g.Bodies()[2]), ResultBytes: len(g.Bodies()[3]),
		})
		f := toolWin
		f.Sources = []core.ToolSource{core.ToolSourceClient}
		if got := e.listTools(t, f, 10); len(got) != 0 {
			t.Errorf("client-only filter got %+v", got)
		}
	}},
	{"tool_list_rejects_an_unbounded_filter", func(t *testing.T, e *env) {
		for _, f := range []core.ToolCallFilter{{}, {From: Base}, {To: Base}, {From: Base, To: Base}} {
			if _, _, err := e.ListToolCalls(context.Background(), f, page(10, nil)); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("ListToolCalls(%+v) err = %v", f, err)
			}
			if _, err := e.ToolSummary(context.Background(), f); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("ToolSummary(%+v) err = %v", f, err)
			}
		}
	}},
	{"tool_list_orders_newest_first_and_pages_without_gaps_or_duplicates", func(t *testing.T, e *env) {
		calls := toolDataset(e.gen)
		e.write(t, calls...)
		want := refTools(calls, toolWin, time.Now())
		if len(want) < 8 {
			t.Fatalf("dataset has %d tool calls", len(want))
		}
		for _, size := range []int{1, 2, 3, 7, 100} {
			if got := e.walkTools(t, toolWin, size); !reflect.DeepEqual(got, want) {
				t.Fatalf("page size %d\n got %+v\nwant %+v", size, got, want)
			}
		}
	}},
	{"tool_list_and_summary_apply_every_filter", func(t *testing.T, e *env) {
		calls := toolDataset(e.gen)
		e.write(t, calls...)
		all := refTools(calls, toolWin, time.Now())
		touched := map[string]bool{}
		for _, c := range toolFilterCases {
			t.Run(c.name, func(t *testing.T) {
				f := toolWin
				c.set(&f)
				for field, val := range reflect.ValueOf(f).Fields() {
					if name := field.Name; name != "From" && name != "To" && !val.IsZero() {
						touched[name] = true
					}
				}
				var want []core.ToolCall
				for _, tc := range all {
					if f.Match(tc) {
						want = append(want, tc)
					}
				}
				if len(want) == 0 || len(want) == len(all) {
					t.Fatalf("filter selects %d of %d tool calls: the dataset does not exercise it", len(want), len(all))
				}
				if got := e.walkTools(t, f, 2); !reflect.DeepEqual(got, want) {
					t.Errorf("list\n got %+v\nwant %+v", got, want)
				}
				e.wantSummary(t, f, refSummary(calls, want))
			})
		}
		for field := range reflect.TypeFor[core.ToolCallFilter]().Fields() {
			if name := field.Name; name != "From" && name != "To" && !touched[name] {
				t.Errorf("ToolCallFilter.%s has no contract case", name)
			}
		}
	}},
	{"tool_filter_combines_fields_with_and", func(t *testing.T, e *env) {
		calls := toolDataset(e.gen)
		e.write(t, calls...)
		f := toolWin
		f.SessionKey, f.Tools, f.IsError = "s1", []string{"Bash", "Read"}, &no
		var want []core.ToolCall
		for _, tc := range refTools(calls, toolWin, time.Now()) {
			if f.Match(tc) {
				want = append(want, tc)
			}
		}
		if len(want) == 0 {
			t.Fatal("dataset has no tool call for the combination")
		}
		if got := e.walkTools(t, f, 2); !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v\nwant %+v", got, want)
		}
	}},
	{"tool_summary_counts_errors_percentiles_and_total_time_per_tool_and_agent_type", func(t *testing.T, e *env) {
		g := e.gen
		// Bash by a main agent: five tool calls of 100 to 500 ms, one of them an error.
		// Bash by a subagent: two of 1 s and 3 s. Read by a main agent: one use with no result.
		var calls []core.CallRecord
		at := Base
		for i := 1; i <= 5; i++ {
			id := "toolu_m" + string(rune('0'+i))
			at = at.Add(10 * time.Second)
			calls = append(calls,
				g.Call(g.At(at), WithAgentType("main"), tuse(id, "Bash", 1)),
				g.Call(g.At(at.Add(time.Second)), tres(id, i == 5, 1), WithPrevToolDurations("Bash="+string(rune('0'+i))+"00")))
		}
		for i, ms := range []int{1000, 3000} {
			id := "toolu_s" + string(rune('0'+i))
			at = at.Add(10 * time.Second)
			calls = append(calls,
				g.Call(g.At(at), WithAgentType("subagent"), tuse(id, "Bash", 1)),
				g.Call(g.At(at.Add(time.Second)), tres(id, false, 1), WithPrevToolDurations("Bash="+strconv.Itoa(ms))))
		}
		calls = append(calls, g.Call(g.At(at.Add(time.Minute)), WithAgentType("main"), tuse("toolu_lost", "Read", 1)))
		e.write(t, calls...)
		e.wantSummary(t, toolWin, []core.ToolStat{
			{Name: "Bash", AgentType: "main", Count: 5, Errors: 1,
				DurationP50: 300 * time.Millisecond, DurationP95: 480 * time.Millisecond, TotalDuration: 1500 * time.Millisecond},
			{Name: "Bash", AgentType: "subagent", Count: 2,
				DurationP50: 2 * time.Second, DurationP95: 2900 * time.Millisecond, TotalDuration: 4 * time.Second},
			{Name: "Read", AgentType: "main", Count: 1},
		})
	}},
}

// toolDataset is nine calls, in two sessions and one without, whose tool halves
// make paired, use-only and result-only tool calls, with errors, header and
// estimated durations, a server tool, and several tools in one call.
func toolDataset(g *Gen) []core.CallRecord {
	at := func(sec int, opts ...CallOpt) core.CallRecord {
		return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second))}, opts...)...)
	}
	return []core.CallRecord{
		at(0, WithSession("s1"), WithAgent("a1", "", "main"), tuse("toolu_1", "Bash", 100), tuse("toolu_2", "Read", 50)),
		at(20, WithSession("s1"), WithAgent("a1", "", "main"),
			tres("toolu_1", true, 300), tres("toolu_2", false, 5000), tuse("toolu_3", "Grep", 10),
			WithPrevToolDurations("Bash=4000;Read=10")),
		at(40, WithSession("s2"), WithAgent("a2", "a1", "subagent"),
			tres("toolu_3", false, 20), tuse("toolu_4", "Edit", 2000), g.WithServerTool("srvtoolu_1", "web_search", false)),
		at(60, WithSession("s2"), WithAgent("a2", "a1", "subagent"), tres("toolu_4", true, 7), tuse("toolu_5", "Bash", 100)),
		at(80, tuse("toolu_6", "Read", 20)),
		at(100, WithSession("s1"), WithAgent("a1", "", "main"), tres("toolu_orphan", false, 900)),
		at(120, WithSession("s1"), WithAgent("a3", "a1", "subagent"), tres("toolu_5", false, 40), tuse("toolu_7", "Bash", 3000)),
		at(140, WithSession("s2"), WithAgent("a2", "a1", "subagent"), tres("toolu_7", false, 60)),
		// Mixed case and punctuation in names and agent types: byte order
		// ("Bash" < "_mcp" < "bash", "Subagent" < "subagent") and glibc locale
		// order differ. The summary sorts bytewise (COLLATE "C").
		at(160, WithSession("s2"), WithAgent("a4", "a1", "Subagent"), tuse("toolu_8", "_mcp", 10), tuse("toolu_9", "bash", 10)),
	}
}

var toolFilterCases = []struct {
	name string
	set  func(*core.ToolCallFilter)
}{
	{"session_key", func(f *core.ToolCallFilter) { f.SessionKey = "s2" }},
	{"agent_id", func(f *core.ToolCallFilter) { f.AgentID = "a1" }},
	{"tools", func(f *core.ToolCallFilter) { f.Tools = []string{"Bash", "Edit"} }},
	{"tools_unknown_name", func(f *core.ToolCallFilter) { f.Tools = []string{""} }},
	{"sources", func(f *core.ToolCallFilter) { f.Sources = []core.ToolSource{core.ToolSourceServer} }},
	{"statuses_use_only", func(f *core.ToolCallFilter) { f.Statuses = []core.ToolCallStatus{core.ToolCallUseOnly} }},
	{"statuses_result_only", func(f *core.ToolCallFilter) { f.Statuses = []core.ToolCallStatus{core.ToolCallResultOnly} }},
	{"statuses_unmatched", func(f *core.ToolCallFilter) {
		f.Statuses = []core.ToolCallStatus{core.ToolCallUseOnly, core.ToolCallResultOnly}
	}},
	{"is_error_true", func(f *core.ToolCallFilter) { f.IsError = &yes }},
	{"is_error_false", func(f *core.ToolCallFilter) { f.IsError = &no }},
	{"min_duration", func(f *core.ToolCallFilter) { f.MinDuration = 4 * time.Second }},
	{"min_arg_bytes", func(f *core.ToolCallFilter) { f.MinArgBytes = 100 }},
	{"min_result_bytes", func(f *core.ToolCallFilter) { f.MinResultBytes = 300 }},
}

// wantTools fails the case unless the filter selects exactly these tool calls.
func (e *env) wantTools(t *testing.T, f core.ToolCallFilter, want ...core.ToolCall) {
	t.Helper()
	if got := e.listTools(t, f, 100); !reflect.DeepEqual(got, want) {
		t.Fatalf("tool calls\n got %+v\nwant %+v", got, want)
	}
}

func (e *env) listTools(t *testing.T, f core.ToolCallFilter, size int) []core.ToolCall {
	t.Helper()
	return e.walkTools(t, f, size)
}

// walkTools lists every page and returns the tool calls in the order seen.
func (e *env) walkTools(t *testing.T, f core.ToolCallFilter, size int) []core.ToolCall {
	t.Helper()
	var out []core.ToolCall
	var after *core.Cursor
	for range 10_000 {
		rows, next, err := e.ListToolCalls(context.Background(), f, page(size, after))
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

func (e *env) wantSummary(t *testing.T, f core.ToolCallFilter, want []core.ToolStat) {
	t.Helper()
	got, err := e.ToolSummary(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary\n got %+v\nwant %+v", got, want)
	}
}

// refHalf is one tool half as the write path stores it.
type refHalf struct {
	call core.CallRecord
	idx  int
	ts   time.Time
}

// refTools is the reference for ListToolCalls: it pairs the halves of the
// calls in Go, by the rules of ADR 0020, for the time range of f, and returns
// every tool call in the range, newest first. now is the clock of the
// pending and use_only rule. It does not apply the other fields of f.
func refTools(calls []core.CallRecord, f core.ToolCallFilter, now time.Time) []core.ToolCall {
	var uses, results []refHalf
	useIDs := map[string]bool{}
	for _, c := range calls {
		end := c.EndedAt
		if end.IsZero() {
			end = c.TS
		}
		idx := 0
		for _, r := range c.ToolResults {
			ts := c.TS
			if r.Source == core.ToolSourceServer {
				ts = end
			}
			results = append(results, refHalf{c, idx, ts})
			idx++
		}
		for _, u := range c.ToolUses {
			uses = append(uses, refHalf{c, idx, end})
			useIDs[u.ToolCallID] = true
			idx++
		}
	}
	// A half's tool call id and body, from its call.
	useOf := func(h refHalf) core.ToolUse { return h.call.ToolUses[h.idx-len(h.call.ToolResults)] }
	resOf := func(h refHalf) core.ToolResult { return h.call.ToolResults[h.idx] }
	before := func(a, b refHalf) bool {
		if c := a.ts.Compare(b.ts); c != 0 {
			return c < 0
		}
		if c := bytes.Compare(a.call.ID[:], b.call.ID[:]); c != 0 {
			return c < 0
		}
		return a.idx < b.idx
	}
	earliest := map[string]refHalf{}
	for _, h := range results {
		id := resOf(h).ToolCallID
		if cur, ok := earliest[id]; !ok || before(h, cur) {
			earliest[id] = h
		}
	}

	// ordinal is the place of each client use, in use order, among the
	// client uses of its name resolved by the same call.
	type resolver struct {
		call core.ID
		name string
	}
	resolved := map[resolver][]refHalf{}
	for _, h := range uses {
		u := useOf(h)
		if r, ok := earliest[u.ToolCallID]; ok && u.Source == core.ToolSourceClient {
			k := resolver{r.call.ID, u.Name}
			resolved[k] = append(resolved[k], h)
		}
	}
	type halfKey struct {
		call core.ID
		idx  int
	}
	ordinal := map[halfKey]int{}
	for _, hs := range resolved {
		slices.SortFunc(hs, func(a, b refHalf) int {
			if before(a, b) {
				return -1
			}
			return 1
		})
		for i, h := range hs {
			ordinal[halfKey{h.call.ID, h.idx}] = i + 1
		}
	}

	type placed struct {
		tc core.ToolCall
		at refHalf
	}
	var out []placed
	inRange := func(ts time.Time) bool { return !ts.Before(f.From) && ts.Before(f.To) }
	for _, h := range uses {
		if !inRange(h.ts) {
			continue
		}
		u := useOf(h)
		tc := core.ToolCall{
			ToolCallID: u.ToolCallID, Name: u.Name, Source: u.Source, SessionKey: h.call.SessionKey, AgentID: h.call.AgentID,
			UseCallID: h.call.ID, UseTS: h.ts, ArgBytes: u.ArgBytes,
		}
		r, ok := earliest[u.ToolCallID]
		if ok && r.ts.Before(f.To.Add(core.ToolResultLookahead)) {
			rr := resOf(r)
			tc.Status, tc.ResultCallID, tc.ResultTS, tc.IsError, tc.ResultBytes = core.ToolCallPaired, r.call.ID, r.ts, &rr.IsError, rr.ResultBytes
			tc.Duration, tc.DurationSource = r.ts.Sub(h.ts), core.DurationEstimated
			if tc.Duration < 0 {
				tc.Duration = 0
			}
			if ms, ok := headerMS(r.call.PrevToolDurations, u, ordinal[halfKey{h.call.ID, h.idx}]); ok {
				tc.Duration, tc.DurationSource = ms, core.DurationHeader
			}
		} else if now.Sub(h.ts) >= core.ToolIdleAfter {
			tc.Status = core.ToolCallUseOnly
		} else {
			tc.Status = core.ToolCallPending
		}
		out = append(out, placed{tc, h})
	}
	for id, h := range earliest {
		if useIDs[id] || !inRange(h.ts) {
			continue
		}
		r := resOf(h)
		tc := core.ToolCall{
			ToolCallID: id, Source: r.Source, Status: core.ToolCallResultOnly, SessionKey: h.call.SessionKey, AgentID: h.call.AgentID,
			ResultCallID: h.call.ID, ResultTS: h.ts, IsError: &r.IsError, ResultBytes: r.ResultBytes,
		}
		out = append(out, placed{tc, h})
	}
	slices.SortFunc(out, func(a, b placed) int {
		switch {
		case before(b.at, a.at):
			return -1
		case before(a.at, b.at):
			return 1
		}
		return 0
	})
	tcs := make([]core.ToolCall, len(out))
	for i, p := range out {
		tcs[i] = p.tc
	}
	return tcs
}

// headerEntry is a well-formed prev_tool_durations entry: a percent-encoded
// tool name, '=', and whole milliseconds.
var headerEntry = regexp.MustCompile(`^(?:[^%=]|%[0-9A-Fa-f]{2})+=[0-9]+$`)

// headerMS reads the duration of the k-th client use of a tool name resolved
// by one call from that call's prev_tool_durations value: entries
// "name=ms" joined by ';', one per tool call, in order. Malformed entries are
// skipped; a value above one year yields no duration.
func headerMS(raw string, u core.ToolUse, k int) (time.Duration, bool) {
	if u.Source != core.ToolSourceClient || k < 1 {
		return 0, false
	}
	seen := 0
	for _, entry := range strings.Split(raw, ";") {
		if !headerEntry.MatchString(entry) {
			continue
		}
		name, ms, _ := strings.Cut(entry, "=")
		if decoded, err := url.PathUnescape(name); err != nil || decoded != u.Name {
			continue
		}
		if seen++; seen != k {
			continue
		}
		v, err := strconv.ParseInt(ms, 10, 64)
		if err != nil || v > 365*24*3600*1000 {
			return 0, false
		}
		return time.Duration(v) * time.Millisecond, true
	}
	return 0, false
}

// refSummary is the reference for ToolSummary over the chosen tool calls.
func refSummary(calls []core.CallRecord, tcs []core.ToolCall) []core.ToolStat {
	agentType := map[core.ID]string{}
	for _, c := range calls {
		agentType[c.ID] = c.AgentType
	}
	type key struct{ name, typ string }
	groups := map[key][]core.ToolCall{}
	for _, tc := range tcs {
		carrier := tc.UseCallID
		if tc.Status == core.ToolCallResultOnly {
			carrier = tc.ResultCallID
		}
		k := key{tc.Name, agentType[carrier]}
		groups[k] = append(groups[k], tc)
	}
	var out []core.ToolStat
	for k, g := range groups {
		st := core.ToolStat{Name: k.name, AgentType: k.typ, Count: len(g)}
		var ms []float64
		for _, tc := range g {
			if tc.IsError != nil && *tc.IsError {
				st.Errors++
			}
			if tc.Status == core.ToolCallPaired {
				ms = append(ms, float64(tc.Duration.Milliseconds()))
				st.TotalDuration += tc.Duration
			}
		}
		slices.Sort(ms)
		st.DurationP50, st.DurationP95 = percentile(ms, 0.5), percentile(ms, 0.95)
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b core.ToolStat) int {
		if a.Name != b.Name {
			return compareStrings(a.Name, b.Name)
		}
		return compareStrings(a.AgentType, b.AgentType)
	})
	return out
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// percentile is the continuous percentile of sorted milliseconds, as
// percentile_cont computes it, rounded to the millisecond.
func percentile(sorted []float64, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := min(lo+1, len(sorted)-1)
	v := sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
	return time.Duration(math.Floor(v+0.5)) * time.Millisecond
}
