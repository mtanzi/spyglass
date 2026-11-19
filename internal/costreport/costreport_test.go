package costreport

import (
	"reflect"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

var t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func cost(n int64) *core.MicroUSD { m := core.MicroUSD(n); return &m }

func call(id string, min int, key string, c *core.MicroUSD) Call {
	return Call{ID: id, TS: at(min), Key: key, Cost: c}
}

func taskIDs(r Report) []string {
	var ids []string
	for _, t := range r.Tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func byID(t *testing.T, r Report, id string) TaskCost {
	t.Helper()
	for _, tc := range r.Tasks {
		if tc.ID == id {
			return tc
		}
	}
	t.Fatalf("task %q not in report", id)
	return TaskCost{}
}

func TestAttributeCostPerTaskAndPerMergedPR(t *testing.T) {
	tasks := []Task{
		{ID: "a", Key: "fm:a", Start: at(0), End: at(60), MergedPR: "pr/1"},
		{ID: "b", Key: "fm:b", Start: at(0), End: at(60), MergedPR: "pr/1"}, // a second task on the same PR
		{ID: "c", Key: "fm:c", Start: at(0), End: at(60), MergedPR: "pr/2"},
	}
	calls := []Call{
		call("1", 5, "fm:a", cost(100)),
		call("2", 6, "fm:a", cost(50)),
		call("3", 7, "fm:b", cost(25)),
		call("4", 8, "fm:c", cost(1000)),
	}
	r := Attribute(tasks, calls)
	if got := byID(t, r, "a").Spend; got != (Spend{Calls: 2, Cost: 150}) {
		t.Errorf("task a = %+v", got)
	}
	want := []PRCost{
		{PR: "pr/1", TaskIDs: []string{"a", "b"}, Spend: Spend{Calls: 3, Cost: 175}},
		{PR: "pr/2", TaskIDs: []string{"c"}, Spend: Spend{Calls: 1, Cost: 1000}},
	}
	if !reflect.DeepEqual(r.MergedPRs, want) {
		t.Errorf("MergedPRs = %+v, want %+v", r.MergedPRs, want)
	}
	if r.Total != (Spend{Calls: 4, Cost: 1175}) || r.Unattributed.Calls != 0 || r.NoPR.Calls != 0 {
		t.Errorf("total %+v unattributed %+v nopr %+v", r.Total, r.Unattributed, r.NoPR)
	}
}

func TestAttributeTaskWithNoCalls(t *testing.T) {
	r := Attribute([]Task{{ID: "idle", Key: "fm:idle", Start: at(0), End: at(10)}}, []Call{call("1", 5, "fm:other", cost(1))})
	if !reflect.DeepEqual(r.TasksWithoutCalls, []string{"idle"}) {
		t.Errorf("TasksWithoutCalls = %v", r.TasksWithoutCalls)
	}
	if got := byID(t, r, "idle").Spend; got != (Spend{}) {
		t.Errorf("idle spend = %+v, want zero", got)
	}
}

func TestAttributeCallWithNoTask(t *testing.T) {
	tasks := []Task{{ID: "a", Key: "fm:a", Start: at(10), End: at(20)}}
	calls := []Call{
		call("early", 5, "fm:a", cost(7)),    // before the window
		call("edge", 20, "fm:a", cost(11)),   // End is exclusive
		call("stray", 15, "other", nil),      // no task has this key
		call("stray2", 16, "other", cost(3)), //
		call("in", 10, "fm:a", cost(2)),      // Start is inclusive
	}
	r := Attribute(tasks, calls)
	if got := byID(t, r, "a").Spend; got != (Spend{Calls: 1, Cost: 2}) {
		t.Errorf("task a = %+v", got)
	}
	if r.Unattributed != (Spend{Calls: 4, Cost: 21, Unpriced: 1}) {
		t.Errorf("Unattributed = %+v", r.Unattributed)
	}
	want := []KeyCost{
		{Key: "fm:a", Spend: Spend{Calls: 2, Cost: 18}},
		{Key: "other", Spend: Spend{Calls: 2, Cost: 3, Unpriced: 1}},
	}
	if !reflect.DeepEqual(r.UnattributedKeys, want) {
		t.Errorf("UnattributedKeys = %+v", r.UnattributedKeys)
	}
	if r.Total.Calls != 5 {
		t.Errorf("Total = %+v", r.Total)
	}
}

func TestAttributeDuplicateTaskRecords(t *testing.T) {
	tasks := []Task{
		{ID: "a", Key: "fm:a", Start: at(10), End: at(20)},
		{ID: "a", Key: "fm:a", Start: at(10), End: at(20)}, // exact repeat
		{ID: "a", Key: "fm:a", Start: at(5), End: at(30), MergedPR: "pr/9"},
	}
	r := Attribute(tasks, []Call{call("1", 7, "fm:a", cost(5)), call("2", 25, "fm:a", cost(6))})
	if len(r.Tasks) != 1 || r.DuplicateTasks != 2 {
		t.Fatalf("tasks %v, duplicates %d", taskIDs(r), r.DuplicateTasks)
	}
	a := r.Tasks[0]
	if a.Spend != (Spend{Calls: 2, Cost: 11}) || a.MergedPR != "pr/9" || !a.Start.Equal(at(5)) || !a.End.Equal(at(30)) {
		t.Errorf("merged task = %+v", a)
	}
	if r.Total.Cost != 11 {
		t.Errorf("a duplicated record must not double the cost: total %+v", r.Total)
	}
}

func TestAttributeDuplicateOpenWindowWins(t *testing.T) {
	r := Attribute([]Task{
		{ID: "a", Key: "k", Start: at(0), End: at(5)},
		{ID: "a", Key: "k", Start: at(0)},
	}, task2Calls(at(100)))
	if r.Tasks[0].Calls != 1 {
		t.Errorf("an open duplicate must keep the window open: %+v", r.Tasks[0])
	}
}

func task2Calls(ts time.Time) []Call { return []Call{{ID: "1", TS: ts, Key: "k", Cost: cost(1)}} }

func TestAttributeOverlappingTasksCountEachCallOnce(t *testing.T) {
	// The same key was used for two tasks whose windows overlap from minute 10 to 20.
	tasks := []Task{
		{ID: "old", Key: "shared", Start: at(0), End: at(20)},
		{ID: "new", Key: "shared", Start: at(10), End: at(40)},
	}
	calls := []Call{
		call("1", 5, "shared", cost(1)),    // only old
		call("2", 15, "shared", cost(10)),  // both: goes to the later start
		call("3", 30, "shared", cost(100)), // only new
	}
	r := Attribute(tasks, calls)
	old, nw := byID(t, r, "old"), byID(t, r, "new")
	if old.Spend != (Spend{Calls: 1, Cost: 1}) || nw.Spend != (Spend{Calls: 2, Cost: 110}) {
		t.Errorf("old %+v new %+v", old.Spend, nw.Spend)
	}
	if nw.Shared != 1 || old.Shared != 0 {
		t.Errorf("Shared old %d new %d, want 0 and 1", old.Shared, nw.Shared)
	}
	if got := old.Cost + nw.Cost; got != r.Total.Cost {
		t.Errorf("tasks sum %d, total %d: a call was counted twice or lost", got, r.Total.Cost)
	}
}

func TestAttributeTaskWithoutMergedPR(t *testing.T) {
	tasks := []Task{
		{ID: "a", Key: "k1", Start: at(0), End: at(10), MergedPR: "pr/1"},
		{ID: "b", Key: "k2", Start: at(0), End: at(10)},
	}
	r := Attribute(tasks, []Call{call("1", 1, "k1", cost(4)), call("2", 1, "k2", cost(9)), call("3", 2, "k2", nil)})
	if r.NoPR != (Spend{Calls: 2, Cost: 9, Unpriced: 1}) {
		t.Errorf("NoPR = %+v", r.NoPR)
	}
	if len(r.MergedPRs) != 1 || r.MergedPRs[0].Cost != 4 {
		t.Errorf("MergedPRs = %+v", r.MergedPRs)
	}
}

func TestAttributeUnpricedIsNotZero(t *testing.T) {
	r := Attribute([]Task{{ID: "a", Key: "k", Start: at(0)}}, []Call{call("1", 1, "k", nil), call("2", 2, "k", cost(3))})
	if got := r.Tasks[0].Spend; got != (Spend{Calls: 2, Cost: 3, Unpriced: 1}) {
		t.Errorf("spend = %+v", got)
	}
}

func TestAttributeNoInput(t *testing.T) {
	r := Attribute(nil, nil)
	if len(r.Tasks) != 0 || r.Total != (Spend{}) {
		t.Errorf("empty report = %+v", r)
	}
}
