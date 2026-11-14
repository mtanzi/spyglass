package core

import "time"

// DurationSource says where a tool call duration came from.
type DurationSource string

const (
	DurationHeader    DurationSource = "header"    // prev_tool_durations on the resolving call
	DurationEstimated DurationSource = "estimated" // gap between the use and the result
)

// The two constants of tool-call pairing. They are fixed, not settings (ADR 0048).
const (
	// ToolIdleAfter is how long a use may wait for its result. A use with no
	// result that is older than this reads as use_only; a younger one is pending.
	ToolIdleAfter = time.Hour

	// ToolResultLookahead is how far past the To bound of a query the lookup
	// for a result reaches, so a use near the end of the range still finds the
	// result that arrives in a later request.
	ToolResultLookahead = 24 * time.Hour
)

// CallSummary is one row of the call log.
type CallSummary struct {
	ID         ID
	TS         time.Time
	KeyName    string
	Client     string
	SessionKey string
	AgentID    string
	PromptID   string
	Provider   string
	Model      string
	Status     int
	ErrorClass string
	StopReason string
	Streamed   bool
	TTFT       time.Duration
	Duration   time.Duration
	Usage      Usage
	Cost       *MicroUSD
	ToolCount  int // tool uses carried by the call
	// ContextCompacted marks the call where the cache prefix resets (the UI compaction marker).
	ContextCompacted bool
}

// CallDetail is one call with all its recorded metadata and its tool halves.
type CallDetail struct {
	CallRecord
}

// SessionSummary is one row of the session list, grouped from call rows.
type SessionSummary struct {
	SessionKey    string
	KeyName       string
	Client        string
	FirstTS       time.Time
	LastTS        time.Time
	EndedAt       time.Time // latest end of a call; the start of a call whose end is not known
	Calls         int
	PromptCount   int // distinct prompt ids
	ToolCallCount int // distinct tool call ids carried by the calls
	Errors        int
	AgentCount    int
	Models        []string
	Usage         Usage
	Cost          *MicroUSD // nil when no call in the session has a known cost
	UnpricedCalls int       // calls whose cost is nil
}

// AgentNode is one distinct (agent_id, parent_agent_id, agent_type) of a session.
type AgentNode struct {
	AgentID       string
	ParentAgentID string
	AgentType     string
	Calls         int
}

// PromptSummary groups the calls of one prompt id inside a session.
type PromptSummary struct {
	PromptID string
	FirstTS  time.Time
	Calls    int
	Usage    Usage
	Cost     *MicroUSD
}

// SessionDetail is a session with its agent tree, prompts and calls (metadata only).
type SessionDetail struct {
	SessionSummary
	Agents  []AgentNode
	Prompts []PromptSummary
	Calls   []CallSummary
}

// ToolCall is a tool use paired with its result at read time. Either half can be missing.
type ToolCall struct {
	ToolCallID     string
	Name           string // from the use; empty for result_only
	Source         ToolSource
	Status         ToolCallStatus
	SessionKey     string
	AgentID        string
	UseCallID      ID
	ResultCallID   ID
	UseTS          time.Time
	ResultTS       time.Time
	Duration       time.Duration
	DurationSource DurationSource
	IsError        *bool // nil without a result
	ArgBytes       int
	ResultBytes    int
}

// ToolStat is the statistics of one tool for one agent type over a filter.
// ToolSummary returns one row per (Name, AgentType). Count covers every tool
// call that matches, including unmatched halves; Errors counts results with
// is_error. The durations cover the tool calls that have one.
type ToolStat struct {
	Name          string
	AgentType     string // empty when the calling agent has no type
	Count         int
	Errors        int
	DurationP50   time.Duration
	DurationP95   time.Duration
	TotalDuration time.Duration
}

// ErrorRate is Errors over Count, or zero for an empty row.
func (s ToolStat) ErrorRate() float64 {
	if s.Count == 0 {
		return 0
	}
	return float64(s.Errors) / float64(s.Count)
}

// UsageBucket is one point of the usage time series. A bucket exists only
// when at least one call falls in it: the reader does not fill gaps.
//
// Unknown values are not zero. Usage and Cost sum what is known, and the
// counters below say how many calls of the bucket are not fully known, so a
// consumer can show the totals as partial.
type UsageBucket struct {
	Start  time.Time // UTC, aligned to the grain
	Group  string    // value of the group-by dimension; empty when ungrouped or not known
	Calls  int
	Errors int // calls with an error class
	Usage  Usage
	Cost   MicroUSD // sum over calls with a known cost

	UnpricedCalls  int // calls with no cost, left out of Cost
	EstimatedCalls int // calls whose usage was estimated, for example after a client abort
	NoUsageCalls   int // calls with no provider usage object; their token columns are zero

	LatencyP50 time.Duration // zero when no call of the bucket has a duration
	LatencyP95 time.Duration
	LatencyP99 time.Duration
}

// PromptCost is the cost of the calls that share one prompt id. Unknown is not
// zero: Cost sums the priced calls only and is nil when no call is priced, and
// UnpricedCalls counts the calls it leaves out.
type PromptCost struct {
	PromptID      string
	SessionKey    string // of the first call of the prompt; empty when it has none
	Calls         int
	Cost          *MicroUSD
	UnpricedCalls int
	// APIEquivalent is true when a priced call went through a
	// client_passthrough provider: part of the cost is what the API would
	// bill, not what the person pays.
	APIEquivalent bool
}

// PromptCosts is the answer to a PromptCostQuery. Prompts are the Limit most
// costly ones: known cost first, highest first, then unknown cost, ties by
// calls (most first) and prompt id. PromptCount is every prompt of the
// matching calls, so a reader can say how many the list leaves out.
// CallsWithoutPrompt counts the matching calls that carry no prompt id.
type PromptCosts struct {
	Prompts            []PromptCost
	PromptCount        int
	CallsWithoutPrompt int
}

// CacheHitRatio is cache_read over all input columns: input_uncached,
// cache_read, cache_write_5m and cache_write_1h. ok is false when the bucket
// has no input tokens, so the ratio is not known.
func (b UsageBucket) CacheHitRatio() (ratio float64, ok bool) {
	u := b.Usage
	in := u.InputUncached + u.CacheRead + u.CacheWrite5m + u.CacheWrite1h
	if in <= 0 {
		return 0, false
	}
	return float64(u.CacheRead) / float64(in), true
}
