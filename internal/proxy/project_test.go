package proxy

import (
	"net/http"
	"strings"
	"testing"
)

// A request whose system prompt names a working directory, as Claude Code's
// does. The parent directories carry a marker that must never be stored.
const detectedReq = `{"model":"claude-opus-4-8","stream":false,"system":"# Environment\n - Primary working directory: /home/MARKER-parent/worktrees/task-17-detected\n","messages":[{"role":"user","content":"FAKEPROMPT-hi"}]}`

// TestProjectPrecedence: the header ranks above the key's project, which ranks
// above the directory name the extractor detects, and a call with none of the
// three has no project (ADR 0059, decision 3).
func TestProjectPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		keyPrj string
		body   string
		hdr    []string
		want   string
	}{
		{"header over key and detection", "from-key", detectedReq, []string{"X-Spyglass-Project", "from-header"}, "from-header"},
		{"key over detection", "from-key", detectedReq, nil, "from-key"},
		{"detection when the key has no project", "", detectedReq, nil, "task-17-detected"},
		{"invalid header falls back to the key", "from-key", detectedReq, []string{"X-Spyglass-Project", strings.Repeat("p", 129)}, "from-key"},
		{"invalid header falls back to detection", "", detectedReq, []string{"X-Spyglass-Project", "bad\tname"}, "task-17-detected"},
		{"blank header falls back to detection", "", detectedReq, []string{"X-Spyglass-Project", "   "}, "task-17-detected"},
		{"header is trimmed", "", detectedReq, []string{"X-Spyglass-Project", "  from-header "}, "from-header"},
		{"none of the three", "", req("false"), nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, jsonUpstream(`{"id":"msg_1","type":"message"}`), nil, func(e *env) { e.keyProj = tt.keyPrj })
			_ = readAll(t, e.do(t, "POST", "/v1/messages", tt.body, append(auth(), tt.hdr...)...).Body)
			if got := e.rec.next(t).Project; got != tt.want {
				t.Errorf("project = %q, want %q", got, tt.want)
			}
		})
	}
}

// The header is for the proxy: it never reaches the provider, valid or not,
// and the other headers still do.
func TestProjectHeaderIsRemovedBeforeUpstream(t *testing.T) {
	for _, v := range []string{"from-header", strings.Repeat("p", 129)} {
		got := make(chan seen, 1)
		e := newEnv(t, capture(got, nil), nil)
		_ = readAll(t, e.do(t, "POST", "/v1/messages", req("false"),
			append(auth(), "X-Spyglass-Project", v, "Anthropic-Beta", "b1")...).Body)
		up := <-got
		if vs := up.header.Values(http.CanonicalHeaderKey("x-spyglass-project")); len(vs) != 0 {
			t.Errorf("header value %.10q: the upstream got x-spyglass-project %q", v, vs)
		}
		if up.header.Get("Anthropic-Beta") != "b1" {
			t.Errorf("anthropic-beta = %q, want it forwarded", up.header.Get("Anthropic-Beta"))
		}
	}
}
