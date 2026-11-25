package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mtanzi/spyglass/internal/core"
)

// Page size of ListCalls: DefaultLimit when Page.Limit is not positive, and
// never more than MaxLimit.
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// ListCalls returns one page of calls matching f, newest first by (ts, id).
// The cursor is nil on the last page. The page after p.After holds the calls
// strictly older than it in that order.
func (s *Store) ListCalls(ctx context.Context, f core.CallFilter, p core.Page) ([]core.CallSummary, *core.Cursor, error) {
	if err := f.Validate(); err != nil {
		return nil, nil, err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)

	w := buildWhere(f)
	if p.After != nil {
		w.add("(c.ts, c.id) < (" + w.arg(p.After.TS) + ", " + w.arg(uuid(p.After.ID)) + ")")
	}
	// One extra row tells whether a next page exists.
	sql := `SELECT ` + summaryColumns + ` FROM calls c WHERE ` + w.sql() +
		` ORDER BY c.ts DESC, c.id DESC LIMIT ` + w.arg(limit+1)
	rows, err := s.pool.Query(ctx, sql, w.args...)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: list calls: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanSummary)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: list calls: %w", err)
	}
	var next *core.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = &core.Cursor{TS: last.TS, ID: last.ID}
	}
	return out, next, nil
}

// CountCalls counts the calls matching f, up to max. When more than max match
// it returns max and capped true. max must be positive.
func (s *Store) CountCalls(ctx context.Context, f core.CallFilter, max int) (int, bool, error) {
	if err := f.Validate(); err != nil {
		return 0, false, err
	}
	if max <= 0 {
		return 0, false, fmt.Errorf("postgres: count calls: max %d: %w", max, core.ErrInvalidFilter)
	}
	w := buildWhere(f)
	sql := `SELECT count(*) FROM (SELECT 1 FROM calls c WHERE ` + w.sql() + ` LIMIT ` + w.arg(max+1) + `) m`
	var n int
	if err := s.pool.QueryRow(ctx, sql, w.args...).Scan(&n); err != nil {
		return 0, false, fmt.Errorf("postgres: count calls: %w", err)
	}
	if n > max {
		return max, true, nil
	}
	return n, false, nil
}

// GetCall returns one call with its tool halves, or core.ErrNotFound.
func (s *Store) GetCall(ctx context.Context, id core.ID) (core.CallDetail, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+detailColumns+` FROM calls c WHERE c.id = $1`, uuid(id))
	if err != nil {
		return core.CallDetail{}, fmt.Errorf("postgres: get call: %w", err)
	}
	c, err := pgx.CollectOneRow(rows, scanDetail)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.CallDetail{}, core.ErrNotFound
	}
	if err != nil {
		return core.CallDetail{}, fmt.Errorf("postgres: get call: %w", err)
	}

	// Results come before uses, each in order, as the write path numbers them.
	halves, err := s.pool.Query(ctx, `
		SELECT kind, source, tool_call_id, tool_name, bytes, is_error
		FROM tool_events WHERE call_id = $1 ORDER BY idx`, uuid(id))
	if err != nil {
		return core.CallDetail{}, fmt.Errorf("postgres: get call: %w", err)
	}
	defer halves.Close()
	for halves.Next() {
		var (
			kind, source, toolCallID string
			name                     *string
			bytes                    int
			isError                  *bool
		)
		if err := halves.Scan(&kind, &source, &toolCallID, &name, &bytes, &isError); err != nil {
			return core.CallDetail{}, fmt.Errorf("postgres: get call: %w", err)
		}
		switch kind {
		case "use":
			c.ToolUses = append(c.ToolUses, core.ToolUse{
				Source: core.ToolSource(source), ToolCallID: toolCallID, Name: str(name), ArgBytes: bytes,
			})
		case "result":
			c.ToolResults = append(c.ToolResults, core.ToolResult{
				Source: core.ToolSource(source), ToolCallID: toolCallID, IsError: isError != nil && *isError, ResultBytes: bytes,
			})
		}
	}
	if err := halves.Err(); err != nil {
		return core.CallDetail{}, fmt.Errorf("postgres: get call: %w", err)
	}
	return c, nil
}

// costMicro reads cost_usd back as the exact count of millionths.
const costMicro = `(c.cost_usd * 1000000)::bigint`

const summaryColumns = `c.id, c.ts, c.key_name, c.client, c.session_key, c.agent_id, c.prompt_id, c.project, c.provider, c.model,
	c.status, c.error_class, c.stop_reason, c.streamed, c.context_compacted, c.ttft_ms, c.duration_ms,
	c.input_uncached, c.cache_read, c.cache_write_5m, c.cache_write_1h, c.output, c.reasoning, ` + costMicro + `,
	(SELECT count(*) FROM tool_events u WHERE u.call_id = c.id AND u.kind = 'use')`

const detailColumns = `c.id, c.ts, c.key_name, c.client, c.client_raw, c.client_request_id,
	c.session_key, c.agent_id, c.parent_agent_id, c.agent_type, c.prompt_id, c.request_class,
	c.compaction, c.context_compacted, c.trace_id, c.project,
	c.family, c.endpoint, c.auth_mode, c.provider, c.model, c.upstream_request_id,
	c.status, c.error_class, c.error_type, c.stop_reason, c.streamed,
	c.ttfb_ms, c.ttft_ms, c.duration_ms, c.ended_at,
	c.input_uncached, c.cache_read, c.cache_write_5m, c.cache_write_1h, c.output, c.reasoning,
	c.usage_raw, c.usage_estimated, c.extraction_partial,
	` + costMicro + `, c.pricing_version, c.prev_tool_durations`

// scanSummary maps one row of summaryColumns to a core.CallSummary.
func scanSummary(r pgx.CollectableRow) (core.CallSummary, error) {
	var (
		id                                                          pgtype.UUID
		ts                                                          time.Time
		key                                                         string
		client, session, agent, prompt, project, model, class, stop *string
		provider                                                    string
		status                                                      *int16
		streamed, compacted                                         bool
		ttft, dur                                                   *int32
		u                                                           core.Usage
		costM                                                       *int64
		tools                                                       int
	)
	err := r.Scan(&id, &ts, &key, &client, &session, &agent, &prompt, &project, &provider, &model,
		&status, &class, &stop, &streamed, &compacted, &ttft, &dur,
		&u.InputUncached, &u.CacheRead, &u.CacheWrite5m, &u.CacheWrite1h, &u.Output, &u.Reasoning, &costM, &tools)
	if err != nil {
		return core.CallSummary{}, err
	}
	return core.CallSummary{
		ID: core.ID(id.Bytes), TS: ts.UTC(), KeyName: key, Client: str(client), SessionKey: str(session),
		AgentID: str(agent), PromptID: str(prompt), Project: str(project), Provider: provider, Model: str(model), Status: code(status),
		ErrorClass: str(class), StopReason: str(stop), Streamed: streamed,
		TTFT: ms(ttft), Duration: ms(dur), Usage: u, Cost: micro(costM), ToolCount: tools,
		ContextCompacted: compacted,
	}, nil
}

// scanDetail maps one row of detailColumns to a core.CallDetail, without its tool halves.
func scanDetail(r pgx.CollectableRow) (core.CallDetail, error) {
	var (
		id                                                      pgtype.UUID
		c                                                       core.CallRecord
		clientRaw, clientReq, session, agent, parent, agentType *string
		promptID, class, traceID, project, model, upstreamID    *string
		errClass, errType, stop, pricing, prevDur               *string
		status                                                  *int16
		ttfb, ttft, dur                                         *int32
		ended                                                   *time.Time
		client                                                  *string
		usageRaw                                                []byte
		costM                                                   *int64
		u                                                       core.Usage
	)
	err := r.Scan(&id, &c.TS, &c.KeyName, &client, &clientRaw, &clientReq,
		&session, &agent, &parent, &agentType, &promptID, &class,
		&c.Compaction, &c.ContextCompacted, &traceID, &project,
		&c.Family, &c.Endpoint, &c.AuthMode, &c.Provider, &model, &upstreamID,
		&status, &errClass, &errType, &stop, &c.Streamed,
		&ttfb, &ttft, &dur, &ended,
		&u.InputUncached, &u.CacheRead, &u.CacheWrite5m, &u.CacheWrite1h, &u.Output, &u.Reasoning,
		&usageRaw, &c.UsageEstimated, &c.ExtractionPartial,
		&costM, &pricing, &prevDur)
	if err != nil {
		return core.CallDetail{}, err
	}
	c.ID, c.TS = core.ID(id.Bytes), c.TS.UTC()
	c.Client, c.ClientRaw, c.ClientRequestID = str(client), str(clientRaw), str(clientReq)
	c.SessionKey, c.AgentID, c.ParentAgentID, c.AgentType = str(session), str(agent), str(parent), str(agentType)
	c.PromptID, c.RequestClass, c.TraceID, c.Project = str(promptID), str(class), str(traceID), str(project)
	c.Model, c.UpstreamRequestID = str(model), str(upstreamID)
	c.Status, c.ErrorClass, c.ErrorType, c.StopReason = code(status), str(errClass), str(errType), str(stop)
	c.TTFB, c.TTFT, c.Duration = ms(ttfb), ms(ttft), ms(dur)
	if ended != nil {
		c.EndedAt = ended.UTC()
	}
	c.Usage = u
	if usageRaw != nil {
		c.UsageRaw = json.RawMessage(usageRaw)
	}
	c.Cost, c.PricingVersion, c.PrevToolDurations = micro(costM), str(pricing), str(prevDur)
	return core.CallDetail{CallRecord: c}, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func code(p *int16) int {
	if p == nil {
		return 0
	}
	return int(*p)
}

func ms(p *int32) time.Duration {
	if p == nil {
		return 0
	}
	return time.Duration(*p) * time.Millisecond
}

func micro(p *int64) *core.MicroUSD {
	if p == nil {
		return nil
	}
	m := core.MicroUSD(*p)
	return &m
}
