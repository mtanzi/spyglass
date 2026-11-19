package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAdmin serves two pages of calls and records the queries it received.
func fakeAdmin(t *testing.T, queries *[]map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/calls" {
			http.NotFound(w, r)
			return
		}
		q := map[string]string{}
		for k, v := range r.URL.Query() {
			q[k] = v[0]
		}
		*queries = append(*queries, q)
		w.Header().Set("Content-Type", "application/json")
		if q["cursor"] == "" {
			_, _ = w.Write([]byte(`{"calls":[
				{"id":"1","ts":"2026-11-01T10:05:00Z","key":"fm:t1","cost_micro_usd":1500000},
				{"id":"2","ts":"2026-11-01T10:06:00.123Z","key":"fm:t1","cost_micro_usd":null}],
				"next_cursor":"page2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"calls":[
			{"id":"3","ts":"2026-11-01T10:07:00Z","key":"personal","cost_micro_usd":250000}],
			"next_cursor":null}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runCLI(t *testing.T, env map[string]string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	now := func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }
	code = run(context.Background(), args, &out, &errb, func(k string) string { return env[k] }, now)
	return out.String(), errb.String(), code
}

func writeTasks(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tasks.csv")
	body := "id,start,end,merged_pr,key\nt1,2026-11-01T10:00:00Z,2026-11-01T11:00:00Z,https://example.test/pr/1,fm:t1\nidle,2026-11-01T10:00:00Z,,,fm:idle\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReportEndToEndJSON(t *testing.T) {
	var queries []map[string]string
	srv := fakeAdmin(t, &queries)
	out, errs, code := runCLI(t, map[string]string{"SPYGLASS_ADMIN_URL": srv.URL}, "--tasks", "file:"+writeTasks(t), "--format", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var got struct {
		Tasks []struct {
			ID           string `json:"id"`
			Calls        int    `json:"calls"`
			Unpriced     int    `json:"unpriced_calls"`
			CostMicroUSD int64  `json:"cost_micro_usd"`
		} `json:"tasks"`
		MergedPRs []struct {
			CostMicroUSD int64 `json:"cost_micro_usd"`
		} `json:"merged_prs"`
		Unattributed struct {
			Calls        int   `json:"calls"`
			CostMicroUSD int64 `json:"cost_micro_usd"`
		} `json:"unattributed"`
		TasksWithoutCalls []string `json:"tasks_without_calls"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(got.Tasks) != 2 || got.Tasks[0].ID != "idle" && got.Tasks[0].ID != "t1" {
		t.Fatalf("tasks = %+v", got.Tasks)
	}
	for _, tk := range got.Tasks {
		if tk.ID == "t1" && (tk.Calls != 2 || tk.Unpriced != 1 || tk.CostMicroUSD != 1_500_000) {
			t.Errorf("t1 = %+v", tk)
		}
	}
	if len(got.MergedPRs) != 1 || got.MergedPRs[0].CostMicroUSD != 1_500_000 ||
		got.Unattributed.Calls != 1 || got.Unattributed.CostMicroUSD != 250_000 ||
		len(got.TasksWithoutCalls) != 1 || got.TasksWithoutCalls[0] != "idle" {
		t.Errorf("report = %s", out)
	}
	// Two pages, and the window starts at the earliest task and ends after now.
	if len(queries) != 2 || queries[1]["cursor"] != "page2" {
		t.Fatalf("queries = %v", queries)
	}
	if q := queries[0]; q["from"] != "2026-11-01T10:00:00Z" || q["to"] != "2026-11-01T12:00:01Z" || q["limit"] != "500" {
		t.Errorf("window query = %v", q)
	}
}

func TestReportWindowFlagsAndTableDefault(t *testing.T) {
	var queries []map[string]string
	srv := fakeAdmin(t, &queries)
	out, _, code := runCLI(t, nil, "--admin-url", srv.URL, "--tasks", "file:"+writeTasks(t),
		"--from", "2026-11-01T09:00:00Z", "--to", "2026-11-01T11:30:00Z")
	if code != 0 || !strings.Contains(out, "Cost per merged PR") || !strings.Contains(out, "$1.500000") {
		t.Errorf("exit %d\n%s", code, out)
	}
	if q := queries[0]; q["from"] != "2026-11-01T09:00:00Z" || q["to"] != "2026-11-01T11:30:00Z" {
		t.Errorf("window query = %v", q)
	}
}

func TestReportErrors(t *testing.T) {
	tasks := "file:" + writeTasks(t)
	cases := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"no flags", nil, 2, "usage:"},
		{"bad format", []string{"--tasks", tasks, "--format", "xml"}, 2, "--format"},
		{"bad source kind", []string{"--tasks", "s3:x"}, 2, "unknown source"},
		{"no path", []string{"--tasks", "file:"}, 2, "want firstmate"},
		{"missing file", []string{"--tasks", "file:/nonexistent/t.csv"}, 1, "open task file"},
		{"bad from", []string{"--tasks", tasks, "--from", "x"}, 2, "--from"},
		{"bad admin url", []string{"--tasks", tasks, "--admin-url", "ftp://x"}, 2, "admin URL"},
		{"admin unreachable", []string{"--tasks", tasks, "--admin-url", "http://127.0.0.1:1"}, 1, "cannot reach the admin API"},
		{"extra arg", []string{"--tasks", tasks, "extra"}, 2, "unexpected argument"},
	}
	for _, c := range cases {
		_, errs, code := runCLI(t, nil, c.args...)
		if code != c.code || !strings.Contains(errs, c.msg) {
			t.Errorf("%s: exit %d, stderr %q; want exit %d containing %q", c.name, code, errs, c.code, c.msg)
		}
	}
}

func TestReportAdminErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }))
	defer srv.Close()
	_, errs, code := runCLI(t, nil, "--admin-url", srv.URL, "--tasks", "file:"+writeTasks(t))
	if code != 1 || !strings.Contains(errs, "answered 500") {
		t.Errorf("exit %d stderr %q", code, errs)
	}
}

func TestReportFirstmateLedger(t *testing.T) {
	var queries []map[string]string
	srv := fakeAdmin(t, &queries)
	ledger := filepath.Join(t.TempDir(), "fleet-ledger.jsonl")
	rec := `{"v":1,"ts":1793527200,"event":"task.dispatched","task":"t1"}` + "\n" + // 2026-11-01T10:00:00Z
		`{"v":1,"ts":1793530800,"event":"task.merged","task":"t1","via":"pr","pr":"https://example.test/pr/1"}` + "\n"
	if err := os.WriteFile(ledger, []byte(rec+rec), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errs, code := runCLI(t, nil, "--admin-url", srv.URL, "--tasks", "firstmate:"+ledger, "--format", "csv")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "merged_pr,https://example.test/pr/1,,,,https://example.test/pr/1,t1,2,1,1500000") {
		t.Errorf("csv:\n%s", out)
	}
}
