package postgres

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

var (
	from = time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	to   = from.Add(24 * time.Hour)
	yes  = true
)

func base() core.CallFilter { return core.CallFilter{From: from, To: to} }

func TestBuildWhereTimeBoundOnly(t *testing.T) {
	w := buildWhere(base())
	if got, want := w.sql(), "c.ts >= $1 AND c.ts < $2"; got != want {
		t.Fatalf("sql = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(w.args, []any{from, to}) {
		t.Fatalf("args = %v", w.args)
	}
}

// Each case sets one field to a non-zero value and expects the predicate that
// follows the time bound, with its argument count.
func TestBuildWhereEachField(t *testing.T) {
	cases := []struct {
		field string
		set   func(*core.CallFilter)
		want  string
		args  int
	}{
		{"Keys", func(f *core.CallFilter) { f.Keys = []string{"alice", "bob"} }, "c.key_name = ANY($3::text[])", 1},
		{"Keys one", func(f *core.CallFilter) { f.Keys = []string{"alice"} }, "c.key_name = $3", 1},
		{"Clients", func(f *core.CallFilter) { f.Clients = []string{"claude-code"} }, "c.client = $3", 1},
		{"Clients unknown", func(f *core.CallFilter) { f.Clients = []string{"", "x"} }, "(c.client = $3 OR c.client IS NULL)", 1},
		{"Projects", func(f *core.CallFilter) { f.Projects = []string{"spyglass"} }, "c.project = $3", 1},
		{"Projects unknown", func(f *core.CallFilter) { f.Projects = []string{"", "x"} }, "(c.project = $3 OR c.project IS NULL)", 1},
		{"Providers", func(f *core.CallFilter) { f.Providers = []string{"anthropic"} }, "c.provider = $3", 1},
		{"Models", func(f *core.CallFilter) { f.Models = []string{"m"} }, "c.model = $3", 1},
		{"Endpoints", func(f *core.CallFilter) { f.Endpoints = []string{"/v1/messages"} }, "c.endpoint = $3", 1},
		{"Streamed", func(f *core.CallFilter) { f.Streamed = &yes }, "c.streamed = $3", 1},
		{"Statuses", func(f *core.CallFilter) { f.Statuses = []int{429, 529} }, "c.status = ANY($3::bigint[])", 1},
		{"Statuses one", func(f *core.CallFilter) { f.Statuses = []int{429} }, "c.status = $3::bigint", 1},
		{"Statuses unknown", func(f *core.CallFilter) { f.Statuses = []int{0} }, "c.status IS NULL", 0},
		{"ErrorClasses", func(f *core.CallFilter) { f.ErrorClasses = []string{"rate_limited"} }, "c.error_class = $3", 1},
		{"SessionKey", func(f *core.CallFilter) { f.SessionKey = "s1" }, "c.session_key = $3", 1},
		{"AgentID", func(f *core.CallFilter) { f.AgentID = "a1" }, "c.agent_id = $3", 1},
		{"AgentTypes", func(f *core.CallFilter) { f.AgentTypes = []string{"subagent"} }, "c.agent_type = $3", 1},
		{"PromptID", func(f *core.CallFilter) { f.PromptID = "p1" }, "c.prompt_id = $3", 1},
		{"RequestClasses", func(f *core.CallFilter) { f.RequestClasses = []string{"main"} }, "c.request_class = $3", 1},
		{"Tools", func(f *core.CallFilter) { f.Tools = []string{"Bash"} },
			"EXISTS (SELECT 1 FROM tool_events u WHERE u.call_id = c.id AND u.kind = 'use' AND u.tool_name = $3)", 1},
		{"ToolError", func(f *core.CallFilter) { f.ToolError = &yes },
			"EXISTS (SELECT 1 FROM tool_events u JOIN tool_events r ON r.tool_call_id = u.tool_call_id AND r.kind = 'result' WHERE u.call_id = c.id AND u.kind = 'use' AND r.is_error = $3)", 1},
		{"Tools and ToolError", func(f *core.CallFilter) { f.Tools = []string{"Bash"}; f.ToolError = &yes },
			"EXISTS (SELECT 1 FROM tool_events u JOIN tool_events r ON r.tool_call_id = u.tool_call_id AND r.kind = 'result' WHERE u.call_id = c.id AND u.kind = 'use' AND u.tool_name = $3 AND r.is_error = $4)", 2},
		{"MinLatency", func(f *core.CallFilter) { f.MinLatency = 1500*time.Millisecond + time.Microsecond }, "c.duration_ms >= $3::bigint", 1},
		{"MinCost", func(f *core.CallFilter) { f.MinCost = 42 }, "c.cost_usd >= $3", 1},
	}
	covered := map[string]bool{}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			f := base()
			c.set(&f)
			w := buildWhere(f)
			want := "c.ts >= $1 AND c.ts < $2 AND " + c.want
			if got := w.sql(); got != want {
				t.Fatalf("sql =\n %s\nwant\n %s", got, want)
			}
			if len(w.args) != 2+c.args {
				t.Fatalf("args = %d, want %d", len(w.args), 2+c.args)
			}
		})
		covered[strings.Fields(c.field)[0]] = true
	}
	// A new CallFilter field needs a branch in buildWhere and a case above.
	for field := range reflect.TypeFor[core.CallFilter]().Fields() {
		name := field.Name
		if name != "From" && name != "To" && !covered[name] {
			t.Errorf("CallFilter.%s has no case", name)
		}
	}
}

func TestBuildWhereArgsAreBound(t *testing.T) {
	evil := "x'; DROP TABLE calls; --"
	f := base()
	f.Keys, f.Clients, f.Providers, f.Models, f.Endpoints = []string{evil}, []string{evil}, []string{evil}, []string{evil}, []string{evil}
	f.Projects = []string{evil}
	f.ErrorClasses, f.AgentTypes, f.RequestClasses, f.Tools = []string{evil}, []string{evil}, []string{evil}, []string{evil}
	f.SessionKey, f.AgentID, f.PromptID = evil, evil, evil
	w := buildWhere(f)
	if strings.Contains(w.sql(), "DROP") || strings.Contains(w.sql(), "'x") {
		t.Fatalf("value in sql text: %s", w.sql())
	}
}

func TestBuildWhereCleansValues(t *testing.T) {
	f := base()
	f.SessionKey = "a\x00b"
	w := buildWhere(f)
	if got := w.args[len(w.args)-1]; got != "ab" {
		t.Fatalf("arg = %q, want NUL dropped", got)
	}
}
