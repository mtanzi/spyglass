package postgres

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

// BackfillResult counts the calls each step of BackfillProjects gave a project.
type BackfillResult struct {
	// ByKey is the number of calls that got the project of their key.
	ByKey int64
	// BySession is the number of calls that got the one project of their session.
	BySession int64
}

// BackfillProjects gives a project to older calls that have none (ADR 0059).
// It runs two UPDATEs in one transaction, each only on rows whose project is
// null, in this order:
//
//  1. A call of a key in keyProjects (key name to project) gets that project.
//  2. A call in a session whose other calls have exactly one distinct non-null
//     project gets that project. A session with none, or with two or more,
//     stays as it is. Step 2 sees the rows step 1 filled.
//
// It never overwrites a project that is set and never detects a name: it only
// spreads names already known. The second run with the same keyProjects
// changes no row, because no null row meets either rule after the first.
func (s *Store) BackfillProjects(ctx context.Context, keyProjects map[string]string) (BackfillResult, error) {
	names := make([]string, 0, len(keyProjects))
	for name := range keyProjects {
		names = append(names, name)
	}
	sort.Strings(names)
	projects := make([]string, len(names))
	for i, name := range names {
		projects[i] = keyProjects[name]
	}

	var res BackfillResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE calls c SET project = k.project
			FROM unnest($1::text[], $2::text[]) AS k(name, project)
			WHERE c.key_name = k.name AND c.project IS NULL`, names, projects)
		if err != nil {
			return fmt.Errorf("projects of keys: %w", err)
		}
		res.ByKey = tag.RowsAffected()

		tag, err = tx.Exec(ctx, `UPDATE calls c SET project = s.project
			FROM (SELECT session_key, min(project) AS project FROM calls
				WHERE session_key IS NOT NULL AND project IS NOT NULL
				GROUP BY session_key HAVING count(DISTINCT project) = 1) s
			WHERE c.session_key = s.session_key AND c.project IS NULL`)
		if err != nil {
			return fmt.Errorf("projects of sessions: %w", err)
		}
		res.BySession = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return BackfillResult{}, fmt.Errorf("postgres: backfill projects: %w", err)
	}
	return res, nil
}
