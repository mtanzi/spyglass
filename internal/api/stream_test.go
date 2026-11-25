package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/core/coretest"
	"github.com/mtanzi/spyglass/internal/httpmw"
	"github.com/mtanzi/spyglass/internal/recorder"
)

// streamWait bounds every wait of the stream tests. It only guards against a
// hang; no test sleeps for it.
const streamWait = 5 * time.Second

// sseFrame is one SSE frame: an event, or a comment when comment is set.
type sseFrame struct {
	id, event, data, comment string
}

// readFrame reads up to the blank line that ends a frame.
func readFrame(br *bufio.Reader) (sseFrame, error) {
	var f sseFrame
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return f, err
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			return f, nil
		case strings.HasPrefix(line, ":"):
			f.comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
		case strings.HasPrefix(line, "id: "):
			f.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			f.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			f.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// readEvent reads the next event frame, skipping comments.
func readEvent(t *testing.T, br *bufio.Reader) sseFrame {
	t.Helper()
	for {
		f, err := readFrame(br)
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
		if f.comment == "" {
			return f
		}
	}
}

// liveEnv is a real recorder behind the admin mux, served over real HTTP
// through the request chain of the admin listener.
type liveEnv struct {
	rec  *recorder.Recorder
	live *countingLive
	srv  *httptest.Server
	// done receives once each time a /admin/stream handler has returned.
	done chan struct{}
}

// countingLive counts the subscriptions that have not ended.
type countingLive struct {
	Live
	active atomic.Int64
}

func (l *countingLive) Subscribe(f func(core.CallRecord) bool) (<-chan recorder.Event, func()) {
	ch, unsub := l.Live.Subscribe(f)
	l.active.Add(1)
	var once sync.Once
	return ch, func() {
		unsub()
		once.Do(func() { l.active.Add(-1) })
	}
}

func newLiveEnv(t *testing.T, keepAlive time.Duration) *liveEnv {
	t.Helper()
	e := &liveEnv{
		rec:  recorder.New(&coretest.Writer{}, recorder.Options{}),
		done: make(chan struct{}, 16),
	}
	e.live = &countingLive{Live: e.rec}
	mux := NewMux(Options{Metrics: nopGatherer{}, Live: e.live, keepAlive: keepAlive})
	chain := httpmw.Wrap(mux, httpmw.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chain.ServeHTTP(w, r)
		if r.URL.Path == "/admin/stream" {
			e.done <- struct{}{}
		}
	}))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), streamWait)
		defer cancel()
		_ = e.rec.Close(ctx)
		e.srv.Close()
	})
	return e
}

// open starts a stream with the query and returns once the response headers
// are in: by then the handler has subscribed. Cancel ends the client side.
func (e *liveEnv) open(t *testing.T, query string) (resp *http.Response, br *bufio.Reader, cancel func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+"/admin/stream"+query, nil)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body), cancel
}

// waitHandlerDone waits until one stream handler has returned.
func (e *liveEnv) waitHandlerDone(t *testing.T) {
	t.Helper()
	select {
	case <-e.done:
	case <-time.After(streamWait):
		t.Fatal("stream handler did not return")
	}
}

func liveCall(model string, status int) core.CallRecord {
	return core.CallRecord{ID: core.NewID(), TS: time.Now(), KeyName: "alice", Provider: "anthropic", Model: model, Status: status}
}

// The stream is real HTTP through the admin request chain: the headers that
// stop proxies from buffering are set, and an event reaches the client while
// the response is still open, so nothing buffers it on the way.
func TestStreamOverRealHTTPIsNotBuffered(t *testing.T) {
	e := newLiveEnv(t, time.Hour)
	resp, br, _ := e.open(t, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	for name, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("the stream sends a CORS header")
	}

	c := liveCall("m", 200)
	c.PromptID, c.Project = "prompt-1", "spyglass"
	e.rec.Finished(c)
	f := readEvent(t, br)
	if f.event != EventCallFinished || f.id != c.ID.String() {
		t.Errorf("frame %+v, want event %s with id %s", f, EventCallFinished, c.ID)
	}
	var row callSummaryJSON
	if err := json.Unmarshal([]byte(f.data), &row); err != nil || row.PromptID != "prompt-1" || row.Project != "spyglass" {
		t.Errorf("data prompt_id = %q project = %q (err %v), want prompt-1 and spyglass", row.PromptID, row.Project, err)
	}

	// The frame matches the documented event schema.
	s := loadSpec(t)
	sch, err := s.c.Compile("openapi.json#/components/schemas/CallFinishedEvent")
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(`{"id":"` + f.id + `","event":"` + f.event + `","data":` + f.data + `}`))
	if err != nil {
		t.Fatalf("data is not JSON: %v\n%s", err, f.data)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("event does not match CallFinishedEvent: %v\n%s", err, f.data)
	}
}

// Only calls that pass the filter reach the client; the others are dropped.
// Events come in the order the recorder fans them out, so the first event
// read proves the earlier calls were dropped, not delayed.
func TestStreamAppliesTheFilter(t *testing.T) {
	e := newLiveEnv(t, time.Hour)
	_, br, _ := e.open(t, "?model=a&status=200")

	e.rec.Finished(liveCall("b", 200))
	e.rec.Finished(liveCall("a", 500))
	want := liveCall("a", 200)
	e.rec.Finished(want)
	if f := readEvent(t, br); f.id != want.ID.String() {
		t.Fatalf("first event is call %s, want %s: the filter let a non-matching call through", f.id, want.ID)
	}
	e.rec.Finished(liveCall("b", 500))
	next := liveCall("a", 200)
	e.rec.Finished(next)
	if f := readEvent(t, br); f.id != next.ID.String() {
		t.Errorf("second event is call %s, want %s", f.id, next.ID)
	}
	if n := e.rec.LiveEventsDropped(); n != 0 {
		t.Errorf("live events dropped = %d, want 0", n)
	}
}

func TestStreamKeepAlive(t *testing.T) {
	e := newLiveEnv(t, time.Millisecond)
	_, br, _ := e.open(t, "")
	f, err := readFrame(br)
	if err != nil {
		t.Fatal(err)
	}
	if f.comment != "keep-alive" || f.event != "" {
		t.Errorf("frame %+v, want a keep-alive comment", f)
	}
}

// A client that goes away ends the handler and its subscription, so no
// goroutine and no recorder subscriber is left behind.
func TestStreamDisconnectUnsubscribes(t *testing.T) {
	e := newLiveEnv(t, time.Hour)
	_, _, cancel := e.open(t, "")
	if n := e.live.active.Load(); n != 1 {
		t.Fatalf("active subscriptions = %d after open, want 1", n)
	}
	cancel()
	e.waitHandlerDone(t)
	if n := e.live.active.Load(); n != 0 {
		t.Errorf("active subscriptions = %d after disconnect, want 0", n)
	}
	var stacks bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&stacks, 1)
	if strings.Contains(stacks.String(), "api.(*handlers).stream") {
		t.Errorf("a stream handler goroutine is still running:\n%s", stacks.String())
	}
	// A subscriber that nobody reads would lose events past its buffer. The
	// recorder has none left, so nothing is dropped.
	for range 1000 {
		e.rec.Finished(liveCall("m", 200))
	}
	if n := e.rec.LiveEventsDropped(); n != 0 {
		t.Errorf("live events dropped = %d after disconnect, want 0: the subscriber was not removed", n)
	}
}

// Shutdown ends open streams: the recorder closes the subscription channels
// and each handler returns, ending its response.
func TestStreamEndsWhenRecorderCloses(t *testing.T) {
	e := newLiveEnv(t, time.Hour)
	_, br, _ := e.open(t, "")
	_, br2, _ := e.open(t, "?model=a")
	ctx, cancel := context.WithTimeout(context.Background(), streamWait)
	defer cancel()
	if err := e.rec.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*bufio.Reader{br, br2} {
		if _, err := io.ReadAll(r); err != nil {
			t.Errorf("stream did not end cleanly: %v", err)
		}
	}
	e.waitHandlerDone(t)
	e.waitHandlerDone(t)
	if n := e.live.active.Load(); n != 0 {
		t.Errorf("active subscriptions = %d after shutdown, want 0", n)
	}
}

// stuckWriter is a response writer whose client never reads: every Write
// blocks until release. Its first Flush (the headers, after the handler has
// subscribed) closes flushed.
type stuckWriter struct {
	h       http.Header
	once    sync.Once
	flushed chan struct{}
	release chan struct{}
}

func (w *stuckWriter) Header() http.Header { return w.h }
func (w *stuckWriter) WriteHeader(int)     {}
func (w *stuckWriter) Flush()              { w.once.Do(func() { close(w.flushed) }) }
func (w *stuckWriter) Write([]byte) (int, error) {
	<-w.release
	return 0, errors.New("client gone")
}

// A subscriber whose client does not read never blocks the recorder: Finished
// returns at once, and the events past the subscriber's buffer are dropped and
// counted.
func TestStreamSlowSubscriberDoesNotBlockRecorder(t *testing.T) {
	rec := recorder.New(&coretest.Writer{}, recorder.Options{})
	defer func() { _ = rec.Close(context.Background()) }()
	h := NewMux(Options{Metrics: nopGatherer{}, Live: rec, keepAlive: time.Hour})

	w := &stuckWriter{h: http.Header{}, flushed: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		h.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/stream", nil))
	}()
	select {
	case <-w.flushed:
	case <-time.After(streamWait):
		t.Fatal("stream did not subscribe")
	}

	const n = 1000
	published := make(chan struct{})
	go func() {
		defer close(published)
		for range n {
			rec.Finished(liveCall("m", 200))
		}
	}()
	select {
	case <-published:
	case <-time.After(streamWait):
		t.Fatal("Finished blocked on a subscriber whose client does not read")
	}
	// The handler holds at most one event in its stuck write; the subscriber
	// buffer holds the rest it can.
	if got := rec.LiveEventsDropped(); got == 0 || got >= n {
		t.Errorf("live events dropped = %d, want between 1 and %d", got, n-1)
	}

	cancel()
	close(w.release)
	select {
	case <-handlerDone:
	case <-time.After(streamWait):
		t.Fatal("stream handler did not return")
	}
}

func TestStreamErrors(t *testing.T) {
	notWired := NewMux(Options{Metrics: nopGatherer{}})
	if rr := get(notWired, "/admin/stream"); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no recorder: status %d, want 503", rr.Code)
	}

	rec := recorder.New(&coretest.Writer{}, recorder.Options{})
	defer func() { _ = rec.Close(context.Background()) }()
	h := NewMux(Options{Metrics: nopGatherer{}, Live: rec})
	s := loadSpec(t)
	for _, q := range []string{"?limit=5", "?cursor=x", "?nope=1", "?status=abc", "?from=2026-10-21T12:00:00Z&to=2026-10-21T11:00:00Z"} {
		rr := get(h, "/admin/stream"+q)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, rr.Code)
			continue
		}
		s.check(t, "/admin/stream", rr.Code, rr.Body.Bytes())
	}
}

func TestParseStreamFilter(t *testing.T) {
	q := func(s string) url.Values { v, _ := url.ParseQuery(s); return v }

	f, err := parseStreamFilter(q("model=a&model=b&streamed=true"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !f.From.IsZero() || !f.To.IsZero() {
		t.Errorf("range [%v, %v), want open: a stream has no default range", f.From, f.To)
	}
	if len(f.Models) != 2 || f.Streamed == nil || !*f.Streamed {
		t.Errorf("filter %+v, want the call-list mapping", f)
	}
	later := core.CallRecord{TS: testNow.Add(time.Hour), Model: "a", Streamed: true}
	if !f.Match(later, nil) {
		t.Error("a call that starts after the stream opens does not match")
	}

	f, err = parseStreamFilter(q("to=2026-10-21T13:00:00Z"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !f.To.Equal(testNow.Add(time.Hour)) || !f.From.IsZero() {
		t.Errorf("range [%v, %v), want only the explicit to", f.From, f.To)
	}
	f, err = parseStreamFilter(q("from=2026-10-21T11:00:00Z"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !f.From.Equal(testNow.Add(-time.Hour)) || !f.To.IsZero() {
		t.Errorf("range [%v, %v), want only the explicit from", f.From, f.To)
	}

	// A from ahead of the server clock, with no to, is a valid open tail.
	f, err = parseStreamFilter(q("from=2026-10-21T13:00:00Z"), testNow)
	if err != nil {
		t.Fatalf("future from: %v", err)
	}
	if !f.From.Equal(testNow.Add(time.Hour)) || !f.To.IsZero() {
		t.Errorf("range [%v, %v), want only the explicit from", f.From, f.To)
	}
	for _, s := range []string{"from=2026-10-21T13:00:00Z&to=2026-10-21T13:00:00Z", "from=2026-10-21T14:00:00Z&to=2026-10-21T13:00:00Z"} {
		if _, err := parseStreamFilter(q(s), testNow); err == nil || err.parameter != "from" {
			t.Errorf("%s: err = %v, want a from error", s, err)
		}
	}
	if _, err := parseStreamFilter(q("from=yesterday"), testNow); err == nil || err.parameter != "from" {
		t.Errorf("bad from: err = %v, want a from error", err)
	}
}

// The tool filters match the tool calls a call carries when it finishes: its
// uses, paired with a result in the same call for a server tool.
func TestStreamToolFilters(t *testing.T) {
	yes, no := true, false
	client := core.CallRecord{ID: core.NewID(), ToolUses: []core.ToolUse{{Source: core.ToolSourceClient, ToolCallID: "toolu_1", Name: "Bash"}},
		// A result for an earlier call's use never counts.
		ToolResults: []core.ToolResult{{Source: core.ToolSourceClient, ToolCallID: "toolu_0", IsError: true}}}
	server := core.CallRecord{ID: core.NewID(),
		ToolUses:    []core.ToolUse{{Source: core.ToolSourceServer, ToolCallID: "srvtoolu_1", Name: "web_search"}},
		ToolResults: []core.ToolResult{{Source: core.ToolSourceServer, ToolCallID: "srvtoolu_1", IsError: true}}}
	bare := core.CallRecord{ID: core.NewID()}

	for _, tc := range []struct {
		name string
		f    core.CallFilter
		c    core.CallRecord
		want bool
	}{
		{"tool name", core.CallFilter{Tools: []string{"Bash"}}, client, true},
		{"other tool name", core.CallFilter{Tools: []string{"Read"}}, client, false},
		{"no tools", core.CallFilter{Tools: []string{"Bash"}}, bare, false},
		{"client result unknown", core.CallFilter{ToolError: &yes}, client, false},
		{"server error", core.CallFilter{ToolError: &yes}, server, true},
		{"server not ok", core.CallFilter{ToolError: &no}, server, false},
		{"server tool and error", core.CallFilter{Tools: []string{"web_search"}, ToolError: &yes}, server, true},
	} {
		if got := tc.f.Match(tc.c, liveToolCalls(tc.c)); got != tc.want {
			t.Errorf("%s: match = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := summaryOf(client).ToolCount; got != 1 {
		t.Errorf("tool count = %d, want 1", got)
	}
}
