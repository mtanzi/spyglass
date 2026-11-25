package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/recorder"
)

// KeepAliveInterval is how often the live tail writes an SSE comment when no
// event is due, so proxies and the browser keep an idle stream open.
const KeepAliveInterval = 15 * time.Second

// EventCallFinished is the one SSE event type of the live tail (ADR 0025).
const EventCallFinished = "call.finished"

// Live is the recorder's live fan-out as the stream sees it.
// *recorder.Recorder implements it: Subscribe returns a channel of the
// finished calls the filter accepts and a function that ends the
// subscription. The channel closes when the recorder closes.
type Live interface {
	Subscribe(filter func(core.CallRecord) bool) (<-chan recorder.Event, func())
}

// knownStreamParams are the call-list filters, without the page parameters.
var knownStreamParams = func() map[string]bool {
	m := make(map[string]bool, len(knownCallParams))
	for name := range knownCallParams {
		if name != "limit" && name != "cursor" {
			m[name] = true
		}
	}
	return m
}()

// parseStreamFilter maps the stream's query onto a core.CallFilter with the
// call-list parser. A stream has no default time range: a side the query does
// not bound with from or to stays open, so calls that start after the stream
// opens still match.
func parseStreamFilter(q url.Values, now time.Time) (core.CallFilter, *apiError) {
	rest := maps.Clone(q)
	delete(rest, "from")
	delete(rest, "to")
	f, err := parseCallFilter(rest, now, knownStreamParams)
	if err != nil {
		return f, err
	}
	if f.From, err = timeParam(q, "from"); err != nil {
		return f, err
	}
	if f.To, err = timeParam(q, "to"); err != nil {
		return f, err
	}
	if !f.From.IsZero() && !f.To.IsZero() && !f.From.Before(f.To) {
		return f, badParam("from", "from must be before to")
	}
	return f, nil
}

// stream serves GET /admin/stream, the live tail of finished calls as SSE.
// Each matching call is one call.finished event whose id is the call id and
// whose data is the call-list row. There is no replay: a client that
// reconnects refetches the first page of the list. The stream ends when the
// client goes away, a write fails, or the recorder closes at shutdown.
func (h *handlers) stream(w http.ResponseWriter, r *http.Request) {
	if h.live == nil {
		notWired(w)
		return
	}
	f, perr := parseStreamFilter(r.URL.Query(), h.now())
	if perr != nil {
		writeError(w, perr)
		return
	}
	rc := http.NewResponseController(w)
	// The stream outlasts any fixed write deadline (ADR 0006).
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		internalError(w, r, err)
		return
	}

	byTool := len(f.Tools) > 0 || f.ToolError != nil
	events, unsubscribe := h.live.Subscribe(func(c core.CallRecord) bool {
		var tools []core.ToolCall
		if byTool {
			tools = liveToolCalls(c)
		}
		return f.Match(c, tools)
	})
	defer unsubscribe()

	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	// No proxy or browser may cache or buffer the stream; X-Accel-Buffering
	// turns buffering off in nginx.
	hd.Set("Cache-Control", "no-cache")
	hd.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if rc.Flush() != nil {
		return
	}

	keepAlive := time.NewTicker(h.keepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return // the recorder closed: the server is shutting down
			}
			if writeCallFinished(w, ev.Call) != nil || rc.Flush() != nil {
				return
			}
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// writeCallFinished writes one call.finished event. The JSON has no newline,
// so it fits one data line.
func writeCallFinished(w io.Writer, c core.CallRecord) error {
	data, err := json.Marshal(toCallSummary(summaryOf(c)))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", c.ID, EventCallFinished, data)
	return err
}

// summaryOf is the call-list row of a finished call.
func summaryOf(c core.CallRecord) core.CallSummary {
	return core.CallSummary{
		ID: c.ID, TS: c.TS, KeyName: c.KeyName, Client: c.Client, SessionKey: c.SessionKey, AgentID: c.AgentID,
		PromptID: c.PromptID, Project: c.Project, Provider: c.Provider, Model: c.Model, Status: c.Status, ErrorClass: c.ErrorClass, StopReason: c.StopReason,
		Streamed: c.Streamed, TTFT: c.TTFT, Duration: c.Duration, Usage: c.Usage, Cost: c.Cost,
		ToolCount: len(c.ToolUses), ContextCompacted: c.ContextCompacted,
	}
}

// liveToolCalls are the tool calls of a call as known when it finishes, for
// the tool and tool_error filters: each use the call carries, paired with a
// result in the same call (a server tool) or pending. A client tool's result
// arrives with a later call, so tool_error matches no client tool on the live
// tail.
func liveToolCalls(c core.CallRecord) []core.ToolCall {
	if len(c.ToolUses) == 0 {
		return nil
	}
	out := make([]core.ToolCall, 0, len(c.ToolUses))
	for _, u := range c.ToolUses {
		t := core.ToolCall{
			ToolCallID: u.ToolCallID, Name: u.Name, Source: u.Source, Status: core.ToolCallPending,
			SessionKey: c.SessionKey, AgentID: c.AgentID, UseCallID: c.ID, UseTS: c.TS, ArgBytes: u.ArgBytes,
		}
		for _, res := range c.ToolResults {
			if res.ToolCallID == u.ToolCallID {
				isErr := res.IsError
				t.Status, t.ResultCallID, t.ResultTS, t.IsError, t.ResultBytes = core.ToolCallPaired, c.ID, c.TS, &isErr, res.ResultBytes
				break
			}
		}
		out = append(out, t)
	}
	return out
}
