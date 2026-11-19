package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/httpmw"
	"github.com/mtanzi/spyglass/internal/keyring"
)

const (
	clientKey   = "lp_alice_secret"
	providerKey = "sk-ant-provider-secret"
)

// fakeRecorder keeps the finished calls.
type fakeRecorder struct {
	mu    sync.Mutex
	calls []core.CallRecord
	got   chan struct{}
}

func newFakeRecorder() *fakeRecorder { return &fakeRecorder{got: make(chan struct{}, 16)} }

func (f *fakeRecorder) Finished(c core.CallRecord) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	f.got <- struct{}{}
}

// next waits for one finished call.
func (f *fakeRecorder) next(t *testing.T) core.CallRecord {
	t.Helper()
	select {
	case <-f.got:
	case <-time.After(5 * time.Second):
		t.Fatal("no finished call")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func (f *fakeRecorder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// env is a proxy in front of a test upstream.
type env struct {
	proxy    *httptest.Server
	holder   *config.Holder
	rec      *fakeRecorder
	cfgMu    sync.Mutex
	baseURL  string
	authMode string
	logger   *slog.Logger // nil discards

	idle      string    // provider idle_timeout; empty keeps the default
	extractor Extractor // nil uses the anthropic extractor
	pricing   string    // YAML for the pricing key, appended to the config
	metrics   *Metrics  // nil records no metrics
	handler   *Handler  // set by start
}

func (e *env) yaml() string {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	idleLine := ""
	if e.idle != "" {
		idleLine = "\n    idle_timeout: " + e.idle
	}
	return fmt.Sprintf(`
database: { url: { env: DB } }
providers:
  - name: anthropic
    kind: anthropic
    base_url: %s
    auth_mode: %s
    credential: { env: PROVKEY }%s
keys:
  - name: alice
    sha256: %s
%s`, e.baseURL, e.authMode, idleLine, core.HashKey(clientKey), e.pricing)
}

// setBaseURL changes the upstream the next Reload points at.
func (e *env) setBaseURL(u string) {
	e.cfgMu.Lock()
	e.baseURL = u
	e.cfgMu.Unlock()
}

func newEnv(t *testing.T, upstream http.Handler, tap Tap, opts ...func(*env)) *env {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	return newEnvURL(t, up.URL, tap, opts...)
}

// newEnvMode is newEnv for a provider with the given auth_mode. A non-nil
// logger receives the handler's and the middleware's logs.
func newEnvMode(t *testing.T, upstream http.Handler, mode string, logger *slog.Logger) *env {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	return startEnv(t, &env{baseURL: up.URL, authMode: mode, rec: newFakeRecorder(), logger: logger}, nil)
}

// newEnvModeTap is newEnvMode with a tap and a trivial upstream.
func newEnvModeTap(t *testing.T, mode string, logger *slog.Logger, tap Tap) *env {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(up.Close)
	return startEnv(t, &env{baseURL: up.URL, authMode: mode, rec: newFakeRecorder(), logger: logger}, tap)
}

func newEnvURL(t *testing.T, upstreamURL string, tap Tap, opts ...func(*env)) *env {
	t.Helper()
	e := &env{baseURL: upstreamURL, authMode: config.AuthProxyKey, rec: newFakeRecorder()}
	for _, o := range opts {
		o(e)
	}
	return startEnv(t, e, tap)
}

func startEnv(t *testing.T, e *env, tap Tap) *env {
	t.Helper()
	e.holder = config.NewHolder("cfg.yaml", config.Options{
		ReadFile: func(string) ([]byte, error) { return []byte(e.yaml()), nil },
		LookupEnv: func(k string) (string, bool) {
			switch k {
			case "DB":
				return "postgres://x", true
			case "PROVKEY":
				return providerKey, true
			}
			return "", false
		},
		BuildKeys: func(keys []core.Key) (config.KeyIndex, error) { return keyring.New(keys) },
	}, nil)
	if err := e.holder.Load(); err != nil {
		t.Fatal(err)
	}
	e.start(t, tap, nil)
	return e
}

func (e *env) start(t *testing.T, tap Tap, mw func(http.Handler) http.Handler) {
	t.Helper()
	logger := e.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := NewHandler(e.holder, Options{Recorder: e.rec, Extractor: e.extractor, Tap: tap, Logger: logger, Metrics: e.metrics})
	e.handler = h
	var root http.Handler = NewMux(h, nil)
	if mw != nil {
		root = mw(root)
	}
	e.proxy = httptest.NewServer(httpmw.Wrap(root, httpmw.Options{Logger: logger, MaxRequestBytes: func() int64 { return 64 << 10 }}))
	t.Cleanup(e.proxy.Close)
}

func (e *env) do(t *testing.T, method, path string, body string, hdr ...string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.proxy.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readAll(t *testing.T, r io.ReadCloser) string {
	t.Helper()
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func auth() []string { return []string{"Authorization", "Bearer " + clientKey} }

// seen is what the upstream received.
type seen struct {
	method, uri string
	header      http.Header
	body        string
}

func capture(into chan<- seen, then http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		into <- seen{r.Method, r.RequestURI, r.Header.Clone(), string(b)}
		if then != nil {
			then(w, r)
		}
	}
}

func TestForwardsBytesAndHeadersUnchanged(t *testing.T) {
	got := make(chan seen, 1)
	e := newEnv(t, capture(got, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}), nil)

	// Odd spacing, unicode and a cache_control marker must survive byte for byte.
	body := "{ \"model\":\"claude-opus-4-8\", \"system\":[{\"type\":\"text\",\"text\":\"héllo\",\"cache_control\":{\"type\":\"ephemeral\"}}] }\n"
	resp := e.do(t, "POST", "/v1/messages?beta=true", body, append(auth(),
		"Content-Type", "application/json",
		"Anthropic-Version", "2023-06-01",
		"Anthropic-Beta", "oauth-2025-04-20,prompt-caching-scope-2026-01-05",
		"Anthropic-Future-Thing", "x",
		"X-Claude-Code-Session-Id", "sess-1",
		"Traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"User-Agent", "claude-cli/2.1.290 (external, cli)",
	)...)
	if s := readAll(t, resp.Body); s != `{"ok":true}` || resp.StatusCode != 200 {
		t.Fatalf("response %d %q", resp.StatusCode, s)
	}

	up := <-got
	if up.body != body {
		t.Errorf("body changed: %q", up.body)
	}
	if up.method != "POST" || up.uri != "/v1/messages?beta=true" {
		t.Errorf("upstream saw %s %s", up.method, up.uri)
	}
	for k, want := range map[string]string{
		"Anthropic-Version":        "2023-06-01",
		"Anthropic-Beta":           "oauth-2025-04-20,prompt-caching-scope-2026-01-05",
		"Anthropic-Future-Thing":   "x",
		"X-Claude-Code-Session-Id": "sess-1",
		"Traceparent":              "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"User-Agent":               "claude-cli/2.1.290 (external, cli)",
		"Content-Type":             "application/json",
		"X-Api-Key":                providerKey,
	} {
		if g := up.header.Get(k); g != want {
			t.Errorf("upstream %s = %q, want %q", k, g, want)
		}
	}
	if g := up.header.Get("Authorization"); g != "" {
		t.Errorf("client Authorization reached upstream: %q", g)
	}
	if g := up.header.Get("X-Forwarded-For"); g != "" {
		t.Errorf("X-Forwarded-For = %q", g)
	}

	rec := e.rec.next(t)
	if rec.KeyName != "alice" || rec.SessionKey != "sess-1" || rec.Client != "claude-cli" ||
		rec.Endpoint != "/v1/messages" || rec.Provider != "anthropic" || rec.AuthMode != "proxy_key" ||
		rec.Status != 200 || rec.ErrorClass != "" || rec.Family != "anthropic" {
		t.Errorf("record %+v", rec)
	}
	if rec.ID != (core.ID{}) && resp.Header.Get("X-Request-Id") != rec.ID.String() {
		t.Errorf("x-request-id %q, record id %s", resp.Header.Get("X-Request-Id"), rec.ID)
	}
}

func TestUpstreamAcceptEncodingIsIdentity(t *testing.T) {
	for _, client := range []string{"gzip, deflate, br", "", "identity"} {
		got := make(chan seen, 1)
		e := newEnv(t, capture(got, nil), nil)
		kv := auth()
		if client != "" {
			kv = append(kv, "Accept-Encoding", client)
		}
		_ = readAll(t, e.do(t, "POST", "/v1/messages", "{}", kv...).Body)
		up := <-got
		if g := up.header.Values("Accept-Encoding"); len(g) != 1 || g[0] != "identity" {
			t.Errorf("client %q: upstream Accept-Encoding = %q, want identity", client, g)
		}
	}
}

func TestVirtualKeyNeverReachesUpstream(t *testing.T) {
	got := make(chan seen, 1)
	e := newEnv(t, capture(got, nil), nil)
	resp := e.do(t, "POST", "/v1/messages", "{}",
		"Authorization", "Bearer "+clientKey,
		"X-Api-Key", "lp_other_key",
		"Anthropic-Beta", "prompt-caching-scope-2026-01-05",
	)
	_ = readAll(t, resp.Body)
	up := <-got
	for k, vs := range up.header {
		for _, v := range vs {
			if strings.Contains(v, "lp_") {
				t.Errorf("header %s carries a virtual key: %q", k, v)
			}
		}
	}
	if up.header.Get("Anthropic-Beta") != "prompt-caching-scope-2026-01-05" {
		t.Errorf("anthropic-beta = %q", up.header.Get("Anthropic-Beta"))
	}
	if g := up.header.Values("X-Api-Key"); len(g) != 1 || g[0] != providerKey {
		t.Errorf("x-api-key values %q, want only the provider key", g)
	}
}

func TestXAPIKeyClientCredentialIsReplaced(t *testing.T) {
	got := make(chan seen, 1)
	e := newEnv(t, capture(got, nil), nil)
	_ = readAll(t, e.do(t, "POST", "/v1/messages", "{}", "X-Api-Key", clientKey).Body)
	up := <-got
	if g := up.header.Values("X-Api-Key"); len(g) != 1 || g[0] != providerKey {
		t.Errorf("x-api-key values %q", g)
	}
}

func TestResponseHeadersPassThrough(t *testing.T) {
	e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Retry-After", "7")
		h.Set("X-Should-Retry", "true")
		h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.42")
		h.Set("Request-Id", "req_upstream_1")
		h.Set("X-Request-Id", "upstream-own-id")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	}), nil)
	resp := e.do(t, "POST", "/v1/messages", "{}", auth()...)
	_ = readAll(t, resp.Body)
	for k, want := range map[string]string{
		"Content-Type":   "application/json",
		"Retry-After":    "7",
		"X-Should-Retry": "true",
		"Anthropic-Ratelimit-Unified-5h-Utilization": "0.42",
		"Request-Id": "req_upstream_1",
	} {
		if g := resp.Header.Get(k); g != want {
			t.Errorf("%s = %q, want %q", k, g, want)
		}
	}
	ids := resp.Header.Values("X-Request-Id")
	if len(ids) != 1 || ids[0] == "upstream-own-id" {
		t.Fatalf("x-request-id values %q; want only the proxy's call id", ids)
	}
	rec := e.rec.next(t)
	if rec.UpstreamRequestID != "req_upstream_1" || rec.ID.String() != ids[0] {
		t.Errorf("record upstream id %q, id %s", rec.UpstreamRequestID, rec.ID)
	}
}

func TestUpstreamErrorsPassThroughUnchanged(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
		class  string
	}{
		{429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, "rate_limited"},
		{500, `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, "upstream_5xx"},
		{529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "upstream_5xx"},
		{400, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`, "invalid_request"},
	} {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "3")
				w.Header().Set("X-Should-Retry", "true")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}), nil)
			resp := e.do(t, "POST", "/v1/messages", "{}", auth()...)
			if resp.StatusCode != tt.status {
				t.Errorf("status %d, want %d", resp.StatusCode, tt.status)
			}
			if resp.Header.Get("Retry-After") != "3" || resp.Header.Get("X-Should-Retry") != "true" {
				t.Errorf("headers %v", resp.Header)
			}
			if resp.Header.Get("X-Request-Id") == "" {
				t.Error("no x-request-id")
			}
			if b := readAll(t, resp.Body); b != tt.body {
				t.Errorf("body %q, want %q", b, tt.body)
			}
			if rec := e.rec.next(t); rec.Status != tt.status || rec.ErrorClass != tt.class {
				t.Errorf("record status %d class %q, want %d %q", rec.Status, rec.ErrorClass, tt.status, tt.class)
			}
		})
	}
}

func TestStreamingFlushesPerChunk(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
		fl.Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		fl.Flush()
	}), nil)

	resp := e.do(t, "POST", "/v1/messages", `{"stream":true}`, auth()...)
	defer func() { _ = resp.Body.Close() }()
	br := bufio.NewReader(resp.Body)
	// The first event must arrive while the upstream is still holding the stream.
	first := make(chan string, 1)
	go func() { l, _ := br.ReadString('\n'); first <- l }()
	select {
	case l := <-first:
		if l != "event: ping\n" {
			t.Errorf("first line %q", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first chunk not flushed before the upstream finished")
	}
	close(release)
	rest, _ := io.ReadAll(br)
	if want := "data: {\"type\":\"ping\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"; string(rest) != want {
		t.Errorf("rest %q, want %q", rest, want)
	}
	if rec := e.rec.next(t); !rec.Streamed {
		t.Error("record not marked streamed")
	}
}

func TestClientAbortCancelsUpstream(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}), nil)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", e.proxy.URL+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+clientKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	_ = resp.Body.Close()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request was not cancelled")
	}
	rec := e.rec.next(t)
	if rec.ErrorClass != string(core.ErrorClassClientAbort) {
		t.Errorf("error class %q, want client_abort", rec.ErrorClass)
	}
}

func TestUpstreamCutMidStreamIsAFailure(t *testing.T) {
	e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		_ = conn.Close()
	}), nil)

	resp := e.do(t, "POST", "/v1/messages", `{"stream":true}`, auth()...)
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	rec := e.rec.next(t)
	if rec.Status != http.StatusOK {
		t.Errorf("status %d, want 200", rec.Status)
	}
	if rec.ErrorClass != string(core.ErrorClassUpstream5xx) {
		t.Errorf("error class %q, want upstream_5xx", rec.ErrorClass)
	}
}

func TestAuthFailuresNeverReachUpstream(t *testing.T) {
	hit := make(chan seen, 1)
	e := newEnv(t, capture(hit, nil), nil)
	for name, hdr := range map[string][]string{
		"missing": nil,
		"unknown": {"Authorization", "Bearer lp_nope"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := e.do(t, "POST", "/v1/messages", "{}", hdr...)
			b := readAll(t, resp.Body)
			if resp.StatusCode != 401 || !strings.Contains(b, `"authentication_error"`) {
				t.Errorf("got %d %s", resp.StatusCode, b)
			}
			if resp.Header.Get("X-Request-Id") == "" {
				t.Error("no x-request-id")
			}
		})
	}
	select {
	case <-hit:
		t.Error("an unauthenticated request reached the upstream")
	default:
	}
	if e.rec.count() != 0 {
		t.Error("an auth failure was recorded as a call")
	}
}

func TestHelloIsLocal(t *testing.T) {
	hit := make(chan seen, 1)
	e := newEnv(t, capture(hit, nil), nil)
	resp := e.do(t, "HEAD", "/api/hello", "")
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("X-Request-Id") == "" {
		t.Errorf("status %d, headers %v", resp.StatusCode, resp.Header)
	}
	select {
	case <-hit:
		t.Error("hello reached the upstream")
	default:
	}
	if e.rec.count() != 0 {
		t.Error("hello was recorded as a call")
	}
}

func TestModelsAndCountTokensAreForwarded(t *testing.T) {
	got := make(chan seen, 2)
	e := newEnv(t, capture(got, nil), nil)
	_ = readAll(t, e.do(t, "GET", "/v1/models?limit=5", "", auth()...).Body)
	if up := <-got; up.method != "GET" || up.uri != "/v1/models?limit=5" {
		t.Errorf("models: %s %s", up.method, up.uri)
	}
	_ = readAll(t, e.do(t, "POST", "/v1/messages/count_tokens", `{"x":1}`, auth()...).Body)
	if up := <-got; up.uri != "/v1/messages/count_tokens" || up.body != `{"x":1}` {
		t.Errorf("count_tokens: %s %q", up.uri, up.body)
	}
}

func TestUnsupportedRequestsGetProtocol404(t *testing.T) {
	hit := make(chan seen, 1)
	e := newEnv(t, capture(hit, nil), nil)
	for _, tt := range []struct{ method, path string }{
		{"POST", "/v1/complete"},
		{"GET", "/v1/messages"},
		{"POST", "/v1/models"},
		{"POST", "/v1/messages/"},
		{"POST", "/v1/chat/completions"},
		{"GET", "/anything"},
		{"GET", "/api/hello"},
	} {
		resp := e.do(t, tt.method, tt.path, "", auth()...)
		b := readAll(t, resp.Body)
		if resp.StatusCode != 404 || !strings.Contains(b, `"not_found_error"`) || resp.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s = %d %s", tt.method, tt.path, resp.StatusCode, b)
		}
	}
	select {
	case <-hit:
		t.Error("an unsupported path reached the upstream")
	default:
	}
}

func TestOversizeBodyIsRejectedBeforeForwarding(t *testing.T) {
	hit := make(chan seen, 1)
	e := newEnv(t, capture(hit, nil), nil)
	// Chunked, so only the reader limit can catch it.
	req, _ := http.NewRequest("POST", e.proxy.URL+"/v1/messages", io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 128<<10))))
	req.Header.Set("Authorization", "Bearer "+clientKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if b := readAll(t, resp.Body); resp.StatusCode != 413 || !strings.Contains(b, "request_too_large") {
		t.Errorf("got %d %s", resp.StatusCode, b)
	}
	select {
	case <-hit:
		t.Error("an oversize body reached the upstream")
	default:
	}
}

func TestUpstreamUnreachableAndTimeout(t *testing.T) {
	t.Run("unreachable", func(t *testing.T) {
		dead := httptest.NewServer(http.NotFoundHandler())
		url := dead.URL
		dead.Close()
		e := newEnvURL(t, url, nil)
		resp := e.do(t, "POST", "/v1/messages", "{}", auth()...)
		b := readAll(t, resp.Body)
		if resp.StatusCode != 502 || !strings.Contains(b, "upstream unreachable") {
			t.Errorf("got %d %s", resp.StatusCode, b)
		}
		if rec := e.rec.next(t); rec.Status != 502 || rec.ErrorClass != "upstream_5xx" {
			t.Errorf("record %d %q", rec.Status, rec.ErrorClass)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		if !isTimeout(context.DeadlineExceeded) || isTimeout(io.EOF) {
			t.Error("isTimeout misclassifies")
		}
	})
}

func TestTapSeam(t *testing.T) {
	var gotBody string
	tap := func(rec *core.CallRecord, request []byte, resp *http.Response) {
		gotBody = string(request)
		rec.Model = "claude-opus-4-8"
		resp.Body = &upper{resp.Body}
	}
	e := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "abc")
	}), tap)
	resp := e.do(t, "POST", "/v1/messages", `{"model":"claude-opus-4-8"}`, auth()...)
	if b := readAll(t, resp.Body); b != "ABC" {
		t.Errorf("tap-wrapped body %q", b)
	}
	rec := e.rec.next(t)
	if gotBody != `{"model":"claude-opus-4-8"}` || rec.Model != "claude-opus-4-8" {
		t.Errorf("tap saw %q, record model %q", gotBody, rec.Model)
	}
}

type upper struct{ io.ReadCloser }

func (u *upper) Read(p []byte) (int, error) {
	n, err := u.ReadCloser.Read(p)
	copy(p, bytes.ToUpper(p[:n]))
	return n, err
}

func TestReloadChangesUpstream(t *testing.T) {
	var firstHits int
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstHits++
		_, _ = io.WriteString(w, "first")
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "second")
	}))
	t.Cleanup(second.Close)

	e := newEnvURL(t, first.URL, nil)
	if b := readAll(t, e.do(t, "POST", "/v1/messages", "{}", auth()...).Body); b != "first" {
		t.Fatalf("before reload: %q", b)
	}
	e.setBaseURL(second.URL)
	if err := e.holder.Reload(); err != nil {
		t.Fatal(err)
	}
	if b := readAll(t, e.do(t, "POST", "/v1/messages", "{}", auth()...).Body); b != "second" {
		t.Errorf("after reload: %q", b)
	}
	if firstHits != 1 {
		t.Errorf("first upstream hit %d times", firstHits)
	}
}

func TestTransportsSwapOnProviderChange(t *testing.T) {
	ts := NewTransports()
	p := config.Provider{Name: "anthropic", BaseURL: "https://a.example"}
	a := ts.For(p)
	if ts.For(p) != a {
		t.Error("same provider got a new transport")
	}
	p.BaseURL = "https://b.example"
	b := ts.For(p)
	if b == a {
		t.Error("changed base_url kept the old transport")
	}
	if ts.For(config.Provider{Name: "other", BaseURL: "https://a.example"}) == b {
		t.Error("two providers share a transport")
	}
}

func TestTransportSettings(t *testing.T) {
	tr := newTransport()
	if tr.ResponseHeaderTimeout <= 0 || tr.DialContext == nil || !tr.ForceAttemptHTTP2 || tr.Proxy != nil {
		t.Errorf("transport %+v", tr)
	}
}
