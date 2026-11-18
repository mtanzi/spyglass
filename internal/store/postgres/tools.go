package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mtanzi/spyglass/internal/core"
)

// Tool calls are derived when read (ADR 0020). A tool_events row of kind
// 'use' is joined to the earliest row of kind 'result' with the same
// tool_call_id. Provider ids are unique on their own, so the join ignores the
// session key. The pairing reaches core.ToolResultLookahead past the To bound
// of the query, and a use with no result for core.ToolIdleAfter reads as
// use_only. Nothing here is stored: there is no tool_calls table or view, so
// the two constants stay in Go.

// maxHeaderMS bounds a duration taken from prev_tool_durations: a year in
// milliseconds. A larger value is not a tool duration, and the gap between the
// use and the result stands in for it.
const maxHeaderMS = 365 * 24 * 60 * 60 * 1000

// ListToolCalls returns one page of tool calls matching f, newest first. A
// paired or use-only tool call is placed at its use; a result-only one at its
// result. The cursor is nil on the last page.
func (s *Store) ListToolCalls(ctx context.Context, f core.ToolCallFilter, p core.Page) ([]core.ToolCall, *core.Cursor, error) {
	if err := f.Validate(); err != nil {
		return nil, nil, err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)

	sql, args := listToolCallsSQL(f, p.After, limit, time.Now())
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: list tool calls: %w", err)
	}
	type row struct {
		call core.ToolCall
		cur  core.Cursor
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var (
			x                                    row
			name, session, agent, source, status *string
			durSrc                               *string
			useCall, resultCall, sortCall        pgtype.UUID
			useTS, resultTS                      *time.Time
			dur                                  *int64
			sortIdx                              int32
			argBytes, resultBytes                int32
			sortTS                               time.Time
			toolCallID                           string
		)
		err := r.Scan(&toolCallID, &name, &source, &status, &session, &agent,
			&useCall, &resultCall, &useTS, &resultTS, &dur, &durSrc,
			&x.call.IsError, &argBytes, &resultBytes, &sortTS, &sortCall, &sortIdx)
		if err != nil {
			return x, err
		}
		c := &x.call
		c.ToolCallID, c.Name, c.Source, c.Status = toolCallID, str(name), core.ToolSource(str(source)), core.ToolCallStatus(str(status))
		c.SessionKey, c.AgentID = str(session), str(agent)
		if useCall.Valid {
			c.UseCallID = core.ID(useCall.Bytes)
		}
		if resultCall.Valid {
			c.ResultCallID = core.ID(resultCall.Bytes)
		}
		if useTS != nil {
			c.UseTS = useTS.UTC()
		}
		if resultTS != nil {
			c.ResultTS = resultTS.UTC()
		}
		if dur != nil {
			c.Duration, c.DurationSource = time.Duration(*dur)*time.Millisecond, core.DurationSource(str(durSrc))
		}
		c.ArgBytes, c.ResultBytes = int(argBytes), int(resultBytes)
		x.cur = core.Cursor{TS: sortTS.UTC(), ID: core.ID(sortCall.Bytes), Idx: int(sortIdx)}
		return x, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: list tool calls: %w", err)
	}
	var next *core.Cursor
	if len(got) > limit {
		got = got[:limit]
		next = &got[limit-1].cur
	}
	out := make([]core.ToolCall, len(got))
	for i, g := range got {
		out[i] = g.call
	}
	return out, next, nil
}

// ToolSummary returns the statistics of the tool calls matching f, one row per
// tool name and agent type, ordered by name and then agent type. The agent type
// is that of the call that carried the use, or the result for a result-only
// tool call.
func (s *Store) ToolSummary(ctx context.Context, f core.ToolCallFilter) ([]core.ToolStat, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	sql, args := toolSummarySQL(f, time.Now())
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: tool summary: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.ToolStat, error) {
		var (
			st       core.ToolStat
			p50, p95 *float64
			total    int64
		)
		if err := r.Scan(&st.Name, &st.AgentType, &st.Count, &st.Errors, &p50, &p95, &total); err != nil {
			return st, err
		}
		st.DurationP50, st.DurationP95 = millisFloat(p50), millisFloat(p95)
		st.TotalDuration = time.Duration(total) * time.Millisecond
		return st, nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: tool summary: %w", err)
	}
	return out, nil
}

// listToolCallsSQL returns the statement and arguments of one page of tool calls.
func listToolCallsSQL(f core.ToolCallFilter, after *core.Cursor, limit int, now time.Time) (string, []any) {
	w := &where{}
	union := toolCallsSQL(w, f, now, after, pushLimit(f, limit))
	conds := toolOuterConds(w, f)
	sql := `SELECT t.tool_call_id, t.name, t.source, t.status, t.session_key, t.agent_id,
		t.use_call_id, t.result_call_id, t.use_ts, t.result_ts, t.dur_ms, t.dur_src,
		t.is_error, t.arg_bytes, t.result_bytes, t.sort_ts, t.sort_call, t.sort_idx
		FROM (` + union + `) t` + whereClause(conds) +
		` ORDER BY t.sort_ts DESC, t.sort_call DESC, t.sort_idx DESC LIMIT ` + w.arg(limit+1)
	return sql, w.args
}

// toolSummarySQL returns the statement and arguments of the tool summary.
func toolSummarySQL(f core.ToolCallFilter, now time.Time) (string, []any) {
	w := &where{}
	union := toolCallsSQL(w, f, now, nil, 0)
	conds := toolOuterConds(w, f)
	sql := `SELECT coalesce(t.name, ''), coalesce(c.agent_type, ''), count(*),
		count(*) FILTER (WHERE t.is_error),
		percentile_cont(0.5) WITHIN GROUP (ORDER BY t.dur_ms) FILTER (WHERE t.dur_ms IS NOT NULL),
		percentile_cont(0.95) WITHIN GROUP (ORDER BY t.dur_ms) FILTER (WHERE t.dur_ms IS NOT NULL),
		coalesce(sum(t.dur_ms), 0)::bigint
		FROM (` + union + `) t
		LEFT JOIN calls c ON c.id = coalesce(t.use_call_id, t.result_call_id)` + whereClause(conds) +
		` GROUP BY 1, 2 ORDER BY coalesce(t.name, '') COLLATE "C", coalesce(c.agent_type, '') COLLATE "C"`
	return sql, w.args
}

// millisFloat converts a duration in fractional milliseconds, rounded to the
// nearest millisecond. NULL, which means no duration is known, reads as zero.
func millisFloat(p *float64) time.Duration {
	if p == nil {
		return 0
	}
	return time.Duration(*p+0.5) * time.Millisecond
}

func whereClause(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + joinAnd(conds)
}

func joinAnd(conds []string) string {
	w := where{conds: conds}
	return w.sql()
}

// collect runs fn against a scratch where that shares w's bind parameters, and
// returns the conditions fn added. It lets one filter be written once and
// applied to several branches of a query.
func (w *where) collect(fn func(*where)) []string {
	t := &where{args: w.args}
	fn(t)
	w.args = t.args
	return t.conds
}

// toolCallsSQL returns a query that yields every tool call of the time range
// of f, one row each, with the columns the list and the summary read. It
// holds only conditions that cut rows early: the time range, the session, the
// agent, the tool names, the sources and the argument size on uses, and the
// range, session, agent and source on results. toolOuterConds applies the whole
// filter on top of it, because a status, an error flag, a duration and a result
// size are known only after the join.
//
//	use branch     a use in [From, To), left joined to its earliest result before
//	               To + core.ToolResultLookahead
//	result branch  a result in [From, To) whose tool call id has no use at all
//
// limit, when positive, cuts each branch to its newest rows before the
// joins, so a page costs the joins of one page, not of the whole range. It is
// valid only when the rest of the filter needs no join (see pushLimit).
//
// after, when set, keeps the rows strictly older than the cursor, in the order
// (sort_ts, sort_call, sort_idx), which is that of the listed half.
func toolCallsSQL(w *where, f core.ToolCallFilter, now time.Time, after *core.Cursor, limit int) string {
	from, to := w.arg(f.From), w.arg(f.To)
	lookahead := w.arg(f.To.Add(core.ToolResultLookahead))
	idleCutoff := w.arg(now.Add(-core.ToolIdleAfter))

	sources := make([]string, len(f.Sources))
	for i, s := range f.Sources {
		sources[i] = string(s)
	}
	cursor := func(alias string) string {
		if after == nil {
			return ""
		}
		return alias + ".ts, " + alias + ".call_id, " + alias + ".idx"
	}

	useConds := []string{"u.kind = 'use'", "u.ts >= " + from, "u.ts < " + to}
	useConds = append(useConds, w.collect(func(t *where) {
		t.textEq("u.session_key", f.SessionKey)
		t.textEq("u.agent_id", f.AgentID)
		t.textIn("u.tool_name", true, f.Tools)
		t.textIn("u.source", false, sources)
		if f.MinArgBytes > 0 {
			t.add("u.bytes >= " + t.arg(int64(f.MinArgBytes)) + "::bigint")
		}
	})...)
	resConds := []string{"r.kind = 'result'", "r.ts >= " + from, "r.ts < " + to}
	resConds = append(resConds, w.collect(func(t *where) {
		t.textEq("r.session_key", f.SessionKey)
		t.textEq("r.agent_id", f.AgentID)
		t.textIn("r.source", false, sources)
	})...)
	if after != nil {
		cur := "(" + w.arg(after.TS) + ", " + w.arg(uuid(after.ID)) + ", " + w.arg(int32(after.Idx)) + ")"
		useConds = append(useConds, "("+cursor("u")+") < "+cur)
		resConds = append(resConds, "("+cursor("r")+") < "+cur)
	}

	order := ""
	if limit > 0 {
		order = " ORDER BY ts DESC, call_id DESC, idx DESC LIMIT " + strconv.Itoa(limit)
	}

	// The header is a list of name=ms entries joined by ';', in call order,
	// with names percent-encoded. The k-th well-formed entry for a name goes to
	// the k-th client use of that name, in use order, among the uses whose
	// earliest result is in the resolving call. A malformed entry is skipped; a
	// value above maxHeaderMS keeps its place but yields no duration, and the
	// gap is used. Server-side tools run inside one response, so the header
	// never applies.
	return `
SELECT u.tool_call_id, u.tool_name AS name, u.source,
	CASE WHEN u.rcall IS NOT NULL THEN 'paired'
	     WHEN u.ts <= ` + idleCutoff + ` THEN 'use_only'
	     ELSE 'pending' END AS status,
	u.session_key, u.agent_id, u.call_id AS use_call_id, u.rcall AS result_call_id,
	u.ts AS use_ts, u.rts AS result_ts,
	CASE WHEN u.rcall IS NULL THEN NULL
	     ELSE coalesce(u.hms, greatest(0, round(extract(epoch FROM u.rts - u.ts) * 1000))::bigint) END AS dur_ms,
	CASE WHEN u.rcall IS NULL THEN NULL
	     WHEN u.hms IS NOT NULL THEN 'header'
	     ELSE 'estimated' END AS dur_src,
	u.ris_error AS is_error, u.bytes AS arg_bytes, coalesce(u.rbytes, 0) AS result_bytes,
	u.ts AS sort_ts, u.call_id AS sort_call, u.idx AS sort_idx
FROM (
	WITH paired AS MATERIALIZED (
		SELECT u.*, r.call_id AS rcall, r.ts AS rts, r.is_error AS ris_error, r.bytes AS rbytes
		FROM (SELECT * FROM tool_events u WHERE ` + joinAnd(useConds) + order + `) u
		LEFT JOIN LATERAL (
			SELECT re.call_id, re.ts, re.is_error, re.bytes FROM tool_events re
			WHERE re.kind = 'result' AND re.tool_call_id = u.tool_call_id AND re.ts < ` + lookahead + `
			ORDER BY re.ts, re.call_id, re.idx LIMIT 1
		) r ON true
	), resolving AS MATERIALIZED (
		SELECT DISTINCT p.rcall FROM paired p JOIN calls c ON c.id = p.rcall
		WHERE p.source = 'client' AND c.prev_tool_durations IS NOT NULL
	), ranked AS MATERIALIZED (
		-- The rank of each client use among the uses its resolving call answers,
		-- per tool name, in use order. Computed once per resolving call.
		SELECT x.rcall, x.call_id, x.idx,
			row_number() OVER (PARTITION BY x.rcall, x.name ORDER BY x.ts, x.call_id, x.idx) AS k
		FROM (
			SELECT DISTINCT re.call_id AS rcall, u2.call_id, u2.idx, u2.ts, convert_to(u2.tool_name, 'UTF8') AS name
			FROM tool_events re
			JOIN tool_events u2 ON u2.kind = 'use' AND u2.source = 'client' AND u2.tool_call_id = re.tool_call_id
			WHERE re.call_id IN (SELECT rcall FROM resolving) AND re.kind = 'result'
			  AND NOT EXISTS (SELECT 1 FROM tool_events r3 WHERE r3.kind = 'result' AND r3.tool_call_id = re.tool_call_id
				AND (r3.ts, r3.call_id, r3.idx) < (re.ts, re.call_id, re.idx))
		) x
	), header AS MATERIALIZED (
		-- The well-formed entries of each resolving call's header, parsed once,
		-- with the percent-encoded name decoded, ranked per name in header order.
		SELECT e.call_id, e.name, e.ms,
			row_number() OVER (PARTITION BY e.call_id, e.name ORDER BY e.ord) AS k
		FROM (
			SELECT c.id AS call_id, s.ord,
				(SELECT string_agg(CASE WHEN left(m.p[1], 1) = '%' THEN decode(substr(m.p[1], 2), 'hex')
				                        ELSE convert_to(m.p[1], 'UTF8') END, ''::bytea ORDER BY m.o)
				 FROM regexp_matches(split_part(s.entry, '=', 1), '%[0-9A-Fa-f]{2}|[^%]+', 'g') WITH ORDINALITY AS m(p, o)) AS name,
				CASE WHEN split_part(s.entry, '=', 2)::numeric <= ` + fmt.Sprint(maxHeaderMS) + `
				     THEN split_part(s.entry, '=', 2)::numeric::bigint END AS ms
			FROM calls c
			CROSS JOIN LATERAL string_to_table(c.prev_tool_durations, ';') WITH ORDINALITY AS s(entry, ord)
			WHERE c.id IN (SELECT rcall FROM resolving)
			  AND s.entry ~ '^([^%=]|%[0-9A-Fa-f]{2})+=[0-9]+$'
		) e
	)
	SELECT p.*, h.ms AS hms
	FROM paired p
	LEFT JOIN ranked k ON p.source = 'client' AND k.rcall = p.rcall AND k.call_id = p.call_id AND k.idx = p.idx
	LEFT JOIN header h ON h.call_id = k.rcall AND h.name = convert_to(p.tool_name, 'UTF8') AND h.k = k.k
) u
UNION ALL
SELECT r.tool_call_id, NULL::text, r.source, 'result_only',
	r.session_key, r.agent_id, NULL::uuid, r.call_id,
	NULL::timestamptz, r.ts,
	NULL::bigint, NULL::text,
	r.is_error, 0, r.bytes,
	r.ts, r.call_id, r.idx
FROM (SELECT * FROM tool_events r WHERE ` + joinAnd(resConds) + `
	AND NOT EXISTS (SELECT 1 FROM tool_events u WHERE u.kind = 'use' AND u.tool_call_id = r.tool_call_id)
	AND NOT EXISTS (SELECT 1 FROM tool_events r2 WHERE r2.kind = 'result' AND r2.tool_call_id = r.tool_call_id
		AND (r2.ts, r2.call_id, r2.idx) < (r.ts, r.call_id, r.idx))` + order + `) r`
}

// toolOuterConds maps the whole of f to conditions over the columns of the
// query toolCallsSQL builds, aliased t. The time range is not repeated.
func toolOuterConds(w *where, f core.ToolCallFilter) []string {
	sources := make([]string, len(f.Sources))
	for i, s := range f.Sources {
		sources[i] = string(s)
	}
	statuses := make([]string, len(f.Statuses))
	for i, s := range f.Statuses {
		statuses[i] = string(s)
	}
	return w.collect(func(t *where) {
		t.textEq("t.session_key", f.SessionKey)
		t.textEq("t.agent_id", f.AgentID)
		t.textIn("t.name", true, f.Tools)
		t.textIn("t.source", false, sources)
		t.textIn("t.status", false, statuses)
		if f.IsError != nil {
			// NULL, a tool call with no result, never equals a bind value.
			t.add("t.is_error = " + t.arg(*f.IsError))
		}
		if f.MinDuration > 0 {
			ms := (f.MinDuration + time.Millisecond - 1) / time.Millisecond
			t.add("t.dur_ms >= " + t.arg(int64(ms)) + "::bigint")
		}
		if f.MinArgBytes > 0 {
			t.add("t.arg_bytes >= " + t.arg(int64(f.MinArgBytes)) + "::bigint")
		}
		if f.MinResultBytes > 0 {
			t.add("t.result_bytes >= " + t.arg(int64(f.MinResultBytes)) + "::bigint")
		}
	})
}

// pushLimit returns the row count to cut each branch of toolCallsSQL to for a
// page of limit rows, or zero when the filter needs the join first: a status,
// an error flag, a duration or a result size is known only after pairing.
func pushLimit(f core.ToolCallFilter, limit int) int {
	if len(f.Statuses) > 0 || f.IsError != nil || f.MinDuration > 0 || f.MinResultBytes > 0 {
		return 0
	}
	return limit + 1
}
