package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

const opusPricing = `pricing:
  - { provider: anthropic, model: claude-opus-4-8, from: 2020-01-01, input: 1.00, output: 2.00 }
  - { provider: anthropic, model: claude-opus-4-8, from: 2026-10-01,
      input: 5.00, output: 25.00, cache_read: 0.50, cache_write_5m: 6.25, cache_write_1h: 10.00 }
`

const messageReq = `{"model":"claude-opus-4-8","stream":%s,"messages":[{"role":"user","content":"FAKEPROMPT-hi"}]}`

func req(stream string) string { return strings.Replace(messageReq, "%s", stream, 1) }

func jsonUpstream(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// TestDefaultExtractorFillsRecordAndCost runs a whole exchange with the
// default extractor: the record gets facts, usage and the cost that the
// pricing version in effect at the call start gives.
func TestDefaultExtractorFillsRecordAndCost(t *testing.T) {
	e := newEnv(t, sseUpstream(fixture(t, "sse_normal.sse"), 64), nil, func(e *env) { e.pricing = opusPricing })
	_ = readAll(t, e.do(t, "POST", "/v1/messages", req("true"), auth()...).Body)
	rec := e.rec.next(t)

	if rec.Model != "claude-opus-4-8" || rec.StopReason != "end_turn" || !rec.Streamed {
		t.Errorf("model %q, stop %q, streamed %v", rec.Model, rec.StopReason, rec.Streamed)
	}
	want := core.Usage{InputUncached: 25, CacheRead: 100, CacheWrite5m: 40, Output: 12}
	if rec.Usage != want || len(rec.UsageRaw) == 0 || rec.UsageEstimated {
		t.Errorf("usage %+v, raw %s, estimated %v", rec.Usage, rec.UsageRaw, rec.UsageEstimated)
	}
	// 25*5 + 100*0.5 + 40*6.25 + 12*25 = 725 USD per million tokens.
	if rec.Cost == nil || *rec.Cost != 725 || rec.PricingVersion != "2026-10-01" {
		t.Errorf("cost %v, pricing version %q", rec.Cost, rec.PricingVersion)
	}
	if rec.AuthMode != "proxy_key" {
		t.Errorf("auth mode %q", rec.AuthMode)
	}
}

// TestCumulativeMessageDeltaUsagePricesTheCall: the usage of message_delta is
// cumulative (streaming docs), so the record and its cost come from the delta
// values, not from the lower input count of message_start. This stream is
// shaped like the docs' web-search example: 2679 input tokens at the start,
// 10682 at the end, with cache fields and server_tool_use.
func TestCumulativeMessageDeltaUsagePricesTheCall(t *testing.T) {
	e := newEnv(t, sseUpstream(fixture(t, "sse_cumulative_usage.sse"), 64), nil, func(e *env) { e.pricing = opusPricing })
	_ = readAll(t, e.do(t, "POST", "/v1/messages", req("true"), auth()...).Body)
	rec := e.rec.next(t)

	want := core.Usage{InputUncached: 10682, CacheRead: 4000, CacheWrite5m: 200, Output: 510}
	if rec.Usage != want || rec.UsageEstimated {
		t.Errorf("usage %+v, estimated %v", rec.Usage, rec.UsageEstimated)
	}
	// 10682*5 + 4000*0.5 + 200*6.25 + 510*25 = 69410 USD per million tokens.
	// Kept at the message_start count of 2679 it would be 28895.
	if rec.Cost == nil || *rec.Cost != 69410 {
		t.Errorf("cost %v", rec.Cost)
	}
}

func TestCostIsNullWithoutMatchingPrice(t *testing.T) {
	// No pricing at all, and a pricing entry for another model.
	for name, pricing := range map[string]string{
		"no pricing": "",
		"other model": `pricing:
  - { provider: anthropic, model: claude-haiku-5-5, from: 2020-01-01, input: 1.00, output: 2.00 }
`,
		"before first version": `pricing:
  - { provider: anthropic, model: claude-opus-4-8, from: 2999-01-01, input: 1.00, output: 2.00 }
`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, sseUpstream(fixture(t, "sse_normal.sse"), 64), nil, func(e *env) { e.pricing = pricing })
			_ = readAll(t, e.do(t, "POST", "/v1/messages", req("true"), auth()...).Body)
			rec := e.rec.next(t)
			if rec.Cost != nil || rec.PricingVersion != "" || rec.Usage.Output != 12 {
				t.Errorf("cost %v, version %q, output %d", rec.Cost, rec.PricingVersion, rec.Usage.Output)
			}
		})
	}
}

func TestErrorResponseHasNoUsageAndNoCost(t *testing.T) {
	e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(529)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"FAKEPROMPT-msg"}}`)
	}), nil, func(e *env) { e.pricing = opusPricing })
	_ = readAll(t, e.do(t, "POST", "/v1/messages", req("false"), auth()...).Body)
	rec := e.rec.next(t)
	if rec.Cost != nil || rec.UsageRaw != nil || rec.ErrorType != "overloaded_error" || rec.ErrorClass != "upstream_5xx" || rec.Model != "claude-opus-4-8" {
		t.Errorf("record = %+v", rec)
	}
}

func TestToolResultsFromRequest(t *testing.T) {
	e := newEnv(t, jsonUpstream(`{"type":"message","content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`), nil)
	body := `{"model":"m","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]},
	  {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"FAKEPROMPT-12345"}]}]}`
	_ = readAll(t, e.do(t, "POST", "/v1/messages", body, auth()...).Body)
	rec := e.rec.next(t)
	if len(rec.ToolResults) != 1 || rec.ToolResults[0].ToolCallID != "toolu_1" || rec.ToolResults[0].ResultBytes != 16 {
		t.Errorf("tool results = %+v", rec.ToolResults)
	}
}

// TestClientAbortIsEstimated: an abort mid-stream keeps the facts read so far,
// and the usage is marked as an estimate.
func TestClientAbortIsEstimated(t *testing.T) {
	started := make(chan struct{})
	e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, fixture(t, "sse_truncated.sse"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}), nil, func(e *env) { e.pricing = opusPricing })
	ctx, cancel := context.WithCancel(context.Background())
	r, _ := http.NewRequestWithContext(ctx, "POST", e.proxy.URL+"/v1/messages", strings.NewReader(req("true")))
	r.Header.Set("Authorization", "Bearer "+clientKey)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	_ = resp.Body.Close()
	rec := e.rec.next(t)
	if rec.ErrorClass != "client_abort" || !rec.UsageEstimated || len(rec.UsageRaw) == 0 || len(rec.ToolUses) != 1 {
		t.Errorf("record = %+v", rec)
	}
}

// TestAbortBeforeResponseKeepsRequestFacts: the upstream never answers, so the
// tap never runs, yet the record still says what was asked.
func TestAbortBeforeResponseKeepsRequestFacts(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer up.Close()
	defer close(release)
	e := newEnvURL(t, up.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "POST", e.proxy.URL+"/v1/messages", strings.NewReader(req("true")))
	r.Header.Set("Authorization", "Bearer "+clientKey)
	if resp, err := http.DefaultClient.Do(r); err == nil {
		_ = resp.Body.Close()
	}
	rec := e.rec.next(t)
	if rec.ErrorClass != "client_abort" || !rec.UsageEstimated || rec.Model != "claude-opus-4-8" || !rec.Streamed {
		t.Errorf("record = %+v", rec)
	}
}

func TestMalformedRequestBodyStillRecords(t *testing.T) {
	e := newEnv(t, jsonUpstream(`{"type":"error","error":{"type":"invalid_request_error","message":"x"}}`), nil)
	_ = readAll(t, e.do(t, "POST", "/v1/messages", "{not json", auth()...).Body)
	rec := e.rec.next(t)
	if rec.Model != "" || !rec.ExtractionPartial || len(rec.ToolResults) != 0 {
		t.Errorf("model %q, partial %v, results %v", rec.Model, rec.ExtractionPartial, rec.ToolResults)
	}
}

// An error event inside a 200 stream classifies the call: the extractor leaves
// the error type, and finish maps it to the class.
func TestMidstreamErrorEventClassifiesCall(t *testing.T) {
	stream, err := os.ReadFile("../../testdata/fixtures/midstream_error/response.sse")
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, sseUpstream(string(stream), 64), nil)
	_ = readAll(t, e.do(t, "POST", "/v1/messages", req("true"), auth()...).Body)
	rec := e.rec.next(t)
	if rec.Status != 200 || rec.ErrorType != "overloaded_error" || rec.ErrorClass != "upstream_5xx" {
		t.Errorf("status %d, error type %q, class %q", rec.Status, rec.ErrorType, rec.ErrorClass)
	}
}
