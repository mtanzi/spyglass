package taskfile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/costreport"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

const csvIn = "id,start,end,merged_pr,key\n" +
	"t1,2026-11-01T10:00:00Z,2026-11-01T11:00:00Z,https://example.test/pr/1,\n" +
	"t2,2026-11-01T12:00:00+01:00,,,team-key\n"

func TestParseCSV(t *testing.T) {
	l, err := ParseCSV(strings.NewReader(csvIn))
	if err != nil {
		t.Fatal(err)
	}
	want := []costreport.Task{
		{ID: "t1", Key: "t1", Start: ts("2026-11-01T10:00:00Z"), End: ts("2026-11-01T11:00:00Z"), MergedPR: "https://example.test/pr/1"},
		{ID: "t2", Key: "team-key", Start: ts("2026-11-01T11:00:00Z")},
	}
	if len(l.Tasks) != 2 || l.Tasks[0] != want[0] || l.Tasks[1] != want[1] || len(l.Warnings) != 0 {
		t.Errorf("got %+v warnings %v", l.Tasks, l.Warnings)
	}
}

func TestParseCSVColumnsInAnyOrderAndOptionalOnes(t *testing.T) {
	l, err := ParseCSV(strings.NewReader("\xef\xbb\xbfStart,ID\n2026-11-01T10:00:00Z,solo\n"))
	if err != nil || len(l.Tasks) != 1 || l.Tasks[0].ID != "solo" || l.Tasks[0].Key != "solo" || !l.Tasks[0].End.IsZero() {
		t.Errorf("tasks %+v err %v", l.Tasks, err)
	}
}

func TestParseCSVBadRowsAreSkipped(t *testing.T) {
	in := "id,start,end\n" +
		"ok,2026-11-01T10:00:00Z,\n" +
		",2026-11-01T10:00:00Z,\n" +
		"nostart,yesterday,\n" +
		"backwards,2026-11-01T10:00:00Z,2026-11-01T09:00:00Z\n" +
		"badend,2026-11-01T10:00:00Z,later\n"
	l, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Tasks) != 1 || l.Tasks[0].ID != "ok" || len(l.Warnings) != 4 {
		t.Errorf("tasks %+v warnings %v", l.Tasks, l.Warnings)
	}
}

func TestParseCSVNeedsIDAndStartColumns(t *testing.T) {
	for _, in := range []string{"", "id,end\nt,\n", "start\n2026-11-01T10:00:00Z\n"} {
		if _, err := ParseCSV(strings.NewReader(in)); err == nil {
			t.Errorf("ParseCSV(%q) = nil error, want an error", in)
		}
	}
}

const jsonIn = `[
 {"id":"t1","start":"2026-11-01T10:00:00Z","end":"2026-11-01T11:00:00Z","merged_pr":"https://example.test/pr/1"},
 {"id":"t2","start":"2026-11-01T12:00:00Z","key":"team-key"},
 {"id":"","start":"2026-11-01T12:00:00Z"},
 "not an object",
 {"id":"t3","start":5}
]`

func TestParseJSON(t *testing.T) {
	for name, in := range map[string]string{"array": jsonIn, "wrapped": `{"tasks":` + jsonIn + `}`} {
		l, err := ParseJSON(strings.NewReader(in))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(l.Tasks) != 2 || l.Tasks[0].ID != "t1" || l.Tasks[0].MergedPR != "https://example.test/pr/1" ||
			l.Tasks[1].Key != "team-key" || !l.Tasks[1].End.IsZero() || len(l.Warnings) != 3 {
			t.Errorf("%s: tasks %+v warnings %v", name, l.Tasks, l.Warnings)
		}
	}
}

func TestParseJSONRejectsOtherShapes(t *testing.T) {
	for _, in := range []string{"", "42", `"x"`, `{"tasks":3}`} {
		if _, err := ParseJSON(strings.NewReader(in)); err == nil {
			t.Errorf("ParseJSON(%q) = nil error", in)
		}
	}
}

func TestFileChoosesFormatByExtension(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"t.csv": csvIn, "t.json": jsonIn, "t.txt": csvIn} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"t.csv", "t.json"} {
		l, err := File{Path: filepath.Join(dir, name)}.Tasks(context.Background())
		if err != nil || len(l.Tasks) != 2 {
			t.Errorf("%s: tasks %+v err %v", name, l.Tasks, err)
		}
	}
	if _, err := (File{Path: filepath.Join(dir, "t.txt")}).Tasks(context.Background()); err == nil {
		t.Error("an unknown extension must be an error")
	}
	if _, err := (File{Path: filepath.Join(dir, "none.csv")}).Tasks(context.Background()); err == nil {
		t.Error("a missing file must be an error")
	}
}
