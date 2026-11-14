package api

import (
	"encoding/json"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// The wire types below are the JSON of api/openapi.yaml. Durations are whole
// milliseconds, money is micro-USD, and an unknown cost is null.

type usageJSON struct {
	InputUncached int `json:"input_uncached"`
	CacheRead     int `json:"cache_read"`
	CacheWrite5m  int `json:"cache_write_5m"`
	CacheWrite1h  int `json:"cache_write_1h"`
	Output        int `json:"output"`
	Reasoning     int `json:"reasoning"`
}

func toUsage(u core.Usage) usageJSON {
	return usageJSON{u.InputUncached, u.CacheRead, u.CacheWrite5m, u.CacheWrite1h, u.Output, u.Reasoning}
}

type callSummaryJSON struct {
	ID               string         `json:"id"`
	TS               time.Time      `json:"ts"`
	Key              string         `json:"key"`
	Client           string         `json:"client"`
	SessionKey       string         `json:"session_key"`
	AgentID          string         `json:"agent_id"`
	PromptID         string         `json:"prompt_id"`
	Provider         string         `json:"provider"`
	Model            string         `json:"model"`
	Status           int            `json:"status"`
	ErrorClass       string         `json:"error_class"`
	StopReason       string         `json:"stop_reason"`
	Streamed         bool           `json:"streamed"`
	TTFTMs           int64          `json:"ttft_ms"`
	DurationMs       int64          `json:"duration_ms"`
	Usage            usageJSON      `json:"usage"`
	CostMicroUSD     *core.MicroUSD `json:"cost_micro_usd"`
	ToolCount        int            `json:"tool_count"`
	ContextCompacted bool           `json:"context_compacted"`
}

func toCallSummary(c core.CallSummary) callSummaryJSON {
	return callSummaryJSON{
		ID: c.ID.String(), TS: c.TS.UTC(), Key: c.KeyName, Client: c.Client, SessionKey: c.SessionKey,
		AgentID: c.AgentID, PromptID: c.PromptID, Provider: c.Provider, Model: c.Model, Status: c.Status, ErrorClass: c.ErrorClass,
		StopReason: c.StopReason, Streamed: c.Streamed, TTFTMs: c.TTFT.Milliseconds(), DurationMs: c.Duration.Milliseconds(),
		Usage: toUsage(c.Usage), CostMicroUSD: c.Cost, ToolCount: c.ToolCount, ContextCompacted: c.ContextCompacted,
	}
}

type callListJSON struct {
	Calls       []callSummaryJSON `json:"calls"`
	NextCursor  *string           `json:"next_cursor"`
	Count       int               `json:"count"`
	CountCapped bool              `json:"count_capped"`
}

type toolUseJSON struct {
	Source     core.ToolSource `json:"source"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	ArgBytes   int             `json:"arg_bytes"`
}

type toolResultJSON struct {
	Source      core.ToolSource `json:"source"`
	ToolCallID  string          `json:"tool_call_id"`
	IsError     bool            `json:"is_error"`
	ResultBytes int             `json:"result_bytes"`
}

type callDetailJSON struct {
	ID                string           `json:"id"`
	TS                time.Time        `json:"ts"`
	Key               string           `json:"key"`
	Client            string           `json:"client"`
	ClientRaw         string           `json:"client_raw"`
	ClientRequestID   string           `json:"client_request_id"`
	SessionKey        string           `json:"session_key"`
	AgentID           string           `json:"agent_id"`
	ParentAgentID     string           `json:"parent_agent_id"`
	AgentType         string           `json:"agent_type"`
	PromptID          string           `json:"prompt_id"`
	RequestClass      string           `json:"request_class"`
	Compaction        bool             `json:"compaction"`
	ContextCompacted  bool             `json:"context_compacted"`
	TraceID           string           `json:"trace_id"`
	Family            string           `json:"family"`
	Endpoint          string           `json:"endpoint"`
	AuthMode          string           `json:"auth_mode"`
	Provider          string           `json:"provider"`
	Model             string           `json:"model"`
	UpstreamRequestID string           `json:"upstream_request_id"`
	Status            int              `json:"status"`
	ErrorClass        string           `json:"error_class"`
	ErrorType         string           `json:"error_type"`
	StopReason        string           `json:"stop_reason"`
	Streamed          bool             `json:"streamed"`
	TTFBMs            int64            `json:"ttfb_ms"`
	TTFTMs            int64            `json:"ttft_ms"`
	DurationMs        int64            `json:"duration_ms"`
	EndedAt           *time.Time       `json:"ended_at"`
	Usage             usageJSON        `json:"usage"`
	UsageRaw          json.RawMessage  `json:"usage_raw"`
	UsageEstimated    bool             `json:"usage_estimated"`
	ExtractionPartial bool             `json:"extraction_partial"`
	CostMicroUSD      *core.MicroUSD   `json:"cost_micro_usd"`
	PricingVersion    string           `json:"pricing_version"`
	PrevToolDurations string           `json:"prev_tool_durations"`
	ToolUses          []toolUseJSON    `json:"tool_uses"`
	ToolResults       []toolResultJSON `json:"tool_results"`
}

func toCallDetail(d core.CallDetail) callDetailJSON {
	out := callDetailJSON{
		ID: d.ID.String(), TS: d.TS.UTC(), Key: d.KeyName, Client: d.Client, ClientRaw: d.ClientRaw,
		ClientRequestID: d.ClientRequestID, SessionKey: d.SessionKey, AgentID: d.AgentID,
		ParentAgentID: d.ParentAgentID, AgentType: d.AgentType, PromptID: d.PromptID,
		RequestClass: d.RequestClass, Compaction: d.Compaction, ContextCompacted: d.ContextCompacted,
		TraceID: d.TraceID, Family: d.Family, Endpoint: d.Endpoint, AuthMode: d.AuthMode,
		Provider: d.Provider, Model: d.Model, UpstreamRequestID: d.UpstreamRequestID,
		Status: d.Status, ErrorClass: d.ErrorClass, ErrorType: d.ErrorType, StopReason: d.StopReason,
		Streamed: d.Streamed, TTFBMs: d.TTFB.Milliseconds(), TTFTMs: d.TTFT.Milliseconds(),
		DurationMs: d.Duration.Milliseconds(), Usage: toUsage(d.Usage), UsageRaw: usageRaw(d.UsageRaw),
		UsageEstimated: d.UsageEstimated, ExtractionPartial: d.ExtractionPartial,
		CostMicroUSD: d.Cost, PricingVersion: d.PricingVersion, PrevToolDurations: d.PrevToolDurations,
		ToolUses:    make([]toolUseJSON, 0, len(d.ToolUses)),
		ToolResults: make([]toolResultJSON, 0, len(d.ToolResults)),
	}
	if !d.EndedAt.IsZero() {
		t := d.EndedAt.UTC()
		out.EndedAt = &t
	}
	for _, u := range d.ToolUses {
		out.ToolUses = append(out.ToolUses, toolUseJSON{u.Source, u.ToolCallID, u.Name, u.ArgBytes})
	}
	for _, r := range d.ToolResults {
		out.ToolResults = append(out.ToolResults, toolResultJSON{r.Source, r.ToolCallID, r.IsError, r.ResultBytes})
	}
	return out
}

// usageRaw returns the provider usage object, or null when none was kept or the
// stored text is not a JSON object.
func usageRaw(raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil || m == nil {
		return json.RawMessage("null")
	}
	return raw
}

type sessionSummaryJSON struct {
	SessionKey    string         `json:"session_key"`
	Key           string         `json:"key"`
	Client        string         `json:"client"`
	FirstTS       time.Time      `json:"first_ts"`
	LastTS        time.Time      `json:"last_ts"`
	EndedAt       time.Time      `json:"ended_at"`
	Calls         int            `json:"calls"`
	Prompts       int            `json:"prompts"`
	ToolCalls     int            `json:"tool_calls"`
	Errors        int            `json:"errors"`
	AgentCount    int            `json:"agent_count"`
	Models        []string       `json:"models"`
	Usage         usageJSON      `json:"usage"`
	CostMicroUSD  *core.MicroUSD `json:"cost_micro_usd"`
	UnpricedCalls int            `json:"unpriced_calls"`
}

func toSessionSummary(s core.SessionSummary) sessionSummaryJSON {
	models := s.Models
	if models == nil {
		models = []string{}
	}
	return sessionSummaryJSON{
		SessionKey: s.SessionKey, Key: s.KeyName, Client: s.Client, FirstTS: s.FirstTS.UTC(), LastTS: s.LastTS.UTC(),
		EndedAt: s.EndedAt.UTC(), Calls: s.Calls, Prompts: s.PromptCount, ToolCalls: s.ToolCallCount, Errors: s.Errors, AgentCount: s.AgentCount, Models: models, Usage: toUsage(s.Usage), CostMicroUSD: s.Cost,
		UnpricedCalls: s.UnpricedCalls,
	}
}

type sessionListJSON struct {
	Sessions   []sessionSummaryJSON `json:"sessions"`
	NextCursor *string              `json:"next_cursor"`
}

type agentJSON struct {
	AgentID       string `json:"agent_id"`
	ParentAgentID string `json:"parent_agent_id"`
	AgentType     string `json:"agent_type"`
	Calls         int    `json:"calls"`
}

type promptJSON struct {
	PromptID     string         `json:"prompt_id"`
	FirstTS      time.Time      `json:"first_ts"`
	Calls        int            `json:"calls"`
	Usage        usageJSON      `json:"usage"`
	CostMicroUSD *core.MicroUSD `json:"cost_micro_usd"`
}

// sessionDetailJSON nests the summary: its calls count and the calls list
// would otherwise share one name.
type sessionDetailJSON struct {
	Summary sessionSummaryJSON `json:"summary"`
	Agents  []agentJSON        `json:"agents"`
	Prompts []promptJSON       `json:"prompts"`
	Calls   []callSummaryJSON  `json:"calls"`
}

func toSessionDetail(d core.SessionDetail) sessionDetailJSON {
	out := sessionDetailJSON{
		Summary: toSessionSummary(d.SessionSummary),
		Agents:  make([]agentJSON, 0, len(d.Agents)),
		Prompts: make([]promptJSON, 0, len(d.Prompts)),
		Calls:   make([]callSummaryJSON, 0, len(d.Calls)),
	}
	for _, a := range d.Agents {
		out.Agents = append(out.Agents, agentJSON{a.AgentID, a.ParentAgentID, a.AgentType, a.Calls})
	}
	for _, p := range d.Prompts {
		out.Prompts = append(out.Prompts, promptJSON{p.PromptID, p.FirstTS.UTC(), p.Calls, toUsage(p.Usage), p.Cost})
	}
	for _, c := range d.Calls {
		out.Calls = append(out.Calls, toCallSummary(c))
	}
	return out
}

// toolCallJSON is one tool call. A half that is missing reads as null, and a
// duration with no source (an unmatched half) reads as null with it.
type toolCallJSON struct {
	ToolCallID     string               `json:"tool_call_id"`
	Name           string               `json:"name"`
	Source         core.ToolSource      `json:"source"`
	Status         core.ToolCallStatus  `json:"status"`
	SessionKey     string               `json:"session_key"`
	AgentID        string               `json:"agent_id"`
	UseCallID      *string              `json:"use_call_id"`
	ResultCallID   *string              `json:"result_call_id"`
	UseTS          *time.Time           `json:"use_ts"`
	ResultTS       *time.Time           `json:"result_ts"`
	DurationMs     *int64               `json:"duration_ms"`
	DurationSource *core.DurationSource `json:"duration_source"`
	IsError        *bool                `json:"is_error"`
	ArgBytes       int                  `json:"arg_bytes"`
	ResultBytes    int                  `json:"result_bytes"`
}

func toToolCall(t core.ToolCall) toolCallJSON {
	out := toolCallJSON{
		ToolCallID: t.ToolCallID, Name: t.Name, Source: t.Source, Status: t.Status, SessionKey: t.SessionKey,
		AgentID: t.AgentID, IsError: t.IsError, ArgBytes: t.ArgBytes, ResultBytes: t.ResultBytes,
	}
	if !t.UseCallID.IsZero() {
		s := t.UseCallID.String()
		out.UseCallID = &s
	}
	if !t.ResultCallID.IsZero() {
		s := t.ResultCallID.String()
		out.ResultCallID = &s
	}
	if !t.UseTS.IsZero() {
		ts := t.UseTS.UTC()
		out.UseTS = &ts
	}
	if !t.ResultTS.IsZero() {
		ts := t.ResultTS.UTC()
		out.ResultTS = &ts
	}
	if t.DurationSource != "" {
		ms, src := t.Duration.Milliseconds(), t.DurationSource
		out.DurationMs, out.DurationSource = &ms, &src
	}
	return out
}

type toolCallListJSON struct {
	ToolCalls  []toolCallJSON `json:"tool_calls"`
	NextCursor *string        `json:"next_cursor"`
}

type toolStatJSON struct {
	Name            string  `json:"name"`
	AgentType       string  `json:"agent_type"`
	Count           int     `json:"count"`
	Errors          int     `json:"errors"`
	ErrorRate       float64 `json:"error_rate"`
	DurationP50Ms   int64   `json:"duration_p50_ms"`
	DurationP95Ms   int64   `json:"duration_p95_ms"`
	TotalDurationMs int64   `json:"total_duration_ms"`
}

type toolSummaryJSON struct {
	Tools []toolStatJSON `json:"tools"`
}

type usageBucketJSON struct {
	Start          time.Time `json:"start"`
	Group          string    `json:"group"`
	Calls          int       `json:"calls"`
	Errors         int       `json:"errors"`
	Usage          usageJSON `json:"usage"`
	CostMicroUSD   int64     `json:"cost_micro_usd"`
	UnpricedCalls  int       `json:"unpriced_calls"`
	EstimatedCalls int       `json:"estimated_calls"`
	NoUsageCalls   int       `json:"no_usage_calls"`
	LatencyP50Ms   int64     `json:"latency_p50_ms"`
	LatencyP95Ms   int64     `json:"latency_p95_ms"`
	LatencyP99Ms   int64     `json:"latency_p99_ms"`
	CacheHitRatio  *float64  `json:"cache_hit_ratio"`
}

type promptCostJSON struct {
	PromptID      string `json:"prompt_id"`
	SessionKey    string `json:"session_key"`
	Calls         int    `json:"calls"`
	CostMicroUSD  *int64 `json:"cost_micro_usd"`
	UnpricedCalls int    `json:"unpriced_calls"`
	APIEquivalent bool   `json:"api_equivalent"`
}

type promptCostsJSON struct {
	Prompts            []promptCostJSON `json:"prompts"`
	PromptCount        int              `json:"prompt_count"`
	CallsWithoutPrompt int              `json:"calls_without_prompt"`
}

type usageSeriesJSON struct {
	GroupBy *core.UsageGroup  `json:"group_by"`
	Grain   core.Grain        `json:"grain"`
	Buckets []usageBucketJSON `json:"buckets"`
}
