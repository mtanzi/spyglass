package cli

import "encoding/json"

// The decode types of the admin API. Each holds only the fields the CLI uses,
// and decoding ignores the rest, so a server of another version still decodes.
// api/openapi.yaml is the contract: the tests validate every fixture against
// it and check that every json tag below names a field of its schema.

type usage struct {
	InputUncached int64 `json:"input_uncached"`
	CacheRead     int64 `json:"cache_read"`
	CacheWrite5m  int64 `json:"cache_write_5m"`
	CacheWrite1h  int64 `json:"cache_write_1h"`
	Output        int64 `json:"output"`
	Reasoning     int64 `json:"reasoning"`
}

func (u usage) in() int64         { return u.InputUncached + u.CacheRead + u.CacheWrite5m + u.CacheWrite1h }
func (u usage) cacheWrite() int64 { return u.CacheWrite5m + u.CacheWrite1h }

type callSummary struct {
	ID               string `json:"id"`
	TS               string `json:"ts"`
	Key              string `json:"key"`
	Client           string `json:"client"`
	SessionKey       string `json:"session_key"`
	AgentID          string `json:"agent_id"`
	PromptID         string `json:"prompt_id"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Status           int    `json:"status"`
	ErrorClass       string `json:"error_class"`
	StopReason       string `json:"stop_reason"`
	Streamed         bool   `json:"streamed"`
	TTFTMS           int64  `json:"ttft_ms"`
	DurationMS       int64  `json:"duration_ms"`
	Usage            usage  `json:"usage"`
	CostMicroUSD     *int64 `json:"cost_micro_usd"`
	ToolCount        int    `json:"tool_count"`
	ContextCompacted bool   `json:"context_compacted"`
}

// callRow is a call of a list with what the CLI adds from /admin/config.
type callRow struct {
	callSummary
	apiEquivalent bool
}

type callList struct {
	Calls       []callSummary `json:"calls"`
	NextCursor  *string       `json:"next_cursor"`
	Count       int64         `json:"count"`
	CountCapped bool          `json:"count_capped"`
}

type toolUse struct {
	Source     string `json:"source"`
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	ArgBytes   int64  `json:"arg_bytes"`
}

type toolResult struct {
	Source      string `json:"source"`
	ToolCallID  string `json:"tool_call_id"`
	IsError     bool   `json:"is_error"`
	ResultBytes int64  `json:"result_bytes"`
}

type callDetail struct {
	ID                string          `json:"id"`
	TS                string          `json:"ts"`
	Key               string          `json:"key"`
	Client            string          `json:"client"`
	ClientRaw         string          `json:"client_raw"`
	ClientRequestID   string          `json:"client_request_id"`
	SessionKey        string          `json:"session_key"`
	AgentID           string          `json:"agent_id"`
	ParentAgentID     string          `json:"parent_agent_id"`
	AgentType         string          `json:"agent_type"`
	PromptID          string          `json:"prompt_id"`
	RequestClass      string          `json:"request_class"`
	Compaction        bool            `json:"compaction"`
	ContextCompacted  bool            `json:"context_compacted"`
	TraceID           string          `json:"trace_id"`
	Family            string          `json:"family"`
	Endpoint          string          `json:"endpoint"`
	AuthMode          string          `json:"auth_mode"`
	Provider          string          `json:"provider"`
	Model             string          `json:"model"`
	UpstreamRequestID string          `json:"upstream_request_id"`
	Status            int             `json:"status"`
	ErrorClass        string          `json:"error_class"`
	ErrorType         string          `json:"error_type"`
	StopReason        string          `json:"stop_reason"`
	Streamed          bool            `json:"streamed"`
	TTFBMS            int64           `json:"ttfb_ms"`
	TTFTMS            int64           `json:"ttft_ms"`
	DurationMS        int64           `json:"duration_ms"`
	EndedAt           *string         `json:"ended_at"`
	Usage             usage           `json:"usage"`
	UsageRaw          json.RawMessage `json:"usage_raw"`
	UsageEstimated    bool            `json:"usage_estimated"`
	ExtractionPartial bool            `json:"extraction_partial"`
	CostMicroUSD      *int64          `json:"cost_micro_usd"`
	PricingVersion    string          `json:"pricing_version"`
	PrevToolDurations string          `json:"prev_tool_durations"`
	ToolUses          []toolUse       `json:"tool_uses"`
	ToolResults       []toolResult    `json:"tool_results"`
}

type sessionSummary struct {
	SessionKey    string   `json:"session_key"`
	Key           string   `json:"key"`
	Client        string   `json:"client"`
	FirstTS       string   `json:"first_ts"`
	LastTS        string   `json:"last_ts"`
	EndedAt       string   `json:"ended_at"`
	Calls         int64    `json:"calls"`
	Prompts       int64    `json:"prompts"`
	ToolCalls     int64    `json:"tool_calls"`
	Errors        int64    `json:"errors"`
	AgentCount    int64    `json:"agent_count"`
	Models        []string `json:"models"`
	Usage         usage    `json:"usage"`
	CostMicroUSD  *int64   `json:"cost_micro_usd"`
	UnpricedCalls int64    `json:"unpriced_calls"`
	LLMMS         int64    `json:"llm_ms"`
	ToolMS        int64    `json:"tool_ms"`
	ToolMSEst     bool     `json:"tool_ms_estimated"`
}

type sessionList struct {
	Sessions   []sessionSummary `json:"sessions"`
	NextCursor *string          `json:"next_cursor"`
}

type agent struct {
	AgentID       string `json:"agent_id"`
	ParentAgentID string `json:"parent_agent_id"`
	AgentType     string `json:"agent_type"`
	Calls         int64  `json:"calls"`
}

type prompt struct {
	PromptID     string `json:"prompt_id"`
	FirstTS      string `json:"first_ts"`
	Calls        int64  `json:"calls"`
	Usage        usage  `json:"usage"`
	CostMicroUSD *int64 `json:"cost_micro_usd"`
}

type sessionDetail struct {
	Summary sessionSummary `json:"summary"`
	Agents  []agent        `json:"agents"`
	Prompts []prompt       `json:"prompts"`
	Calls   []callSummary  `json:"calls"`
}

type usageBucket struct {
	Start         string   `json:"start"`
	Group         string   `json:"group"`
	Calls         int64    `json:"calls"`
	Errors        int64    `json:"errors"`
	Usage         usage    `json:"usage"`
	CostMicroUSD  int64    `json:"cost_micro_usd"`
	UnpricedCalls int64    `json:"unpriced_calls"`
	LatencyP95MS  int64    `json:"latency_p95_ms"`
	CacheHitRatio *float64 `json:"cache_hit_ratio"`
}

type usageSeries struct {
	Buckets []usageBucket `json:"buckets"`
}

type configView struct {
	Providers []providerView `json:"providers"`
	Pricing   []pricingView  `json:"pricing"`
	Keys      []keyView      `json:"keys"`
}

type providerView struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"base_url"`
	AuthMode string `json:"auth_mode"`
}

type pricingView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	From     string `json:"from"`
	Price    price  `json:"price"`
}

type price struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
}

type keyView struct {
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
}

type errorResponse struct {
	Error struct {
		Type      string `json:"type"`
		Message   string `json:"message"`
		Parameter string `json:"parameter"`
	} `json:"error"`
}
