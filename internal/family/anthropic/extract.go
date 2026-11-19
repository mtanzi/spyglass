package anthropic

import (
	"github.com/tidwall/gjson"

	"github.com/mtanzi/spyglass/internal/core"
)

// endpointMessages is the endpoint whose bodies carry tool halves and usage.
const endpointMessages = "/v1/messages"

// Extractor turns one exchange into the fields of a call record (ADR 0014). It
// is pure: it reads the request bytes and the facts the scanner kept, writes
// into the record, and keeps neither. The proxy's tap calls it once, when the
// response body is closed.
type Extractor struct{}

// Extract fills the request and response facts of rec. The caller has set the
// fields that come from headers and from the exchange itself (key, correlation,
// endpoint, status, timings). Extract never fails and never panics: a field it
// cannot read stays at its zero value, which the store writes as null, and a
// request body that is not JSON marks the record partial.
//
// It writes the tool halves in a fixed order: client results in request order,
// then server results and uses, each in response order. The store gives each
// half its index from that order and its timestamp from its kind and source
// (ADR 0017): a client result at the request start, a server result or a use at
// the response end.
func (Extractor) Extract(rec *core.CallRecord, request []byte, facts Facts) {
	ExtractRequest(rec, request)
	extractResponse(rec, facts)
}

// ExtractRequest fills the request facts of rec: the model and the tool
// results. Extract calls it. The proxy calls it alone for a call that got no
// response, so the record still says what was asked.
func ExtractRequest(rec *core.CallRecord, body []byte) {
	if len(body) == 0 {
		return
	}
	if !gjson.ValidBytes(body) {
		rec.ExtractionPartial = true
		return
	}
	rec.Model = boundedString(gjson.GetBytes(body, "model"))
	if rec.Endpoint == endpointMessages {
		rec.ToolResults = requestToolResults(body)
	}
}

func extractResponse(rec *core.CallRecord, f Facts) {
	rec.Usage = NormalizeUsage(f.Usage)
	rec.UsageRaw = f.UsageRaw
	rec.StopReason = f.StopReason
	rec.ErrorType = f.ErrorType
	rec.ExtractionPartial = rec.ExtractionPartial || f.Partial
	// A response that stopped before its end carries usage that may be short.
	rec.UsageEstimated = rec.UsageEstimated || (len(f.UsageRaw) > 0 && !f.Complete)
	if rec.Endpoint == endpointMessages {
		rec.ToolResults = append(rec.ToolResults, f.ToolResults...)
		rec.ToolUses = f.ToolUses
	}
}

// NormalizeUsage maps the provider's usage fields into the disjoint columns.
// Anthropic's input_tokens counts only the uncached input; cache reads and
// cache writes are reported beside it, so each token lands in one column.
//
// A cache write has two TTLs. When the response splits it, the 5 minute and
// 1 hour parts are taken as sent, and any part of cache_creation_input_tokens
// the split does not account for goes to the 5 minute column. When it does not
// split it (an older response), the whole write is a 5 minute write, the
// default TTL. Anthropic folds thinking tokens into output_tokens and reports
// no count of them, so Reasoning stays zero.
func NormalizeUsage(u Usage) core.Usage {
	n := core.Usage{
		InputUncached: val(u.InputTokens),
		CacheRead:     val(u.CacheReadInputTokens),
		CacheWrite5m:  val(u.Ephemeral5mInputTokens),
		CacheWrite1h:  val(u.Ephemeral1hInputTokens),
		Output:        val(u.OutputTokens),
	}
	if rest := val(u.CacheCreationInputTokens) - n.CacheWrite5m - n.CacheWrite1h; rest > 0 {
		n.CacheWrite5m += rest
	}
	return n
}

func val(p *int) int {
	if p == nil || *p < 0 {
		return 0
	}
	return *p
}

// ErrorClassOf maps a provider error type to the error class of a failure that
// arrives inside a response with a success status, such as an error event in
// an SSE stream (ADR 0007). The mapping follows the HTTP status each type
// travels with. An unknown type is the provider's fault, so it is upstream_5xx.
func ErrorClassOf(errType string) core.ErrorClass {
	switch errType {
	case "rate_limit_error":
		return core.ErrorClassRateLimited
	case "authentication_error", "permission_error":
		return core.ErrorClassAuth
	case "timeout_error":
		return core.ErrorClassTimeout
	case "invalid_request_error", "not_found_error", "request_too_large":
		return core.ErrorClassInvalidRequest
	}
	return core.ErrorClassUpstream5xx // overloaded_error, api_error and the rest
}

// requestToolResults returns the tool_result blocks the client sent back in
// the trailing run of user messages. A client resends the whole conversation
// with every request, so results before the last assistant message were
// recorded by the calls that first carried them. The API merges consecutive
// user messages, so every message of the run is one turn and holds new results.
func requestToolResults(body []byte) []core.ToolResult {
	var run []gjson.Result
	gjson.GetBytes(body, "messages").ForEach(func(_, m gjson.Result) bool {
		if m.Get("role").String() == "user" {
			run = append(run, m)
		} else {
			run = nil
		}
		return true
	})
	var out []core.ToolResult
	for _, m := range run {
		m.Get("content").ForEach(func(_, blk gjson.Result) bool {
			if blk.Get("type").String() == "tool_result" {
				out = append(out, core.ToolResult{
					Source:      core.ToolSourceClient,
					ToolCallID:  boundedString(blk.Get("tool_use_id")),
					IsError:     blk.Get("is_error").Type == gjson.True,
					ResultBytes: resultSize(blk.Get("content")),
				})
			}
			return true
		})
	}
	return out
}

// resultSize measures a tool result without keeping it. A string result counts
// its bytes. A list of blocks counts the text of each text block and the whole
// JSON of any other block, such as an image.
func resultSize(c gjson.Result) int {
	switch {
	case c.Type == gjson.String:
		return len(c.Str)
	case c.IsArray():
		n := 0
		c.ForEach(func(_, blk gjson.Result) bool {
			if t := blk.Get("text"); blk.Get("type").String() == "text" && t.Type == gjson.String {
				n += len(t.Str)
			} else {
				n += len(blk.Raw)
			}
			return true
		})
		return n
	}
	return 0
}
