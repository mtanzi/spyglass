package core

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

// baseCall is a call every single-field case below matches or misses on exactly one field.
func baseCall() CallRecord {
	return CallRecord{
		ID:           NewIDAt(t0),
		TS:           t0,
		KeyName:      "marco-laptop",
		Client:       "claude-cli",
		Provider:     "anthropic",
		Model:        "claude-opus-4-8",
		Endpoint:     "/v1/messages",
		Streamed:     true,
		Status:       429,
		ErrorClass:   "rate_limit",
		Project:      "spyglass",
		SessionKey:   "s1",
		AgentID:      "a1",
		AgentType:    "subagent",
		PromptID:     "p1",
		RequestClass: "main",
		Duration:     90 * time.Second,
		Cost:         ptr(MicroUSD(2_000_000)),
		ToolUses:     []ToolUse{{ToolCallID: "toolu_1", Name: "Bash"}, {ToolCallID: "toolu_2", Name: "Read"}},
		ToolResults:  []ToolResult{{ToolCallID: "toolu_0", IsError: true}},
	}
}

// baseTools are the read-time pairs of baseCall: its Bash use failed, its Read
// use has no result yet, and the failed result it carries answers an earlier call.
func baseTools(c CallRecord) []ToolCall {
	return []ToolCall{
		{ToolCallID: "toolu_0", Name: "Grep", Status: ToolCallPaired, UseCallID: NewIDAt(t0.Add(-time.Minute)), ResultCallID: c.ID, IsError: ptr(true)},
		{ToolCallID: "toolu_1", Name: "Bash", Status: ToolCallPaired, UseCallID: c.ID, IsError: ptr(true)},
		{ToolCallID: "toolu_2", Name: "Read", Status: ToolCallPending, UseCallID: c.ID},
	}
}

func TestCallFilterMatchSingleField(t *testing.T) {
	tests := []struct {
		name string
		hit  CallFilter
		miss CallFilter
	}{
		{"from", CallFilter{From: t0}, CallFilter{From: t0.Add(time.Nanosecond)}},
		{"to", CallFilter{To: t0.Add(time.Nanosecond)}, CallFilter{To: t0}},
		{"keys", CallFilter{Keys: []string{"x", "marco-laptop"}}, CallFilter{Keys: []string{"x"}}},
		{"clients", CallFilter{Clients: []string{"claude-cli"}}, CallFilter{Clients: []string{"curl"}}},
		{"projects", CallFilter{Projects: []string{"x", "spyglass"}}, CallFilter{Projects: []string{"x"}}},
		{"providers", CallFilter{Providers: []string{"anthropic"}}, CallFilter{Providers: []string{"openai"}}},
		{"models", CallFilter{Models: []string{"claude-opus-4-8"}}, CallFilter{Models: []string{"claude-haiku-5-5"}}},
		{"endpoints", CallFilter{Endpoints: []string{"/v1/messages"}}, CallFilter{Endpoints: []string{"/v1/messages/count_tokens"}}},
		{"streamed true", CallFilter{Streamed: ptr(true)}, CallFilter{Streamed: ptr(false)}},
		{"statuses", CallFilter{Statuses: []int{500, 429}}, CallFilter{Statuses: []int{200}}},
		{"error classes", CallFilter{ErrorClasses: []string{"rate_limit"}}, CallFilter{ErrorClasses: []string{"overloaded"}}},
		{"session key", CallFilter{SessionKey: "s1"}, CallFilter{SessionKey: "s2"}},
		{"agent id", CallFilter{AgentID: "a1"}, CallFilter{AgentID: "a2"}},
		{"agent types", CallFilter{AgentTypes: []string{"subagent"}}, CallFilter{AgentTypes: []string{"main"}}},
		{"prompt id", CallFilter{PromptID: "p1"}, CallFilter{PromptID: "p2"}},
		{"request classes", CallFilter{RequestClasses: []string{"main"}}, CallFilter{RequestClasses: []string{"compaction"}}},
		{"tools", CallFilter{Tools: []string{"Read"}}, CallFilter{Tools: []string{"Grep"}}},
		{"tool error true", CallFilter{ToolError: ptr(true)}, CallFilter{ToolError: ptr(false)}},
		{"min latency", CallFilter{MinLatency: 90 * time.Second}, CallFilter{MinLatency: 91 * time.Second}},
		{"min cost", CallFilter{MinCost: 2_000_000}, CallFilter{MinCost: 2_000_001}},
	}
	c := baseCall()
	tools := baseTools(c)
	for _, tt := range tests {
		if !tt.hit.Match(c, tools) {
			t.Errorf("%s: filter should match", tt.name)
		}
		if tt.miss.Match(c, tools) {
			t.Errorf("%s: filter should not match", tt.name)
		}
	}
}

func TestCallFilterMatchEmptyMatchesAll(t *testing.T) {
	if !(CallFilter{}).Match(CallRecord{}, nil) || !(CallFilter{}).Match(baseCall(), nil) {
		t.Error("zero filter should match every call")
	}
}

func TestCallFilterMatchTimeRangeHalfOpen(t *testing.T) {
	f := CallFilter{From: t0, To: t0.Add(time.Hour)}
	for _, tt := range []struct {
		ts   time.Time
		want bool
	}{
		{t0.Add(-time.Nanosecond), false},
		{t0, true},
		{t0.Add(time.Hour - time.Nanosecond), true},
		{t0.Add(time.Hour), false},
	} {
		if got := f.Match(CallRecord{TS: tt.ts}, nil); got != tt.want {
			t.Errorf("Match at %v = %v, want %v", tt.ts, got, tt.want)
		}
	}
}

func TestCallFilterMatchToolError(t *testing.T) {
	c := CallRecord{
		ID:          NewIDAt(t0),
		ToolUses:    []ToolUse{{ToolCallID: "toolu_1", Name: "Bash"}, {ToolCallID: "toolu_2", Name: "Read"}},
		ToolResults: []ToolResult{{ToolCallID: "toolu_0", IsError: true}},
	}
	earlier := NewIDAt(t0.Add(-time.Minute))
	failedRead := ToolCall{ToolCallID: "toolu_0", Name: "Read", UseCallID: earlier, ResultCallID: c.ID, IsError: ptr(true)}
	bash := func(isErr *bool) ToolCall {
		return ToolCall{ToolCallID: "toolu_1", Name: "Bash", UseCallID: c.ID, IsError: isErr}
	}
	read := func(isErr *bool) ToolCall {
		return ToolCall{ToolCallID: "toolu_2", Name: "Read", UseCallID: c.ID, IsError: isErr}
	}
	bashErr := CallFilter{Tools: []string{"Bash"}, ToolError: ptr(true)}
	tests := []struct {
		name  string
		f     CallFilter
		tools []ToolCall
		want  bool
	}{
		{"bash use carrying a failed read result", bashErr, []ToolCall{failedRead, bash(nil)}, false},
		{"bash use that later fails", bashErr, []ToolCall{failedRead, bash(ptr(true))}, true},
		{"bash succeeded while read failed", bashErr, []ToolCall{bash(ptr(false)), read(ptr(true))}, false},
		{"carried failed result alone", CallFilter{ToolError: ptr(true)}, []ToolCall{failedRead, bash(nil)}, false},
		{"error on any own use", CallFilter{ToolError: ptr(true)}, []ToolCall{bash(ptr(false)), read(ptr(true))}, true},
		{"success on own use", CallFilter{ToolError: ptr(false)}, []ToolCall{bash(ptr(false))}, true},
		{"pending use has no error state", CallFilter{ToolError: ptr(false)}, []ToolCall{bash(nil)}, false},
		{"no tool calls", CallFilter{ToolError: ptr(true)}, nil, false},
		{"tool used by an earlier call", CallFilter{Tools: []string{"Read"}}, []ToolCall{failedRead}, false},
	}
	for _, tt := range tests {
		if got := tt.f.Match(c, tt.tools); got != tt.want {
			t.Errorf("%s: Match = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestToolCallFilterMatch(t *testing.T) {
	use := t0
	tc := ToolCall{
		Name: "Bash", Source: ToolSourceClient, Status: ToolCallPaired, SessionKey: "s1", AgentID: "a1",
		UseTS: use, ResultTS: use.Add(time.Minute), Duration: time.Minute, IsError: ptr(true),
		ArgBytes: 100, ResultBytes: 2000,
	}
	tests := []struct {
		name string
		hit  ToolCallFilter
		miss ToolCallFilter
	}{
		{"from", ToolCallFilter{From: use}, ToolCallFilter{From: use.Add(time.Nanosecond)}},
		{"to", ToolCallFilter{To: use.Add(time.Nanosecond)}, ToolCallFilter{To: use}},
		{"session key", ToolCallFilter{SessionKey: "s1"}, ToolCallFilter{SessionKey: "s2"}},
		{"agent id", ToolCallFilter{AgentID: "a1"}, ToolCallFilter{AgentID: "a2"}},
		{"tools", ToolCallFilter{Tools: []string{"Read", "Bash"}}, ToolCallFilter{Tools: []string{"Read"}}},
		{"sources", ToolCallFilter{Sources: []ToolSource{ToolSourceClient}}, ToolCallFilter{Sources: []ToolSource{ToolSourceServer}}},
		{"statuses", ToolCallFilter{Statuses: []ToolCallStatus{ToolCallPaired}}, ToolCallFilter{Statuses: []ToolCallStatus{ToolCallPending}}},
		{"is error", ToolCallFilter{IsError: ptr(true)}, ToolCallFilter{IsError: ptr(false)}},
		{"min duration", ToolCallFilter{MinDuration: time.Minute}, ToolCallFilter{MinDuration: time.Minute + 1}},
		{"min arg bytes", ToolCallFilter{MinArgBytes: 100}, ToolCallFilter{MinArgBytes: 101}},
		{"min result bytes", ToolCallFilter{MinResultBytes: 2000}, ToolCallFilter{MinResultBytes: 2001}},
	}
	for _, tt := range tests {
		if !tt.hit.Match(tc) {
			t.Errorf("%s: filter should match", tt.name)
		}
		if tt.miss.Match(tc) {
			t.Errorf("%s: filter should not match", tt.name)
		}
	}

	pending := ToolCall{Name: "Bash", Status: ToolCallPending, UseTS: use}
	if (ToolCallFilter{IsError: ptr(false)}).Match(pending) {
		t.Error("IsError matched a tool call without a result")
	}
	if (ToolCallFilter{MinResultBytes: 1}).Match(pending) {
		t.Error("MinResultBytes matched a tool call without a result")
	}
	resultOnly := ToolCall{Status: ToolCallResultOnly, ResultTS: use.Add(time.Hour)}
	if (ToolCallFilter{From: use, To: use.Add(time.Minute)}).Match(resultOnly) {
		t.Error("result_only tool call matched on its zero use time")
	}
	if !(ToolCallFilter{From: use.Add(time.Hour), To: use.Add(2 * time.Hour)}).Match(resultOnly) {
		t.Error("result_only tool call missed on its result time")
	}
}

func TestCallFilterMatchUnknownCost(t *testing.T) {
	c := baseCall()
	c.Cost = nil
	if (CallFilter{MinCost: 1}).Match(c, nil) {
		t.Error("MinCost matched a call with unknown cost")
	}
	if !(CallFilter{}).Match(c, nil) {
		t.Error("unfiltered cost should match unknown cost")
	}
}

func TestCallFilterMatchCombined(t *testing.T) {
	full := CallFilter{
		From: t0, To: t0.Add(time.Hour),
		Keys: []string{"marco-laptop"}, Clients: []string{"claude-cli"}, Providers: []string{"anthropic"},
		Models: []string{"claude-opus-4-8"}, Statuses: []int{429}, ErrorClasses: []string{"rate_limit"},
		SessionKey: "s1", AgentID: "a1", AgentTypes: []string{"subagent"}, PromptID: "p1",
		RequestClasses: []string{"main"}, Tools: []string{"Bash"}, ToolError: ptr(true),
		MinLatency: time.Minute, MinCost: 1_000_000,
	}
	c := baseCall()
	tools := baseTools(c)
	if !full.Match(c, tools) {
		t.Fatal("all fields set should match the base call")
	}
	// Breaking any one field of the combined filter must reject the call.
	breakers := map[string]func(*CallFilter){
		"keys":      func(f *CallFilter) { f.Keys = []string{"x"} },
		"clients":   func(f *CallFilter) { f.Clients = []string{"x"} },
		"providers": func(f *CallFilter) { f.Providers = []string{"x"} },
		"models":    func(f *CallFilter) { f.Models = []string{"x"} },
		"errclass":  func(f *CallFilter) { f.ErrorClasses = []string{"x"} },
		"agenttype": func(f *CallFilter) { f.AgentTypes = []string{"x"} },
		"reqclass":  func(f *CallFilter) { f.RequestClasses = []string{"x"} },
		"statuses":  func(f *CallFilter) { f.Statuses = []int{200} },
		"session":   func(f *CallFilter) { f.SessionKey = "x" },
		"agent":     func(f *CallFilter) { f.AgentID = "x" },
		"prompt":    func(f *CallFilter) { f.PromptID = "x" },
		"tools":     func(f *CallFilter) { f.Tools = []string{"x"} },
		"toolerr":   func(f *CallFilter) { f.ToolError = ptr(false) },
		"latency":   func(f *CallFilter) { f.MinLatency = time.Hour },
		"cost":      func(f *CallFilter) { f.MinCost = 1 << 40 },
		"from":      func(f *CallFilter) { f.From = t0.Add(time.Minute) },
		"to":        func(f *CallFilter) { f.To = t0 },
	}
	for name, brk := range breakers {
		f := full
		brk(&f)
		if f.Match(c, tools) {
			t.Errorf("combined filter with broken %s still matched", name)
		}
	}
}

func TestFilterValidate(t *testing.T) {
	good := CallFilter{From: t0, To: t0.Add(time.Hour)}
	if err := good.Validate(); err != nil {
		t.Errorf("good range: %v", err)
	}
	for name, f := range map[string]CallFilter{
		"zero":     {},
		"no from":  {To: t0},
		"no to":    {From: t0},
		"empty":    {From: t0, To: t0},
		"reversed": {From: t0.Add(time.Hour), To: t0},
	} {
		if f.Validate() != ErrInvalidFilter {
			t.Errorf("%s: Validate did not reject", name)
		}
	}
	if (SessionFilter{}).Validate() != ErrInvalidFilter || (ToolCallFilter{}).Validate() != ErrInvalidFilter {
		t.Error("session and tool-call filters must require a range")
	}
	if (SessionFilter{From: t0, To: t0.Add(1)}).Validate() != nil || (ToolCallFilter{From: t0, To: t0.Add(1)}).Validate() != nil {
		t.Error("valid range rejected")
	}
}

func TestUsageQueryValidate(t *testing.T) {
	r := CallFilter{From: t0, To: t0.Add(time.Hour)}
	if err := (UsageQuery{Filter: r, GroupBy: GroupModel, Grain: GrainHour}).Validate(); err != nil {
		t.Errorf("valid query: %v", err)
	}
	for name, q := range map[string]UsageQuery{
		"no range":  {Grain: GrainDay},
		"bad group": {Filter: r, GroupBy: "tool", Grain: GrainDay},
		"no grain":  {Filter: r},
		"bad grain": {Filter: r, Grain: "week"},
	} {
		if q.Validate() != ErrInvalidFilter {
			t.Errorf("%s: Validate did not reject", name)
		}
	}
}
