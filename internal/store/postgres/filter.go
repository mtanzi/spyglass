package postgres

import (
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mtanzi/spyglass/internal/core"
)

// where builds the WHERE clause of a call query. Values are only ever bind
// parameters; the SQL text holds column names and operators from this file.
type where struct {
	conds []string
	args  []any
}

// arg binds v and returns its placeholder.
func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

func (w *where) add(cond string) { w.conds = append(w.conds, cond) }

// sql returns the conditions joined by AND. A built filter always has the time
// bound, so it is never empty.
func (w *where) sql() string { return strings.Join(w.conds, " AND ") }

// textIn matches col against any of vals. The empty string means "not known",
// which the write path stores as NULL, so it also matches NULL when nullable.
func (w *where) textIn(col string, nullable bool, vals []string) {
	if len(vals) == 0 {
		return
	}
	var set []string
	unknown := false
	for _, v := range vals {
		if v == "" {
			unknown = true
			continue
		}
		set = append(set, clean(v))
	}
	var parts []string
	switch {
	case len(set) == 1:
		// A plain equality, unlike a one-element ANY, lets the planner read
		// an index on (col, ts) in ts order instead of sorting every match.
		parts = append(parts, col+" = "+w.arg(set[0]))
	case len(set) > 1:
		parts = append(parts, col+" = ANY("+w.arg(set)+"::text[])")
	}
	if unknown {
		if nullable {
			parts = append(parts, col+" IS NULL")
		} else {
			parts = append(parts, col+" = ''")
		}
	}
	w.add(group(parts))
}

// textEq matches col against one non-empty value.
func (w *where) textEq(col, v string) {
	if v != "" {
		w.add(col + " = " + w.arg(clean(v)))
	}
}

// uuidIn matches col against any of ids.
func (w *where) uuidIn(col string, ids []core.ID) {
	if len(ids) == 0 {
		return
	}
	set := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		set[i] = uuid(id)
	}
	w.add(col + " = ANY(" + w.arg(set) + "::uuid[])")
}

func group(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

// buildWhere maps a filter to a WHERE clause over `calls c`. The filter must
// have passed Validate. Every field of core.CallFilter has a branch here.
func buildWhere(f core.CallFilter) *where {
	w := &where{}
	w.add("c.ts >= " + w.arg(f.From))
	w.add("c.ts < " + w.arg(f.To))

	w.textIn("c.key_name", false, f.Keys)
	w.textIn("c.client", true, f.Clients)
	w.textIn("c.provider", false, f.Providers)
	w.textIn("c.model", true, f.Models)
	w.textIn("c.endpoint", false, f.Endpoints)
	if f.Streamed != nil {
		w.add("c.streamed = " + w.arg(*f.Streamed))
	}
	if len(f.Statuses) > 0 {
		var set []int64
		unknown := false
		for _, s := range f.Statuses {
			if s == 0 {
				unknown = true
				continue
			}
			set = append(set, int64(s))
		}
		var parts []string
		switch {
		case len(set) == 1:
			parts = append(parts, "c.status = "+w.arg(set[0])+"::bigint")
		case len(set) > 1:
			parts = append(parts, "c.status = ANY("+w.arg(set)+"::bigint[])")
		}
		if unknown {
			parts = append(parts, "c.status IS NULL")
		}
		w.add(group(parts))
	}
	w.textIn("c.error_class", true, f.ErrorClasses)
	w.textEq("c.session_key", f.SessionKey)
	w.textEq("c.agent_id", f.AgentID)
	w.textIn("c.agent_type", true, f.AgentTypes)
	w.textEq("c.prompt_id", f.PromptID)
	w.textIn("c.request_class", true, f.RequestClasses)
	w.tools(f)
	if f.MinLatency > 0 {
		// duration_ms is stored truncated to the millisecond.
		ms := (f.MinLatency + time.Millisecond - 1) / time.Millisecond
		w.add("c.duration_ms >= " + w.arg(int64(ms)) + "::bigint")
	}
	if f.MinCost > 0 {
		// cost_usd is NULL when unknown, and NULL never satisfies >=.
		w.add("c.cost_usd >= " + w.arg(cost(&f.MinCost)))
	}
	return w
}

// tools adds the Tools and ToolError conditions. Both hold on the same tool
// call: a use carried by this call, and when ToolError is set, the result with
// the same tool call id.
func (w *where) tools(f core.CallFilter) {
	if len(f.Tools) == 0 && f.ToolError == nil {
		return
	}
	inner := &where{args: w.args}
	inner.add("u.call_id = c.id")
	inner.add("u.kind = 'use'")
	inner.textIn("u.tool_name", true, f.Tools)
	join := ""
	if f.ToolError != nil {
		join = " JOIN tool_events r ON r.tool_call_id = u.tool_call_id AND r.kind = 'result'"
		inner.add("r.is_error = " + inner.arg(*f.ToolError))
	}
	w.args = inner.args
	w.add("EXISTS (SELECT 1 FROM tool_events u" + join + " WHERE " + inner.sql() + ")")
}
