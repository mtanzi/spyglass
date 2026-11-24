package proxy

import (
	"net/http"
	"strings"

	"github.com/mtanzi/spyglass/internal/core"
)

// maxCorrelationValue bounds a correlation value. A longer value is malformed
// and stored as null, so a hostile header cannot bloat the call row.
const maxCorrelationValue = 256

// requestClasses are the values of x-claude-code-request-class the proxy stores.
var requestClasses = map[string]bool{
	"main": true, "subagent": true, "workflow": true, "compaction": true, "auxiliary": true,
}

// correlationFields is the header table. Each row reads one header into one
// field of the call record. A missing or malformed value leaves the field at
// its zero value, which the store writes as null.
var correlationFields = []struct {
	header string
	set    func(*core.CallRecord, string)
	valid  func(string) bool // nil accepts any value within the length bound
}{
	{"X-Claude-Code-Session-Id", func(c *core.CallRecord, v string) { c.SessionKey = v }, nil},
	{"X-Claude-Code-Agent-Id", func(c *core.CallRecord, v string) { c.AgentID = v }, nil},
	{"X-Claude-Code-Parent-Agent-Id", func(c *core.CallRecord, v string) { c.ParentAgentID = v }, nil},
	{"X-Claude-Code-Agent-Type", func(c *core.CallRecord, v string) { c.AgentType = v }, nil},
	{"X-Claude-Code-Prompt-Id", func(c *core.CallRecord, v string) { c.PromptID = v }, nil},
	{"X-Claude-Code-Request-Class", func(c *core.CallRecord, v string) { c.RequestClass = v }, func(v string) bool { return requestClasses[v] }},
	{"X-Claude-Code-Compaction", func(c *core.CallRecord, v string) { c.Compaction = v == "true" }, isBool},
	{"X-Claude-Code-Context-Compacted", func(c *core.CallRecord, v string) { c.ContextCompacted = v == "true" }, isBool},
	{"X-Claude-Code-Prev-Tool-Durations", func(c *core.CallRecord, v string) { c.PrevToolDurations = v }, nil},
	{"X-Spyglass-Project", func(c *core.CallRecord, v string) { c.Project = v }, func(v string) bool { _, ok := core.ProjectName(v); return ok }},
	{"Traceparent", func(c *core.CallRecord, v string) { c.TraceID = v[3:35] }, isTraceparent},
}

// Correlate reads the harness headers of h into rec. It reads only the headers
// in the table and User-Agent, never a credential header, and never fails: an
// unknown or malformed value is left unset. It does not change h; the handler
// removes x-spyglass-project, which is not a harness header, before the
// upstream request.
func Correlate(h http.Header, rec *core.CallRecord) {
	for _, f := range correlationFields {
		v := strings.TrimSpace(h.Get(f.header))
		if v == "" || len(v) > maxCorrelationValue || (f.valid != nil && !f.valid(v)) {
			continue
		}
		f.set(rec, v)
	}
	ua := h.Get("User-Agent")
	if len(ua) > maxCorrelationValue {
		ua = ""
	}
	rec.ClientRaw = ua
	rec.Client = ClientName(ua)
}

// ClientName reduces a User-Agent value to its product token: the text before
// the first "/" or space, so "claude-cli/2.1.290 (external, cli)" becomes
// "claude-cli". A value that starts with a separator or is empty gives "".
func ClientName(userAgent string) string {
	ua := strings.TrimSpace(userAgent)
	if i := strings.IndexAny(ua, "/ \t"); i >= 0 {
		ua = ua[:i]
	}
	if len(ua) > maxCorrelationValue {
		return ""
	}
	return ua
}

func isBool(v string) bool {
	return v == "true" || v == "false"
}

// isTraceparent reports whether v is a W3C traceparent: 00-<32 hex>-<16 hex>-<2 hex>,
// with a trace id and a parent id that are not all zero. Hex is lowercase.
func isTraceparent(v string) bool {
	if len(v) != 55 || v[:3] != "00-" || v[35] != '-' || v[52] != '-' {
		return false
	}
	traceID, parentID, flags := v[3:35], v[36:52], v[53:]
	return isHex(traceID) && isHex(parentID) && isHex(flags) &&
		strings.Trim(traceID, "0") != "" && strings.Trim(parentID, "0") != ""
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
