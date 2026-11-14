package postgres

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtanzi/spyglass/internal/core"
)

// groupColumns is the whitelist of group-by columns. Only these strings are
// ever put in the SQL text. A NULL group value reads as the empty string, which
// is how the core types say "not known".
var groupColumns = map[core.UsageGroup]string{
	core.GroupNone:       `''`,
	core.GroupKey:        `c.key_name`,
	core.GroupModel:      `coalesce(c.model, '')`,
	core.GroupClient:     `coalesce(c.client, '')`,
	core.GroupProvider:   `c.provider`,
	core.GroupSession:    `coalesce(c.session_key, '')`,
	core.GroupErrorClass: `coalesce(c.error_class, '')`,
}

// grainUnits maps a grain to its date_trunc unit.
var grainUnits = map[core.Grain]string{
	core.GrainMinute: "minute",
	core.GrainHour:   "hour",
	core.GrainDay:    "day",
}

// Usage aggregates the calls matching q.Filter into buckets of q.Grain, one
// per group value, oldest first and by group inside a bucket. Buckets are
// aligned in UTC whatever the session time zone is. A range with no calls
// returns no buckets.
//
// Cost sums only the calls with a known cost; the Unpriced, Estimated and
// NoUsage counters report the calls that are not fully known. Latency
// percentiles use percentile_cont over the raw duration_ms values, and a call
// with no duration is left out.
func (s *Store) Usage(ctx context.Context, q core.UsageQuery) ([]core.UsageBucket, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	group, unit := groupColumns[q.GroupBy], grainUnits[q.Grain]
	if group == "" || unit == "" {
		return nil, core.ErrInvalidFilter
	}
	w := buildWhere(q.Filter)
	sql := `SELECT date_trunc('` + unit + `', c.ts, 'UTC') AS bucket, ` + group + ` AS grp,
		count(*),
		count(*) FILTER (WHERE c.error_class IS NOT NULL),
		sum(c.input_uncached)::bigint, sum(c.cache_read)::bigint,
		sum(c.cache_write_5m)::bigint, sum(c.cache_write_1h)::bigint,
		sum(c.output)::bigint, sum(c.reasoning)::bigint,
		coalesce(sum(c.cost_usd * 1000000), 0)::bigint,
		count(*) FILTER (WHERE c.cost_usd IS NULL),
		count(*) FILTER (WHERE c.usage_estimated),
		count(*) FILTER (WHERE c.usage_raw IS NULL),
		percentile_cont(ARRAY[0.5, 0.95, 0.99]) WITHIN GROUP (ORDER BY c.duration_ms)
	FROM calls c WHERE ` + w.sql() + ` GROUP BY 1, 2 ORDER BY 1, 2`
	rows, err := s.pool.Query(ctx, sql, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: usage: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanBucket)
	if err != nil {
		return nil, fmt.Errorf("postgres: usage: %w", err)
	}
	return out, nil
}

func scanBucket(r pgx.CollectableRow) (core.UsageBucket, error) {
	var (
		b    core.UsageBucket
		cost int64
		pct  []float64 // NULL when no call has a duration
	)
	u := &b.Usage
	err := r.Scan(&b.Start, &b.Group, &b.Calls, &b.Errors,
		&u.InputUncached, &u.CacheRead, &u.CacheWrite5m, &u.CacheWrite1h, &u.Output, &u.Reasoning,
		&cost, &b.UnpricedCalls, &b.EstimatedCalls, &b.NoUsageCalls, &pct)
	if err != nil {
		return core.UsageBucket{}, err
	}
	b.Start, b.Cost = b.Start.UTC(), core.MicroUSD(cost)
	if len(pct) == 3 {
		b.LatencyP50, b.LatencyP95, b.LatencyP99 = msFloat(pct[0]), msFloat(pct[1]), msFloat(pct[2])
	}
	return b, nil
}

// msFloat converts a fractional number of milliseconds to the nearest nanosecond.
func msFloat(v float64) time.Duration {
	return time.Duration(math.Round(v * float64(time.Millisecond)))
}

// PromptCosts ranks the prompts of the calls matching q.Filter by cost and
// returns the first q.Limit, with the number of prompts and of calls without a
// prompt id over the same calls. One snapshot serves both reads.
//
// A call belongs to a prompt when its prompt_id is not empty. Cost sums the
// priced calls only and is NULL when no call of the prompt is priced, as in
// Usage. The session key is the one of the prompt's first call. The order is
// known cost first, highest first, then unknown cost; ties go to the prompt
// with more calls, then to the lower prompt id.
func (s *Store) PromptCosts(ctx context.Context, q core.PromptCostQuery) (core.PromptCosts, error) {
	if err := q.Validate(); err != nil {
		return core.PromptCosts{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return core.PromptCosts{}, fmt.Errorf("postgres: prompt costs: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fail := func(err error) (core.PromptCosts, error) {
		return core.PromptCosts{}, fmt.Errorf("postgres: prompt costs: %w", err)
	}

	w := buildWhere(q.Filter)
	where, filterArgs := w.sql(), slices.Clone(w.args)
	limit := w.arg(q.Limit)
	rows, err := tx.Query(ctx, `WITH p AS (
		SELECT c.prompt_id, (array_agg(coalesce(c.session_key, '') ORDER BY c.ts, c.id))[1] AS session_key,
			count(*) AS calls, (sum(c.cost_usd) * 1000000)::bigint AS cost,
			count(*) FILTER (WHERE c.cost_usd IS NULL) AS unpriced,
			coalesce(bool_or(c.auth_mode = 'client_passthrough' AND c.cost_usd IS NOT NULL), false) AS api_equivalent
		FROM calls c WHERE `+where+` AND coalesce(c.prompt_id, '') <> '' GROUP BY c.prompt_id)
		SELECT prompt_id, session_key, calls, cost, unpriced, api_equivalent, count(*) OVER ()
		FROM p ORDER BY (cost IS NULL), cost DESC, calls DESC, prompt_id LIMIT `+limit, w.args...)
	if err != nil {
		return fail(err)
	}
	var total int
	out := core.PromptCosts{}
	out.Prompts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.PromptCost, error) {
		var (
			p    core.PromptCost
			cost *int64
		)
		if err := r.Scan(&p.PromptID, &p.SessionKey, &p.Calls, &cost, &p.UnpricedCalls, &p.APIEquivalent, &total); err != nil {
			return p, err
		}
		if cost != nil {
			c := core.MicroUSD(*cost)
			p.Cost = &c
		}
		return p, nil
	})
	if err != nil {
		return fail(err)
	}
	out.PromptCount = total

	err = tx.QueryRow(ctx, `SELECT count(*) FROM calls c WHERE `+where+` AND coalesce(c.prompt_id, '') = ''`,
		filterArgs...).Scan(&out.CallsWithoutPrompt)
	if err != nil {
		return fail(err)
	}
	return out, nil
}
