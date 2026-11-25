package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// projectCostWin is the time range of the project cost cases.
var projectCostWin = core.CallFilter{From: Base, To: Base.Add(time.Hour)}

func projectNames(got core.ProjectCosts) []string {
	var names []string
	for _, p := range got.Projects {
		names = append(names, p.Project)
	}
	return names
}

var projectCostCases = []testCase{
	{"project_costs_rank_known_cost_first_then_unknown_with_ties_by_calls_and_name", func(t *testing.T, e *env) {
		g := e.gen
		at := func(sec int, project string, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second)), WithProject(project)}, opts...)...)
		}
		e.write(t,
			at(0, "big", WithCost(4000)), at(1, "big", WithCost(1000)),
			at(2, "tie-b", WithCost(3000)), at(3, "tie-a", WithCost(3000)),
			at(4, "tie-many", WithCost(2000)), at(5, "tie-many", WithCost(1000)),
			at(6, "zero", WithCost(0)),
			// No known cost: after every known cost, even a known zero and even with more calls.
			at(7, "unknown-many", WithoutCost()), at(8, "unknown-many", WithoutCost()), at(9, "unknown-many", WithoutCost()),
			at(10, "unknown-few", WithoutCost()),
			// A call outside the range is not counted.
			g.Call(g.At(Base.Add(2*time.Hour)), WithProject("late"), WithCost(88_888)),
		)
		ctx := context.Background()
		all := []string{"big", "tie-many", "tie-a", "tie-b", "zero", "unknown-many", "unknown-few"}
		for _, tc := range []struct {
			limit int
			want  []string
		}{{100, all}, {3, all[:3]}, {1, all[:1]}} {
			got, err := e.ProjectCosts(ctx, core.ProjectCostQuery{Filter: projectCostWin, Limit: tc.limit})
			if err != nil {
				t.Fatalf("limit %d: %v", tc.limit, err)
			}
			// project_count covers the projects past the limit.
			if !slices.Equal(projectNames(got), tc.want) || got.ProjectCount != 7 {
				t.Errorf("limit %d: projects %v of %d, want %v of 7", tc.limit, projectNames(got), got.ProjectCount, tc.want)
			}
		}
		got, err := e.ProjectCosts(ctx, core.ProjectCostQuery{Filter: projectCostWin, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []struct {
			cost     int64 // -1: unknown
			calls    int
			unpriced int
		}{{5000, 2, 0}, {3000, 2, 0}, {3000, 1, 0}, {3000, 1, 0}, {0, 1, 0}, {-1, 3, 3}, {-1, 1, 1}} {
			p := got.Projects[i]
			if known := p.Cost != nil; known != (want.cost >= 0) || (known && int64(*p.Cost) != want.cost) ||
				p.Calls != want.calls || p.UnpricedCalls != want.unpriced {
				t.Errorf("%s: cost %v calls %d unpriced %d, want cost %d calls %d unpriced %d",
					p.Project, p.Cost, p.Calls, p.UnpricedCalls, want.cost, want.calls, want.unpriced)
			}
		}
	}},
	{"project_costs_report_calls_with_no_project_as_unassigned_and_never_rank_them", func(t *testing.T, e *env) {
		g := e.gen
		at := func(sec int, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second))}, opts...)...)
		}
		e.write(t,
			at(0, WithProject("p"), WithCost(10)),
			// The unassigned calls cost more than any project and still do not rank.
			at(1, WithCost(500)), at(2, WithCost(250)), at(3, WithoutCost()),
		)
		ctx := context.Background()
		got, err := e.ProjectCosts(ctx, core.ProjectCostQuery{Filter: projectCostWin, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		u := got.Unassigned
		if want := []string{"p"}; !slices.Equal(projectNames(got), want) || got.ProjectCount != 1 {
			t.Errorf("projects %v of %d, want %v of 1", projectNames(got), got.ProjectCount, want)
		}
		if u.Calls != 3 || u.Cost == nil || *u.Cost != 750 || u.UnpricedCalls != 1 {
			t.Errorf("unassigned = %+v (cost %v), want 3 calls, cost 750, 1 unpriced", u, u.Cost)
		}

		// Only unpriced calls with no project: the cost is unknown, not zero.
		f := projectCostWin
		f.Models = []string{"no-such-model"}
		e.write(t, at(4, WithModel("only-unpriced"), WithoutCost()))
		f.Models = []string{"only-unpriced"}
		got, err = e.ProjectCosts(ctx, core.ProjectCostQuery{Filter: f, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if u := got.Unassigned; u.Calls != 1 || u.Cost != nil || u.UnpricedCalls != 1 {
			t.Errorf("unassigned = %+v, want 1 unpriced call and an unknown cost", u)
		}

		// With no call at all, the answer is empty and the unassigned cost is a known zero.
		f.Models = []string{"nothing-matches"}
		got, err = e.ProjectCosts(ctx, core.ProjectCostQuery{Filter: f, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Projects) != 0 || got.ProjectCount != 0 || got.Unassigned.Calls != 0 || got.Unassigned.Cost == nil || *got.Unassigned.Cost != 0 {
			t.Errorf("empty answer = %+v", got)
		}
	}},
	{"project_costs_count_distinct_sessions_and_flag_api_equivalent_only_for_a_priced_passthrough_call", func(t *testing.T, e *env) {
		g := e.gen
		pass := WithAuthMode("client_passthrough")
		at := func(sec int, project string, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second)), WithProject(project)}, opts...)...)
		}
		e.write(t,
			at(0, "priced-pass", WithSession("s1"), WithCost(7), pass), at(1, "priced-pass", WithSession("s1"), WithCost(3)),
			at(2, "priced-pass", WithSession("s2"), WithCost(1)), at(3, "priced-pass", WithSession(""), WithCost(1)),
			at(4, "unpriced-pass", WithSession("s1"), WithoutCost(), pass), at(5, "unpriced-pass", WithCost(2)),
			at(6, "proxy", WithCost(1)),
		)
		got, err := e.ProjectCosts(context.Background(), core.ProjectCostQuery{Filter: projectCostWin, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		type row struct {
			sessions int
			api      bool
		}
		have := map[string]row{}
		for _, p := range got.Projects {
			have[p.Project] = row{p.Sessions, p.APIEquivalent}
		}
		// A call with no session key is in no session, and a session that spans two projects counts in both.
		if want := (map[string]row{"priced-pass": {2, true}, "unpriced-pass": {1, false}, "proxy": {0, false}}); len(have) != 3 ||
			have["priced-pass"] != want["priced-pass"] || have["unpriced-pass"] != want["unpriced-pass"] || have["proxy"] != want["proxy"] {
			t.Errorf("sessions and api_equivalent = %v, want %v", have, want)
		}
	}},
	{"project_costs_apply_the_call_filter_and_the_totals_equal_the_usage_total", func(t *testing.T, e *env) {
		g := e.gen
		at := func(sec int, project string, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second)), WithProject(project)}, opts...)...)
		}
		e.write(t,
			at(0, "a", WithModel("model-a"), WithCost(100)), at(1, "a", WithModel("model-b"), WithCost(40)),
			at(2, "b", WithModel("model-b"), WithCost(9000)), at(3, "", WithModel("model-a"), WithCost(7)),
			at(4, "b", WithModel("model-a"), WithoutCost()),
		)
		ctx := context.Background()
		got, err := e.ProjectCosts(ctx, core.ProjectCostQuery{Filter: projectCostWin, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var cost int64
		var calls, unpriced int
		for _, p := range got.Projects {
			calls, unpriced = calls+p.Calls, unpriced+p.UnpricedCalls
			if p.Cost != nil {
				cost += int64(*p.Cost)
			}
		}
		calls, unpriced = calls+got.Unassigned.Calls, unpriced+got.Unassigned.UnpricedCalls
		if got.Unassigned.Cost != nil {
			cost += int64(*got.Unassigned.Cost)
		}
		buckets, err := e.Usage(ctx, core.UsageQuery{Filter: projectCostWin, Grain: core.GrainDay})
		if err != nil {
			t.Fatal(err)
		}
		var wantCost int64
		var wantCalls, wantUnpriced int
		for _, b := range buckets {
			wantCost, wantCalls, wantUnpriced = wantCost+int64(b.Cost), wantCalls+b.Calls, wantUnpriced+b.UnpricedCalls
		}
		if cost != wantCost || calls != wantCalls || unpriced != wantUnpriced || wantCost != 9147 {
			t.Errorf("projects+unassigned: cost %d calls %d unpriced %d; usage: cost %d calls %d unpriced %d; want cost 9147",
				cost, calls, unpriced, wantCost, wantCalls, wantUnpriced)
		}

		f := projectCostWin
		f.Models = []string{"model-a"}
		got, err = e.ProjectCosts(ctx, core.ProjectCostQuery{Filter: f, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		// Only the model-a calls count: a has 100, b has one unpriced call, one call has no project.
		if want := []string{"a", "b"}; !slices.Equal(projectNames(got), want) || got.ProjectCount != 2 {
			t.Fatalf("projects %v of %d, want %v of 2", projectNames(got), got.ProjectCount, want)
		}
		if a := got.Projects[0]; a.Calls != 1 || a.Cost == nil || *a.Cost != 100 {
			t.Errorf("a = %+v, want the one call of model-a at cost 100", a)
		}
		if got.Unassigned.Calls != 1 || got.Unassigned.Cost == nil || *got.Unassigned.Cost != 7 {
			t.Errorf("unassigned = %+v, want one call at cost 7", got.Unassigned)
		}

		// A project filter keeps its project and drops the unassigned calls.
		f = projectCostWin
		f.Projects = []string{"b"}
		got, err = e.ProjectCosts(ctx, core.ProjectCostQuery{Filter: f, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"b"}; !slices.Equal(projectNames(got), want) || got.ProjectCount != 1 || got.Unassigned.Calls != 0 {
			t.Errorf("projects %v of %d, unassigned %+v, want only b", projectNames(got), got.ProjectCount, got.Unassigned)
		}
	}},
	{"project_costs_reject_a_bad_query", func(t *testing.T, e *env) {
		for _, q := range []core.ProjectCostQuery{
			{Filter: core.CallFilter{}, Limit: 10},
			{Filter: core.CallFilter{From: projectCostWin.To, To: projectCostWin.From}, Limit: 10},
			{Filter: projectCostWin, Limit: 0},
			{Filter: projectCostWin, Limit: -1},
			{Filter: projectCostWin, Limit: 101},
		} {
			if _, err := e.ProjectCosts(context.Background(), q); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("ProjectCosts(%+v) err = %v", q, err)
			}
		}
	}},
}
