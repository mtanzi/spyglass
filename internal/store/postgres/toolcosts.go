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
//	u    one row per (call, tool): the uses k, the errors and result bytes of the
//	     paired results. A use pairs with its earliest result, as in the tool-call
//	     list.
//	n    adds n, the uses of the whole call, and the rank of the tool by name.
//	sh   the share div(cost*k, n) in micro-USD, whole numbers only.
//	t    the part of the call's cost that goes to the tool: the share, plus, for
//	     the first tool by name, what the shares left over (less than the number
//	     of tools of the call).
//
// A call with no cost has a NULL share, so the sum of a tool whose calls are all
// unpriced is NULL: unknown, not zero.
func (s *Store) ToolCosts(ctx context.Context, q core.ToolCostQuery) (core.ToolCosts, error) {
	if err := q.Validate(); err != nil {
		return core.ToolCosts{}, err
	}
	w := buildWhere(q.Filter)
	lookahead := w.arg(q.Filter.To.Add(core.ToolResultLookahead))
	limit := w.arg(q.Limit)
	rows, err := s.pool.Query(ctx, `WITH u AS (
		SELECT c.id AS call_id, coalesce(e.tool_name, '') AS tool, c.cost_usd * 1000000 AS cost, c.auth_mode,
			count(*) AS k,
			count(*) FILTER (WHERE r.is_error) AS errors,
			coalesce(sum(r.bytes), 0) AS result_bytes
		FROM calls c
		JOIN tool_events e ON e.call_id = c.id AND e.kind = 'use'
		LEFT JOIN LATERAL (
			SELECT re.is_error, re.bytes FROM tool_events re
			WHERE re.kind = 'result' AND re.tool_call_id = e.tool_call_id AND re.ts < `+lookahead+`
			ORDER BY re.ts, re.call_id, re.idx LIMIT 1
		) r ON true
		WHERE `+w.sql()+`
		GROUP BY c.id, coalesce(e.tool_name, ''), c.cost_usd, c.auth_mode
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
