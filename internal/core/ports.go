package core

import (
	"context"
	"errors"
)

// ErrNotFound is returned by CallReader when a call or session does not exist.
var ErrNotFound = errors.New("core: not found")

// ErrUnwritable is matched by a CallWriter error caused by the content of the
// records, not by the state of the store or the connection.
var ErrUnwritable = errors.New("core: unwritable record")

// CallWriter receives finished calls from the recorder. It inserts only.
type CallWriter interface {
	// WriteBatch inserts the calls with their tool halves.
	// Idempotent on CallRecord.ID, atomic per batch. Returns after commit.
	// A returned error that matches errors.Is(err, ErrUnwritable) means the
	// batch holds at least one record the store will never accept. Any other
	// error is transient: retry the same batch.
	WriteBatch(ctx context.Context, calls []CallRecord) error
}

// CallReader serves every admin view. All relational views are derived here.
// Lists order by (ts, id) descending. Missing items return ErrNotFound.
type CallReader interface {
	ListCalls(ctx context.Context, f CallFilter, p Page) ([]CallSummary, *Cursor, error)
	// CountCalls counts matching calls up to max; capped is true when more exist.
	CountCalls(ctx context.Context, f CallFilter, max int) (n int, capped bool, err error)
	GetCall(ctx context.Context, id ID) (CallDetail, error) // with its tool halves
	ListSessions(ctx context.Context, f SessionFilter, p Page) ([]SessionSummary, *Cursor, error)
	GetSession(ctx context.Context, key string) (SessionDetail, error) // agents, prompts, calls
	ListToolCalls(ctx context.Context, f ToolCallFilter, p Page) ([]ToolCall, *Cursor, error)
	ToolSummary(ctx context.Context, f ToolCallFilter) ([]ToolStat, error)
	Usage(ctx context.Context, q UsageQuery) ([]UsageBucket, error) // tokens, cost, latency percentiles
	// PromptCosts ranks the prompts of the matching calls by cost (ADR 0057).
	PromptCosts(ctx context.Context, q PromptCostQuery) (PromptCosts, error)
}
