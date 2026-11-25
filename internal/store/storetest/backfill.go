package storetest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// projects returns the stored project of each call, in the order given.
func (e *env) projects(t *testing.T, calls []core.CallRecord) []string {
	t.Helper()
	out := make([]string, len(calls))
	for i, c := range calls {
		d, err := e.GetCall(context.Background(), c.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = d.Project
	}
	return out
}

var backfillCases = []testCase{
	{"backfill_spreads_key_projects_then_single_project_sessions", func(t *testing.T, e *env) {
		g := e.gen
		at := func(sec int, opts ...CallOpt) core.CallRecord {
			return g.Call(append([]CallOpt{g.At(Base.Add(time.Duration(sec) * time.Second))}, opts...)...)
		}
		calls := []core.CallRecord{
			// 0, 1: a mapped key. The null call fills; the call with a project keeps it.
			at(0, WithKey("fm:task-1")),
			at(1, WithKey("fm:task-1"), WithProject("typed-by-header")),
			// 2: a key with no mapping, in no session: stays null.
			at(2, WithKey("plain")),
			// 3, 4: a session whose only project is "alpha": the null call fills.
			at(3, WithKey("plain"), WithSession("one-project"), WithProject("alpha")),
			at(4, WithKey("plain"), WithSession("one-project")),
			// 5, 6, 7: a session with two projects: the null call stays null.
			at(5, WithKey("plain"), WithSession("two-projects"), WithProject("alpha")),
			at(6, WithKey("plain"), WithSession("two-projects"), WithProject("beta")),
			at(7, WithKey("plain"), WithSession("two-projects")),
			// 8: a session with no project at all: stays null.
			at(8, WithKey("plain"), WithSession("no-project")),
			// 9, 10: step 2 sees what step 1 filled. The mapped key gives the
			// session its one project, which then spreads to the call of another key.
			at(9, WithKey("fm:task-2"), WithSession("spread-from-key")),
			at(10, WithKey("plain"), WithSession("spread-from-key")),
			// 11, 12: the key project conflicts with the project of another call in
			// the session, so the session has two and the call of the other key stays null.
			at(11, WithKey("fm:task-1"), WithSession("conflict")),
			at(12, WithKey("plain"), WithSession("conflict"), WithProject("other")),
			at(13, WithKey("plain"), WithSession("conflict")),
		}
		e.write(t, calls...)

		keys := map[string]string{"fm:task-1": "spyglass", "fm:task-2": "firstmate"}
		got, err := e.BackfillProjects(context.Background(), keys)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"spyglass", "typed-by-header", "",
			"alpha", "alpha",
			"alpha", "beta", "",
			"",
			"firstmate", "firstmate",
			"spyglass", "other", "",
		}
		if p := e.projects(t, calls); !slices.Equal(p, want) {
			t.Errorf("projects after backfill = %q, want %q", p, want)
		}
		// Step 1: calls 0, 9 and 11. Step 2: calls 4 and 10.
		if got.ByKey != 3 || got.BySession != 2 {
			t.Errorf("result = %+v, want 3 calls by key and 2 by session", got)
		}
	}},
	{"backfill_second_run_changes_no_row", func(t *testing.T, e *env) {
		g := e.gen
		calls := []core.CallRecord{
			g.Call(WithKey("fm:task-1")),
			g.Call(WithKey("plain"), WithSession("s"), WithProject("alpha")),
			g.Call(WithKey("plain"), WithSession("s")),
			g.Call(WithKey("plain")),
		}
		e.write(t, calls...)
		keys := map[string]string{"fm:task-1": "spyglass"}
		ctx := context.Background()
		first, err := e.BackfillProjects(ctx, keys)
		if err != nil {
			t.Fatal(err)
		}
		if first.ByKey != 1 || first.BySession != 1 {
			t.Fatalf("first run = %+v, want 1 by key and 1 by session", first)
		}
		before := e.projects(t, calls)
		second, err := e.BackfillProjects(ctx, keys)
		if err != nil {
			t.Fatal(err)
		}
		if second.ByKey != 0 || second.BySession != 0 {
			t.Errorf("second run = %+v, want no row changed", second)
		}
		if after := e.projects(t, calls); !slices.Equal(after, before) {
			t.Errorf("projects after the second run = %q, want %q", after, before)
		}
	}},
	{"backfill_without_mapped_keys_still_spreads_session_projects", func(t *testing.T, e *env) {
		g := e.gen
		calls := []core.CallRecord{
			g.Call(WithSession("s"), WithProject("alpha")),
			g.Call(WithSession("s")),
		}
		e.write(t, calls...)
		got, err := e.BackfillProjects(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.ByKey != 0 || got.BySession != 1 {
			t.Errorf("result = %+v, want 0 by key and 1 by session", got)
		}
		if p := e.projects(t, calls); !slices.Equal(p, []string{"alpha", "alpha"}) {
			t.Errorf("projects = %q, want alpha twice", p)
		}
	}},
}
