package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/core/coretest"
)

// spyReader wraps the canned reader and records what the handlers pass in.
type spyReader struct {
	*coretest.Reader
	filter   core.CallFilter
	page     core.Page
	countMax int
}

func (s *spyReader) ListCalls(ctx context.Context, f core.CallFilter, p core.Page) ([]core.CallSummary, *core.Cursor, error) {
	s.filter, s.page = f, p
	return s.Reader.ListCalls(ctx, f, p)
}

func (s *spyReader) CountCalls(ctx context.Context, f core.CallFilter, max int) (int, bool, error) {
	s.countMax = max
	return s.Reader.CountCalls(ctx, f, max)
}

func newTestMux(t *testing.T, r core.CallReader, view config.View) http.Handler {
	t.Helper()
	return NewMux(Options{
		Metrics: nopGatherer{},
		Calls:   r,
		Config:  func() config.View { return view },
		Now:     func() time.Time { return testNow },
	})
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
	return rr
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, rr.Body)
	}
	return v
}

func cost(n core.MicroUSD) *core.MicroUSD { return &n }

func TestListCalls(t *testing.T) {
	id := core.NewIDAt(testNow.Add(-time.Hour))
	next := core.Cursor{TS: testNow.Add(-time.Hour), ID: id}
	rd := &spyReader{Reader: &coretest.Reader{
		Calls: []core.CallSummary{
			{ID: id, TS: testNow.Add(-time.Hour), KeyName: "alice", Client: "claude-code", PromptID: "prompt-1", Project: "spyglass", Provider: "anthropic",
				Model: "claude-opus-4-8", Status: 200, Streamed: true, TTFT: 420 * time.Millisecond, Duration: 5 * time.Second,
				Usage: core.Usage{InputUncached: 10, Output: 20}, Cost: cost(1234), ToolCount: 2, ContextCompacted: true},
			{ID: core.NewID(), TS: testNow.Add(-2 * time.Hour), Status: 529},
		},
		Count: 7, Next: &next,
	}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/calls?model=a&model=b&status=529&limit=2")
	if rr.Code != 200 {
		t.Fatalf("status = %d\n%s", rr.Code, rr.Body)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	body := decode[callListJSON](t, rr)
	if body.Count != 7 || body.CountCapped || len(body.Calls) != 2 || body.NextCursor == nil {
		t.Fatalf("body = %+v", body)
	}
	if c, err := decodeCursor(*body.NextCursor); err != nil || c.ID != id || !c.TS.Equal(next.TS) {
		t.Errorf("next_cursor = %q: %+v %v", *body.NextCursor, c, err)
	}
	first := body.Calls[0]
	if first.ID != id.String() || first.Key != "alice" || first.PromptID != "prompt-1" || first.Project != "spyglass" || first.TTFTMs != 420 || first.DurationMs != 5000 ||
		first.CostMicroUSD == nil || *first.CostMicroUSD != 1234 || !first.ContextCompacted || first.Usage.Output != 20 {
		t.Errorf("first call = %+v", first)
	}
	if body.Calls[1].CostMicroUSD != nil {
		t.Error("unknown cost must stay null, not zero")
	}
	if got := rd.filter; len(got.Models) != 2 || got.Statuses[0] != 529 || !got.To.Equal(testNow) || !got.From.Equal(testNow.Add(-24*time.Hour)) {
		t.Errorf("filter passed to the reader = %+v", got)
	}
	if rd.page.Limit != 2 || rd.page.After != nil {
		t.Errorf("page passed to the reader = %+v", rd.page)
	}
	if rd.countMax != CountCap {
		t.Errorf("count max = %d, want %d", rd.countMax, CountCap)
	}
}

func TestListCallsLastPageAndEmpty(t *testing.T) {
	rr := get(newTestMux(t, &coretest.Reader{}, config.View{}), "/admin/calls")
	if rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"calls":[]`) || !strings.Contains(rr.Body.String(), `"next_cursor":null`) {
		t.Errorf("empty page must carry [] and null: %s", rr.Body)
	}
}

func TestListCallsCountCapped(t *testing.T) {
	rr := get(newTestMux(t, &coretest.Reader{Count: CountCap, Capped: true}, config.View{}), "/admin/calls")
	body := decode[callListJSON](t, rr)
	if body.Count != 10000 || !body.CountCapped {
		t.Errorf("count = %d capped = %v", body.Count, body.CountCapped)
	}
}

func TestListCallsPassesCursor(t *testing.T) {
	c := core.Cursor{TS: testNow.Add(-3 * time.Hour), ID: core.NewID()}
	rd := &spyReader{Reader: &coretest.Reader{}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/calls?cursor="+encodeCursor(c))
	if rr.Code != 200 || rd.page.After == nil || rd.page.After.ID != c.ID || !rd.page.After.TS.Equal(c.TS) {
		t.Errorf("code %d, after = %+v", rr.Code, rd.page.After)
	}
}

func TestListCallsErrors(t *testing.T) {
	boom := errors.New("pq: connection to 10.0.0.5 refused")
	tests := []struct {
		name   string
		reader core.CallReader
		target string
		status int
		typ    ErrorType
	}{
		{"unknown parameter", &coretest.Reader{}, "/admin/calls?nope=1", 400, ErrInvalidParameter},
		{"bad cursor", &coretest.Reader{}, "/admin/calls?cursor=zzz", 400, ErrInvalidCursor},
		{"store failure", &coretest.Reader{Err: boom}, "/admin/calls", 500, ErrInternal},
		{"no reader", nil, "/admin/calls", 503, ErrInternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := get(newTestMux(t, tc.reader, config.View{}), tc.target)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d\n%s", rr.Code, tc.status, rr.Body)
			}
			e := decode[errorBody](t, rr).Error
			if e.Type != tc.typ {
				t.Errorf("type = %q, want %q", e.Type, tc.typ)
			}
			if strings.Contains(rr.Body.String(), "10.0.0.5") {
				t.Errorf("store error text leaked: %s", rr.Body)
			}
		})
	}
}

func TestGetCall(t *testing.T) {
	id := core.NewIDAt(testNow)
	d := core.CallDetail{CallRecord: core.CallRecord{
		ID: id, TS: testNow, KeyName: "alice", Project: "spyglass", Model: "claude-opus-4-8", Status: 200, Endpoint: "/v1/messages",
		ContextCompacted: true, Duration: 1500 * time.Millisecond, EndedAt: testNow.Add(1500 * time.Millisecond),
		UsageRaw: json.RawMessage(`{"input_tokens":3}`), PricingVersion: "2026-10-01", Cost: cost(5),
		ToolUses:    []core.ToolUse{{Source: core.ToolSourceClient, ToolCallID: "toolu_1", Name: "Bash", ArgBytes: 42}},
		ToolResults: []core.ToolResult{{Source: core.ToolSourceClient, ToolCallID: "toolu_0", IsError: true, ResultBytes: 9}},
	}}
	h := newTestMux(t, &coretest.Reader{Details: map[core.ID]core.CallDetail{id: d}}, config.View{})

	rr := get(h, "/admin/calls/"+id.String())
	if rr.Code != 200 {
		t.Fatalf("status = %d\n%s", rr.Code, rr.Body)
	}
	got := decode[callDetailJSON](t, rr)
	if got.ID != id.String() || got.Project != "spyglass" || got.DurationMs != 1500 || got.EndedAt == nil || !got.ContextCompacted ||
		got.PricingVersion != "2026-10-01" || string(got.UsageRaw) != `{"input_tokens":3}` {
		t.Errorf("detail = %+v", got)
	}
	if len(got.ToolUses) != 1 || got.ToolUses[0].Name != "Bash" || got.ToolUses[0].ArgBytes != 42 ||
		len(got.ToolResults) != 1 || !got.ToolResults[0].IsError {
		t.Errorf("tools = %+v %+v", got.ToolUses, got.ToolResults)
	}

	for _, tc := range []struct {
		name, target string
		status       int
		typ          ErrorType
	}{
		{"missing", "/admin/calls/" + core.NewID().String(), 404, ErrNotFound},
		{"not an id", "/admin/calls/abc", 400, ErrInvalidID},
	} {
		rr := get(h, tc.target)
		if rr.Code != tc.status || decode[errorBody](t, rr).Error.Type != tc.typ {
			t.Errorf("%s: %d %s", tc.name, rr.Code, rr.Body)
		}
	}
}

func TestGetCallNullables(t *testing.T) {
	id := core.NewID()
	rd := &coretest.Reader{Details: map[core.ID]core.CallDetail{id: {CallRecord: core.CallRecord{ID: id}}}}
	rr := get(newTestMux(t, rd, config.View{}), "/admin/calls/"+id.String())
	for _, want := range []string{`"ended_at":null`, `"usage_raw":null`, `"cost_micro_usd":null`, `"tool_uses":[]`, `"tool_results":[]`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body lacks %s: %s", want, rr.Body)
		}
	}
}

func TestGetCallStoreFailure(t *testing.T) {
	rr := get(newTestMux(t, &coretest.Reader{Err: errors.New("boom")}, config.View{}), "/admin/calls/"+core.NewID().String())
	if rr.Code != 500 || decode[errorBody](t, rr).Error.Type != ErrInternal {
		t.Errorf("%d %s", rr.Code, rr.Body)
	}
}

func TestGetConfig(t *testing.T) {
	view := config.View{
		Providers: []config.ProviderView{{Name: "anthropic", Kind: "anthropic", BaseURL: "https://api.anthropic.com", AuthMode: "proxy_key", IdleTimeout: config.Duration(90 * time.Second)}},
		Pricing:   []config.PricingView{{Provider: "anthropic", Model: "m", Price: config.PriceView{Input: 5_000_000}}},
		Keys:      []config.KeyView{{Name: "alice"}, {Name: "ci", Disabled: true}},
	}
	rr := get(newTestMux(t, &coretest.Reader{}, view), "/admin/config")
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	got := decode[struct {
		Providers []struct {
			Name        string `json:"name"`
			IdleTimeout string `json:"idle_timeout"`
		} `json:"providers"`
		Pricing []struct {
			Price struct {
				Input int64 `json:"input"`
			} `json:"price"`
		} `json:"pricing"`
		Keys []struct {
			Name     string `json:"name"`
			Disabled bool   `json:"disabled"`
		} `json:"keys"`
	}](t, rr)
	if len(got.Providers) != 1 || got.Providers[0].IdleTimeout != "90s" || got.Pricing[0].Price.Input != 5_000_000 ||
		len(got.Keys) != 2 || got.Keys[0].Disabled || !got.Keys[1].Disabled {
		t.Errorf("config = %+v", got)
	}
}

func TestOpenAPIEndpoint(t *testing.T) {
	rr := get(newTestMux(t, &coretest.Reader{}, config.View{}), "/admin/openapi.json")
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d type %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	doc := decode[map[string]any](t, rr)
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	paths, _ := doc["paths"].(map[string]any)
	for _, p := range []string{"/admin/calls", "/admin/calls/{id}", "/admin/config", "/admin/openapi.json"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("document lacks path %s", p)
		}
	}
}

func TestUnknownAdminRoute(t *testing.T) {
	h := newTestMux(t, &coretest.Reader{}, config.View{})
	for _, target := range []string{"/admin/nope", "/admin/requests", "/admin/"} {
		rr := get(h, target)
		if rr.Code != 404 || decode[errorBody](t, rr).Error.Type != ErrNotFound {
			t.Errorf("%s: %d %s", target, rr.Code, rr.Body)
		}
	}
}

func TestNoCORSHeaders(t *testing.T) {
	h := newTestMux(t, &coretest.Reader{}, config.View{})
	for _, target := range []string{"/admin/calls", "/admin/config", "/admin/openapi.json", "/admin/nope", "/admin/calls?bad=1"} {
		rr := get(h, target)
		for k := range rr.Header() {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s sets %s", target, k)
			}
		}
	}
}

var errBoom = errors.New("boom")
