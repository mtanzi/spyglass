package storetest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// Base is the default start time of generated records.
var Base = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

// Fake body text. A generator sizes its tool halves from these strings, so a
// store that kept body text would hold them. Each is unique per generator and
// is never given to a record as text.
type bodies struct {
	Prompt, Response, ToolArgs, ToolResult string
}

// Gen produces call records with controllable timestamps and ids. Records
// from one Gen are distinct; ids from one Gen sort in creation order. It is
// not safe for concurrent use.
type Gen struct {
	next   time.Time
	seq    uint32
	tag    uint64
	bodies bodies
}

// NewGen returns a generator whose first record starts at Base and whose
// records follow one second apart.
func NewGen() *Gen { return NewGenAt(Base) }

// NewGenAt returns a generator whose first record starts at t.
func NewGenAt(t time.Time) *Gen {
	g := &Gen{next: t}
	g.tag = uint64(time.Now().UnixNano())
	g.bodies = bodies{
		Prompt:     fmt.Sprintf("FAKE-PROMPT-%x please refactor the billing module", g.tag),
		Response:   fmt.Sprintf("FAKE-RESPONSE-%x I will start with the invoice totals", g.tag),
		ToolArgs:   fmt.Sprintf(`{"command":"FAKE-TOOLARGS-%x rm -rf build"}`, g.tag),
		ToolResult: fmt.Sprintf("FAKE-TOOLRESULT-%x exit status 0", g.tag),
	}
	return g
}

// Bodies returns the fake prompt, response, tool argument and tool result text
// the generator used to size its records. None of it may be stored.
func (g *Gen) Bodies() []string {
	b := g.bodies
	return []string{b.Prompt, b.Response, b.ToolArgs, b.ToolResult}
}

// CallOpt changes a generated call.
type CallOpt func(*core.CallRecord)

// Call returns a finished call that started one second after the previous one
// from this generator. Opts apply last.
func (g *Gen) Call(opts ...CallOpt) core.CallRecord {
	ts := g.next
	g.next = g.next.Add(time.Second)
	c := core.CallRecord{
		ID: g.ID(ts), TS: ts, EndedAt: ts.Add(1500 * time.Millisecond),
		KeyName: "alice", Client: "claude-code", ClientRaw: "claude-cli/2.1.0",
		Family: "anthropic", Endpoint: "/v1/messages", AuthMode: "proxy_key", Provider: "anthropic",
		Model: "claude-opus-5-5", Status: 200, StopReason: "end_turn", Streamed: true,
		TTFB: 100 * time.Millisecond, TTFT: 300 * time.Millisecond, Duration: 1500 * time.Millisecond,
		Usage: core.Usage{
			InputUncached: len(g.bodies.Prompt), Output: len(g.bodies.Response),
		},
	}
	c.UsageRaw = json.RawMessage(fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d}`, c.Usage.InputUncached, c.Usage.Output))
	for _, o := range opts {
		o(&c)
	}
	return c
}

// Calls returns n calls, each with the same opts.
func (g *Gen) Calls(n int, opts ...CallOpt) []core.CallRecord {
	out := make([]core.CallRecord, n)
	for i := range out {
		out[i] = g.Call(opts...)
	}
	return out
}

// ID returns a UUIDv7 for ts. Ids for the same ts from one Gen sort in the
// order they were made.
func (g *Gen) ID(ts time.Time) core.ID {
	id := core.NewIDAt(ts)
	g.seq++
	id[6], id[7] = 0x70, 0
	binary.BigEndian.PutUint32(id[8:12], g.seq&0x3fffffff|0x80000000) // keeps variant 10
	return id
}

// At starts the call at ts and ends it as long after as it lasted. It also
// gives the call a new id for ts.
func (g *Gen) At(ts time.Time) CallOpt {
	return func(c *core.CallRecord) {
		c.TS, c.ID = ts, g.ID(ts)
		c.EndedAt = ts.Add(c.Duration)
	}
}

// WithID sets the call id.
func WithID(id core.ID) CallOpt { return func(c *core.CallRecord) { c.ID = id } }

// WithKey sets the key name.
func WithKey(name string) CallOpt { return func(c *core.CallRecord) { c.KeyName = name } }

// WithSession sets the session key.
func WithSession(key string) CallOpt { return func(c *core.CallRecord) { c.SessionKey = key } }

// WithAgent sets the agent, its parent and its type.
func WithAgent(id, parent, typ string) CallOpt {
	return func(c *core.CallRecord) { c.AgentID, c.ParentAgentID, c.AgentType = id, parent, typ }
}

// WithModel sets the model.
func WithModel(model string) CallOpt { return func(c *core.CallRecord) { c.Model = model } }

// WithError makes the call fail with the given status and error class.
func WithError(status int, class string) CallOpt {
	return func(c *core.CallRecord) { c.Status, c.ErrorClass = status, class }
}

// WithCost sets the cost.
func WithCost(m core.MicroUSD) CallOpt { return func(c *core.CallRecord) { c.Cost = &m } }

// WithUsage sets the token columns.
func WithUsage(u core.Usage) CallOpt { return func(c *core.CallRecord) { c.Usage = u } }

// WithDuration sets the call duration and moves the end of the call to match.
func WithDuration(d time.Duration) CallOpt {
	return func(c *core.CallRecord) { c.Duration, c.EndedAt = d, c.TS.Add(d) }
}

// WithClient sets the client.
func WithClient(name string) CallOpt { return func(c *core.CallRecord) { c.Client = name } }

// WithProvider sets the provider.
func WithProvider(name string) CallOpt { return func(c *core.CallRecord) { c.Provider = name } }

// WithEstimatedUsage marks the usage as estimated.
func WithEstimatedUsage() CallOpt { return func(c *core.CallRecord) { c.UsageEstimated = true } }

// WithoutUsageRaw drops the provider usage object, as for a call that failed
// before any usage was reported.
func WithoutUsageRaw() CallOpt { return func(c *core.CallRecord) { c.UsageRaw = nil } }

// WithoutCost makes the cost unknown.
func WithoutCost() CallOpt { return func(c *core.CallRecord) { c.Cost = nil } }

// WithUse adds a client tool use to the response of the call. Its argument
// size is that of the fake tool arguments.
func (g *Gen) WithUse(toolCallID, name string) CallOpt {
	return func(c *core.CallRecord) {
		c.ToolUses = append(c.ToolUses, core.ToolUse{
			Source: core.ToolSourceClient, ToolCallID: toolCallID, Name: name, ArgBytes: len(g.bodies.ToolArgs),
		})
	}
}

// WithResult adds a client tool result to the request of the call. Its size is
// that of the fake tool result.
func (g *Gen) WithResult(toolCallID string, isError bool) CallOpt {
	return func(c *core.CallRecord) {
		c.ToolResults = append(c.ToolResults, core.ToolResult{
			Source: core.ToolSourceClient, ToolCallID: toolCallID, IsError: isError, ResultBytes: len(g.bodies.ToolResult),
		})
	}
}

// ToolPair returns two calls that share a tool call: the first carries the use,
// the second, later, the result. The pair is in one session.
func (g *Gen) ToolPair(toolCallID, session string) (use, result core.CallRecord) {
	use = g.Call(WithSession(session), g.WithUse(toolCallID, "Bash"))
	result = g.Call(WithSession(session), g.WithResult(toolCallID, false))
	return use, result
}

// contains reports whether s holds any of the texts.
func contains(s string, texts []string) (string, bool) {
	for _, t := range texts {
		if strings.Contains(s, t) {
			return t, true
		}
	}
	return "", false
}

// WithPrevToolDurations sets the raw x-claude-code-prev-tool-durations value.
func WithPrevToolDurations(raw string) CallOpt {
	return func(c *core.CallRecord) { c.PrevToolDurations = raw }
}

// WithAgentType sets the agent type only.
func WithAgentType(typ string) CallOpt { return func(c *core.CallRecord) { c.AgentType = typ } }

// WithAuthMode sets the auth mode of the provider: proxy_key or client_passthrough.
func WithAuthMode(mode string) CallOpt { return func(c *core.CallRecord) { c.AuthMode = mode } }

// WithPrompt sets the prompt id.
func WithPrompt(id string) CallOpt { return func(c *core.CallRecord) { c.PromptID = id } }

// WithServerTool adds a server-side tool to the response of the call: a use and
// its result, both timed at response end. The sizes are those of the fake text.
func (g *Gen) WithServerTool(toolCallID, name string, isError bool) CallOpt {
	return func(c *core.CallRecord) {
		c.ToolUses = append(c.ToolUses, core.ToolUse{
			Source: core.ToolSourceServer, ToolCallID: toolCallID, Name: name, ArgBytes: len(g.bodies.ToolArgs),
		})
		c.ToolResults = append(c.ToolResults, core.ToolResult{
			Source: core.ToolSourceServer, ToolCallID: toolCallID, IsError: isError, ResultBytes: len(g.bodies.ToolResult),
		})
	}
}
