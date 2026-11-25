package api

import (
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

var testNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func TestParseCallParams(t *testing.T) {
	cur := core.Cursor{TS: time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC), ID: core.NewID()}
	rng := func(f core.CallFilter) core.CallFilter {
		f.From, f.To = testNow.Add(-24*time.Hour), testNow
		return f
	}
	tests := []struct {
		name  string
		query string
		want  core.CallFilter
		limit int
		after *core.Cursor
	}{
		{name: "defaults to the last 24 hours", query: "", want: rng(core.CallFilter{}), limit: DefaultLimit},
		{name: "explicit range", query: "from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z",
			want:  core.CallFilter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
			limit: DefaultLimit},
		{name: "offset time zone becomes UTC", query: "from=2026-10-01T02:00:00%2B02:00&to=2026-10-02T00:00:00Z",
			want:  core.CallFilter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
			limit: DefaultLimit},
		{name: "from only runs to now", query: "from=2026-10-21T00:00:00Z",
			want:  core.CallFilter{From: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), To: testNow},
			limit: DefaultLimit},
		{name: "to only reaches 24 hours back", query: "to=2026-10-02T00:00:00Z",
			want:  core.CallFilter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
			limit: DefaultLimit},
		{name: "repeated values", query: "model=a&model=b&key=ci&client=claude-code&provider=anthropic&agent_type=Explore&request_class=main&error_class=rate_limited",
			want: rng(core.CallFilter{Models: []string{"a", "b"}, Keys: []string{"ci"}, Clients: []string{"claude-code"},
				Providers: []string{"anthropic"}, AgentTypes: []string{"Explore"}, RequestClasses: []string{"main"}, ErrorClasses: []string{"rate_limited"}}),
			limit: DefaultLimit},
		{name: "projects", query: "project=spyglass&project=billing",
			want: rng(core.CallFilter{Projects: []string{"spyglass", "billing"}}), limit: DefaultLimit},
		{name: "statuses", query: "status=429&status=529",
			want: rng(core.CallFilter{Statuses: []int{429, 529}}), limit: DefaultLimit},
		{name: "latency and cost", query: "latency_ms_gte=60000&cost_micro_usd_gte=1500",
			want: rng(core.CallFilter{MinLatency: time.Minute, MinCost: 1500}), limit: DefaultLimit},
		{name: "latency at the maximum", query: "latency_ms_gte=1099511627776",
			want: rng(core.CallFilter{MinLatency: (1 << 40) * time.Millisecond}), limit: DefaultLimit},
		{name: "tool and tool_error", query: "tool=Bash&tool=Read&tool_error=true",
			want: rng(core.CallFilter{Tools: []string{"Bash", "Read"}, ToolError: ptr(true)}), limit: DefaultLimit},
		{name: "endpoint and streamed", query: "endpoint=/v1/messages&streamed=true",
			want: rng(core.CallFilter{Endpoints: []string{"/v1/messages"}, Streamed: ptr(true)}), limit: DefaultLimit},
		{name: "streamed false", query: "streamed=false", want: rng(core.CallFilter{Streamed: ptr(false)}), limit: DefaultLimit},
		{name: "scalars", query: "session=s1&agent=a1&prompt=p1",
			want: rng(core.CallFilter{SessionKey: "s1", AgentID: "a1", PromptID: "p1"}), limit: DefaultLimit},
		{name: "limit", query: "limit=500", want: rng(core.CallFilter{}), limit: 500},
		{name: "cursor", query: "cursor=" + encodeCursor(cur), want: rng(core.CallFilter{}), limit: DefaultLimit, after: &cur},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			got, perr := parseCallParams(q, testNow)
			if perr != nil {
				t.Fatalf("error: %v", perr)
			}
			if !reflect.DeepEqual(got.Filter, tc.want) {
				t.Errorf("filter = %+v\nwant     %+v", got.Filter, tc.want)
			}
			if got.Page.Limit != tc.limit {
				t.Errorf("limit = %d, want %d", got.Page.Limit, tc.limit)
			}
			if !reflect.DeepEqual(got.Page.After, tc.after) {
				t.Errorf("after = %+v, want %+v", got.Page.After, tc.after)
			}
			if err := got.Filter.Validate(); err != nil {
				t.Errorf("filter does not validate: %v", err)
			}
		})
	}
}

func TestParseCallParamsErrors(t *testing.T) {
	tests := []struct {
		name  string
		query string
		typ   ErrorType
		param string
	}{
		{"unknown parameter", "colour=red", ErrInvalidParameter, "colour"},
		{"misspelt parameter", "modle=a", ErrInvalidParameter, "modle"},
		{"bad from", "from=yesterday", ErrInvalidParameter, "from"},
		{"bad to", "to=2026-10-01", ErrInvalidParameter, "to"},
		{"repeated from", "from=2026-10-01T00:00:00Z&from=2026-10-02T00:00:00Z", ErrInvalidParameter, "from"},
		{"empty range", "from=2026-10-02T00:00:00Z&to=2026-10-02T00:00:00Z", ErrInvalidParameter, "from"},
		{"reversed range", "from=2026-10-03T00:00:00Z&to=2026-10-02T00:00:00Z", ErrInvalidParameter, "from"},
		{"from in the future", "from=2026-10-22T00:00:00Z", ErrInvalidParameter, "from"},
		{"status not a number", "status=abc", ErrInvalidParameter, "status"},
		{"status too small", "status=99", ErrInvalidParameter, "status"},
		{"status too large", "status=600", ErrInvalidParameter, "status"},
		{"streamed not a bool", "streamed=yes", ErrInvalidParameter, "streamed"},
		{"streamed repeated", "streamed=true&streamed=false", ErrInvalidParameter, "streamed"},
		{"tool_error not a bool", "tool_error=1", ErrInvalidParameter, "tool_error"},
		{"latency negative", "latency_ms_gte=-1", ErrInvalidParameter, "latency_ms_gte"},
		{"latency not a number", "latency_ms_gte=1.5", ErrInvalidParameter, "latency_ms_gte"},
		{"latency above the maximum", "latency_ms_gte=1099511627777", ErrInvalidParameter, "latency_ms_gte"},
		{"cost negative", "cost_micro_usd_gte=-5", ErrInvalidParameter, "cost_micro_usd_gte"},
		{"limit zero", "limit=0", ErrInvalidParameter, "limit"},
		{"limit too large", "limit=501", ErrInvalidParameter, "limit"},
		{"limit not a number", "limit=many", ErrInvalidParameter, "limit"},
		{"empty model", "model=", ErrInvalidParameter, "model"},
		{"empty project", "project=", ErrInvalidParameter, "project"},
		{"session repeated", "session=a&session=b", ErrInvalidParameter, "session"},
		{"cursor garbage", "cursor=garbage", ErrInvalidCursor, "cursor"},
		{"cursor repeated", "cursor=a&cursor=b", ErrInvalidParameter, "cursor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			_, perr := parseCallParams(q, testNow)
			if perr == nil {
				t.Fatal("no error")
			}
			if perr.typ != tc.typ || perr.parameter != tc.param || perr.status != 400 {
				t.Errorf("error = %d %s %q, want 400 %s %q", perr.status, perr.typ, perr.parameter, tc.typ, tc.param)
			}
		})
	}
}
