package postgres

import (
	"context"
	"fmt"

	"github.com/mtanzi/spyglass/internal/core"
)

// ProjectCosts ranks the projects of the calls that match q.Filter by cost:
// known cost first, highest first, then unknown cost; ties go to more calls,
// then to the lower project name (ADR 0059). Calls with no project are not a
// project: they come back as Unassigned.
//
// The calls are read once. One aggregate per project (and one for the NULL
// project) is ranked and cut to q.Limit in SQL, and the count of every project
// comes from a window over the groups before the cut, so it includes projects
// past the limit. Cost sums the priced calls only and is NULL when none is
// priced, as in Usage.
func (s *Store) ProjectCosts(ctx context.Context, q core.ProjectCostQuery) (core.ProjectCosts, error) {
	if err := q.Validate(); err != nil {
		return core.ProjectCosts{}, err
	}
	w := buildWhere(q.Filter)
	limit := w.arg(q.Limit)
	const rank = `(p.cost IS NULL), p.cost DESC, p.calls DESC, p.project COLLATE "C"`
	rows, err := s.pool.Query(ctx, `WITH p AS (
		SELECT c.project, count(*) AS calls, count(DISTINCT c.session_key) AS sessions,
			(sum(c.cost_usd) * 1000000)::bigint AS cost,
			count(*) FILTER (WHERE c.cost_usd IS NULL) AS unpriced,
			coalesce(bool_or(c.auth_mode = 'client_passthrough' AND c.cost_usd IS NOT NULL), false) AS api_equivalent
		FROM calls c WHERE `+w.sql()+` GROUP BY c.project)
	SELECT * FROM (
		(SELECT p.project, p.calls, p.sessions, p.cost, p.unpriced, p.api_equivalent, p.total FROM (
			SELECT p.*, count(*) OVER () AS total FROM p WHERE p.project IS NOT NULL) p
		ORDER BY `+rank+` LIMIT `+limit+`)
		UNION ALL
		SELECT p.project, p.calls, p.sessions, p.cost, p.unpriced, p.api_equivalent, 0 FROM p WHERE p.project IS NULL
	) p ORDER BY (p.project IS NULL), `+rank, w.args...)
	if err != nil {
		return core.ProjectCosts{}, fmt.Errorf("postgres: project costs: %w", err)
	}
	var out core.ProjectCosts
	zero := core.MicroUSD(0)
	out.Unassigned.Cost = &zero
	out.Projects = []core.ProjectCost{}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var (
				project *string
				c       core.ProjectCost
				cost    *int64
				total   int
			)
			if err := rows.Scan(&project, &c.Calls, &c.Sessions, &cost, &c.UnpricedCalls, &c.APIEquivalent, &total); err != nil {
				return err
			}
			if cost != nil {
				m := core.MicroUSD(*cost)
				c.Cost = &m
			}
			if project == nil {
				out.Unassigned = core.UnassignedCost{Calls: c.Calls, Cost: c.Cost, UnpricedCalls: c.UnpricedCalls}
				continue
			}
			c.Project = *project
			out.ProjectCount = total
			out.Projects = append(out.Projects, c)
		}
		return rows.Err()
	}()
	if err != nil {
		return core.ProjectCosts{}, fmt.Errorf("postgres: project costs: %w", err)
	}
	return out, nil
}
