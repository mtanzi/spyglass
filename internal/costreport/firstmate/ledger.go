// Package firstmate is the TaskSource adapter for firstmate's fleet ledger,
// state/fleet-ledger.jsonl. It is the only place that knows the ledger format
// and the fm:<task-id> key naming (ADR 0040).
package firstmate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/mtanzi/spyglass/internal/costreport"
)

// KeyPrefix is the key naming of ADR 0040: a crewmate's virtual key is
// fm:<task-id>.
const KeyPrefix = "fm:"

const (
	maxLine   = 256 << 10 // a longer line is skipped, not read into memory
	maxTaskID = 128
)

// taskIDRE bounds what the ledger may put into a key name and a report cell.
var taskIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Ledger reads a fleet ledger file.
type Ledger struct{ Path string }

// Tasks opens the file and parses it.
func (l Ledger) Tasks(ctx context.Context) (costreport.TaskList, error) {
	f, err := os.Open(l.Path)
	if err != nil {
		return costreport.TaskList{}, fmt.Errorf("open ledger: %w", err)
	}
	defer func() { _ = f.Close() }()
	return Parse(ctx, f)
}

type record struct {
	TS    json.Number `json:"ts"`
	Event string      `json:"event"`
	Task  string      `json:"task"`
	Via   string      `json:"via"`
	PR    string      `json:"pr"`
}

type taskState struct {
	dispatched, first, merged, cleaned time.Time
	pr                                 string
}

// Parse reads a ledger. The ledger is untrusted input and is delivered at
// least once, so Parse skips what it cannot use, names it in Warnings, and
// reads a repeated record as one.
//
// The window of a task starts at task.dispatched (at its first record when
// none exists) and ends at task.cleaned_up, else at task.merged, else it stays
// open. A status record can precede its dispatch record, so the start is the
// earliest time over all records when dispatch is absent, and the earlier of
// the two when both exist. A task.merged record with via "pr" names the merged
// PR; a local merge names none.
func Parse(ctx context.Context, r io.Reader) (costreport.TaskList, error) {
	var w costreport.Warner
	states := map[string]*taskState{}
	br := bufio.NewReaderSize(r, 64<<10)
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return costreport.TaskList{}, err
		}
		line, tooLong, err := readLine(br)
		if len(line) > 0 || tooLong {
			handle(n, line, tooLong, states, &w)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return costreport.TaskList{}, fmt.Errorf("read ledger: %w", err)
		}
	}

	ids := make([]string, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out costreport.TaskList
	for _, id := range ids {
		s := states[id]
		t := costreport.Task{ID: id, Key: KeyPrefix + id, Start: s.first, MergedPR: s.pr}
		if !s.dispatched.IsZero() && s.dispatched.Before(t.Start) {
			t.Start = s.dispatched
		}
		switch {
		case !s.cleaned.IsZero():
			t.End = s.cleaned
		case !s.merged.IsZero():
			t.End = s.merged
		}
		if !t.End.IsZero() && !t.End.After(t.Start) {
			// A window must have a width; the call that closes it is not lost.
			t.End = t.Start.Add(time.Second)
		}
		out.Tasks = append(out.Tasks, t)
	}
	out.Warnings = w.List()
	return out, nil
}

// readLine reads one line without its newline. A line over maxLine is drained
// and reported as tooLong with no content.
func readLine(br *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, isPrefix, e := br.ReadLine()
		if e != nil {
			return line, tooLong, e
		}
		if !tooLong {
			if len(line)+len(chunk) > maxLine {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
}

func handle(n int, line []byte, tooLong bool, states map[string]*taskState, w *costreport.Warner) {
	if tooLong {
		w.Addf("line %d: longer than %d bytes, skipped", n, maxLine)
		return
	}
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		w.Addf("line %d: not a JSON object, skipped", n)
		return
	}
	switch rec.Event {
	case "task.dispatched", "task.status", "task.pr_ready", "task.merged", "task.cleaned_up":
	default:
		return // unknown events are ignored by contract
	}
	if len(rec.Task) > maxTaskID || !taskIDRE.MatchString(rec.Task) {
		w.Addf("line %d: task id is missing or not a plain id, skipped", n)
		return
	}
	secs, err := rec.TS.Int64()
	if err != nil || secs <= 0 || secs > 1<<40 {
		w.Addf("line %d: ts is not a Unix time in seconds, skipped", n)
		return
	}
	ts := time.Unix(secs, 0).UTC()
	s := states[rec.Task]
	if s == nil {
		s = &taskState{first: ts}
		states[rec.Task] = s
	}
	if ts.Before(s.first) {
		s.first = ts
	}
	switch rec.Event {
	case "task.dispatched":
		if s.dispatched.IsZero() || ts.Before(s.dispatched) {
			s.dispatched = ts
		}
	case "task.merged":
		if s.merged.IsZero() || ts.After(s.merged) {
			s.merged = ts
		}
		if rec.Via == "pr" {
			if pr, ok := cleanPR(rec.PR); ok {
				s.pr = pr
			} else {
				w.Addf("line %d: task.merged has no usable PR URL", n)
			}
		}
	case "task.cleaned_up":
		if s.cleaned.IsZero() || ts.After(s.cleaned) {
			s.cleaned = ts
		}
	}
}

// cleanPR accepts an absolute http(s) URL without credentials, and returns it
// in canonical form so that a repeated record names the same PR.
func cleanPR(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || len(raw) > 512 {
		return "", false
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String(), true
}
