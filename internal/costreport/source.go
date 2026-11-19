package costreport

import (
	"context"
	"fmt"
	"slices"
)

// TaskSource is the seam between attribution and wherever a task list lives.
// An adapter reads one kind of input and returns tasks; everything specific to
// that input (file formats, naming rules) stays inside the adapter. Adapters
// treat their input as untrusted: a record they cannot use is skipped and
// named in Warnings, and it never aborts the read.
type TaskSource interface {
	Tasks(ctx context.Context) (TaskList, error)
}

// TaskList is what a TaskSource returns.
type TaskList struct {
	Tasks    []Task
	Warnings []string // one line per skipped or suspect record, bounded
}

// MaxWarnings bounds TaskList.Warnings. Adapters add one last line that counts
// the warnings they dropped.
const MaxWarnings = 20

// Warner collects adapter warnings up to MaxWarnings.
type Warner struct {
	list    []string
	dropped int
}

// Addf records one warning, or counts it once the bound is reached.
func (w *Warner) Addf(format string, args ...any) {
	if len(w.list) >= MaxWarnings {
		w.dropped++
		return
	}
	w.list = append(w.list, fmt.Sprintf(format, args...))
}

// List returns the warnings, with a last line that counts the dropped ones.
func (w *Warner) List() []string {
	if w.dropped == 0 {
		return w.list
	}
	return append(slices.Clone(w.list), fmt.Sprintf("%d more warnings not shown", w.dropped))
}
