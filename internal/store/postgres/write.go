package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mtanzi/spyglass/internal/core"
)

// maxParams is the Postgres limit of bind parameters in one statement.
const maxParams = 65535

var callColumns = []string{
	"id", "ts", "key_name", "client", "client_raw", "client_request_id",
	"session_key", "agent_id", "parent_agent_id", "agent_type", "prompt_id", "request_class",
	"compaction", "context_compacted", "trace_id", "project",
	"family", "endpoint", "auth_mode", "provider", "model", "upstream_request_id",
	"status", "error_class", "error_type", "stop_reason", "streamed",
	"ttfb_ms", "ttft_ms", "duration_ms", "ended_at",
	"input_uncached", "cache_read", "cache_write_5m", "cache_write_1h", "output", "reasoning",
	"usage_raw", "usage_estimated", "extraction_partial",
	"cost_usd", "pricing_version", "prev_tool_durations",
}

var toolEventColumns = []string{
	"call_id", "idx", "ts", "kind", "source", "tool_call_id", "tool_name", "bytes", "is_error",
	"session_key", "agent_id",
}

// WriteBatch inserts the calls with their tool halves in one transaction and
// returns after commit. A call whose id is already stored is skipped with its
// halves, so replaying a batch inserts nothing. A failed batch leaves nothing.
// An error caused by the content of a record matches core.ErrUnwritable
// (classify); every other error is transient.
func (s *Store) WriteBatch(ctx context.Context, calls []core.CallRecord) error {
	if len(calls) == 0 {
		return nil
	}
	callRows := make([][]any, 0, len(calls))
	var toolRows [][]any
	for i := range calls {
		c := &calls[i]
		callRows = append(callRows, callRow(c))
		toolRows = appendToolRows(toolRows, c)
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := insert(ctx, tx, "calls", callColumns, callRows); err != nil {
			return err
		}
		return insert(ctx, tx, "tool_events", toolEventColumns, toolRows)
	})
	if err != nil {
		return fmt.Errorf("postgres: write batch: %w", err)
	}
	return nil
}

// insert runs multi-row INSERT … ON CONFLICT DO NOTHING statements, as many
// rows per statement as the parameter limit allows.
func insert(ctx context.Context, tx pgx.Tx, table string, cols []string, rows [][]any) error {
	perStmt := maxParams / len(cols)
	for len(rows) > 0 {
		chunk := rows[:min(perStmt, len(rows))]
		rows = rows[len(chunk):]
		var sql strings.Builder
		fmt.Fprintf(&sql, "INSERT INTO %s (%s) VALUES ", table, strings.Join(cols, ", "))
		args := make([]any, 0, len(chunk)*len(cols))
		for i, row := range chunk {
			if i > 0 {
				sql.WriteString(", ")
			}
			sql.WriteByte('(')
			for j, v := range row {
				if j > 0 {
					sql.WriteString(", ")
				}
				args = append(args, v)
				sql.WriteByte('$')
				sql.WriteString(strconv.Itoa(len(args)))
			}
			sql.WriteByte(')')
		}
		sql.WriteString(" ON CONFLICT DO NOTHING")
		if _, err := tx.Exec(ctx, sql.String(), args...); err != nil {
			return fmt.Errorf("insert %s: %w", table, classify(err, cols))
		}
	}
	return nil
}

// callRow maps a call record to the values of callColumns, in order.
func callRow(c *core.CallRecord) []any {
	u := c.Usage
	return []any{
		uuid(c.ID), c.TS, clean(c.KeyName), text(c.Client), text(c.ClientRaw), text(c.ClientRequestID),
		text(c.SessionKey), text(c.AgentID), text(c.ParentAgentID), text(c.AgentType), text(c.PromptID), text(c.RequestClass),
		c.Compaction, c.ContextCompacted, text(c.TraceID), text(c.Project),
		clean(c.Family), clean(c.Endpoint), clean(c.AuthMode), clean(c.Provider), text(c.Model), text(c.UpstreamRequestID),
		status(c.Status), text(c.ErrorClass), text(c.ErrorType), text(c.StopReason), c.Streamed,
		millis(c.TTFB), millis(c.TTFT), millis(c.Duration), timestamp(c.EndedAt),
		u.InputUncached, u.CacheRead, u.CacheWrite5m, u.CacheWrite1h, u.Output, u.Reasoning,
		usageRaw(c.UsageRaw), c.UsageEstimated, c.ExtractionPartial,
		cost(c.Cost), text(c.PricingVersion), text(c.PrevToolDurations),
	}
}

// appendToolRows appends the values of toolEventColumns for each tool half of
// c. The index is the position within the call: results first, in request
// order, then uses, in response order. It depends only on the record, so a
// replayed call produces the same keys. A client result is timed at request
// start. A use or a server result is timed at response end, or at request start
// when the end is not known.
func appendToolRows(rows [][]any, c *core.CallRecord) [][]any {
	id, session, agent := uuid(c.ID), text(c.SessionKey), text(c.AgentID)
	endTS := c.EndedAt
	if endTS.IsZero() {
		endTS = c.TS
	}
	idx := 0
	for _, r := range c.ToolResults {
		ts := c.TS
		if r.Source == core.ToolSourceServer {
			ts = endTS
		}
		rows = append(rows, []any{
			id, idx, ts, "result", clean(string(r.Source)), clean(r.ToolCallID), nil, r.ResultBytes, r.IsError,
			session, agent,
		})
		idx++
	}
	for _, u := range c.ToolUses {
		rows = append(rows, []any{
			id, idx, endTS, "use", clean(string(u.Source)), clean(u.ToolCallID), text(u.Name), u.ArgBytes, nil,
			session, agent,
		})
		idx++
	}
	return rows
}

func uuid(id core.ID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// clean makes s storable in a Postgres text column: NUL bytes are dropped and
// invalid UTF-8 is replaced with U+FFFD.
func clean(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "\uFFFD")
}

// text maps the empty string, which means "not known", to NULL.
func text(s string) any {
	if s = clean(s); s == "" {
		return nil
	}
	return s
}

func status(code int) any {
	if code == 0 {
		return nil
	}
	return code
}

func millis(d time.Duration) any {
	if d == 0 {
		return nil
	}
	return d.Milliseconds()
}

func timestamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// unstorableEscape matches the escapes jsonb may reject: \u0000, and the
// surrogate escapes, which it rejects when unpaired.
var unstorableEscape = regexp.MustCompile(`\\u(0000|[dD][89a-fA-F])`)

// usageRaw maps a usage object that jsonb would reject to NULL: empty, not
// valid JSON, not valid UTF-8, or holding an unstorable escape.
func usageRaw(raw []byte) any {
	if len(raw) == 0 || !json.Valid(raw) || !utf8.Valid(raw) || unstorableEscape.Match(raw) {
		return nil
	}
	return raw
}

// cost maps MicroUSD to numeric(12,6) exactly: the count of millionths with
// exponent -6.
func cost(c *core.MicroUSD) any {
	if c == nil {
		return nil
	}
	return pgtype.Numeric{Int: big.NewInt(int64(*c)), Exp: -6, Valid: true}
}
