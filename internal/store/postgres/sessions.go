package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mtanzi/spyglass/internal/core"
)

// Sessions and agents are derived when read (ADR 0020): a session is the
// calls that share a session_key, and its agent tree is the distinct
// (agent_id, parent_agent_id, agent_type) set of those calls. A call with a
// null session_key is in no session. A call with a null agent_id is not an
// agent node. There are no sessions or agents tables.

// sessionColumns aggregates one session out of its calls, under the names of
// sessionFields. sessionSelect reads them back in the order of scanSession,
// and counts the tool calls of call_ids: it runs on the selected sessions
// only, never on every group of the range.
const sessionColumns = `c.session_key,
	(array_agg(c.key_name ORDER BY c.ts, c.id))[1],
	(array_agg(c.client ORDER BY c.ts, c.id) FILTER (WHERE c.client IS NOT NULL))[1],
	min(c.ts), max(c.ts), (array_agg(c.id ORDER BY c.ts DESC, c.id DESC))[1],
	max(coalesce(c.ended_at, c.ts)), count(*), count(DISTINCT c.prompt_id),
	count(*) FILTER (WHERE c.error_class IS NOT NULL),
	count(DISTINCT (c.agent_id, coalesce(c.parent_agent_id, ''), coalesce(c.agent_type, ''))) FILTER (WHERE c.agent_id IS NOT NULL),
	array_agg(DISTINCT c.model COLLATE "C" ORDER BY c.model COLLATE "C") FILTER (WHERE c.model IS NOT NULL),
	` + usageSums + `, ` + costSum + `, count(*) FILTER (WHERE c.cost_usd IS NULL),
	coalesce(sum(c.duration_ms), 0)::bigint, array_agg(c.id)`

const sessionFieldNames = `session_key, key_name, client, first_ts, last_ts, last_id, ended_at, calls, prompts,
	errors, agents, models, input_uncached, cache_read, cache_write_5m, cache_write_1h, output, reasoning, cost, unpriced, llm_ms, call_ids`

const sessionFields = `s(` + sessionFieldNames + `)`

const sessionSelect = `s.session_key, s.key_name, s.client, s.first_ts, s.last_ts, s.last_id, s.ended_at, s.calls, s.prompts,
	(SELECT count(DISTINCT t.tool_call_id) FROM tool_events t WHERE t.call_id = ANY(s.call_ids)),
	s.errors, s.agents, s.models, s.input_uncached, s.cache_read, s.cache_write_5m, s.cache_write_1h, s.output, s.reasoning, s.cost, s.unpriced, s.llm_ms`

// usageSums and costSum add the usage columns and the known costs of a group.
const usageSums = `sum(c.input_uncached)::bigint, sum(c.cache_read)::bigint, sum(c.cache_write_5m)::bigint,
	sum(c.cache_write_1h)::bigint, sum(c.output)::bigint, sum(c.reasoning)::bigint`

// costSum is NULL when no call of the group has a known cost.
const costSum = `(sum(c.cost_usd) * 1000000)::bigint`

// session is one aggregated session and the id of its newest call, which
// places it in the list.
type session struct {
	core.SessionSummary
	lastID core.ID
}

func scanSession(r pgx.CollectableRow) (session, error) { return scanSessionWith(r) }

// scanSessionWith scans the columns of sessionSelect, then any extra columns
// the query adds after them into extra.
func scanSessionWith(r pgx.CollectableRow, extra ...any) (session, error) {
	var (
		s                                session
		key, name                        string
		client                           *string
		first, last, end                 time.Time
		lastID                           pgtype.UUID
		calls, prompts, tools, errs      int
		agents, unpriced                 int
		llmMS                            int64
		models                           []string
		in, cr, cw5, cw1, out, rs, costM = new(int64), new(int64), new(int64), new(int64), new(int64), new(int64), (*int64)(nil)
	)
	dest := append([]any{&key, &name, &client, &first, &last, &lastID, &end, &calls, &prompts, &tools, &errs, &agents, &models,
		in, cr, cw5, cw1, out, rs, &costM, &unpriced, &llmMS}, extra...)
	if err := r.Scan(dest...); err != nil {
		return s, err
	}
	s.SessionSummary = core.SessionSummary{
		SessionKey: key, KeyName: name, Client: str(client), FirstTS: first.UTC(), LastTS: last.UTC(),
		EndedAt: end.UTC(), Calls: calls, PromptCount: prompts, ToolCallCount: tools, Errors: errs, AgentCount: agents, Models: models,
		Usage: core.Usage{
			InputUncached: int(*in), CacheRead: int(*cr), CacheWrite5m: int(*cw5), CacheWrite1h: int(*cw1),
			Output: int(*out), Reasoning: int(*rs),
		},
		Cost: micro(costM), UnpricedCalls: unpriced, LLMTime: time.Duration(llmMS) * time.Millisecond,
	}
	s.lastID = core.ID(lastID.Bytes)
	return s, nil
}

// ListSessions returns one page of sessions, newest first. A session sits at
// its newest call in the time range: it orders by that call's (ts, id), and the
// cursor carries them. A session is listed when at least one of its calls in
// the range satisfies the call fields of f. Its summary then counts all its
// calls in the range, whichever fields they satisfy. MinCost applies to that
// total. Calls with no session key are never listed.
func (s *Store) ListSessions(ctx context.Context, f core.SessionFilter, p core.Page) ([]core.SessionSummary, *core.Cursor, error) {
	if err := f.Validate(); err != nil {
		return nil, nil, err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)

	sql, args := listSessionsSQL(f, p.After, limit)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: list sessions: %w", err)
	}
	got, err := pgx.CollectRows(rows, scanSession)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: list sessions: %w", err)
	}
	var next *core.Cursor
	if len(got) > limit {
		got = got[:limit]
		last := got[limit-1]
		next = &core.Cursor{TS: last.LastTS, ID: last.lastID}
	}
	out := make([]core.SessionSummary, len(got))
	keys := make([]string, len(got))
	for i, g := range got {
		out[i] = g.SessionSummary
		keys[i] = g.SessionKey
	}
	if err := fillToolTime(ctx, s.pool, out, keys, f.From, f.To); err != nil {
		return nil, nil, fmt.Errorf("postgres: list sessions: %w", err)
	}
	return out, next, nil
}

// queryer is what fillToolTime needs from a pool or a transaction.
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// fillToolTime sets ToolTime and ToolTimeEstimated of the sessions, in one
// query over the paired tool calls of all of them (the pairing of the tool
// explorer, so header and estimated durations agree with it). A tool call
// with no known duration adds nothing. It runs on the selected sessions only.
func fillToolTime(ctx context.Context, q queryer, sessions []core.SessionSummary, keys []string, from, to time.Time) error {
	if len(sessions) == 0 {
		return nil
	}
	w := &where{}
	union := toolCallsSQL(w, core.ToolCallFilter{From: from, To: to, SessionKeys: keys}, time.Now(), nil, 0)
	rows, err := q.Query(ctx, `SELECT t.session_key, coalesce(sum(t.dur_ms), 0)::bigint,
		coalesce(bool_or(t.dur_src = 'estimated'), false)
		FROM (`+union+`) t WHERE t.session_key IS NOT NULL GROUP BY t.session_key`, w.args...)
	if err != nil {
		return err
	}
	type total struct {
		ms        int64
		estimated bool
	}
	totals, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (struct {
		key string
		total
	}, error) {
		var x struct {
			key string
			total
		}
		err := r.Scan(&x.key, &x.ms, &x.estimated)
		return x, err
	})
	if err != nil {
		return err
	}
	byKey := make(map[string]total, len(totals))
	for _, t := range totals {
		byKey[t.key] = t.total
	}
	for i := range sessions {
		t := byKey[sessions[i].SessionKey]
		sessions[i].ToolTime = time.Duration(t.ms) * time.Millisecond
		sessions[i].ToolTimeEstimated = t.estimated
	}
	return nil
}

// listSessionsSQL returns the statement and arguments of one page of sessions.
func listSessionsSQL(f core.SessionFilter, after *core.Cursor, limit int) (string, []any) {
	w := &where{}
	w.add("c.ts >= " + w.arg(f.From))
	w.add("c.ts < " + w.arg(f.To))
	w.add("c.session_key IS NOT NULL")
	w.textEq("c.session_key", f.SessionKey)
	hit := w.collect(func(t *where) {
		t.textIn("c.key_name", false, f.Keys)
		t.textIn("c.client", true, f.Clients)
		t.textIn("c.provider", false, f.Providers)
		t.textIn("c.model", true, f.Models)
		t.textIn("c.error_class", true, f.ErrorClasses)
	})
	var having []string
	if len(hit) > 0 {
		having = append(having, "bool_or("+joinAnd(hit)+")")
	}
	if f.MinCost > 0 {
		// The total is NULL when no call has a known cost, and NULL never satisfies >=.
		having = append(having, "sum(c.cost_usd) >= "+w.arg(cost(&f.MinCost)))
	}
	page := `SELECT * FROM (SELECT ` + sessionColumns + ` FROM calls c WHERE ` + w.sql() +
		` GROUP BY c.session_key` + havingClause(having) + `) ` + sessionFields
	if after != nil {
		page += ` WHERE (s.last_ts, s.last_id) < (` + w.arg(after.TS) + `, ` + w.arg(uuid(after.ID)) + `)`
	}
	page += ` ORDER BY s.last_ts DESC, s.last_id DESC LIMIT ` + w.arg(limit+1)
	return `SELECT ` + sessionSelect + ` FROM (` + page + `) s ORDER BY s.last_ts DESC, s.last_id DESC`, w.args
}

func havingClause(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " HAVING " + joinAnd(conds)
}

// GetSession returns the session with the given key over all its calls, or
// core.ErrNotFound when no call has the key. One snapshot serves all four
// reads.
//
// Calls are newest first by (ts, id), like every list. Prompts are newest
// first by their first call. Agents are in the order of their first call. The
// agent tree is built by the caller from the parent ids.
func (s *Store) GetSession(ctx context.Context, key string) (core.SessionDetail, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return core.SessionDetail{}, fmt.Errorf("postgres: get session: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fail := func(err error) (core.SessionDetail, error) {
		return core.SessionDetail{}, fmt.Errorf("postgres: get session: %w", err)
	}
	key = clean(key)

	rows, err := tx.Query(ctx, `SELECT `+sessionSelect+` FROM (SELECT `+sessionColumns+`
		FROM calls c WHERE c.session_key = $1 GROUP BY c.session_key) `+sessionFields, key)
	if err != nil {
		return fail(err)
	}
	sum, err := pgx.CollectOneRow(rows, scanSession)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SessionDetail{}, core.ErrNotFound
	}
	if err != nil {
		return fail(err)
	}
	d := core.SessionDetail{SessionSummary: sum.SessionSummary}
	// Every call of the session is in the summary, so the tool window covers
	// them all: a tool use is timed at the end of its response.
	tt := []core.SessionSummary{d.SessionSummary}
	if err := fillToolTime(ctx, tx, tt, []string{key}, d.FirstTS, d.EndedAt.Add(time.Hour)); err != nil {
		return fail(err)
	}
	d.SessionSummary = tt[0]

	rows, err = tx.Query(ctx, `SELECT c.agent_id, c.parent_agent_id, c.agent_type, count(*)
		FROM calls c WHERE c.session_key = $1 AND c.agent_id IS NOT NULL
		GROUP BY c.agent_id, c.parent_agent_id, c.agent_type
		ORDER BY min(c.ts), c.agent_id COLLATE "C", c.parent_agent_id COLLATE "C" NULLS FIRST, c.agent_type COLLATE "C" NULLS FIRST`, key)
	if err != nil {
		return fail(err)
	}
	d.Agents, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.AgentNode, error) {
		var (
			n           core.AgentNode
			parent, typ *string
		)
		if err := r.Scan(&n.AgentID, &parent, &typ, &n.Calls); err != nil {
			return n, err
		}
		n.ParentAgentID, n.AgentType = str(parent), str(typ)
		return n, nil
	})
	if err != nil {
		return fail(err)
	}

	rows, err = tx.Query(ctx, `SELECT c.prompt_id, min(c.ts), count(*), `+usageSums+`, `+costSum+`
		FROM calls c WHERE c.session_key = $1 AND c.prompt_id IS NOT NULL
		GROUP BY c.prompt_id ORDER BY min(c.ts) DESC, c.prompt_id COLLATE "C"`, key)
	if err != nil {
		return fail(err)
	}
	d.Prompts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.PromptSummary, error) {
		var (
			p                         core.PromptSummary
			first                     time.Time
			in, cr, cw5, cw1, out, rs int64
			costM                     *int64
		)
		if err := r.Scan(&p.PromptID, &first, &p.Calls, &in, &cr, &cw5, &cw1, &out, &rs, &costM); err != nil {
			return p, err
		}
		p.FirstTS = first.UTC()
		p.Usage = core.Usage{
			InputUncached: int(in), CacheRead: int(cr), CacheWrite5m: int(cw5), CacheWrite1h: int(cw1),
			Output: int(out), Reasoning: int(rs),
		}
		p.Cost = micro(costM)
		return p, nil
	})
	if err != nil {
		return fail(err)
	}

	rows, err = tx.Query(ctx, `SELECT `+summaryColumns+` FROM calls c WHERE c.session_key = $1
		ORDER BY c.ts DESC, c.id DESC`, key)
	if err != nil {
		return fail(err)
	}
	d.Calls, err = pgx.CollectRows(rows, scanSummary)
	if err != nil {
		return fail(err)
	}
	return d, nil
}
