package api

import (
	"errors"
	"net/http"

	"github.com/mtanzi/spyglass/internal/core"
)

// readError answers a failed reader call: a filter the reader refuses is the
// client's mistake, a missing item is a 404 and anything else is internal.
func readError(w http.ResponseWriter, r *http.Request, err error, what string) {
	switch {
	case errors.Is(err, core.ErrNotFound):
		writeError(w, &apiError{status: http.StatusNotFound, typ: ErrNotFound, message: "no such " + what})
	case errors.Is(err, core.ErrInvalidFilter):
		writeError(w, &apiError{status: http.StatusBadRequest, typ: ErrInvalidParameter, message: "the query is not valid"})
	default:
		internalError(w, r, err)
	}
}

func nextCursor(c *core.Cursor) *string {
	if c == nil {
		return nil
	}
	s := encodeCursor(*c)
	return &s
}

// listSessions serves GET /admin/sessions.
func (h *handlers) listSessions(w http.ResponseWriter, r *http.Request) {
	if h.calls == nil {
		notWired(w)
		return
	}
	p, perr := parseSessionParams(r.URL.Query(), h.now())
	if perr != nil {
		writeError(w, perr)
		return
	}
	rows, next, err := h.calls.ListSessions(r.Context(), p.Filter, p.Page)
	if err != nil {
		readError(w, r, err, "session")
		return
	}
	out := sessionListJSON{Sessions: make([]sessionSummaryJSON, 0, len(rows)), NextCursor: nextCursor(next)}
	for _, s := range rows {
		out.Sessions = append(out.Sessions, toSessionSummary(s))
	}
	writeJSON(w, http.StatusOK, out)
}

// getSession serves GET /admin/sessions/{key}. The key is the session header as
// the client sent it, so it can hold characters such as ':' or '/', which the
// client percent-encodes in the path. PathValue returns it decoded.
func (h *handlers) getSession(w http.ResponseWriter, r *http.Request) {
	if h.calls == nil {
		notWired(w)
		return
	}
	d, err := h.calls.GetSession(r.Context(), r.PathValue("key"))
	if err != nil {
		readError(w, r, err, "session")
		return
	}
	writeJSON(w, http.StatusOK, toSessionDetail(d))
}

// listToolCalls serves GET /admin/tool-calls.
func (h *handlers) listToolCalls(w http.ResponseWriter, r *http.Request) {
	if h.calls == nil {
		notWired(w)
		return
	}
	p, perr := parseToolCallParams(r.URL.Query(), h.now())
	if perr != nil {
		writeError(w, perr)
		return
	}
	rows, next, err := h.calls.ListToolCalls(r.Context(), p.Filter, p.Page)
	if err != nil {
		readError(w, r, err, "tool call")
		return
	}
	out := toolCallListJSON{ToolCalls: make([]toolCallJSON, 0, len(rows)), NextCursor: nextCursor(next)}
	for _, t := range rows {
		out.ToolCalls = append(out.ToolCalls, toToolCall(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// toolSummary serves GET /admin/tool-calls/summary.
func (h *handlers) toolSummary(w http.ResponseWriter, r *http.Request) {
	if h.calls == nil {
		notWired(w)
		return
	}
	f, perr := parseToolCallFilter(r.URL.Query(), h.now(), knownToolSummaryParams)
	if perr != nil {
		writeError(w, perr)
		return
	}
	rows, err := h.calls.ToolSummary(r.Context(), f)
	if err != nil {
		readError(w, r, err, "tool call")
		return
	}
	out := toolSummaryJSON{Tools: make([]toolStatJSON, 0, len(rows))}
	for _, s := range rows {
		out.Tools = append(out.Tools, toolStatJSON{
			Name: s.Name, AgentType: s.AgentType, Count: s.Count, Errors: s.Errors, ErrorRate: s.ErrorRate(),
			DurationP50Ms: s.DurationP50.Milliseconds(), DurationP95Ms: s.DurationP95.Milliseconds(),
			TotalDurationMs: s.TotalDuration.Milliseconds(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// getUsage serves GET /admin/usage.
func (h *handlers) getUsage(w http.ResponseWriter, r *http.Request) {
	if h.calls == nil {
		notWired(w)
		return
	}
	q, perr := parseUsageParams(r.URL.Query(), h.now())
	if perr != nil {
		writeError(w, perr)
		return
	}
	rows, err := h.calls.Usage(r.Context(), q)
	if err != nil {
		readError(w, r, err, "usage")
		return
	}
	out := usageSeriesJSON{Grain: q.Grain, Buckets: make([]usageBucketJSON, 0, len(rows))}
	if q.GroupBy != core.GroupNone {
		out.GroupBy = &q.GroupBy
	}
	for _, b := range rows {
		jb := usageBucketJSON{
			Start: b.Start.UTC(), Group: b.Group, Calls: b.Calls, Errors: b.Errors, Usage: toUsage(b.Usage),
			CostMicroUSD: int64(b.Cost), UnpricedCalls: b.UnpricedCalls, EstimatedCalls: b.EstimatedCalls,
			NoUsageCalls: b.NoUsageCalls, LatencyP50Ms: b.LatencyP50.Milliseconds(),
			LatencyP95Ms: b.LatencyP95.Milliseconds(), LatencyP99Ms: b.LatencyP99.Milliseconds(),
		}
		if ratio, ok := b.CacheHitRatio(); ok {
			jb.CacheHitRatio = &ratio
		}
		out.Buckets = append(out.Buckets, jb)
	}
	writeJSON(w, http.StatusOK, out)
}

// getPromptCosts serves GET /admin/usage/prompts.
func (h *handlers) getPromptCosts(w http.ResponseWriter, r *http.Request) {
	if h.calls == nil {
		notWired(w)
		return
	}
	q, perr := parsePromptCostParams(r.URL.Query(), h.now())
	if perr != nil {
		writeError(w, perr)
		return
	}
	res, err := h.calls.PromptCosts(r.Context(), q)
	if err != nil {
		readError(w, r, err, "usage")
		return
	}
	out := promptCostsJSON{
		Prompts: make([]promptCostJSON, 0, len(res.Prompts)), PromptCount: res.PromptCount,
		CallsWithoutPrompt: res.CallsWithoutPrompt,
	}
	for _, p := range res.Prompts {
		jp := promptCostJSON{
			PromptID: p.PromptID, SessionKey: p.SessionKey, Calls: p.Calls,
			UnpricedCalls: p.UnpricedCalls, APIEquivalent: p.APIEquivalent,
		}
		if p.Cost != nil {
			c := int64(*p.Cost)
			jp.CostMicroUSD = &c
		}
		out.Prompts = append(out.Prompts, jp)
	}
	writeJSON(w, http.StatusOK, out)
}
