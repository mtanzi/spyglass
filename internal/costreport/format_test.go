package costreport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
)

// fixedReport is a finished Report; the formatters must show it and compute nothing.
func fixedReport() Report {
	return Report{
		Tasks: []TaskCost{
			{Task: Task{ID: "fix-login", Key: "fm:fix-login", Start: at(0), End: at(30), MergedPR: "https://example.test/pr/7"},
				Spend: Spend{Calls: 3, Cost: 1_234_567, Unpriced: 1}},
			{Task: Task{ID: "=cmd", Key: "fm:=cmd", Start: at(5)}, Spend: Spend{}},
		},
		MergedPRs:         []PRCost{{PR: "https://example.test/pr/7", TaskIDs: []string{"fix-login"}, Spend: Spend{Calls: 3, Cost: 1_234_567, Unpriced: 1}}},
		NoPR:              Spend{},
		Unattributed:      Spend{Calls: 2, Cost: 500},
		UnattributedKeys:  []KeyCost{{Key: "personal", Spend: Spend{Calls: 2, Cost: 500}}},
		Total:             Spend{Calls: 5, Cost: 1_235_067, Unpriced: 1},
		TasksWithoutCalls: []string{"=cmd"},
		DuplicateTasks:    1,
		Warnings:          []string{"line 3: not a JSON object, skipped"},
	}
}

func TestWriteTable(t *testing.T) {
	var b bytes.Buffer
	if err := WriteTable(&b, fixedReport()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"fix-login", "$1.234567", "https://example.test/pr/7", "open",
		"calls with no task", "$0.000500", "personal",
		"Tasks with no calls: =cmd", "Duplicate task records merged: 1", "warning: line 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestWriteTableStripsControlCharacters(t *testing.T) {
	r := Report{Tasks: []TaskCost{{Task: Task{ID: "a\x1b[31mred", Start: at(0)}}}}
	var b bytes.Buffer
	_ = WriteTable(&b, r)
	if strings.ContainsRune(b.String(), 0x1b) {
		t.Errorf("table passes an escape character through: %q", b.String())
	}
}

func TestWriteJSON(t *testing.T) {
	var b bytes.Buffer
	if err := WriteJSON(&b, fixedReport()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tasks []struct {
			ID           string `json:"id"`
			Calls        int    `json:"calls"`
			Unpriced     int    `json:"unpriced_calls"`
			CostMicroUSD int64  `json:"cost_micro_usd"`
			MergedPR     string `json:"merged_pr"`
			End          string `json:"end"`
		} `json:"tasks"`
		MergedPRs []struct {
			PR           string   `json:"pr"`
			TaskIDs      []string `json:"task_ids"`
			CostMicroUSD int64    `json:"cost_micro_usd"`
		} `json:"merged_prs"`
		Total struct {
			CostMicroUSD int64 `json:"cost_micro_usd"`
		} `json:"total"`
		Unattributed struct {
			Calls int `json:"calls"`
		} `json:"unattributed"`
		TasksWithoutCalls []string `json:"tasks_without_calls"`
		Warnings          []string `json:"warnings"`
	}
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b.String())
	}
	if len(got.Tasks) != 2 || got.Tasks[0].CostMicroUSD != 1_234_567 || got.Tasks[0].Unpriced != 1 ||
		got.Tasks[0].MergedPR != "https://example.test/pr/7" || got.Tasks[1].End != "" {
		t.Errorf("tasks = %+v", got.Tasks)
	}
	if len(got.MergedPRs) != 1 || got.MergedPRs[0].CostMicroUSD != 1_234_567 || got.Total.CostMicroUSD != 1_235_067 ||
		got.Unattributed.Calls != 2 || len(got.TasksWithoutCalls) != 1 || len(got.Warnings) != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestWriteJSONEmptyReportUsesArrays(t *testing.T) {
	var b bytes.Buffer
	_ = WriteJSON(&b, Report{})
	for _, want := range []string{`"tasks": []`, `"merged_prs": []`, `"warnings": []`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("empty report lacks %s:\n%s", want, b.String())
		}
	}
}

func TestWriteCSV(t *testing.T) {
	var b bytes.Buffer
	if err := WriteCSV(&b, fixedReport()); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&b).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"kind", "id", "key", "start", "end", "merged_pr", "tasks", "calls", "unpriced_calls", "cost_micro_usd"},
		{"task", "fix-login", "fm:fix-login", "2026-11-01T10:00:00Z", "2026-11-01T10:30:00Z", "https://example.test/pr/7", "", "3", "1", "1234567"},
		{"task", "'=cmd", "fm:=cmd", "2026-11-01T10:05:00Z", "", "", "", "0", "0", "0"},
		{"merged_pr", "https://example.test/pr/7", "", "", "", "https://example.test/pr/7", "fix-login", "3", "1", "1234567"},
		{"unattributed", "", "", "", "", "", "", "2", "0", "500"},
		{"total", "", "", "", "", "", "", "5", "1", "1235067"},
	}
	if len(rows) != len(want) {
		t.Fatalf("%d rows, want %d: %v", len(rows), len(want), rows)
	}
	for i := range want {
		if strings.Join(rows[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("row %d = %v, want %v", i, rows[i], want[i])
		}
	}
}

func TestFormattersNames(t *testing.T) {
	for _, n := range []string{"table", "json", "csv"} {
		if Formatters[n] == nil {
			t.Errorf("no formatter %q", n)
		}
	}
}
