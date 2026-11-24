package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtanzi/spyglass/internal/core"
)

// MonthlyCosts returns one row per calendar month of [q.Filter.From,
// q.Filter.To) in the zone q.TZ, oldest first. A month with no calls is
// present with zero calls and a known cost of zero; the usage query leaves such
// gaps, this one fills them (ADR 0059).
//
// The zone is a bind parameter of date_trunc and AT TIME ZONE, never part of
// the SQL text. Months are matched to the rows by their zone-local label, so
// the server and the database agree on a month by name, not by instant.
// Cost sums the priced calls only and is NULL when none is priced, as in Usage.
// A month is partial when the range starts after its first instant or ends
// before its last.
func (s *Store) MonthlyCosts(ctx context.Context, q core.MonthlyCostQuery) ([]core.MonthCost, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	loc, err := core.LoadZone(q.TZ)
	if err != nil {
		return nil, err
	}
	w := buildWhere(q.Filter)
	tz := w.arg(q.TZ) + "::text"
	rows, err := s.pool.Query(ctx, `SELECT to_char(date_trunc('month', c.ts, `+tz+`) AT TIME ZONE `+tz+`, 'YYYY-MM'),
		count(*), (sum(c.cost_usd) * 1000000)::bigint,
		count(*) FILTER (WHERE c.cost_usd IS NULL),
		coalesce(bool_or(c.auth_mode = 'client_passthrough' AND c.cost_usd IS NOT NULL), false)
	FROM calls c WHERE `+w.sql()+` GROUP BY 1`, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: monthly costs: %w", err)
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.MonthCost, error) {
		var (
			m    core.MonthCost
			cost *int64
		)
		if err := r.Scan(&m.Month, &m.Calls, &cost, &m.UnpricedCalls, &m.APIEquivalent); err != nil {
			return m, err
		}
		if cost != nil {
			c := core.MicroUSD(*cost)
			m.Cost = &c
		}
		return m, nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: monthly costs: %w", err)
	}
	byMonth := make(map[string]core.MonthCost, len(found))
	for _, m := range found {
		byMonth[m.Month] = m
	}

	spans := core.MonthsOf(q.Filter.From, q.Filter.To, loc, core.MaxMonths)
	out := make([]core.MonthCost, 0, len(spans))
	for _, sp := range spans {
		m, ok := byMonth[sp.Label]
		if !ok {
			zero := core.MicroUSD(0)
			m = core.MonthCost{CostTotals: core.CostTotals{Cost: &zero}}
		}
		m.Month, m.Start, m.End = sp.Label, sp.Start, sp.End
		m.Partial = q.Filter.From.After(sp.Start) || q.Filter.To.Before(sp.End)
		out = append(out, m)
	}
	return out, nil
}
