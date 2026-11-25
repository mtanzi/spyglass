package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// toolCostWin is the range of the tool-cost cases: the first hour after Base.
var toolCostWin = core.CallFilter{From: Base, To: Base.Add(time.Hour)}

// uses adds a client use of each named tool to a call, with tool call ids made
// from prefix.
func (g *Gen) uses(prefix string, names ...string) CallOpt {
	return func(c *core.CallRecord) {
		for i, n := range names {
			g.WithUse(fmt.Sprintf("%s_%d", prefix, i), n)(c)
		}
	}
}

// wantTool is one expected row. cost -1 means an unknown (nil) cost.
type wantTool struct {
	tool     string
	uses     int
	cost     int64
	unpriced int
}

func (e *env) toolCosts(t *testing.T, q core.ToolCostQuery) core.ToolCosts {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = core.MaxRanked
	}
	got, err := e.ToolCosts(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func checkTools(t *testing.T, got []core.ToolCost, want []wantTool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tools %+v, want %+v", got, want)
	}
	for i, w := range want {
		g := got[i]
		known := g.Cost != nil
		if g.Tool != w.tool || g.Uses != w.uses || known != (w.cost >= 0) || (known && int64(*g.Cost) != w.cost) || g.UnpricedCalls != w.unpriced {
			t.Errorf("row %d = %+v (cost %v), want %s with %d uses, cost %d, %d unpriced", i, g, g.Cost, w.tool, w.uses, w.cost, w.unpriced)
		}
	}
}

// sumTools adds up the known cost of the tool rows.
func sumTools(tools []core.ToolCost) (sum int64) {
	for _, tc := range tools {
		if tc.Cost != nil {
			sum += int64(*tc.Cost)
		}
	}
	return sum
}

var toolCostCases = []testCase{
	{"tool_costs_split_a_call_equally_among_its_uses", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			// Two uses of different tools: half each.
			g.Call(WithCost(100), g.uses("a", "Bash", "Read")),
			// Two uses of one tool: the tool gets all of it.
			g.Call(WithCost(40), g.uses("b", "Edit", "Edit")),
		)
		got := e.toolCosts(t, core.ToolCostQuery{Filter: toolCostWin})
		checkTools(t, got.Tools, []wantTool{{"Bash", 1, 50, 0}, {"Read", 1, 50, 0}, {"Edit", 2, 40, 0}})
		if got.ToolCount != 3 {
			t.Errorf("tool count = %d, want 3", got.ToolCount)
		}
	}},
	{"tool_costs_round_down_and_give_the_remainder_to_the_first_tool_by_name", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			// 10 over three uses: shares 3, 3, 3, remainder 1 to "a".
			g.Call(WithCost(10), g.uses("a", "c", "a", "b")),
			// 7 over the uses y, x, x: x floor(14/3) = 4, y floor(7/3) = 2,
			// remainder 1 to x, the first by name, though y was used first.
			g.Call(WithCost(7), g.uses("b", "y", "x", "x")),
		)
		got := e.toolCosts(t, core.ToolCostQuery{Filter: toolCostWin})
		checkTools(t, got.Tools, []wantTool{{"x", 2, 5, 0}, {"a", 1, 4, 0}, {"b", 1, 3, 0}, {"c", 1, 3, 0}, {"y", 1, 2, 0}})
	}},
	{"tool_costs_add_up_to_the_total_of_the_same_calls", func(t *testing.T, e *env) {
		g := e.gen
		from, to := toolCostWin.From, toolCostWin.To
		tools := []string{"Bash", "Read", "Edit", "Grep"}
		var calls []core.CallRecord
		for i := range 60 {
			opts := []CallOpt{g.At(from.Add(time.Duration(i) * time.Minute))}
			if i%7 != 3 { // every seventh call has an unknown cost
				opts = append(opts, WithCost(core.MicroUSD((i*37+11)%1000)))
			}
			if i%5 == 0 {
				opts = append(opts, WithAuthMode("client_passthrough"))
			}
			if i%2 == 0 {
				opts = append(opts, WithModel("other-model"))
			}
			var names []string
			for j := range i % 6 { // 0 to 5 uses, so a sixth of the calls use no tool
				names = append(names, tools[(i+j*j)%len(tools)])
			}
			calls = append(calls, g.Call(append(opts, g.uses(fmt.Sprintf("c%d", i), names...))...))
		}
		calls = append(calls,
			// Starts before from and its uses land after from: not selected.
			g.Call(g.At(from.Add(-10*time.Second)), WithDuration(30*time.Second), WithCost(999), g.uses("early", "Bash", "Read")),
			// Starts before to and its uses land after to: selected, with its tools.
			g.Call(g.At(to.Add(-10*time.Second)), WithDuration(30*time.Second), WithCost(1001), g.uses("late", "Bash", "Read", "Grep")),
			// The range is half-open: a call at from is in, a call at to is out.
			g.Call(g.At(from), WithCost(13), g.uses("first", "Edit")),
			g.Call(g.At(to), WithCost(17), g.uses("last", "Edit")),
		)
		e.write(t, calls...)

		for _, f := range []core.CallFilter{
			toolCostWin,
			{From: from, To: to, Models: []string{"other-model"}},
		} {
			// Both sums come from the records, not from the store.
			var total, noTool int64
			for _, c := range calls {
				inModel := len(f.Models) == 0 || c.Model == f.Models[0]
				if c.Cost == nil || c.TS.Before(f.From) || !c.TS.Before(f.To) || !inModel {
					continue
				}
				total += int64(*c.Cost)
				if len(c.ToolUses) == 0 {
					noTool += int64(*c.Cost)
				}
			}
			got := e.toolCosts(t, core.ToolCostQuery{Filter: f})
			if sum := sumTools(got.Tools); sum+noTool != total {
				t.Errorf("filter %+v: tool costs %d + calls with no tool %d = %d, want the total %d", f, sum, noTool, sum+noTool, total)
			}
			// The usage series is the other definition of the total of a range.
			buckets, err := e.Usage(context.Background(), core.UsageQuery{Filter: f, Grain: core.GrainDay})
			if err != nil {
				t.Fatal(err)
			}
			var usageTotal int64
			for _, b := range buckets {
				usageTotal += int64(b.Cost)
			}
			if usageTotal != total {
				t.Errorf("filter %+v: usage total %d, want %d", f, usageTotal, total)
			}
		}
	}},
	{"tool_costs_select_calls_by_the_call_time_not_the_use_time", func(t *testing.T, e *env) {
		g := e.gen
		from, to := toolCostWin.From, toolCostWin.To
		e.write(t,
			g.Call(g.At(from.Add(-10*time.Second)), WithDuration(30*time.Second), WithCost(900), g.uses("early", "Early")),
			g.Call(g.At(to.Add(-10*time.Second)), WithDuration(30*time.Second), WithCost(600), g.uses("late", "Late", "Late", "Late")),
		)
		got := e.toolCosts(t, core.ToolCostQuery{Filter: toolCostWin})
		checkTools(t, got.Tools, []wantTool{{"Late", 3, 600, 0}})
	}},
	{"tool_costs_leave_the_unpriced_out_of_cost_and_count_them", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			// Unpriced, two uses of one tool: 2 uses, 1 unpriced call, no cost.
			g.Call(WithoutCost(), g.uses("a", "Read", "Read")),
			// Priced: 30 split between Read and Bash.
			g.Call(WithCost(30), g.uses("b", "Read", "Bash")),
			// Every call that used Glob is unpriced, so its cost is unknown, not zero.
			g.Call(WithoutCost(), WithAuthMode("client_passthrough"), g.uses("c", "Glob")),
			// A priced passthrough call.
			g.Call(WithCost(8), WithAuthMode("client_passthrough"), g.uses("d", "Write")),
		)
		got := e.toolCosts(t, core.ToolCostQuery{Filter: toolCostWin})
		// Known cost first and highest first, ties by uses, then unknown cost.
		checkTools(t, got.Tools, []wantTool{{"Read", 3, 15, 1}, {"Bash", 1, 15, 0}, {"Write", 1, 8, 0}, {"Glob", 1, -1, 1}})
		for _, tc := range got.Tools {
			if want := tc.Tool == "Write"; tc.APIEquivalent != want {
				t.Errorf("%s api_equivalent = %v, want %v: only a priced passthrough call sets it", tc.Tool, tc.APIEquivalent, want)
			}
		}
	}},
	{"tool_costs_count_errors_and_result_bytes_of_the_paired_results", func(t *testing.T, e *env) {
		g := e.gen
		resultBytes := int64(len(g.Bodies()[3]))
		e.write(t,
			g.Call(g.At(Base), WithCost(20), g.WithUse("e1", "Bash"), g.WithUse("e2", "Bash"), g.WithUse("e3", "Bash"), g.WithUse("e4", "Bash")),
			g.Call(g.At(Base.Add(time.Minute)), g.WithResult("e1", true), g.WithResult("e2", false)),
			// The result of e3 lands after the range, inside the lookahead, and still pairs.
			// e4 never gets one.
			g.Call(g.At(Base.Add(2*time.Hour)), g.WithResult("e3", true)),
		)
		got := e.toolCosts(t, core.ToolCostQuery{Filter: toolCostWin})
		if len(got.Tools) != 1 {
			t.Fatalf("tools %+v, want only Bash", got.Tools)
		}
		if b := got.Tools[0]; b.Uses != 4 || b.Errors != 2 || b.ResultBytes != 3*resultBytes {
			t.Errorf("Bash = %+v, want 4 uses, 2 errors and %d result bytes", b, 3*resultBytes)
		}
	}},
	{"tool_costs_rank_and_bound_the_list", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			g.Call(WithCost(100), g.uses("a", "T1")),
			g.Call(WithCost(300), g.uses("b", "T2")),
			g.Call(WithCost(300), g.uses("c", "T3", "T3")), // tie with T2 on cost, more uses
			g.Call(WithCost(50), g.uses("d", "T4")),
			g.Call(WithoutCost(), g.uses("e", "T5")),
		)
		got := e.toolCosts(t, core.ToolCostQuery{Filter: toolCostWin, Limit: 3})
		checkTools(t, got.Tools, []wantTool{{"T3", 2, 300, 0}, {"T2", 1, 300, 0}, {"T1", 1, 100, 0}})
		if got.ToolCount != 5 {
			t.Errorf("tool count = %d, want 5, which includes the tools past the limit", got.ToolCount)
		}
	}},
	{"tool_costs_of_a_filter_that_keeps_every_call_equal_the_unfiltered", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			g.Call(g.At(Base), WithProvider("p1"), WithSession("s1"), WithCost(30), g.WithUse("f1", "Bash"), g.WithUse("f2", "Read"), g.WithUse("f3", "Bash")),
			g.Call(g.At(Base.Add(time.Minute)), WithProvider("p1"), WithSession("s1"), WithCost(12), g.WithResult("f1", true), g.WithResult("f2", false), g.WithUse("f4", "Edit")),
			g.Call(g.At(Base.Add(2*time.Hour)), WithProvider("p1"), WithSession("s1"), g.WithResult("f3", false), g.WithResult("f4", true)),
		)
		want := e.toolCosts(t, core.ToolCostQuery{Filter: toolCostWin})
		checkTools(t, want.Tools, []wantTool{{"Bash", 2, 20, 0}, {"Edit", 1, 12, 0}, {"Read", 1, 10, 0}})
		for _, f := range []core.CallFilter{
			{From: toolCostWin.From, To: toolCostWin.To, Providers: []string{"p1"}},
			{From: toolCostWin.From, To: toolCostWin.To, SessionKey: "s1"},
		} {
			got := e.toolCosts(t, core.ToolCostQuery{Filter: f})
			if !reflect.DeepEqual(got, want) {
				t.Errorf("filter %+v: %+v, want the unfiltered %+v", f, got, want)
			}
		}
	}},
	{"tool_costs_reject_an_invalid_query", func(t *testing.T, e *env) {
		for _, q := range []core.ToolCostQuery{
			{Filter: toolCostWin, Limit: 0},
			{Filter: toolCostWin, Limit: core.MaxRanked + 1},
			{Filter: core.CallFilter{From: Base, To: Base}, Limit: 10},
			{Filter: core.CallFilter{From: Base.Add(time.Hour), To: Base}, Limit: 10},
		} {
			if _, err := e.ToolCosts(context.Background(), q); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("ToolCosts(%+v) err = %v, want ErrInvalidFilter", q, err)
			}
		}
	}},
}
