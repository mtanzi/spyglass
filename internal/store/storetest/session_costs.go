package storetest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// sessionCostWin is the time range of the session cost cases.
var sessionCostWin = core.CallFilter{From: Base, To: Base.Add(time.Hour)}

func sessionKeys(got core.SessionCosts) []string {
	var keys []string
	for _, s := range got.Sessions {
		keys = append(keys, s.SessionKey)
	}
	return keys
}

var sessionCostCases = []testCase{
	{"session_costs_rank_known_cost_first_then_unknown_with_ties_by_calls_and_key", func(t *testing.T, e *env) {
		g := e.gen
		at := func(sec int, key string, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second)), WithSession(key)}, opts...)...)
		}
		e.write(t,
			at(0, "big", WithCost(4000)), at(1, "big", WithCost(1000)),
			at(2, "tie-b", WithCost(3000)), at(3, "tie-a", WithCost(3000)),
			at(4, "tie-many", WithCost(2000)), at(5, "tie-many", WithCost(1000)),
			at(6, "zero", WithCost(0)),
			// No known cost: after every known cost, even a known zero and even with more calls.
			at(7, "unknown-many", WithoutCost()), at(8, "unknown-many", WithoutCost()), at(9, "unknown-many", WithoutCost()),
			at(10, "unknown-few", WithoutCost()),
			// A call with no session is in none, and a call outside the range is not counted.
			at(11, "", WithCost(99_999)),
			g.Call(g.At(Base.Add(2*time.Hour)), WithSession("late"), WithCost(88_888)),
		)
		ctx := context.Background()
		all := []string{"big", "tie-many", "tie-a", "tie-b", "zero", "unknown-many", "unknown-few"}
		for _, tc := range []struct {
			limit int
			want  []string
		}{{100, all}, {3, all[:3]}, {1, all[:1]}} {
			got, err := e.SessionCosts(ctx, core.SessionCostQuery{Filter: sessionCostWin, Limit: tc.limit})
			if err != nil {
				t.Fatalf("limit %d: %v", tc.limit, err)
			}
			if !slices.Equal(sessionKeys(got), tc.want) || got.SessionCount != 7 {
				t.Errorf("limit %d: sessions %v of %d, want %v of 7", tc.limit, sessionKeys(got), got.SessionCount, tc.want)
			}
		}
		got, err := e.SessionCosts(ctx, core.SessionCostQuery{Filter: sessionCostWin, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []struct {
			cost     int64 // -1: unknown
			calls    int
			unpriced int
		}{{5000, 2, 0}, {3000, 2, 0}, {3000, 1, 0}, {3000, 1, 0}, {0, 1, 0}, {-1, 3, 3}, {-1, 1, 1}} {
			s := got.Sessions[i]
			if known := s.Cost != nil; known != (want.cost >= 0) || (known && int64(*s.Cost) != want.cost) ||
				s.Calls != want.calls || s.UnpricedCalls != want.unpriced {
				t.Errorf("%s: cost %v calls %d unpriced %d, want cost %d calls %d unpriced %d",
					s.SessionKey, s.Cost, s.Calls, s.UnpricedCalls, want.cost, want.calls, want.unpriced)
			}
		}
	}},
	{"session_costs_equal_the_numbers_of_the_session_list", func(t *testing.T, e *env) {
		e.write(t, sessionDataset(e.gen)...)
		got, err := e.SessionCosts(context.Background(), core.SessionCostQuery{Filter: sessionCostWin, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		// s3 costs 9000, s1 3000 over its calls in the range (the 500 call is two hours later), s2 has no known cost.
		if want := []string{"s3", "s1", "s2"}; !slices.Equal(sessionKeys(got), want) || got.SessionCount != 3 {
			t.Fatalf("sessions %v of %d, want %v of 3", sessionKeys(got), got.SessionCount, want)
		}
		listed := map[string]core.SessionSummary{}
		for _, s := range e.walkSessions(t, core.SessionFilter{From: sessionCostWin.From, To: sessionCostWin.To}, 10) {
			listed[s.SessionKey] = s
		}
		for _, s := range got.Sessions {
			if want := listed[s.SessionKey]; !reflect.DeepEqual(s.SessionSummary, want) {
				t.Errorf("session %s\n got %+v\nwant %+v", s.SessionKey, s.SessionSummary, want)
			}
		}
		if s1 := got.Sessions[1]; s1.Errors != 1 || s1.AgentCount != 2 || s1.ToolCallCount != 0 || s1.Cost == nil || *s1.Cost != 3000 {
			t.Errorf("s1 = %+v", s1)
		}
	}},
	{"session_costs_count_tool_calls_and_tool_time_of_the_ranked_sessions", func(t *testing.T, e *env) {
		g := e.gen
		use, result := g.ToolPair("toolu_1", "with-tools")
		use.TS, use.ID = Base.Add(time.Second), g.ID(Base.Add(time.Second))
		result.TS, result.ID = Base.Add(4*time.Second), g.ID(Base.Add(4*time.Second))
		use.Cost, result.Cost = new(core.MicroUSD), new(core.MicroUSD)
		*use.Cost, *result.Cost = 600, 400
		e.write(t, use, result, g.Call(g.At(Base.Add(10*time.Second)), WithSession("plain"), WithCost(1)))
		got, err := e.SessionCosts(context.Background(), core.SessionCostQuery{Filter: sessionCostWin, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"with-tools", "plain"}; !slices.Equal(sessionKeys(got), want) {
			t.Fatalf("sessions %v, want %v", sessionKeys(got), want)
		}
		if s := got.Sessions[0]; s.ToolCallCount != 1 || s.ToolTime <= 0 || s.Cost == nil || *s.Cost != 1000 {
			t.Errorf("with-tools = %+v, want 1 tool call, a tool time and cost 1000", s)
		}
		if s := got.Sessions[1]; s.ToolCallCount != 0 || s.ToolTime != 0 {
			t.Errorf("plain = %+v, want no tool calls", s)
		}
	}},
	{"session_costs_flag_api_equivalent_only_for_a_priced_passthrough_call", func(t *testing.T, e *env) {
		g := e.gen
		pass := WithAuthMode("client_passthrough")
		at := func(sec int, key string, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second)), WithSession(key)}, opts...)...)
		}
		e.write(t,
			at(0, "priced-pass", WithCost(7), pass), at(1, "priced-pass", WithCost(3)),
			at(2, "unpriced-pass", WithoutCost(), pass), at(3, "unpriced-pass", WithCost(2)),
			at(4, "proxy", WithCost(1)),
		)
		got, err := e.SessionCosts(context.Background(), core.SessionCostQuery{Filter: sessionCostWin, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		flags := map[string]bool{}
		for _, s := range got.Sessions {
			flags[s.SessionKey] = s.APIEquivalent
		}
		if want := map[string]bool{"priced-pass": true, "unpriced-pass": false, "proxy": false}; !reflect.DeepEqual(flags, want) {
			t.Errorf("api_equivalent = %v, want %v", flags, want)
		}
	}},
	{"session_costs_apply_the_call_filter_to_the_counted_calls", func(t *testing.T, e *env) {
		g := e.gen
		at := func(sec int, key string, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second)), WithSession(key)}, opts...)...)
		}
		e.write(t,
			at(0, "mixed", WithModel("model-a"), WithCost(100)), at(1, "mixed", WithModel("model-b"), WithCost(40)),
			at(2, "only-b", WithModel("model-b"), WithCost(9000)),
		)
		f := sessionCostWin
		f.Models = []string{"model-a"}
		got, err := e.SessionCosts(context.Background(), core.SessionCostQuery{Filter: f, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		// Only the call of model-a counts: no cost from model-b, and only-b is not a session of the answer.
		if len(got.Sessions) != 1 || got.SessionCount != 1 {
			t.Fatalf("sessions %v of %d, want only mixed", sessionKeys(got), got.SessionCount)
		}
		if s := got.Sessions[0]; s.SessionKey != "mixed" || s.Calls != 1 || s.Cost == nil || *s.Cost != 100 {
			t.Errorf("mixed = %+v, want the one call of model-a at cost 100", s)
		}
	}},
	{"session_costs_count_tool_time_over_the_matching_calls_only", func(t *testing.T, e *env) {
		g := e.gen
		at := func(sec int, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second)), WithSession("mixed")}, opts...)...)
		}
		e.write(t,
			at(0, WithModel("model-b"), g.WithUse("toolu_b", "Bash")), at(6, WithModel("model-b"), g.WithResult("toolu_b", false)),
			at(5, WithModel("model-a"), g.WithUse("toolu_a", "Bash")), at(9, WithModel("model-a"), g.WithResult("toolu_a", false)),
		)
		f := sessionCostWin
		f.Models = []string{"model-a"}
		got, err := e.SessionCosts(context.Background(), core.SessionCostQuery{Filter: f, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Sessions) != 1 {
			t.Fatalf("sessions %v, want only mixed", sessionKeys(got))
		}
		// Each call lasts 1.5 s and a tool use is at the end of its call. Only
		// the tool call of model-a counts: use at 6.5 s, result at 9 s, 2.5 s.
		// The 4.5 s of model-b (use at 1.5 s, result at 6 s) do not.
		if s := got.Sessions[0]; s.ToolCallCount != 1 || s.ToolTime != 2500*time.Millisecond || !s.ToolTimeEstimated {
			t.Errorf("mixed = %+v, want 1 tool call and an estimated tool time of 2.5s", s)
		}
	}},
	{"session_costs_reject_a_bad_query", func(t *testing.T, e *env) {
		for _, q := range []core.SessionCostQuery{
			{Filter: core.CallFilter{}, Limit: 10},
			{Filter: core.CallFilter{From: sessionCostWin.To, To: sessionCostWin.From}, Limit: 10},
			{Filter: sessionCostWin, Limit: 0},
			{Filter: sessionCostWin, Limit: -1},
			{Filter: sessionCostWin, Limit: 101},
		} {
			if _, err := e.SessionCosts(context.Background(), q); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("SessionCosts(%+v) err = %v", q, err)
			}
		}
	}},
}
