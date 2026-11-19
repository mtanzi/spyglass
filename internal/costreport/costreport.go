// Package costreport attributes the cost of recorded calls to tasks and to
// merged pull requests (ADR 0040).
//
// Attribute is the whole interface of the module: tasks and calls in, a
// plain-data Report out. The module knows nothing about where tasks come from
// (TaskSource adapters own that), how call data is read, or how a Report is
// shown (the formatters own that). It does no I/O.
package costreport

import (
	"sort"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// Task is one unit of work with a time window and the virtual key its calls
// carry.
type Task struct {
	ID  string
	Key string // virtual key name of the task's calls
	// Start is the first instant of the window, End the first instant after
	// it: the window is half-open [Start, End), as a core.CallFilter range is.
	// A zero End means the task is still open and has no upper bound.
	Start, End time.Time
	// MergedPR names the pull request this task merged, for example its URL.
	// Empty means no merged PR is known.
	MergedPR string
}

// Call is the part of a recorded call that attribution needs.
type Call struct {
	ID   string
	TS   time.Time
	Key  string
	Cost *core.MicroUSD // nil when the call has no known cost
}

// Spend is a sum over calls. Cost counts only calls with a known cost;
// Unpriced counts the calls without one, so an unknown cost is never shown as
// zero.
type Spend struct {
	Calls    int
	Cost     core.MicroUSD
	Unpriced int
}

func (s *Spend) add(c Call) {
	s.Calls++
	if c.Cost == nil {
		s.Unpriced++
		return
	}
	s.Cost += *c.Cost
}

func (s *Spend) merge(o Spend) {
	s.Calls += o.Calls
	s.Cost += o.Cost
	s.Unpriced += o.Unpriced
}

// TaskCost is the spend of one task.
type TaskCost struct {
	Task
	Spend
	// Shared counts the calls that fell in the window of this task and of
	// another task with the same key. Each such call is counted once, for the
	// task with the latest Start.
	Shared int
}

// PRCost is the spend of the tasks that merged one pull request.
type PRCost struct {
	PR      string
	TaskIDs []string
	Spend
}

// KeyCost is the spend of calls that no task claims, by key name.
type KeyCost struct {
	Key string
	Spend
}

// Report is the result of Attribute. It holds plain data only.
type Report struct {
	// Tasks has one entry per distinct task id, in order of Start then ID.
	Tasks []TaskCost
	// MergedPRs has one entry per distinct merged PR, in order of PR.
	MergedPRs []PRCost
	// NoPR is the spend of tasks without a merged PR.
	NoPR Spend
	// Unattributed is the spend of calls that no task claims, and
	// UnattributedKeys the same split by key name, in order of key.
	Unattributed     Spend
	UnattributedKeys []KeyCost
	// Total is the spend of every input call.
	Total Spend
	// TasksWithoutCalls lists the ids of tasks that claimed no call.
	TasksWithoutCalls []string
	// DuplicateTasks counts task records dropped because their id was seen.
	DuplicateTasks int
	// Warnings is filled by the caller from the TaskSource.
	Warnings []string
}

// Attribute joins calls to tasks by key and time window.
//
// A call belongs to a task when the call key equals the task key and the call
// time lies in [Start, End). A call that fits the windows of several tasks
// belongs to the one with the latest Start (ties: the smaller id), so no call
// is counted twice and the sum over the report equals Total.
//
// Records with the same task id are one task: the window is the union of
// their windows (an open End wins), the key and the merged PR are the first
// non-empty value. This makes a source that delivers a record twice safe.
func Attribute(tasks []Task, calls []Call) Report {
	merged, dups := mergeTasks(tasks)
	rep := Report{DuplicateTasks: dups}

	byKey := map[string][]int{} // key -> indexes into merged, latest Start first
	for i, t := range merged {
		byKey[t.Key] = append(byKey[t.Key], i)
	}
	for _, idx := range byKey {
		sort.SliceStable(idx, func(a, b int) bool {
			ta, tb := merged[idx[a]], merged[idx[b]]
			if !ta.Start.Equal(tb.Start) {
				return ta.Start.After(tb.Start)
			}
			return ta.ID < tb.ID
		})
	}

	costs := make([]TaskCost, len(merged))
	for i, t := range merged {
		costs[i].Task = t
	}
	orphan := map[string]*Spend{}
	for _, c := range calls {
		rep.Total.add(c)
		var hits []int
		for _, i := range byKey[c.Key] {
			if inWindow(merged[i], c.TS) {
				hits = append(hits, i)
			}
		}
		if len(hits) == 0 {
			rep.Unattributed.add(c)
			s := orphan[c.Key]
			if s == nil {
				s = &Spend{}
				orphan[c.Key] = s
			}
			s.add(c)
			continue
		}
		costs[hits[0]].add(c)
		if len(hits) > 1 {
			costs[hits[0]].Shared++
		}
	}

	prs := map[string]*PRCost{}
	for _, tc := range costs {
		if tc.Calls == 0 {
			rep.TasksWithoutCalls = append(rep.TasksWithoutCalls, tc.ID)
		}
		if tc.MergedPR == "" {
			rep.NoPR.merge(tc.Spend)
			continue
		}
		p := prs[tc.MergedPR]
		if p == nil {
			p = &PRCost{PR: tc.MergedPR}
			prs[tc.MergedPR] = p
		}
		p.TaskIDs = append(p.TaskIDs, tc.ID)
		p.merge(tc.Spend)
	}

	sort.SliceStable(costs, func(a, b int) bool {
		if !costs[a].Start.Equal(costs[b].Start) {
			return costs[a].Start.Before(costs[b].Start)
		}
		return costs[a].ID < costs[b].ID
	})
	rep.Tasks = costs
	sort.Strings(rep.TasksWithoutCalls)
	for _, p := range prs {
		sort.Strings(p.TaskIDs)
		rep.MergedPRs = append(rep.MergedPRs, *p)
	}
	sort.Slice(rep.MergedPRs, func(a, b int) bool { return rep.MergedPRs[a].PR < rep.MergedPRs[b].PR })
	for k, s := range orphan {
		rep.UnattributedKeys = append(rep.UnattributedKeys, KeyCost{Key: k, Spend: *s})
	}
	sort.Slice(rep.UnattributedKeys, func(a, b int) bool { return rep.UnattributedKeys[a].Key < rep.UnattributedKeys[b].Key })
	return rep
}

func inWindow(t Task, ts time.Time) bool {
	if ts.Before(t.Start) {
		return false
	}
	return t.End.IsZero() || ts.Before(t.End)
}

// mergeTasks collapses records with the same id and counts the dropped ones.
func mergeTasks(tasks []Task) ([]Task, int) {
	index := map[string]int{}
	var out []Task
	dups := 0
	for _, t := range tasks {
		i, seen := index[t.ID]
		if !seen {
			index[t.ID] = len(out)
			out = append(out, t)
			continue
		}
		dups++
		m := &out[i]
		if t.Start.Before(m.Start) {
			m.Start = t.Start
		}
		switch {
		case m.End.IsZero():
		case t.End.IsZero(), t.End.After(m.End):
			m.End = t.End
		}
		if m.Key == "" {
			m.Key = t.Key
		}
		if m.MergedPR == "" {
			m.MergedPR = t.MergedPR
		}
	}
	return out, dups
}
