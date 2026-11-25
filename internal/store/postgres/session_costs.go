package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mtanzi/spyglass/internal/core"
)

// SessionCosts ranks the sessions of the calls that match q.Filter by cost:
// known cost first, highest first, then unknown cost; ties go to more calls,
// then to the lower session key (ADR 0059). A call with no session key is in no
// session.
//
// Each session is the sessionColumns aggregate of the session list, so its
// cost, unpriced count and error count are the numbers the list and the session
// view show for the same calls. The aggregate is ranked and cut to q.Limit in
// SQL, and only those sessions get their tool calls counted and their tool time
// summed, both over the tool calls of the matching calls. The count of every
// session of the matching calls comes from a window over the groups before the
// cut.
func (s *Store) SessionCosts(ctx context.Context, q core.SessionCostQuery) (core.SessionCosts, error) {
	if err := q.Validate(); err != nil {
		return core.SessionCosts{}, err
	}
	w := buildWhere(q.Filter)
	w.add("c.session_key IS NOT NULL")
	limit := w.arg(q.Limit)
	const rank = `(s.cost IS NULL), s.cost DESC, s.calls DESC, s.session_key COLLATE "C"`
	rows, err := s.pool.Query(ctx, `SELECT `+sessionSelect+`, s.api_equivalent, s.total, s.call_ids FROM (
		SELECT s.*, count(*) OVER () AS total FROM (SELECT `+sessionColumns+`,
			coalesce(bool_or(c.auth_mode = 'client_passthrough' AND c.cost_usd IS NOT NULL), false)
			FROM calls c WHERE `+w.sql()+` GROUP BY c.session_key) s(`+sessionFieldNames+`, api_equivalent)
		ORDER BY `+rank+` LIMIT `+limit+`) s ORDER BY `+rank, w.args...)
	if err != nil {
		return core.SessionCosts{}, fmt.Errorf("postgres: session costs: %w", err)
	}
	var out core.SessionCosts
	var callIDs []core.ID
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.SessionCost, error) {
		var c core.SessionCost
		var ids []pgtype.UUID
		sess, err := scanSessionWith(r, &c.APIEquivalent, &out.SessionCount, &ids)
		for _, id := range ids {
			callIDs = append(callIDs, core.ID(id.Bytes))
		}
		c.SessionSummary = sess.SessionSummary
		return c, err
	})
	if err != nil {
		return core.SessionCosts{}, fmt.Errorf("postgres: session costs: %w", err)
	}
	sums := make([]core.SessionSummary, len(got))
	keys := make([]string, len(got))
	for i, g := range got {
		sums[i], keys[i] = g.SessionSummary, g.SessionKey
	}
	if err := fillToolTime(ctx, s.pool, sums, keys, callIDs, q.Filter.From, q.Filter.To); err != nil {
		return core.SessionCosts{}, fmt.Errorf("postgres: session costs: %w", err)
	}
	out.Sessions = make([]core.SessionCost, len(got))
	for i := range got {
		out.Sessions[i] = core.SessionCost{SessionSummary: sums[i], APIEquivalent: got[i].APIEquivalent}
	}
	return out, nil
}
