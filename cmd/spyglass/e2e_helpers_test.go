package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jackc/pgx/v5"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/fakeupstream"
	"github.com/mtanzi/spyglass/internal/keyring"
	"github.com/mtanzi/spyglass/internal/store/postgres"
	"github.com/mtanzi/spyglass/internal/store/postgres/pgtest"
)

// End-to-end support (ADR 0038). An e2eEnv is the real serve wiring (openStore,
// buildApp and the two listeners) against a fake Anthropic upstream and a test
// Postgres. Tests talk to it as a client does, through anthropic-sdk-go, and
// read results as an operator does, through the admin API.

const (
	e2eProviderSecret = "e2e-provider-secret"
	// e2eDefaultKey is the name of the key every e2eEnv starts with.
	e2eDefaultKey = "alice"
)

// syncBuffer is a log sink that tests can read while the proxy writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// e2eEnv is one running proxy with its fake upstream and database.
type e2eEnv struct {
	t        *testing.T
	Upstream *fakeupstream.Server
	Logs     *syncBuffer
	DBURL    string // the real database, for direct scans
	PG       *tcpProxy
	run      *running
	keys     map[string]string // name -> plaintext
	stopped  bool
}

// newE2E starts the proxy with one key, e2eDefaultKey. The store connects to
// Postgres through a forwarder that a test can cut to simulate an outage.
func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	dbURL := pgtest.NewDatabase(t)
	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	pg := newTCPProxy(t, u.Host)
	u.Host = pg.Addr()

	e := &e2eEnv{t: t, Upstream: fakeupstream.New(t), Logs: &syncBuffer{}, DBURL: dbURL, PG: pg, keys: map[string]string{}}
	e.CreateKey(e2eDefaultKey)
	log := newLogger(config.Log{Level: "debug", Format: "json"}, e.Logs)

	st, err := openStore(context.Background(), postgres.Config{URL: u.String()}, log, defaultStartupRetry())
	if err != nil {
		t.Fatal(err)
	}
	a, err := buildApp(e.holder(), st, log)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	e.run = start(t, a)
	t.Cleanup(func() {
		e.PG.Restore() // a flush needs the database
		e.Stop()
	})
	return e
}

// CreateKey makes a virtual key the way keygen does and returns its plaintext.
// A key made before the proxy starts is in its first config; the keys of a
// running proxy are not changed.
func (e *e2eEnv) CreateKey(name string) string {
	e.t.Helper()
	k, err := newKey(rand.Reader)
	if err != nil {
		e.t.Fatal(err)
	}
	e.keys[name] = k
	return k
}

// Key returns the plaintext of a key made with CreateKey.
func (e *e2eEnv) Key(name string) string { return e.keys[name] }

// holder builds the config holder: one proxy_key provider at the fake
// upstream, the keys made so far, and a price for the fixture model.
func (e *e2eEnv) holder() *config.Holder {
	var keys strings.Builder
	for name, k := range e.keys {
		fmt.Fprintf(&keys, "  - name: %s\n    sha256: %s\n", name, core.HashKey(k))
	}
	yaml := fmt.Sprintf(`
database: { url: { env: DB } }
providers:
  - name: anthropic
    kind: anthropic
    base_url: %s
    auth_mode: proxy_key
    credential: { env: PROVKEY }
keys:
%s
pricing:
  - { provider: anthropic, model: claude-opus-4-8, from: 2026-01-01, input: 5.00, output: 25.00, cache_read: 0.50, cache_write_5m: 6.25, cache_write_1h: 10.00 }
`, e.Upstream.URL, keys.String())
	h := config.NewHolder("e2e.yaml", config.Options{
		ReadFile: func(string) ([]byte, error) { return []byte(yaml), nil },
		LookupEnv: func(k string) (string, bool) {
			switch k {
			case "DB":
				return "postgres://unused", true
			case "PROVKEY":
				return e2eProviderSecret, true
			}
			return "", false
		},
		BuildKeys: func(keys []core.Key) (config.KeyIndex, error) { return keyring.New(keys) },
	}, nil)
	if err := h.Load(); err != nil {
		e.t.Fatal(err)
	}
	return h
}

// Stop shuts the proxy down the way SIGTERM does. It is safe to call twice.
func (e *e2eEnv) Stop() {
	e.t.Helper()
	if e.stopped {
		return
	}
	e.stopped = true
	if err := e.run.stop(e.t); err != nil {
		e.t.Errorf("shutdown: %v", err)
	}
}

// DataURL and AdminURL are the base URLs of the two planes.
func (e *e2eEnv) DataURL() string  { return e.run.data }
func (e *e2eEnv) AdminURL() string { return e.run.admin }

// Client is an anthropic-sdk-go client aimed at the proxy with the given key.
// It does not retry, so an error status reaches the test as the proxy sent it.
func (e *e2eEnv) Client(key string, opts ...option.RequestOption) anthropic.Client {
	base := []option.RequestOption{
		option.WithBaseURL(e.DataURL()),
		option.WithAPIKey(""),
		option.WithAuthToken(key),
		option.WithMaxRetries(0),
	}
	return anthropic.NewClient(append(base, opts...)...)
}

// DefaultClient is a client for the default key.
func (e *e2eEnv) DefaultClient(opts ...option.RequestOption) anthropic.Client {
	return e.Client(e.Key(e2eDefaultKey), opts...)
}

// e2eFixture loads a fixture of testdata/fixtures.
func e2eFixture(t *testing.T, name string) fakeupstream.Fixture {
	t.Helper()
	f, err := fakeupstream.Load(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// withMarker returns f with every FAKEPROMPT text, in the request and in the
// response, replaced by marker.
func withMarker(f fakeupstream.Fixture, marker string) fakeupstream.Fixture {
	sub := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("FAKEPROMPT"), []byte(marker)) }
	f.Request, f.Response = sub(f.Request), sub(f.Response)
	return f
}

// params reads a fixture request into SDK parameters.
func params(t *testing.T, f fakeupstream.Fixture) anthropic.MessageNewParams {
	t.Helper()
	var p anthropic.MessageNewParams
	if err := json.Unmarshal(f.Request, &p); err != nil {
		t.Fatalf("fixture %s request: %v", f.Name, err)
	}
	return p
}

// Stream sends the fixture request as a streaming call and returns the
// accumulated message and the stream error.
func (e *e2eEnv) Stream(c anthropic.Client, f fakeupstream.Fixture, opts ...option.RequestOption) (anthropic.Message, error) {
	e.t.Helper()
	return streamMessage(context.Background(), c, params(e.t, f), opts...)
}

func streamMessage(ctx context.Context, c anthropic.Client, p anthropic.MessageNewParams, opts ...option.RequestOption) (anthropic.Message, error) {
	var msg anthropic.Message
	s := c.Messages.NewStreaming(ctx, p, opts...)
	defer func() { _ = s.Close() }()
	for s.Next() {
		if err := msg.Accumulate(s.Current()); err != nil {
			return msg, err
		}
	}
	return msg, s.Err()
}

// Send sends the fixture request as a non-streaming call.
func (e *e2eEnv) Send(c anthropic.Client, f fakeupstream.Fixture, opts ...option.RequestOption) (*anthropic.Message, error) {
	e.t.Helper()
	return c.Messages.New(context.Background(), params(e.t, f), opts...)
}

// Admin reads a path of the admin API and decodes the JSON into out, which may
// be nil. It fails the test on any status but 200.
func (e *e2eEnv) Admin(path string, out any) []byte {
	e.t.Helper()
	code, body := e.AdminStatus(path)
	if code != http.StatusOK {
		e.t.Fatalf("GET %s = %d: %s", path, code, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			e.t.Fatalf("GET %s: %v: %s", path, err, body)
		}
	}
	return body
}

// AdminStatus reads a path of the admin API and returns the status and body.
func (e *e2eEnv) AdminStatus(path string) (int, []byte) {
	e.t.Helper()
	cl := http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Get(e.AdminURL() + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// e2eCall is the part of an admin call summary the tests read.
type e2eCall struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	SessionKey string `json:"session_key"`
	Model      string `json:"model"`
	Status     int    `json:"status"`
	ErrorClass string `json:"error_class"`
	StopReason string `json:"stop_reason"`
	Streamed   bool   `json:"streamed"`
	Usage      struct {
		InputUncached int `json:"input_uncached"`
		CacheWrite5m  int `json:"cache_write_5m"`
		Output        int `json:"output"`
	} `json:"usage"`
	CostMicroUSD *int64 `json:"cost_micro_usd"`
	ToolCount    int    `json:"tool_count"`
}

// e2eDetail is the part of an admin call detail the tests read.
type e2eDetail struct {
	e2eCall
	Client            string `json:"client"`
	AgentID           string `json:"agent_id"`
	ParentAgentID     string `json:"parent_agent_id"`
	AgentType         string `json:"agent_type"`
	PromptID          string `json:"prompt_id"`
	RequestClass      string `json:"request_class"`
	Project           string `json:"project"`
	Compaction        bool   `json:"compaction"`
	ContextCompacted  bool   `json:"context_compacted"`
	TraceID           string `json:"trace_id"`
	AuthMode          string `json:"auth_mode"`
	ErrorType         string `json:"error_type"`
	UpstreamRequestID string `json:"upstream_request_id"`
	PrevToolDurations string `json:"prev_tool_durations"`
	UsageEstimated    bool   `json:"usage_estimated"`
	ToolUses          []struct {
		Source     string `json:"source"`
		ToolCallID string `json:"tool_call_id"`
		Name       string `json:"name"`
		ArgBytes   int    `json:"arg_bytes"`
	} `json:"tool_uses"`
	ToolResults []struct {
		Source      string `json:"source"`
		ToolCallID  string `json:"tool_call_id"`
		IsError     bool   `json:"is_error"`
		ResultBytes int    `json:"result_bytes"`
	} `json:"tool_results"`
}

// e2eToolCall is one paired tool call of the admin API.
type e2eToolCall struct {
	ToolCallID   string  `json:"tool_call_id"`
	Name         string  `json:"name"`
	Source       string  `json:"source"`
	Status       string  `json:"status"`
	UseCallID    *string `json:"use_call_id"`
	ResultCallID *string `json:"result_call_id"`
}

// WaitCalls polls the call log until it holds at least n calls, then returns
// them oldest first. The recorder writes in batches, so a row shows up a
// moment after its call ends.
func (e *e2eEnv) WaitCalls(n int) []e2eCall {
	e.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for {
		var list struct {
			Calls []e2eCall `json:"calls"`
		}
		if code, body := e.AdminStatus("/admin/calls?limit=200"); code == http.StatusOK {
			if err := json.Unmarshal(body, &list); err != nil {
				e.t.Fatalf("/admin/calls: %v: %s", err, body)
			}
			if len(list.Calls) >= n {
				// The API lists newest first.
				for i, j := 0, len(list.Calls)-1; i < j; i, j = i+1, j-1 {
					list.Calls[i], list.Calls[j] = list.Calls[j], list.Calls[i]
				}
				return list.Calls
			}
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("call log holds %d calls after 45s, want %d\nlogs:\n%s", len(list.Calls), n, e.Logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Detail reads one call from the admin API.
func (e *e2eEnv) Detail(id string) e2eDetail {
	e.t.Helper()
	var d e2eDetail
	e.Admin("/admin/calls/"+id, &d)
	return d
}

// ToolCalls reads the paired tool calls of a session.
func (e *e2eEnv) ToolCalls(session string) []e2eToolCall {
	e.t.Helper()
	var out struct {
		ToolCalls []e2eToolCall `json:"tool_calls"`
	}
	e.Admin("/admin/tool-calls?session="+url.QueryEscape(session)+"&limit=100", &out)
	return out.ToolCalls
}

// StoredText returns every value of every table of the database as text, one
// row per entry, so a test can search all of it.
func (e *e2eEnv) StoredText() []string {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, e.DBURL)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		e.t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, tbl := range tables {
		q := "SELECT t::text FROM " + pgx.Identifier{tbl}.Sanitize() + " t"
		rows, err := conn.Query(ctx, q)
		if err != nil {
			e.t.Fatalf("scan %s: %v", tbl, err)
		}
		vals, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			e.t.Fatalf("scan %s: %v", tbl, err)
		}
		for _, v := range vals {
			out = append(out, tbl+": "+v)
		}
	}
	return out
}

// tcpProxy forwards TCP to a target and can be cut and restored: a stand-in for
// a Postgres that goes away and comes back.
type tcpProxy struct {
	t      *testing.T
	target string
	addr   string

	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]struct{}
}

func newTCPProxy(t *testing.T, target string) *tcpProxy {
	t.Helper()
	p := &tcpProxy{t: t, target: target, conns: map[net.Conn]struct{}{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.addr = ln.Addr().String()
	p.serve(ln)
	t.Cleanup(p.Cut)
	return p
}

// Addr is the host:port to connect to instead of the target.
func (p *tcpProxy) Addr() string { return p.addr }

func (p *tcpProxy) serve(ln net.Listener) {
	p.mu.Lock()
	p.ln = ln
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.forward(c)
		}
	}()
}

func (p *tcpProxy) forward(c net.Conn) {
	up, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = c.Close()
		return
	}
	p.mu.Lock()
	p.conns[c], p.conns[up] = struct{}{}, struct{}{}
	p.mu.Unlock()
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go pipe(up, c)
	go pipe(c, up)
	<-done
	_ = c.Close()
	_ = up.Close()
	p.mu.Lock()
	delete(p.conns, c)
	delete(p.conns, up)
	p.mu.Unlock()
}

// Cut closes the listener and every open connection: the database is gone.
func (p *tcpProxy) Cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		_ = p.ln.Close()
		p.ln = nil
	}
	for c := range p.conns {
		_ = c.Close()
	}
}

// Restore listens again on the same address: the database is back.
func (p *tcpProxy) Restore() {
	p.mu.Lock()
	up := p.ln != nil
	p.mu.Unlock()
	if up {
		return
	}
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ {
		if ln, err = net.Listen("tcp", p.addr); err == nil {
			p.serve(ln)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.t.Errorf("restore postgres forwarder: %v", err)
}
