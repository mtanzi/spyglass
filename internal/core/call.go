package core

import (
	"encoding/json"
	"time"
)

// ToolSource says who ran a tool: the client, or the provider inside one response.
type ToolSource string

const (
	ToolSourceClient ToolSource = "client"
	ToolSourceServer ToolSource = "server"
)

// ToolUse is the "use" half of a tool call: the model asked for a tool in the
// response of the carrying call. Argument text is never kept, only its size.
type ToolUse struct {
	Source     ToolSource
	ToolCallID string // provider id, for example toolu_...
	Name       string
	ArgBytes   int
}

// ToolResult is the "result" half: the client returned a result in the request
// of the carrying call, or a server-side tool returned one in its response.
// Result text is never kept, only its size.
type ToolResult struct {
	Source      ToolSource
	ToolCallID  string // the id of the use it answers
	IsError     bool
	ResultBytes int
}

// CallRecord is the append-only record of one LLM call, with its tool halves.
// It holds facts about the exchange, never request or response bodies.
// Zero values mean "not known": empty strings, zero durations, nil Cost.
type CallRecord struct {
	ID ID // UUIDv7 assigned at request start; also x-request-id
	TS time.Time

	KeyName         string
	Client          string
	ClientRaw       string
	ClientRequestID string

	SessionKey       string
	AgentID          string
	ParentAgentID    string
	AgentType        string
	PromptID         string
	RequestClass     string
	Compaction       bool
	ContextCompacted bool
	TraceID          string

	// Project is the derived project name of the call (ADR 0059): the header
	// x-spyglass-project, else the project of the key, else the directory name
	// the extractor read. Never a path. Empty means no project.
	Project string

	Family            string
	Endpoint          string
	AuthMode          string // proxy_key or client_passthrough
	Provider          string
	Model             string // as the client asked for it
	UpstreamRequestID string

	Status     int
	ErrorClass string
	ErrorType  string
	StopReason string
	Streamed   bool

	TTFB     time.Duration
	TTFT     time.Duration
	Duration time.Duration
	EndedAt  time.Time

	Usage             Usage
	UsageRaw          json.RawMessage // provider usage object, kept for recomputation
	UsageEstimated    bool
	ExtractionPartial bool

	Cost           *MicroUSD // nil when no pricing matched; unknown is not zero
	PricingVersion string

	PrevToolDurations string // raw x-claude-code-prev-tool-durations value

	ToolUses    []ToolUse
	ToolResults []ToolResult
}
