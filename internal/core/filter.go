package core

import (
	"errors"
	"slices"
	"time"
)

// ErrInvalidFilter is returned by Validate for a filter an adapter must refuse.
var ErrInvalidFilter = errors.New("core: invalid filter")

// CallFilter mirrors the call-log query parameters. Zero values mean "no constraint".
// A slice field matches when the call has any of the listed values.
// Every stored query is time-bounded: From and To are required (the API defaults
// to the last 24 hours). Match skips a zero bound so the live tail can reuse it.
type CallFilter struct {
	From, To       time.Time // half-open [From, To)
	Keys           []string  // key names
	Clients        []string
	Providers      []string
	Models         []string
	Endpoints      []string
	Streamed       *bool
	Statuses       []int
	ErrorClasses   []string
	SessionKey     string
	AgentID        string
	AgentTypes     []string
	PromptID       string
	RequestClasses []string
	Tools          []string // calls that made a tool call with one of these names
	ToolError      *bool    // calls that made a tool call whose paired result has this is_error
	MinLatency     time.Duration
	MinCost        MicroUSD // calls with a known cost at or above this
}

// Validate reports whether the time range is explicit and non-empty.
func (f CallFilter) Validate() error { return validateRange(f.From, f.To) }

// Match reports whether one call satisfies the filter. The live tail uses it, and
// the contract suite uses it as the reference for adapter filters.
//
// Tools and ToolError are matched on the tool calls the call made: tools holds
// the read-time pairs, and only those whose UseCallID is c.ID count. Both must
// hold on the same tool call, so Tools=[Bash] with ToolError=true means a Bash
// use of this call whose result is an error. The results c carries answer an
// earlier call and never count.
func (f CallFilter) Match(c CallRecord, tools []ToolCall) bool {
	if !f.From.IsZero() && c.TS.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && !c.TS.Before(f.To) {
		return false
	}
	switch {
	case !in(f.Keys, c.KeyName),
		!in(f.Clients, c.Client),
		!in(f.Providers, c.Provider),
		!in(f.Models, c.Model),
		!in(f.Endpoints, c.Endpoint),
		!in(f.Statuses, c.Status),
		!in(f.ErrorClasses, c.ErrorClass),
		!in(f.AgentTypes, c.AgentType),
		!in(f.RequestClasses, c.RequestClass):
		return false
	case f.SessionKey != "" && f.SessionKey != c.SessionKey,
		f.AgentID != "" && f.AgentID != c.AgentID,
		f.PromptID != "" && f.PromptID != c.PromptID:
		return false
	}
	if f.Streamed != nil && *f.Streamed != c.Streamed {
		return false
	}
	if len(f.Tools) > 0 || f.ToolError != nil {
		tf := ToolCallFilter{Tools: f.Tools, IsError: f.ToolError}
		if !slices.ContainsFunc(tools, func(t ToolCall) bool { return t.UseCallID == c.ID && tf.Match(t) }) {
			return false
		}
	}
	if f.MinLatency > 0 && c.Duration < f.MinLatency {
		return false
	}
	if f.MinCost > 0 && (c.Cost == nil || *c.Cost < f.MinCost) {
		return false
	}
	return true
}

// SessionFilter selects sessions: groups of calls by session key within the time range.
// A session matches when it has at least one call that satisfies the fields.
type SessionFilter struct {
	From, To     time.Time // half-open [From, To)
	Keys         []string
	Clients      []string
	Providers    []string
	Models       []string
	ErrorClasses []string
	SessionKey   string
	MinCost      MicroUSD // session total cost
}

// Validate reports whether the time range is explicit and non-empty.
func (f SessionFilter) Validate() error { return validateRange(f.From, f.To) }

// ToolCallStatus is the pairing state of a tool call, derived when read.
type ToolCallStatus string

const (
	ToolCallPaired     ToolCallStatus = "paired"
	ToolCallUseOnly    ToolCallStatus = "use_only"    // a use with no result, older than the idle threshold
	ToolCallResultOnly ToolCallStatus = "result_only" // a result with no use
	ToolCallPending    ToolCallStatus = "pending"     // a use with no result, within the idle threshold
)

// ToolCallFilter selects tool calls, which are a use joined to its result.
type ToolCallFilter struct {
	From, To    time.Time // half-open [From, To), on the use (or the result, for result_only)
	SessionKey  string
	AgentID     string
	Tools       []string
	Sources     []ToolSource
	Statuses    []ToolCallStatus
	IsError     *bool
	MinDuration time.Duration
	// MinArgBytes and MinResultBytes select tool calls whose argument or
	// result is at least that large. A tool call without a result has no
	// result size (zero) and never matches MinResultBytes.
	MinArgBytes    int
	MinResultBytes int
}

// Validate reports whether the time range is explicit and non-empty.
func (f ToolCallFilter) Validate() error { return validateRange(f.From, f.To) }

// Match reports whether one tool call satisfies the filter. It is the reference
// for adapter tool-call filters. IsError never matches a tool call without a
// result. Match skips a zero time bound, as CallFilter.Match does.
func (f ToolCallFilter) Match(t ToolCall) bool {
	ts := t.UseTS
	if t.Status == ToolCallResultOnly {
		ts = t.ResultTS
	}
	if !f.From.IsZero() && ts.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && !ts.Before(f.To) {
		return false
	}
	switch {
	case !in(f.Tools, t.Name),
		!in(f.Sources, t.Source),
		!in(f.Statuses, t.Status):
		return false
	case f.SessionKey != "" && f.SessionKey != t.SessionKey,
		f.AgentID != "" && f.AgentID != t.AgentID:
		return false
	case f.IsError != nil && (t.IsError == nil || *t.IsError != *f.IsError):
		return false
	case f.MinDuration > 0 && t.Duration < f.MinDuration:
		return false
	case f.MinArgBytes > 0 && t.ArgBytes < f.MinArgBytes:
		return false
	case f.MinResultBytes > 0 && t.ResultBytes < f.MinResultBytes:
		return false
	}
	return true
}

// UsageGroup is the dimension a usage query groups by.
type UsageGroup string

const (
	GroupNone       UsageGroup = ""
	GroupKey        UsageGroup = "key"
	GroupModel      UsageGroup = "model"
	GroupClient     UsageGroup = "client"
	GroupProvider   UsageGroup = "provider"
	GroupSession    UsageGroup = "session"
	GroupErrorClass UsageGroup = "error_class"
)

// Grain is the width of one usage bucket. Buckets are aligned in UTC.
type Grain string

const (
	GrainMinute Grain = "minute"
	GrainHour   Grain = "hour"
	GrainDay    Grain = "day"
)

// UsageQuery asks for tokens, cost and latency percentiles over time. The
// filter carries the time range and any call constraints.
type UsageQuery struct {
	Filter  CallFilter
	GroupBy UsageGroup
	Grain   Grain
}

// Validate checks the time range, and that GroupBy and Grain are known values.
func (q UsageQuery) Validate() error {
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	switch q.GroupBy {
	case GroupNone, GroupKey, GroupModel, GroupClient, GroupProvider, GroupSession, GroupErrorClass:
	default:
		return ErrInvalidFilter
	}
	switch q.Grain {
	case GrainMinute, GrainHour, GrainDay:
	default:
		return ErrInvalidFilter
	}
	return nil
}

// PromptCostQuery asks for the cost of each prompt over the calls matching the
// filter. Limit bounds the prompts returned, highest cost first; the totals in
// the result still cover every matching call.
type PromptCostQuery struct {
	Filter CallFilter
	Limit  int
}

// Validate checks the time range and that Limit is at least one.
func (q PromptCostQuery) Validate() error {
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	if q.Limit < 1 {
		return ErrInvalidFilter
	}
	return nil
}

// Page is keyset pagination on (ts, id), newest first, forward only.
type Page struct {
	After *Cursor // nil for the first page; items strictly older than the cursor
	Limit int     // the adapter clamps it to a maximum
}

// Cursor is the position of the last item of a page. The api package encodes it on the wire.
// Idx breaks ties for lists whose items can share (TS, ID): tool calls, where
// one call carries several tool halves. It is zero for the other lists.
type Cursor struct {
	TS  time.Time
	ID  ID
	Idx int
}

func validateRange(from, to time.Time) error {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return ErrInvalidFilter
	}
	return nil
}

// in reports whether v is allowed by set; an empty set allows everything.
func in[T comparable](set []T, v T) bool {
	return len(set) == 0 || slices.Contains(set, v)
}
