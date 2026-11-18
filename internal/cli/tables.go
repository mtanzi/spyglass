package cli

import "strings"

// The column tables of the commands (ADR 0055, "Output"). Each lists the API
// fields it reads, so the tests can check them against api/openapi.yaml.

func join[T any](parts ...[]T) []T {
	var out []T
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func callUsage(r *callRow) usage { return r.Usage }

// callCols is the call list: one row per call (schema CallSummary).
var callCols = colTable[callRow]{schema: "CallSummary", cols: join([]column[callRow]{
	{name: "id", api: []string{"id"}, desc: "call id (UUIDv7); also the x-request-id", deflt: true, value: func(r *callRow) any { return r.ID }},
	{name: "ts", api: []string{"ts"}, unit: "RFC 3339 UTC", desc: "start of the call", value: func(r *callRow) any { return r.TS }},
	{name: "key", api: []string{"key"}, desc: "virtual key name", value: func(r *callRow) any { return r.Key }},
	{name: "client", api: []string{"client"}, desc: "client name", value: func(r *callRow) any { return r.Client }},
	{name: "session_key", api: []string{"session_key"}, desc: "session key; empty when the call has no session", value: func(r *callRow) any { return r.SessionKey }},
	{name: "agent_id", api: []string{"agent_id"}, desc: "agent id", value: func(r *callRow) any { return r.AgentID }},
	{name: "prompt_id", api: []string{"prompt_id"}, desc: "prompt id; empty when not known", value: func(r *callRow) any { return r.PromptID }},
	{name: "provider", api: []string{"provider"}, desc: "provider name", value: func(r *callRow) any { return r.Provider }},
	{name: "model", api: []string{"model"}, desc: "model as the client asked for it", deflt: true, value: func(r *callRow) any { return r.Model }},
	{name: "status", api: []string{"status"}, desc: "HTTP status returned to the client", deflt: true, value: func(r *callRow) any { return r.Status }},
	{name: "error_class", api: []string{"error_class"}, desc: "error class; empty on success", value: func(r *callRow) any { return r.ErrorClass }},
	{name: "stop_reason", api: []string{"stop_reason"}, desc: "stop reason of the model", value: func(r *callRow) any { return r.StopReason }},
	{name: "streamed", api: []string{"streamed"}, desc: "the call was streamed", value: func(r *callRow) any { return r.Streamed }},
	{name: "ttft_ms", api: []string{"ttft_ms"}, unit: "ms", desc: "time to first token; 0 when not known", value: func(r *callRow) any { return r.TTFTMS }},
	{name: "duration_ms", api: []string{"duration_ms"}, unit: "ms", desc: "total duration of the call", value: func(r *callRow) any { return r.DurationMS }},
}, usageCols("usage", callUsage, false), []column[callRow]{
	{name: "cost_usd", api: []string{"cost_micro_usd"}, unit: "USD", desc: costDesc, deflt: true, value: func(r *callRow) any { return costUSD(r.CostMicroUSD) }},
	{name: "api_equivalent", unit: "bool",
		desc:  "the cost is an API-equivalent price, not a bill: the call went through a client_passthrough provider (ADR 0052); read from /admin/config",
		value: func(r *callRow) any { return r.apiEquivalent }},
	{name: "tool_count", api: []string{"tool_count"}, desc: "tool uses carried by the call", value: func(r *callRow) any { return r.ToolCount }},
	{name: "context_compacted", api: []string{"context_compacted"}, desc: "the cache prefix resets at this call", value: func(r *callRow) any { return r.ContextCompacted }},
})}

func detailUsage(r *callDetailRow) usage { return r.Usage }

// callDetailRow is one call of `calls show` with its API-equivalent flag.
type callDetailRow struct {
	callDetail
	apiEquivalent bool
}

func str(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// callDetailCols is one call with every recorded field (schema CallDetail).
var callDetailCols = colTable[callDetailRow]{schema: "CallDetail", cols: join([]column[callDetailRow]{
	{name: "id", api: []string{"id"}, desc: "call id (UUIDv7)", deflt: true, value: func(r *callDetailRow) any { return r.ID }},
	{name: "ts", api: []string{"ts"}, unit: "RFC 3339 UTC", desc: "start of the call", deflt: true, value: func(r *callDetailRow) any { return r.TS }},
	{name: "ended_at", api: []string{"ended_at"}, unit: "RFC 3339 UTC", desc: "end of the call; null when not known", deflt: true, value: func(r *callDetailRow) any { return str(r.EndedAt) }},
	{name: "key", api: []string{"key"}, desc: "virtual key name", deflt: true, value: func(r *callDetailRow) any { return r.Key }},
	{name: "client", api: []string{"client"}, desc: "client name", deflt: true, value: func(r *callDetailRow) any { return r.Client }},
	{name: "client_raw", api: []string{"client_raw"}, desc: "client user agent as received", deflt: true, value: func(r *callDetailRow) any { return r.ClientRaw }},
	{name: "client_request_id", api: []string{"client_request_id"}, desc: "request id the client sent", deflt: true, value: func(r *callDetailRow) any { return r.ClientRequestID }},
	{name: "session_key", api: []string{"session_key"}, desc: "session key", deflt: true, value: func(r *callDetailRow) any { return r.SessionKey }},
	{name: "agent_id", api: []string{"agent_id"}, desc: "agent id", deflt: true, value: func(r *callDetailRow) any { return r.AgentID }},
	{name: "parent_agent_id", api: []string{"parent_agent_id"}, desc: "parent agent id; empty for the main agent", deflt: true, value: func(r *callDetailRow) any { return r.ParentAgentID }},
	{name: "agent_type", api: []string{"agent_type"}, desc: "agent type", deflt: true, value: func(r *callDetailRow) any { return r.AgentType }},
	{name: "prompt_id", api: []string{"prompt_id"}, desc: "prompt id", deflt: true, value: func(r *callDetailRow) any { return r.PromptID }},
	{name: "request_class", api: []string{"request_class"}, desc: "request class", deflt: true, value: func(r *callDetailRow) any { return r.RequestClass }},
	{name: "compaction", api: []string{"compaction"}, desc: "the call is a compaction request", deflt: true, value: func(r *callDetailRow) any { return r.Compaction }},
	{name: "context_compacted", api: []string{"context_compacted"}, desc: "the cache prefix resets at this call", deflt: true, value: func(r *callDetailRow) any { return r.ContextCompacted }},
	{name: "trace_id", api: []string{"trace_id"}, desc: "trace id", deflt: true, value: func(r *callDetailRow) any { return r.TraceID }},
	{name: "family", api: []string{"family"}, desc: "protocol family", deflt: true, value: func(r *callDetailRow) any { return r.Family }},
	{name: "endpoint", api: []string{"endpoint"}, desc: "request path", deflt: true, value: func(r *callDetailRow) any { return r.Endpoint }},
	{name: "auth_mode", api: []string{"auth_mode"}, desc: "proxy_key or client_passthrough", deflt: true, value: func(r *callDetailRow) any { return r.AuthMode }},
	{name: "provider", api: []string{"provider"}, desc: "provider name", deflt: true, value: func(r *callDetailRow) any { return r.Provider }},
	{name: "model", api: []string{"model"}, desc: "model as the client asked for it", deflt: true, value: func(r *callDetailRow) any { return r.Model }},
	{name: "upstream_request_id", api: []string{"upstream_request_id"}, desc: "request id of the provider", deflt: true, value: func(r *callDetailRow) any { return r.UpstreamRequestID }},
	{name: "status", api: []string{"status"}, desc: "HTTP status returned to the client", deflt: true, value: func(r *callDetailRow) any { return r.Status }},
	{name: "error_class", api: []string{"error_class"}, desc: "error class; empty on success", deflt: true, value: func(r *callDetailRow) any { return r.ErrorClass }},
	{name: "error_type", api: []string{"error_type"}, desc: "error type of the provider", deflt: true, value: func(r *callDetailRow) any { return r.ErrorType }},
	{name: "stop_reason", api: []string{"stop_reason"}, desc: "stop reason of the model", deflt: true, value: func(r *callDetailRow) any { return r.StopReason }},
	{name: "streamed", api: []string{"streamed"}, desc: "the call was streamed", deflt: true, value: func(r *callDetailRow) any { return r.Streamed }},
	{name: "ttfb_ms", api: []string{"ttfb_ms"}, unit: "ms", desc: "time to first byte", deflt: true, value: func(r *callDetailRow) any { return r.TTFBMS }},
	{name: "ttft_ms", api: []string{"ttft_ms"}, unit: "ms", desc: "time to first token", deflt: true, value: func(r *callDetailRow) any { return r.TTFTMS }},
	{name: "duration_ms", api: []string{"duration_ms"}, unit: "ms", desc: "total duration of the call", deflt: true, value: func(r *callDetailRow) any { return r.DurationMS }},
}, defaultOn(usageCols("usage", detailUsage, true)), []column[callDetailRow]{
	{name: "usage_raw", api: []string{"usage_raw"}, desc: "the provider usage object as received, numbers only, as JSON text; null when there was none", deflt: true,
		value: func(r *callDetailRow) any {
			if len(r.UsageRaw) == 0 || string(r.UsageRaw) == "null" {
				return nil
			}
			return string(r.UsageRaw)
		}},
	{name: "usage_estimated", api: []string{"usage_estimated"}, desc: "the usage was estimated, for example after a client abort", deflt: true, value: func(r *callDetailRow) any { return r.UsageEstimated }},
	{name: "extraction_partial", api: []string{"extraction_partial"}, desc: "the extractor could not read the whole exchange", deflt: true, value: func(r *callDetailRow) any { return r.ExtractionPartial }},
	{name: "cost_usd", api: []string{"cost_micro_usd"}, unit: "USD", desc: costDesc, deflt: true, value: func(r *callDetailRow) any { return costUSD(r.CostMicroUSD) }},
	{name: "api_equivalent", api: []string{"auth_mode"}, unit: "bool", desc: "the cost is an API-equivalent price, not a bill: auth_mode is client_passthrough (ADR 0052)", deflt: true,
		value: func(r *callDetailRow) any { return r.apiEquivalent }},
	{name: "pricing_version", api: []string{"pricing_version"}, desc: "the from date of the matched price", deflt: true, value: func(r *callDetailRow) any { return r.PricingVersion }},
	{name: "prev_tool_durations", api: []string{"prev_tool_durations"}, desc: "tool durations the client reported", deflt: true, value: func(r *callDetailRow) any { return r.PrevToolDurations }},
})}

func defaultOn[T any](cols []column[T]) []column[T] {
	for i := range cols {
		cols[i].deflt = true
	}
	return cols
}

var toolUseCols = colTable[toolUse]{schema: "ToolUse", cols: defaultOn([]column[toolUse]{
	{name: "name", api: []string{"name"}, desc: "tool name", value: func(r *toolUse) any { return r.Name }},
	{name: "source", api: []string{"source"}, desc: "who ran the tool: client or server", value: func(r *toolUse) any { return r.Source }},
	{name: "tool_call_id", api: []string{"tool_call_id"}, desc: "provider id of the tool use", value: func(r *toolUse) any { return r.ToolCallID }},
	{name: "arg_bytes", api: []string{"arg_bytes"}, unit: "bytes", desc: "size of the arguments; the arguments are never stored", value: func(r *toolUse) any { return r.ArgBytes }},
})}

var toolResultCols = colTable[toolResult]{schema: "ToolResult", cols: defaultOn([]column[toolResult]{
	{name: "tool_call_id", api: []string{"tool_call_id"}, desc: "id of the tool use the result answers", value: func(r *toolResult) any { return r.ToolCallID }},
	{name: "source", api: []string{"source"}, desc: "who ran the tool: client or server", value: func(r *toolResult) any { return r.Source }},
	{name: "is_error", api: []string{"is_error"}, desc: "the tool result is an error", value: func(r *toolResult) any { return r.IsError }},
	{name: "result_bytes", api: []string{"result_bytes"}, unit: "bytes", desc: "size of the result; the result is never stored", value: func(r *toolResult) any { return r.ResultBytes }},
})}

func sessionUsage(r *sessionSummary) usage { return r.Usage }

// sessionCols is one session (schema SessionSummary).
var sessionCols = colTable[sessionSummary]{schema: "SessionSummary", cols: join([]column[sessionSummary]{
	{name: "session_key", api: []string{"session_key"}, desc: "session key, exactly as the client sent it", deflt: true, value: func(r *sessionSummary) any { return r.SessionKey }},
	{name: "key", api: []string{"key"}, desc: "virtual key name of the session", value: func(r *sessionSummary) any { return r.Key }},
	{name: "client", api: []string{"client"}, desc: "client name", value: func(r *sessionSummary) any { return r.Client }},
	{name: "first_ts", api: []string{"first_ts"}, unit: "RFC 3339 UTC", desc: "start of the first call in the window", value: func(r *sessionSummary) any { return r.FirstTS }},
	{name: "last_ts", api: []string{"last_ts"}, unit: "RFC 3339 UTC", desc: "start of the newest call in the window", value: func(r *sessionSummary) any { return r.LastTS }},
	{name: "ended_at", api: []string{"ended_at"}, unit: "RFC 3339 UTC", desc: "latest end of a call", value: func(r *sessionSummary) any { return r.EndedAt }},
	{name: "calls", api: []string{"calls"}, desc: "calls of the session in the window", deflt: true, value: func(r *sessionSummary) any { return r.Calls }},
	{name: "prompts", api: []string{"prompts"}, desc: "distinct prompt ids", value: func(r *sessionSummary) any { return r.Prompts }},
	{name: "tool_calls", api: []string{"tool_calls"}, desc: "distinct tool call ids", value: func(r *sessionSummary) any { return r.ToolCalls }},
	{name: "errors", api: []string{"errors"}, desc: "calls with an error class", deflt: true, value: func(r *sessionSummary) any { return r.Errors }},
	{name: "agent_count", api: []string{"agent_count"}, desc: "distinct agents", value: func(r *sessionSummary) any { return r.AgentCount }},
	{name: "models", api: []string{"models"}, desc: "models used, joined by a semicolon", value: func(r *sessionSummary) any { return strings.Join(r.Models, ";") }},
}, usageCols("usage", sessionUsage, false), []column[sessionSummary]{
	{name: "cost_usd", api: []string{"cost_micro_usd"}, unit: "USD",
		desc:  "sum of the known call costs in USD, rounded to four decimals; `unpriced` when no call has a known cost. It leaves out the calls counted in unpriced_calls",
		deflt: true, value: func(r *sessionSummary) any { return costUSD(r.CostMicroUSD) }},
	{name: "unpriced_calls", api: []string{"unpriced_calls"}, desc: "calls of the session with no known cost, left out of cost_usd (ADR 0056)", value: func(r *sessionSummary) any { return r.UnpricedCalls }},
	{name: "llm_ms", api: []string{"llm_ms"}, unit: "ms", desc: "model time: sum of the call durations. Parallel calls overlap in wall time and still add up", value: func(r *sessionSummary) any { return r.LLMMS }},
	{name: "tool_ms", api: []string{"tool_ms"}, unit: "ms", desc: "tool time: sum of the known tool call durations. Parallel tool calls overlap in wall time and still add up", value: func(r *sessionSummary) any { return r.ToolMS }},
	{name: "tool_ms_estimated", api: []string{"tool_ms_estimated"}, desc: "true when a summed tool duration was estimated, not reported by the client", value: func(r *sessionSummary) any { return r.ToolMSEst }},
})}

var agentCols = colTable[agent]{schema: "Agent", cols: defaultOn([]column[agent]{
	{name: "agent_id", api: []string{"agent_id"}, desc: "agent id", value: func(r *agent) any { return r.AgentID }},
	{name: "parent_agent_id", api: []string{"parent_agent_id"}, desc: "parent agent id; empty for the main agent", value: func(r *agent) any { return r.ParentAgentID }},
	{name: "agent_type", api: []string{"agent_type"}, desc: "agent type", value: func(r *agent) any { return r.AgentType }},
	{name: "calls", api: []string{"calls"}, desc: "calls of the agent", value: func(r *agent) any { return r.Calls }},
})}

func promptUsage(r *prompt) usage { return r.Usage }

var promptCols = colTable[prompt]{schema: "Prompt", cols: join([]column[prompt]{
	{name: "prompt_id", api: []string{"prompt_id"}, desc: "prompt id", deflt: true, value: func(r *prompt) any { return r.PromptID }},
	{name: "first_ts", api: []string{"first_ts"}, unit: "RFC 3339 UTC", desc: "start of the first call of the prompt", deflt: true, value: func(r *prompt) any { return r.FirstTS }},
	{name: "calls", api: []string{"calls"}, desc: "calls of the prompt", deflt: true, value: func(r *prompt) any { return r.Calls }},
}, defaultOn(usageCols("usage", promptUsage, false))[:2], []column[prompt]{
	{name: "cost_usd", api: []string{"cost_micro_usd"}, unit: "USD", desc: costDesc, deflt: true, value: func(r *prompt) any { return costUSD(r.CostMicroUSD) }},
})}

func bucketUsage(r *usageBucket) usage { return r.Usage }

// bucketCost is the cost of a usage bucket: `unpriced` when every call of the
// bucket is unpriced, otherwise the sum of the known costs.
func bucketCost(b *usageBucket) any {
	if b.Calls > 0 && b.UnpricedCalls >= b.Calls {
		return unpriced
	}
	return usdFromMicro(b.CostMicroUSD)
}

// todayCols is the headline of the home view (schema UsageBucket, ungrouped).
var todayCols = colTable[usageBucket]{schema: "UsageBucket", cols: defaultOn(join([]column[usageBucket]{
	{name: "calls", api: []string{"calls"}, desc: "calls in the window", value: func(r *usageBucket) any { return r.Calls }},
	{name: "errors", api: []string{"errors"}, desc: "calls with an error class", value: func(r *usageBucket) any { return r.Errors }},
	{name: "error_rate", api: []string{"errors", "calls"}, unit: "ratio", desc: "errors over calls, rounded to four decimals", value: func(r *usageBucket) any { return ratio(r.Errors, r.Calls) }},
	{name: "cost_usd", api: []string{"cost_micro_usd", "unpriced_calls", "calls"}, unit: "USD",
		desc: "sum of the known call costs in USD, rounded to four decimals; `unpriced` when no call has a price", value: func(r *usageBucket) any { return bucketCost(r) }},
	{name: "unpriced_calls", api: []string{"unpriced_calls"}, desc: "calls with no known cost, left out of cost_usd", value: func(r *usageBucket) any { return r.UnpricedCalls }},
}, usageCols("usage", bucketUsage, false)[:2], []column[usageBucket]{
	{name: "cache_hit_ratio", api: []string{"cache_hit_ratio"}, unit: "ratio", desc: "cache_read over all input tokens; null when there are no input tokens",
		value: func(r *usageBucket) any {
			if r.CacheHitRatio == nil {
				return nil
			}
			return *r.CacheHitRatio
		}},
	{name: "latency_p95_ms", api: []string{"latency_p95_ms"}, unit: "ms", desc: "95th percentile of the call duration; 0 when no call has a duration", value: func(r *usageBucket) any { return r.LatencyP95MS }},
}))}

// topSessionCols is the session ranking of the home view (schema UsageBucket
// grouped by session).
var topSessionCols = colTable[usageBucket]{schema: "UsageBucket", cols: defaultOn([]column[usageBucket]{
	{name: "session_key", api: []string{"group"}, desc: "session key", value: func(r *usageBucket) any { return r.Group }},
	{name: "calls", api: []string{"calls"}, desc: "calls of the session in the window", value: func(r *usageBucket) any { return r.Calls }},
	{name: "cost_usd", api: []string{"cost_micro_usd", "unpriced_calls", "calls"}, unit: "USD", desc: "sum of the known call costs in USD; the ranking key, most expensive first", value: func(r *usageBucket) any { return bucketCost(r) }},
	{name: "unpriced_calls", api: []string{"unpriced_calls"}, desc: "calls of the session with no known cost, left out of cost_usd", value: func(r *usageBucket) any { return r.UnpricedCalls }},
})}

var providerCols = colTable[providerView]{schema: "ProviderView", cols: defaultOn([]column[providerView]{
	{name: "name", api: []string{"name"}, desc: "provider name", value: func(r *providerView) any { return r.Name }},
	{name: "kind", api: []string{"kind"}, desc: "provider kind", value: func(r *providerView) any { return r.Kind }},
	{name: "auth_mode", api: []string{"auth_mode"}, desc: "proxy_key, or client_passthrough (costs are API-equivalent)", value: func(r *providerView) any { return r.AuthMode }},
	{name: "base_url", api: []string{"base_url"}, desc: "upstream base URL", value: func(r *providerView) any { return r.BaseURL }},
})}

var pricingCols = colTable[pricingView]{schema: "PricingView", cols: defaultOn([]column[pricingView]{
	{name: "provider", api: []string{"provider"}, desc: "provider name", value: func(r *pricingView) any { return r.Provider }},
	{name: "model", api: []string{"model"}, desc: "model name", value: func(r *pricingView) any { return r.Model }},
	{name: "from", api: []string{"from"}, unit: "YYYY-MM-DD", desc: "first day the price applies", value: func(r *pricingView) any { return r.From }},
	{name: "input_usd_per_mtok", api: []string{"price.input"}, unit: "USD per million tokens", desc: "price of uncached input", value: func(r *pricingView) any { return perMTok(r.Price.Input) }},
	{name: "output_usd_per_mtok", api: []string{"price.output"}, unit: "USD per million tokens", desc: "price of output", value: func(r *pricingView) any { return perMTok(r.Price.Output) }},
	{name: "cache_read_usd_per_mtok", api: []string{"price.cache_read"}, unit: "USD per million tokens", desc: "price of cache reads", value: func(r *pricingView) any { return perMTok(r.Price.CacheRead) }},
	{name: "cache_write_5m_usd_per_mtok", api: []string{"price.cache_write_5m"}, unit: "USD per million tokens", desc: "price of 5 minute cache writes", value: func(r *pricingView) any { return perMTok(r.Price.CacheWrite5m) }},
	{name: "cache_write_1h_usd_per_mtok", api: []string{"price.cache_write_1h"}, unit: "USD per million tokens", desc: "price of 1 hour cache writes", value: func(r *pricingView) any { return perMTok(r.Price.CacheWrite1h) }},
})}

var keyCols = colTable[keyView]{schema: "KeyView", cols: defaultOn([]column[keyView]{
	{name: "name", api: []string{"name"}, desc: "virtual key name; the key itself and its hash are never shown", value: func(r *keyView) any { return r.Name }},
	{name: "disabled", api: []string{"disabled"}, desc: "the key is disabled", value: func(r *keyView) any { return r.Disabled }},
})}

// allTables lists every column table, for `describe` and the tests.
func allTables() []tableDoc {
	return []tableDoc{
		todayCols.doc("home.today"),
		topSessionCols.doc("home.top_sessions"),
		sessionCols.doc("sessions"),
		agentCols.doc("sessions.show.agents"),
		promptCols.doc("sessions.show.prompts"),
		callCols.doc("calls"),
		callDetailCols.doc("calls.show"),
		toolUseCols.doc("calls.show.tool_uses"),
		toolResultCols.doc("calls.show.tool_results"),
		providerCols.doc("config.providers"),
		pricingCols.doc("config.pricing"),
		keyCols.doc("config.keys"),
	}
}
