package firstmate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/costreport"
)

func parse(t *testing.T, ledger string) costreport.TaskList {
	t.Helper()
	l, err := Parse(context.Background(), strings.NewReader(ledger))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func find(t *testing.T, l costreport.TaskList, id string) costreport.Task {
	t.Helper()
	for _, tk := range l.Tasks {
		if tk.ID == id {
			return tk
		}
	}
	t.Fatalf("task %q not parsed: %+v", id, l.Tasks)
	return costreport.Task{}
}

func unix(s int64) time.Time { return time.Unix(s, 0).UTC() }

const lifecycle = `{"v":1,"ts":1000,"event":"task.dispatched","task":"fix-login","kind":"ship","project":"webapp","harness":"claude","model":null}
{"v":1,"ts":1010,"event":"task.status","task":"fix-login","state":"working","key":null,"text":" bug reproduced"}
{"v":1,"ts":1500,"event":"task.pr_ready","task":"fix-login","pr":"https://github.com/acme/webapp/pull/7"}
{"v":1,"ts":1600,"event":"task.merged","task":"fix-login","via":"pr","pr":"https://github.com/acme/webapp/pull/7"}
{"v":1,"ts":1700,"event":"task.cleaned_up","task":"fix-login"}
`

func TestParseLifecycle(t *testing.T) {
	l := parse(t, lifecycle)
	if len(l.Warnings) != 0 {
		t.Errorf("warnings = %v", l.Warnings)
	}
	got := find(t, l, "fix-login")
	want := costreport.Task{ID: "fix-login", Key: "fm:fix-login", Start: unix(1000), End: unix(1700), MergedPR: "https://github.com/acme/webapp/pull/7"}
	if got != want {
		t.Errorf("task = %+v, want %+v", got, want)
	}
}

func TestParseWindowEnds(t *testing.T) {
	l := parse(t, `{"ts":100,"event":"task.dispatched","task":"open"}
{"ts":100,"event":"task.dispatched","task":"merged"}
{"ts":200,"event":"task.merged","task":"merged","via":"local"}
`)
	if tk := find(t, l, "open"); !tk.End.IsZero() {
		t.Errorf("a task that is not merged or cleaned up stays open, got End %v", tk.End)
	}
	tk := find(t, l, "merged")
	if !tk.End.Equal(unix(200)) || tk.MergedPR != "" {
		t.Errorf("a local merge ends the window and names no PR: %+v", tk)
	}
}

func TestParseToleratesDuplicates(t *testing.T) {
	doubled := lifecycle + lifecycle
	a, b := parse(t, lifecycle), parse(t, doubled)
	if len(b.Tasks) != 1 || b.Tasks[0] != a.Tasks[0] || len(b.Warnings) != 0 {
		t.Errorf("a repeated record must change nothing:\n%+v\n%+v", a, b)
	}
}

func TestParseStatusBeforeDispatch(t *testing.T) {
	l := parse(t, `{"ts":90,"event":"task.status","task":"t","state":"working"}
{"ts":100,"event":"task.dispatched","task":"t"}
`)
	if tk := find(t, l, "t"); !tk.Start.Equal(unix(90)) {
		t.Errorf("start = %v, want the earlier status record", tk.Start)
	}
}

func TestParseStatusOnlyTask(t *testing.T) {
	// A ledger turned on late has no dispatch record for a running task.
	l := parse(t, `{"ts":90,"event":"task.status","task":"late","state":"working"}`)
	if tk := find(t, l, "late"); !tk.Start.Equal(unix(90)) || !tk.End.IsZero() {
		t.Errorf("task = %+v", tk)
	}
}

func TestParseMalformedAndHostileInput(t *testing.T) {
	long := `{"ts":1,"event":"task.status","task":"x","text":"` + strings.Repeat("a", maxLine) + `"}`
	in := strings.Join([]string{
		`not json`,
		`{"ts":100,"event":"task.dispatched","task":"good"}`,
		`{"ts":"soon","event":"task.dispatched","task":"badts"}`,
		`{"ts":-5,"event":"task.dispatched","task":"negts"}`,
		`{"ts":100,"event":"task.dispatched","task":"../../etc"}`,
		`{"ts":100,"event":"task.dispatched","task":"a b"}`,
		`{"ts":100,"event":"task.dispatched","task":""}`,
		`{"ts":100,"event":"task.dispatched"}`,
		`{"ts":100,"event":"task.future_event","task":"ignored"}`,
		`{"ts":100,"event":"task.merged","task":"good","via":"pr","pr":"javascript:alert(1)"}`,
		`{"ts":100,"event":"task.merged","task":"cred","via":"pr","pr":"https://user:pw@host/pull/1"}`,
		long,
		`[1,2]`,
		`{"ts":200,"event":"task.merged","task":"good","via":"pr","pr":"https://github.com/a/b/pull/3?x=1#y"}`,
	}, "\n")
	l := parse(t, in)
	if find(t, l, "good").MergedPR != "https://github.com/a/b/pull/3" {
		t.Errorf("good = %+v", find(t, l, "good"))
	}
	for _, tk := range l.Tasks {
		if tk.ID != "good" && tk.ID != "cred" {
			t.Errorf("unexpected task %q parsed from hostile input", tk.ID)
		}
		if tk.ID == "cred" && tk.MergedPR != "" {
			t.Errorf("a PR URL with credentials was kept: %q", tk.MergedPR)
		}
	}
	if len(l.Warnings) == 0 {
		t.Error("skipped records must be named in Warnings")
	}
	if len(l.Warnings) > costreport.MaxWarnings+1 {
		t.Errorf("warnings are not bounded: %d", len(l.Warnings))
	}
}

func TestParseBoundsWarnings(t *testing.T) {
	l := parse(t, strings.Repeat("garbage\n", 100))
	if len(l.Warnings) != costreport.MaxWarnings+1 || !strings.Contains(l.Warnings[len(l.Warnings)-1], "80 more") {
		t.Errorf("warnings = %d, last %q", len(l.Warnings), l.Warnings[len(l.Warnings)-1])
	}
}

func TestParseLastLineWithoutNewline(t *testing.T) {
	l := parse(t, `{"ts":5,"event":"task.dispatched","task":"t"}`)
	find(t, l, "t")
}

func TestLedgerReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet-ledger.jsonl")
	if err := os.WriteFile(path, []byte(lifecycle), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Ledger{Path: path}.Tasks(context.Background())
	if err != nil || len(l.Tasks) != 1 {
		t.Fatalf("tasks %+v, err %v", l.Tasks, err)
	}
	if _, err := (Ledger{Path: path + ".missing"}).Tasks(context.Background()); err == nil {
		t.Error("a missing ledger must be an error")
	}
}

func TestParseKeyNaming(t *testing.T) {
	if got := find(t, parse(t, `{"ts":5,"event":"task.dispatched","task":"abc-1"}`), "abc-1").Key; got != "fm:abc-1" {
		t.Errorf("key = %q, want fm:abc-1", got)
	}
}
