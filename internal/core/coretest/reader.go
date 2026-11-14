package coretest

import (
	"context"

	"github.com/mtanzi/spyglass/internal/core"
)

// Reader is a core.CallReader that returns canned values. Set the fields a test
// needs; the rest return zero values. GetCall and GetSession return
// core.ErrNotFound when no canned value matches. Err, when set, fails every method.
type Reader struct {
	Calls      []core.CallSummary
	Count      int
	Capped     bool
	Details    map[core.ID]core.CallDetail
	Sessions   []core.SessionSummary
	SessionMap map[string]core.SessionDetail
	ToolCalls  []core.ToolCall
	ToolStats  []core.ToolStat
	Buckets    []core.UsageBucket
	Prompts    core.PromptCosts
	Next       *core.Cursor
	Err        error
}

var _ core.CallReader = (*Reader)(nil)

func (r *Reader) ListCalls(context.Context, core.CallFilter, core.Page) ([]core.CallSummary, *core.Cursor, error) {
	return r.Calls, r.Next, r.Err
}

func (r *Reader) CountCalls(context.Context, core.CallFilter, int) (int, bool, error) {
	return r.Count, r.Capped, r.Err
}

func (r *Reader) GetCall(_ context.Context, id core.ID) (core.CallDetail, error) {
	if r.Err != nil {
		return core.CallDetail{}, r.Err
	}
	d, ok := r.Details[id]
	if !ok {
		return core.CallDetail{}, core.ErrNotFound
	}
	return d, nil
}

func (r *Reader) ListSessions(context.Context, core.SessionFilter, core.Page) ([]core.SessionSummary, *core.Cursor, error) {
	return r.Sessions, r.Next, r.Err
}

func (r *Reader) GetSession(_ context.Context, key string) (core.SessionDetail, error) {
	if r.Err != nil {
		return core.SessionDetail{}, r.Err
	}
	d, ok := r.SessionMap[key]
	if !ok {
		return core.SessionDetail{}, core.ErrNotFound
	}
	return d, nil
}

func (r *Reader) ListToolCalls(context.Context, core.ToolCallFilter, core.Page) ([]core.ToolCall, *core.Cursor, error) {
	return r.ToolCalls, r.Next, r.Err
}

func (r *Reader) ToolSummary(context.Context, core.ToolCallFilter) ([]core.ToolStat, error) {
	return r.ToolStats, r.Err
}

func (r *Reader) Usage(context.Context, core.UsageQuery) ([]core.UsageBucket, error) {
	return r.Buckets, r.Err
}

func (r *Reader) PromptCosts(context.Context, core.PromptCostQuery) (core.PromptCosts, error) {
	return r.Prompts, r.Err
}
