package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtanzi/spyglass/internal/core"
)

// ToolCosts ranks the tools of the calls matching q.Filter by attributed cost
// (ADR 0059). The cost of a model call is split equally among the tool uses it
// requested, and a tool's cost is the sum of its shares. Calls are selected by
// their own time, so the tool costs plus the cost of the calls with no tool use
// equal the total cost of the same range and filters.
//
// The shares of one call are computed once per tool, not once per use:
//
//	e    the tool uses of the selected calls.
//	r    the earliest result of each tool call id of e, read once over the
//	     range (see below).
//	u    one row per (call, tool): the uses k, the errors and result bytes of the
//	     paired results. A use pairs with its earliest result, as in the tool-call
//	     list.
//	n    adds n, the uses of the whole call, and the rank of the tool by name.
//	sh   the share div(cost*k, n) in micro-USD, whole numbers only.
//	t    the part of the call's cost that goes to the tool: the share, plus, for
//	     the first tool by name, what the shares left over (less than the number
//	     of tools of the call).
//
// The results are read once, not once per use. A lookup per use (a lateral
// subquery on tool_call_id) took 16.6 s over 1.5 million calls and 58 s over
// 4.5 million (deployment.md, "Sizing"); one pass with DISTINCT ON over the
// results of the range and a hash join takes 4.8 s and 15 s.
//
// When the filter selects few calls (a session, an agent, a prompt or tools),
// the pass reads only the results of the uses in e, so a one-session filter
// takes milliseconds. Other filters can keep almost every call (one provider,
// one client, the only key), and there the restriction keeps every result anyway
// and makes the planner sort them on disk (22 s instead of 4.8 s), so it is left
// out. e is not materialized, so the
// planner estimates its rows: a materialized e is guessed at 200 rows and read
// with a nested loop.
//
// The pass starts at the range start: a result is never earlier than its use,
// and a use is never earlier than its call, so no result of a selected call is
// left out. It ends a day after the range end, as the tool-call list does
// (ToolResultLookahead).
//
// A call with no cost has a NULL share, so the sum of a tool whose calls are all
// unpriced is NULL: unknown, not zero.
func (s *Store) ToolCosts(ctx context.Context, q core.ToolCostQuery) (core.ToolCosts, error) {
	if err := q.Validate(); err != nil {
		return core.ToolCosts{}, err
	}
	w := buildWhere(q.Filter)
	from := w.arg(q.Filter.From)
	lookahead := w.arg(q.Filter.To.Add(core.ToolResultLookahead))
	limit := w.arg(q.Limit)
	selected := ""
	if f := q.Filter; f.SessionKey != "" || f.AgentID != "" || f.PromptID != "" || len(f.Tools) > 0 {
		selected = "AND tool_call_id IN (SELECT tool_call_id FROM e)"
	}
	rows, err := s.pool.Query(ctx, `WITH e AS NOT MATERIALIZED (
		SELECT c.id AS call_id, coalesce(e.tool_name, '') AS tool, c.cost_usd * 1000000 AS cost, c.auth_mode, e.tool_call_id
		FROM calls c
		JOIN tool_events e ON e.call_id = c.id AND e.kind = 'use'
		WHERE `+w.sql()+`
	), r AS (
		SELECT DISTINCT ON (tool_call_id) tool_call_id, is_error, bytes
		FROM tool_events
		WHERE kind = 'result' AND ts >= `+from+` AND ts < `+lookahead+` `+selected+`
		ORDER BY tool_call_id, ts, call_id, idx
	), u AS (
		SELECT e.call_id, e.tool, e.cost, e.auth_mode,
			count(*) AS k,
			count(*) FILTER (WHERE r.is_error) AS errors,
			coalesce(sum(r.bytes), 0) AS result_bytes
		FROM e
		LEFT JOIN r ON r.tool_call_id = e.tool_call_id
		GROUP BY e.call_id, e.tool, e.cost, e.auth_mode
	), n AS (
		SELECT u.*, sum(k) OVER (PARTITION BY call_id) AS n,
			row_number() OVER (PARTITION BY call_id ORDER BY tool COLLATE "C") AS rn
		FROM u
	), sh AS (
		SELECT n.*, div(cost * k, n) AS share FROM n
	), t AS (
		SELECT sh.*, share + CASE WHEN rn = 1 THEN cost - sum(share) OVER (PARTITION BY call_id) ELSE 0 END AS part
		FROM sh
	)
	SELECT tool, sum(k)::bigint, sum(errors)::bigint, sum(result_bytes)::bigint, sum(part)::bigint,
		count(*) FILTER (WHERE cost IS NULL),
		coalesce(bool_or(auth_mode = 'client_passthrough' AND cost IS NOT NULL), false),
		count(*) OVER ()
	FROM t GROUP BY tool
	ORDER BY (sum(part) IS NULL), sum(part) DESC, sum(k) DESC, tool COLLATE "C" LIMIT `+limit, w.args...)
	if err != nil {
		return core.ToolCosts{}, fmt.Errorf("postgres: tool costs: %w", err)
	}
	out := core.ToolCosts{}
	out.Tools, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.ToolCost, error) {
		var (
			t    core.ToolCost
			cost *int64
		)
		if err := r.Scan(&t.Tool, &t.Uses, &t.Errors, &t.ResultBytes, &cost, &t.UnpricedCalls, &t.APIEquivalent, &out.ToolCount); err != nil {
			return t, err
		}
		if cost != nil {
			c := core.MicroUSD(*cost)
			t.Cost = &c
		}
		return t, nil
	})
	if err != nil {
		return core.ToolCosts{}, fmt.Errorf("postgres: tool costs: %w", err)
	}
	return out, nil
}
