package costreport

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// Formatter writes a finished Report. The formatters are thin output adapters:
// they decide nothing about the numbers.
type Formatter func(w io.Writer, r Report) error

// Formatters maps the --format names to their Formatter.
var Formatters = map[string]Formatter{
	"table": WriteTable,
	"json":  WriteJSON,
	"csv":   WriteCSV,
}

// FormatNames lists the format names for help text and errors.
const FormatNames = "table, json, csv"

func usd(c core.MicroUSD) string {
	neg := ""
	if c < 0 {
		neg, c = "-", -c
	}
	return fmt.Sprintf("%s$%d.%06d", neg, c/1_000_000, c%1_000_000)
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// plain replaces control characters, so that an id or URL from an untrusted
// task list cannot write terminal escape sequences.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return '?'
		}
		return r
	}, s)
}

// WriteTable writes the report as aligned text.
func WriteTable(w io.Writer, r Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(tw, format, a...) }

	p("Cost per task\n")
	p("TASK\tSTART\tEND\tCALLS\tUNPRICED\tCOST\tMERGED PR\n")
	for _, t := range r.Tasks {
		end := rfc3339(t.End)
		if end == "" {
			end = "open"
		}
		p("%s\t%s\t%s\t%d\t%d\t%s\t%s\n", plain(t.ID), rfc3339(t.Start), end, t.Calls, t.Unpriced, usd(t.Cost), plain(t.MergedPR))
	}
	p("\nCost per merged PR\n")
	p("PR\tTASKS\tCALLS\tUNPRICED\tCOST\n")
	for _, c := range r.MergedPRs {
		p("%s\t%s\t%d\t%d\t%s\n", plain(c.PR), plain(strings.Join(c.TaskIDs, ",")), c.Calls, c.Unpriced, usd(c.Cost))
	}
	p("\nSummary\n")
	p("SCOPE\tCALLS\tUNPRICED\tCOST\n")
	p("all calls\t%d\t%d\t%s\n", r.Total.Calls, r.Total.Unpriced, usd(r.Total.Cost))
	p("calls with no task\t%d\t%d\t%s\n", r.Unattributed.Calls, r.Unattributed.Unpriced, usd(r.Unattributed.Cost))
	p("tasks with no merged PR\t%d\t%d\t%s\n", r.NoPR.Calls, r.NoPR.Unpriced, usd(r.NoPR.Cost))
	if len(r.UnattributedKeys) > 0 {
		p("\nCalls with no task, by key\n")
		p("KEY\tCALLS\tUNPRICED\tCOST\n")
		for _, k := range r.UnattributedKeys {
			p("%s\t%d\t%d\t%s\n", plain(k.Key), k.Calls, k.Unpriced, usd(k.Cost))
		}
	}
	if len(r.TasksWithoutCalls) > 0 {
		ids := make([]string, len(r.TasksWithoutCalls))
		for i, id := range r.TasksWithoutCalls {
			ids[i] = plain(id)
		}
		p("\nTasks with no calls: %s\n", strings.Join(ids, ", "))
	}
	if r.DuplicateTasks > 0 {
		p("\nDuplicate task records merged: %d\n", r.DuplicateTasks)
	}
	for _, m := range r.Warnings {
		p("warning: %s\n", plain(m))
	}
	return tw.Flush()
}

type jsonSpend struct {
	Calls        int   `json:"calls"`
	Unpriced     int   `json:"unpriced_calls"`
	CostMicroUSD int64 `json:"cost_micro_usd"`
}

func js(s Spend) jsonSpend { return jsonSpend{s.Calls, s.Unpriced, int64(s.Cost)} }

type jsonTask struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Start    string `json:"start"`
	End      string `json:"end,omitempty"`
	MergedPR string `json:"merged_pr,omitempty"`
	Shared   int    `json:"shared_calls"`
	jsonSpend
}

type jsonPR struct {
	PR      string   `json:"pr"`
	TaskIDs []string `json:"task_ids"`
	jsonSpend
}

type jsonKey struct {
	Key string `json:"key"`
	jsonSpend
}

// WriteJSON writes the report as one indented JSON object. Costs are integers
// in micro-USD, as in the admin API.
func WriteJSON(w io.Writer, r Report) error {
	out := struct {
		Tasks             []jsonTask `json:"tasks"`
		MergedPRs         []jsonPR   `json:"merged_prs"`
		NoPR              jsonSpend  `json:"tasks_without_merged_pr"`
		Unattributed      jsonSpend  `json:"unattributed"`
		UnattributedKeys  []jsonKey  `json:"unattributed_keys"`
		Total             jsonSpend  `json:"total"`
		TasksWithoutCalls []string   `json:"tasks_without_calls"`
		DuplicateTasks    int        `json:"duplicate_task_records"`
		Warnings          []string   `json:"warnings"`
	}{
		Tasks: []jsonTask{}, MergedPRs: []jsonPR{}, UnattributedKeys: []jsonKey{},
		NoPR: js(r.NoPR), Unattributed: js(r.Unattributed), Total: js(r.Total),
		TasksWithoutCalls: []string{}, DuplicateTasks: r.DuplicateTasks, Warnings: []string{},
	}
	for _, t := range r.Tasks {
		out.Tasks = append(out.Tasks, jsonTask{t.ID, t.Key, rfc3339(t.Start), rfc3339(t.End), t.MergedPR, t.Shared, js(t.Spend)})
	}
	for _, p := range r.MergedPRs {
		out.MergedPRs = append(out.MergedPRs, jsonPR{p.PR, p.TaskIDs, js(p.Spend)})
	}
	for _, k := range r.UnattributedKeys {
		out.UnattributedKeys = append(out.UnattributedKeys, jsonKey{k.Key, js(k.Spend)})
	}
	out.TasksWithoutCalls = append(out.TasksWithoutCalls, r.TasksWithoutCalls...)
	out.Warnings = append(out.Warnings, r.Warnings...)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// csvCell neutralises a spreadsheet formula in a cell from untrusted input.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// WriteCSV writes one row per task, one per merged PR, and one each for the
// unattributed calls and the total. The kind column tells them apart.
func WriteCSV(w io.Writer, r Report) error {
	cw := csv.NewWriter(w)
	spend := func(s Spend) []string {
		return []string{strconv.Itoa(s.Calls), strconv.Itoa(s.Unpriced), strconv.FormatInt(int64(s.Cost), 10)}
	}
	row := func(kind, id, key, start, end, pr, tasks string, s Spend) {
		_ = cw.Write(append([]string{kind, csvCell(id), csvCell(key), start, end, csvCell(pr), csvCell(tasks)}, spend(s)...))
	}
	_ = cw.Write([]string{"kind", "id", "key", "start", "end", "merged_pr", "tasks", "calls", "unpriced_calls", "cost_micro_usd"})
	for _, t := range r.Tasks {
		row("task", t.ID, t.Key, rfc3339(t.Start), rfc3339(t.End), t.MergedPR, "", t.Spend)
	}
	for _, p := range r.MergedPRs {
		row("merged_pr", p.PR, "", "", "", p.PR, strings.Join(p.TaskIDs, " "), p.Spend)
	}
	row("unattributed", "", "", "", "", "", "", r.Unattributed)
	row("total", "", "", "", "", "", "", r.Total)
	cw.Flush()
	return cw.Error()
}
