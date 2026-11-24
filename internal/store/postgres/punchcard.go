package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtanzi/spyglass/internal/core"
)

// Punchcard returns one row per non-empty (weekday, hour) cell of the calls
// that match q.Filter, Monday first and hour 0 first, with the local clock read
// in the zone q.TZ (ADR 0059).
//
// The zone is a bind parameter of AT TIME ZONE, never part of the SQL text. A
// cell is a wall-clock reading, so the repeated hour of a DST change adds into
// one cell and the skipped hour adds nothing. Cost sums the priced calls only
// and is NULL when none is priced, as in Usage.
func (s *Store) Punchcard(ctx context.Context, q core.PunchcardQuery) ([]core.PunchcardCell, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	w := buildWhere(q.Filter)
	local := "c.ts AT TIME ZONE " + w.arg(q.TZ) + "::text"
	rows, err := s.pool.Query(ctx, `SELECT extract(isodow FROM `+local+`)::int, extract(hour FROM `+local+`)::int,
		count(*), (sum(c.cost_usd) * 1000000)::bigint,
		count(*) FILTER (WHERE c.cost_usd IS NULL),
		coalesce(bool_or(c.auth_mode = 'client_passthrough' AND c.cost_usd IS NOT NULL), false)
	FROM calls c WHERE `+w.sql()+` GROUP BY 1, 2 ORDER BY 1, 2`, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: punchcard: %w", err)
	}
	cells, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.PunchcardCell, error) {
		var (
			c    core.PunchcardCell
			cost *int64
		)
		if err := r.Scan(&c.Weekday, &c.Hour, &c.Calls, &cost, &c.UnpricedCalls, &c.APIEquivalent); err != nil {
			return c, err
		}
		if cost != nil {
			v := core.MicroUSD(*cost)
			c.Cost = &v
		}
		return c, nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: punchcard: %w", err)
	}
	return cells, nil
}
