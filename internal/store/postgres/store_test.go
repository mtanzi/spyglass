package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/store/postgres"
	"github.com/mtanzi/spyglass/internal/store/postgres/pgtest"
	"github.com/mtanzi/spyglass/internal/store/storetest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

// open opens a store on url without migrating it.
func open(t *testing.T, url string) *postgres.Store {
	t.Helper()
	s, err := postgres.Open(context.Background(), postgres.Config{
		URL: url, MaxConns: 4,
		// A small batch makes the retention cases cross batch edges with few rows.
		Retention: postgres.Retention{BatchSize: 4, BatchPause: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// newStore returns a migrated store on a fresh database and a plain
// connection to the same database for checking rows.
func newStore(t *testing.T) (*postgres.Store, *pgx.Conn) {
	t.Helper()
	url := pgtest.NewDatabase(t)
	s := open(t, url)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return s, conn
}

func count(t *testing.T, conn *pgx.Conn, table string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenRejectsBadURLWithoutQuotingIt(t *testing.T) {
	_, err := postgres.Open(context.Background(), postgres.Config{URL: "postgres://u:s3cret@host:notaport/db"})
	if err == nil {
		t.Fatal("Open accepted a bad url")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error quotes the password: %v", err)
	}
}

func TestMigrateFromEmptyIsIdempotent(t *testing.T) {
	url := pgtest.NewDatabase(t)
	s := open(t, url)
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	// Concurrent starts serialize on goose's session lock.
	stores := []*postgres.Store{open(t, url), open(t, url)}
	errs := make([]error, len(stores))
	var wg sync.WaitGroup
	for i, st := range stores {
		wg.Go(func() { errs[i] = st.Migrate(ctx) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var applied int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Fatalf("applied migrations = %d, want 2", applied)
	}

	types := map[string]string{}
	rows, err := conn.Query(ctx, `
		SELECT table_name || '.' || column_name, format_type(atttypid, atttypmod)
		FROM information_schema.columns
		JOIN pg_attribute ON attrelid = (table_name)::regclass AND attname = column_name
		WHERE table_schema = 'public' AND table_name IN ('calls', 'tool_events')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var col, typ string
		if err := rows.Scan(&col, &typ); err != nil {
			t.Fatal(err)
		}
		types[col] = typ
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for col, want := range map[string]string{
		"calls.id":                "uuid",
		"calls.usage_raw":         "jsonb",
		"calls.cost_usd":          "numeric(12,6)",
		"calls.context_compacted": "boolean",
		"calls.project":           "text",
		"tool_events.call_id":     "uuid",
		"tool_events.idx":         "integer",
	} {
		if types[col] != want {
			t.Errorf("%s type = %q, want %q", col, types[col], want)
		}
	}

	var indexes []string
	if err := conn.QueryRow(ctx, `
		SELECT array_agg(indexdef ORDER BY indexdef) FROM pg_indexes
		WHERE tablename IN ('calls', 'tool_events') AND indexname NOT LIKE '%_pkey'`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ON public.calls USING btree (ts DESC, id DESC)",
		"ON public.calls USING btree (session_key, ts)",
		"ON public.calls USING btree (key_name, ts DESC)",
		"ON public.calls USING btree (ts DESC) WHERE (error_class IS NOT NULL)",
		"ON public.tool_events USING btree (tool_call_id)",
		"ON public.tool_events USING btree (ts DESC)",
		"ON public.tool_events USING btree (tool_name, ts DESC) WHERE (kind = 'use'::text)",
	} {
		found := false
		for _, def := range indexes {
			found = found || strings.HasSuffix(def, want)
		}
		if !found {
			t.Errorf("no index %q in %q", want, indexes)
		}
	}
}

var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

// fullCall is a call record with every field set.
func fullCall() core.CallRecord {
	cost := core.MicroUSD(1_234_567)
	return core.CallRecord{
		ID: core.NewIDAt(t0), TS: t0,
		KeyName: "alice", Client: "claude-code", ClientRaw: "claude-cli/2.1.0", ClientRequestID: "creq-1",
		SessionKey: "sess-1", AgentID: "agent-1", ParentAgentID: "agent-0", AgentType: "subagent",
		PromptID: "prompt-1", RequestClass: "main", Compaction: true, ContextCompacted: true, TraceID: "trace-1",
		Project: "spyglass",
		Family:  "anthropic", Endpoint: "/v1/messages", AuthMode: "proxy_key", Provider: "anthropic",
		Model: "claude-opus-5-5", UpstreamRequestID: "req_up",
		Status: 529, ErrorClass: "upstream_overloaded", ErrorType: "overloaded_error", StopReason: "tool_use",
		Streamed: true,
		TTFB:     120 * time.Millisecond, TTFT: 340 * time.Millisecond, Duration: 2500 * time.Millisecond,
		EndedAt: t0.Add(2500 * time.Millisecond),
		Usage: core.Usage{
			InputUncached: 10, CacheRead: 2000, CacheWrite5m: 300, CacheWrite1h: 40, Output: 500, Reasoning: 60,
		},
		UsageRaw:       json.RawMessage(`{"input_tokens":10,"output_tokens":500}`),
		UsageEstimated: true, ExtractionPartial: true,
		Cost: &cost, PricingVersion: "2026-10-01", PrevToolDurations: "toolu_a=1.5",
		ToolResults: []core.ToolResult{
			{Source: core.ToolSourceClient, ToolCallID: "toolu_a", IsError: true, ResultBytes: 77},
		},
		ToolUses: []core.ToolUse{
			{Source: core.ToolSourceClient, ToolCallID: "toolu_b", Name: "Bash", ArgBytes: 42},
			{Source: core.ToolSourceServer, ToolCallID: "srvtoolu_c", Name: "web_search", ArgBytes: 9},
		},
	}
}

func TestServerToolResultTimedAtResponseEnd(t *testing.T) {
	s, conn := newStore(t)
	ctx := context.Background()
	c := fullCall()
	c.ToolResults = []core.ToolResult{
		{Source: core.ToolSourceClient, ToolCallID: "toolu_a", ResultBytes: 1},
		{Source: core.ToolSourceServer, ToolCallID: "srvtoolu_c", IsError: true, ResultBytes: 2},
	}
	if err := s.WriteBatch(ctx, []core.CallRecord{c}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]time.Time{"toolu_a": c.TS, "srvtoolu_c": c.EndedAt} {
		var ts time.Time
		if err := conn.QueryRow(ctx, `SELECT ts FROM tool_events WHERE kind = 'result' AND tool_call_id = $1`, id).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		if !ts.Equal(want) {
			t.Errorf("%s ts = %v, want %v", id, ts, want)
		}
	}
}

func TestWriteBatchFirstWrite(t *testing.T) {
	s, conn := newStore(t)
	ctx := context.Background()
	full := fullCall()
	bare := core.CallRecord{
		ID: core.NewIDAt(t0.Add(time.Second)), TS: t0.Add(time.Second),
		KeyName: "bob", Family: "anthropic", Endpoint: "/v1/messages", AuthMode: "client_passthrough",
		Provider: "anthropic",
		ToolUses: []core.ToolUse{{Source: core.ToolSourceClient, ToolCallID: "toolu_d", Name: "Read"}},
	}
	if err := s.WriteBatch(ctx, []core.CallRecord{full, bare}); err != nil {
		t.Fatal(err)
	}

	// Every column as text, so a mapping mistake shows as a readable diff.
	if _, err := conn.Exec(ctx, "SET TIME ZONE 'UTC'"); err != nil {
		t.Fatal(err)
	}
	got := map[core.ID]string{}
	rows, err := conn.Query(ctx, `
		SELECT id, concat_ws('|', ts, key_name, client, client_raw, client_request_id,
			session_key, agent_id, parent_agent_id, agent_type, prompt_id, request_class,
			compaction, context_compacted, trace_id, project, family, endpoint, auth_mode, provider, model,
			upstream_request_id, status, error_class, error_type, stop_reason, streamed,
			ttfb_ms, ttft_ms, duration_ms, ended_at, input_uncached, cache_read, cache_write_5m,
			cache_write_1h, output, reasoning, usage_raw, jsonb_typeof(usage_raw), usage_estimated,
			extraction_partial, cost_usd, pricing_version, prev_tool_durations)
		FROM calls`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id [16]byte
		var line string
		if err := rows.Scan(&id, &line); err != nil {
			t.Fatal(err)
		}
		got[core.ID(id)] = line
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[core.ID]string{
		full.ID: "2026-10-27 09:00:00+00|alice|claude-code|claude-cli/2.1.0|creq-1|" +
			"sess-1|agent-1|agent-0|subagent|prompt-1|main|" +
			"t|t|trace-1|spyglass|anthropic|/v1/messages|proxy_key|anthropic|claude-opus-5-5|" +
			"req_up|529|upstream_overloaded|overloaded_error|tool_use|t|" +
			"120|340|2500|2026-10-27 09:00:02.5+00|10|2000|300|" +
			`40|500|60|{"input_tokens": 10, "output_tokens": 500}|object|t|` +
			"t|1.234567|2026-10-01|toolu_a=1.5",
		// concat_ws skips NULLs: the unknown fields of bare are NULL.
		bare.ID: "2026-10-27 09:00:01+00|bob|f|f|anthropic|/v1/messages|client_passthrough|anthropic|" +
			"f|0|0|0|0|0|0|f|f",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("call %s:\n got %s\nwant %s", id, got[id], w)
		}
	}

	type event struct {
		call     core.ID
		idx      int
		ts       time.Time
		kind     string
		source   string
		toolCall string
		name     *string
		bytes    int
		isError  *bool
		session  *string
		agent    *string
	}
	rows, err = conn.Query(ctx, `
		SELECT call_id, idx, ts, kind, source, tool_call_id, tool_name, bytes, is_error, session_key, agent_id
		FROM tool_events ORDER BY ts, call_id, idx`)
	if err != nil {
		t.Fatal(err)
	}
	var events []event
	for rows.Next() {
		var e event
		var id [16]byte
		if err := rows.Scan(&id, &e.idx, &e.ts, &e.kind, &e.source, &e.toolCall, &e.name, &e.bytes,
			&e.isError, &e.session, &e.agent); err != nil {
			t.Fatal(err)
		}
		e.call = core.ID(id)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	yes := true
	wantEvents := []event{
		{full.ID, 0, t0, "result", "client", "toolu_a", nil, 77, &yes, str("sess-1"), str("agent-1")},
		{bare.ID, 0, bare.TS, "use", "client", "toolu_d", str("Read"), 0, nil, nil, nil},
		{full.ID, 1, full.EndedAt, "use", "client", "toolu_b", str("Bash"), 42, nil, str("sess-1"), str("agent-1")},
		{full.ID, 2, full.EndedAt, "use", "server", "srvtoolu_c", str("web_search"), 9, nil, str("sess-1"), str("agent-1")},
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("tool events = %d, want %d", len(events), len(wantEvents))
	}
	for i, w := range wantEvents {
		g := events[i]
		if g.call != w.call || g.idx != w.idx || !g.ts.Equal(w.ts) || g.kind != w.kind || g.source != w.source ||
			g.toolCall != w.toolCall || deref(g.name) != deref(w.name) || (g.name == nil) != (w.name == nil) ||
			g.bytes != w.bytes || (g.isError == nil) != (w.isError == nil) ||
			(g.isError != nil && *g.isError != *w.isError) ||
			deref(g.session) != deref(w.session) || (g.session == nil) != (w.session == nil) ||
			deref(g.agent) != deref(w.agent) || (g.agent == nil) != (w.agent == nil) {
			t.Errorf("event %d = %+v, want %+v", i, g, w)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestWriteBatchEmpty(t *testing.T) {
	s, conn := newStore(t)
	if err := s.WriteBatch(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, conn, "calls"); n != 0 {
		t.Fatalf("calls = %d, want 0", n)
	}
}

func TestWriteBatchReplayInsertsNothing(t *testing.T) {
	s, conn := newStore(t)
	ctx := context.Background()
	a, b, c := fullCall(), fullCall(), fullCall()
	b.ID, c.ID = core.NewIDAt(t0), core.NewIDAt(t0)
	if err := s.WriteBatch(ctx, []core.CallRecord{a, b}); err != nil {
		t.Fatal(err)
	}
	// The same batch again, an overlapping batch, and a duplicate inside one batch.
	for _, batch := range [][]core.CallRecord{{a, b}, {b, c}, {c, c, a}} {
		if err := s.WriteBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, conn, "calls"); n != 3 {
		t.Fatalf("calls = %d, want 3", n)
	}
	if n := count(t, conn, "tool_events"); n != 9 {
		t.Fatalf("tool events = %d, want 9", n)
	}
}

func TestWriteBatchFailedBatchLeavesNothing(t *testing.T) {
	s, conn := newStore(t)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, "ALTER TABLE calls ADD CONSTRAINT reject CHECK (key_name <> 'reject')"); err != nil {
		t.Fatal(err)
	}
	// Enough calls to span several statements, so the failing row is in a
	// later statement than rows that were already inserted.
	batch := manyCalls(3000)
	batch[len(batch)-1].KeyName = "reject"
	if err := s.WriteBatch(ctx, batch); err == nil {
		t.Fatal("WriteBatch accepted a row the constraint rejects")
	}
	if n := count(t, conn, "calls"); n != 0 {
		t.Fatalf("calls = %d after a failed batch, want 0", n)
	}
	if n := count(t, conn, "tool_events"); n != 0 {
		t.Fatalf("tool events = %d after a failed batch, want 0", n)
	}

	// The same batch, fixed, is written in full.
	batch[len(batch)-1].KeyName = "alice"
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if n := count(t, conn, "calls"); n != len(batch) {
		t.Fatalf("calls = %d, want %d", n, len(batch))
	}
}

// The recorder isolates records by this classification (ADR 0051): a row the
// store rejects matches core.ErrUnwritable and names its cause; a store it
// cannot reach does not.
func TestWriteBatchClassifiesErrors(t *testing.T) {
	s, conn := newStore(t)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, "ALTER TABLE calls ADD CONSTRAINT reject CHECK (key_name <> 'reject')"); err != nil {
		t.Fatal(err)
	}
	batch := manyCalls(3)
	batch[1].KeyName = "reject"
	err := s.WriteBatch(ctx, batch)
	if !errors.Is(err, core.ErrUnwritable) {
		t.Fatalf("constraint violation: err = %v, want ErrUnwritable", err)
	}
	cause, ok := errors.AsType[interface {
		error
		SQLState() string
		Constraint() string
	}](err)
	if !ok || cause.SQLState() != "23514" || cause.Constraint() != "reject" {
		t.Fatalf("constraint violation names no cause: %v", err)
	}

	// A value the driver cannot encode fails before the statement is sent.
	bad := fullCall()
	bad.ToolUses[0].ArgBytes = 1 << 40
	err = s.WriteBatch(ctx, []core.CallRecord{bad})
	if !errors.Is(err, core.ErrUnwritable) {
		t.Fatalf("encode failure: err = %v, want ErrUnwritable", err)
	}
	column, ok := errors.AsType[interface {
		error
		Column() string
	}](err)
	if !ok || column.Column() != "bytes" {
		t.Fatalf("encode failure names no column: %v", err)
	}

	s.Close()
	err = s.WriteBatch(ctx, manyCalls(1))
	if err == nil {
		t.Fatal("WriteBatch succeeded on a closed pool")
	}
	if errors.Is(err, core.ErrUnwritable) {
		t.Fatalf("closed pool: err = %v, want a transient error", err)
	}
}

func TestWriteBatchSanitizesText(t *testing.T) {
	s, conn := newStore(t)
	ctx := context.Background()
	c := fullCall()
	c.KeyName = "ali\x00ce"
	c.Provider = "anthro\xffpic"
	c.ClientRaw = "claude-cli/2.1.0 \xe9t\xe9"
	c.SessionKey = "sess\x00-1"
	c.AgentID = "\x00"
	c.ToolUses = []core.ToolUse{{Source: core.ToolSourceClient, ToolCallID: "tool\x00u_b", Name: "Ba\xffsh"}}
	c.ToolResults = nil
	if err := s.WriteBatch(ctx, []core.CallRecord{c}); err != nil {
		t.Fatal(err)
	}
	var keyName, provider, clientRaw, session string
	var agent *string
	if err := conn.QueryRow(ctx,
		"SELECT key_name, provider, client_raw, session_key, agent_id FROM calls").
		Scan(&keyName, &provider, &clientRaw, &session, &agent); err != nil {
		t.Fatal(err)
	}
	if keyName != "alice" || provider != "anthro\uFFFDpic" || clientRaw != "claude-cli/2.1.0 \uFFFDt\uFFFD" ||
		session != "sess-1" || agent != nil {
		t.Errorf("calls row = %q %q %q %q %v", keyName, provider, clientRaw, session, agent)
	}
	var toolCall, name, toolSession string
	if err := conn.QueryRow(ctx, "SELECT tool_call_id, tool_name, session_key FROM tool_events").
		Scan(&toolCall, &name, &toolSession); err != nil {
		t.Fatal(err)
	}
	if toolCall != "toolu_b" || name != "Ba\uFFFDsh" || toolSession != "sess-1" {
		t.Errorf("tool event = %q %q %q", toolCall, name, toolSession)
	}
}

func TestWriteBatchManyHalvesInOneCall(t *testing.T) {
	s, conn := newStore(t)
	c := fullCall()
	c.ToolResults = nil
	c.ToolUses = make([]core.ToolUse, 40000)
	for i := range c.ToolUses {
		c.ToolUses[i] = core.ToolUse{Source: core.ToolSourceClient, ToolCallID: "toolu_" + strconv.Itoa(i), Name: "Bash"}
	}
	if err := s.WriteBatch(context.Background(), []core.CallRecord{c}); err != nil {
		t.Fatal(err)
	}
	var n, maxIdx int
	if err := conn.QueryRow(context.Background(), "SELECT count(*), max(idx) FROM tool_events").Scan(&n, &maxIdx); err != nil {
		t.Fatal(err)
	}
	if n != len(c.ToolUses) || maxIdx != len(c.ToolUses)-1 {
		t.Fatalf("tool events = %d, max idx = %d, want %d, %d", n, maxIdx, len(c.ToolUses), len(c.ToolUses)-1)
	}
}

func TestWriteBatchStoresUnstorableUsageRawAsNull(t *testing.T) {
	s, conn := newStore(t)
	ctx := context.Background()
	var batch []core.CallRecord
	for i, raw := range []string{
		`{"not json`, "{\"a\":\"\xff\"}", `{"a":"\u0000"}`, `{"a":"\ud800"}`, `{"a":"\uDC00x"}`,
	} {
		c := fullCall()
		c.ID = core.NewIDAt(t0.Add(time.Duration(i) * time.Second))
		c.UsageRaw = json.RawMessage(raw)
		batch = append(batch, c)
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM calls WHERE usage_raw IS NULL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(batch) {
		t.Fatalf("calls with NULL usage_raw = %d, want %d", n, len(batch))
	}
}

func TestWriteBatchChunksLargeBatches(t *testing.T) {
	s, conn := newStore(t)
	// 3000 calls exceed the parameter limit for calls, and their 3 halves each
	// (9000 rows of 11 columns) exceed it for tool_events.
	batch := manyCalls(3000)
	if err := s.WriteBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if n := count(t, conn, "calls"); n != 3000 {
		t.Fatalf("calls = %d, want 3000", n)
	}
	if n := count(t, conn, "tool_events"); n != 9000 {
		t.Fatalf("tool events = %d, want 9000", n)
	}
}

func manyCalls(n int) []core.CallRecord {
	calls := make([]core.CallRecord, n)
	for i := range calls {
		c := fullCall()
		c.TS = t0.Add(time.Duration(i) * time.Millisecond)
		c.ID = core.NewIDAt(c.TS)
		calls[i] = c
	}
	return calls
}

func TestWriteBatchHalvesInAnyOrder(t *testing.T) {
	ctx := context.Background()
	// use carries the use of toolu_x in its response; result returns it in
	// the next request. Written in either order, in one batch or two, the
	// store holds the same pair.
	use := core.CallRecord{
		ID: core.NewIDAt(t0), TS: t0, EndedAt: t0.Add(time.Second),
		KeyName: "k", Family: "anthropic", Endpoint: "/v1/messages", AuthMode: "proxy_key", Provider: "anthropic",
		SessionKey: "s",
		ToolUses:   []core.ToolUse{{Source: core.ToolSourceClient, ToolCallID: "toolu_x", Name: "Grep", ArgBytes: 5}},
	}
	result := core.CallRecord{
		ID: core.NewIDAt(t0.Add(3 * time.Second)), TS: t0.Add(3 * time.Second),
		KeyName: "k", Family: "anthropic", Endpoint: "/v1/messages", AuthMode: "proxy_key", Provider: "anthropic",
		SessionKey:  "s",
		ToolResults: []core.ToolResult{{Source: core.ToolSourceClient, ToolCallID: "toolu_x", ResultBytes: 8}},
	}
	for name, batches := range map[string][][]core.CallRecord{
		"use first":      {{use}, {result}},
		"result first":   {{result}, {use}},
		"one batch":      {{result, use}},
		"result replays": {{result}, {result}, {use}, {result}},
	} {
		t.Run(name, func(t *testing.T) {
			s, conn := newStore(t)
			for _, b := range batches {
				if err := s.WriteBatch(ctx, b); err != nil {
					t.Fatal(err)
				}
			}
			var pairs int
			var gap time.Duration
			err := conn.QueryRow(ctx, `
				SELECT count(*), coalesce(max(r.ts - u.ts), '0')
				FROM tool_events u JOIN tool_events r USING (tool_call_id)
				WHERE u.kind = 'use' AND r.kind = 'result'`).Scan(&pairs, &gap)
			if err != nil {
				t.Fatal(err)
			}
			if pairs != 1 || gap != 2*time.Second {
				t.Fatalf("pairs = %d, gap = %v; want 1 pair 2s apart", pairs, gap)
			}
		})
	}
}

func TestRunRetentionDeletesPastTheKeepPeriod(t *testing.T) {
	url := pgtest.NewDatabase(t)
	s, err := postgres.Open(context.Background(), postgres.Config{
		URL: url, MaxConns: 4,
		Retention: postgres.Retention{Keep: 400 * 24 * time.Hour, BatchSize: 2, BatchPause: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	g := storetest.NewGenAt(time.Now().Add(-500 * 24 * time.Hour))
	oldCalls := g.Calls(3, g.WithUse("toolu_old", "Bash"))
	young := g.Call(g.At(time.Now().Add(-time.Hour)), g.WithUse("toolu_new", "Bash"))
	if err := s.WriteBatch(ctx, append(oldCalls, young)); err != nil {
		t.Fatal(err)
	}
	if got := s.RetentionStats().LastRun; !got.IsZero() {
		t.Fatalf("last run is %v before any run", got)
	}
	calls, halves, err := s.RunRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || halves != 3 {
		t.Fatalf("deleted %d calls and %d halves, want 3 and 3", calls, halves)
	}
	if n, _ := s.CountRows(ctx, "calls"); n != 1 {
		t.Fatalf("%d calls left, want 1", n)
	}
	st := s.RetentionStats()
	if st.Deleted != 6 || time.Since(st.LastRun) > time.Minute {
		t.Fatalf("stats %+v after a run that deleted 6 rows", st)
	}
	// Running it again is safe.
	if calls, halves, err = s.RunRetention(ctx); err != nil || calls != 0 || halves != 0 {
		t.Fatalf("second run deleted %d calls and %d halves, err %v", calls, halves, err)
	}
}

func TestRunRetentionNeedsAKeepPeriod(t *testing.T) {
	s, _ := newStore(t)
	if _, _, err := s.RunRetention(context.Background()); err == nil {
		t.Fatal("RunRetention ran without a retention period")
	}
	if err := s.StartRetention(context.Background()); err == nil {
		t.Fatal("StartRetention started without a retention period")
	}
}

func TestStartAndStopRetention(t *testing.T) {
	url := pgtest.NewDatabase(t)
	s, err := postgres.Open(context.Background(), postgres.Config{
		URL: url, Retention: postgres.Retention{Keep: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	ctx := context.Background()
	if err := s.StartRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.StartRetention(ctx); err == nil {
		t.Fatal("StartRetention started a second job")
	}
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.StopRetention(stopCtx); err != nil {
		t.Fatal(err)
	}
	if err := s.StopRetention(stopCtx); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	// A stopped job can start again, and a cancelled parent context ends it.
	runCtx, end := context.WithCancel(ctx)
	if err := s.StartRetention(runCtx); err != nil {
		t.Fatal(err)
	}
	end()
	if err := s.StopRetention(stopCtx); err != nil {
		t.Fatal(err)
	}
}

// The bucket start does not depend on the session time zone.
func TestUsageBucketsIgnoreSessionTimeZone(t *testing.T) {
	url := pgtest.NewDatabase(t)
	setup := open(t, url)
	if err := setup.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	s := open(t, url+sep+"timezone=Pacific/Kiritimati") // UTC+14
	g := storetest.NewGenAt(time.Date(2026, 10, 27, 23, 30, 0, 0, time.UTC))
	ctx := context.Background()
	if err := s.WriteBatch(ctx, g.Calls(2)); err != nil {
		t.Fatal(err)
	}
	var tz string
	conn, err := pgx.Connect(ctx, url+sep+"timezone=Pacific/Kiritimati")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.QueryRow(ctx, "SHOW timezone").Scan(&tz); err != nil || tz != "Pacific/Kiritimati" {
		t.Fatalf("session time zone %q, err %v: the test does not change it", tz, err)
	}
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	got, err := s.Usage(ctx, core.UsageQuery{
		Filter: core.CallFilter{From: from, To: from.Add(48 * time.Hour)}, Grain: core.GrainDay,
	})
	if err != nil || len(got) != 1 || !got[0].Start.Equal(from) || got[0].Calls != 2 {
		t.Fatalf("buckets %+v, err %v; want one UTC day starting %v", got, err, from)
	}
}
