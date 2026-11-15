package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
)

// oauthToken stands in for the client's own subscription credential.
const oauthToken = "sk-ant-oat01-PASSTHROUGH-MARKER"

func passthroughAuth(extra ...string) []string {
	return append([]string{
		"Authorization", "Bearer " + oauthToken,
		"X-Spyglass-Key", clientKey,
		"Anthropic-Beta", "oauth-2025-04-20,claude-code-20250219",
	}, extra...)
}

func TestPassthroughForwardsClientCredentialUnchanged(t *testing.T) {
	got := make(chan seen, 1)
	e := newEnvMode(t, capture(got, nil), config.AuthClientPassthrough, nil)
	resp := e.do(t, "POST", "/v1/messages", "{}", passthroughAuth()...)
	_ = readAll(t, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	up := <-got
	if g := up.header.Values("Authorization"); len(g) != 1 || g[0] != "Bearer "+oauthToken {
		t.Errorf("upstream Authorization = %q", g)
	}
	if g := up.header.Get("Anthropic-Beta"); g != "oauth-2025-04-20,claude-code-20250219" {
		t.Errorf("upstream anthropic-beta = %q", g)
	}
	if g := up.header.Values("X-Api-Key"); len(g) != 0 {
		t.Errorf("proxy injected x-api-key %q; it must not hold or inject a credential", g)
	}
	rec := e.rec.next(t)
	if rec.KeyName != "alice" || rec.AuthMode != config.AuthClientPassthrough {
		t.Errorf("record key %q auth_mode %q", rec.KeyName, rec.AuthMode)
	}
}

func TestPassthroughForwardsClientXAPIKeyUnchanged(t *testing.T) {
	got := make(chan seen, 1)
	e := newEnvMode(t, capture(got, nil), config.AuthClientPassthrough, nil)
	_ = readAll(t, e.do(t, "POST", "/v1/messages", "{}", "X-Spyglass-Key", clientKey, "X-Api-Key", "sk-ant-api03-client").Body)
	up := <-got
	if g := up.header.Values("X-Api-Key"); len(g) != 1 || g[0] != "sk-ant-api03-client" {
		t.Errorf("upstream x-api-key = %q", g)
	}
}

func TestPassthroughKeyHeaderNeverReachesUpstream(t *testing.T) {
	got := make(chan seen, 1)
	e := newEnvMode(t, capture(got, nil), config.AuthClientPassthrough, nil)
	_ = readAll(t, e.do(t, "POST", "/v1/messages", "{}", passthroughAuth()...).Body)
	up := <-got
	for k, vs := range up.header {
		for _, v := range vs {
			if strings.Contains(v, "lp_") {
				t.Errorf("header %s carries a virtual key: %q", k, v)
			}
		}
	}
	if g := up.header.Values("X-Spyglass-Key"); len(g) != 0 {
		t.Errorf("x-spyglass-key reached upstream: %q", g)
	}
}

// The x-llm-proxy-key of 0.1.0 is no longer a key header (ADR 0054): a request
// that carries only it is rejected, and it never reaches the upstream, so an
// old client's key does not leak. With x-spyglass-key present it is ignored.
func TestPassthroughLegacyKeyHeaderIsNotHonoured(t *testing.T) {
	var logs syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := make(chan seen, 2)
	e := newEnvMode(t, capture(got, nil), config.AuthClientPassthrough, logger)

	legacy := []string{"Authorization", "Bearer " + oauthToken, "X-Llm-Proxy-Key", clientKey}
	resp := e.do(t, "POST", "/v1/messages", "{}", legacy...)
	b := readAll(t, resp.Body)
	if resp.StatusCode != 401 || !strings.Contains(b, `"authentication_error"`) {
		t.Errorf("legacy header only: got %d %s, want 401", resp.StatusCode, b)
	}
	select {
	case <-got:
		t.Error("a request with only the legacy header reached the upstream")
	default:
	}
	if e.rec.count() != 0 {
		t.Error("an auth failure was recorded as a call")
	}

	resp = e.do(t, "POST", "/v1/messages", "{}", append(passthroughAuth(), "X-Llm-Proxy-Key", "lp_nope")...)
	_ = readAll(t, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("both headers: status %d", resp.StatusCode)
	}
	up := <-got
	if g := append(up.header.Values("X-Llm-Proxy-Key"), up.header.Values("X-Spyglass-Key")...); len(g) != 0 {
		t.Errorf("a key header reached upstream: %q", g)
	}
	_ = e.rec.next(t)

	out := logs.String()
	if strings.Contains(out, "x-llm-proxy-key") || strings.Contains(out, "deprecated") {
		t.Errorf("logs still mention the legacy header:\n%s", out)
	}
	if strings.Contains(out, clientKey) || strings.Contains(out, "lp_nope") {
		t.Errorf("logs carry a virtual key:\n%s", out)
	}
}

func TestWrongPlaceCredentialIsRejected(t *testing.T) {
	for _, tt := range []struct {
		name string
		mode string
		hdr  []string
	}{
		{"passthrough virtual key in Authorization", config.AuthClientPassthrough,
			[]string{"X-Spyglass-Key", clientKey, "Authorization", "Bearer " + clientKey}},
		{"passthrough virtual key in bare Authorization", config.AuthClientPassthrough,
			[]string{"X-Spyglass-Key", clientKey, "Authorization", clientKey}},
		{"passthrough virtual key in x-api-key", config.AuthClientPassthrough,
			[]string{"X-Spyglass-Key", clientKey, "X-Api-Key", clientKey}},
		{"passthrough virtual key only in Authorization", config.AuthClientPassthrough,
			[]string{"Authorization", "Bearer " + clientKey}},
		{"passthrough request without key header", config.AuthClientPassthrough,
			[]string{"Authorization", "Bearer " + oauthToken}},
		{"passthrough unknown key header", config.AuthClientPassthrough,
			[]string{"Authorization", "Bearer " + oauthToken, "X-Spyglass-Key", "lp_nope"}},
		{"proxy_key with x-spyglass-key", config.AuthProxyKey,
			append(auth(), "X-Spyglass-Key", clientKey)},
		{"proxy_key with only x-spyglass-key", config.AuthProxyKey,
			[]string{"X-Spyglass-Key", clientKey}},
		{"passthrough virtual key in Authorization with legacy header", config.AuthClientPassthrough,
			[]string{"X-Llm-Proxy-Key", clientKey, "Authorization", "Bearer " + clientKey}},
		{"passthrough unknown legacy key header", config.AuthClientPassthrough,
			[]string{"Authorization", "Bearer " + oauthToken, "X-Llm-Proxy-Key", "lp_nope"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hit := make(chan seen, 1)
			e := newEnvMode(t, capture(hit, nil), tt.mode, nil)
			resp := e.do(t, "POST", "/v1/messages", "{}", tt.hdr...)
			b := readAll(t, resp.Body)
			if resp.StatusCode != 401 || !strings.Contains(b, `"authentication_error"`) {
				t.Errorf("got %d %s", resp.StatusCode, b)
			}
			select {
			case <-hit:
				t.Error("a rejected request reached the upstream")
			default:
			}
			if e.rec.count() != 0 {
				t.Error("an auth failure was recorded as a call")
			}
		})
	}
}

func TestPassthroughRateLimitHeadersAreForwarded(t *testing.T) {
	want := map[string]string{
		"Anthropic-Ratelimit-Unified-Status":               "allowed",
		"Anthropic-Ratelimit-Unified-5h-Utilization":       "0.42",
		"Anthropic-Ratelimit-Unified-7d-Reset":             "1793000000",
		"Anthropic-Ratelimit-Unified-Representative-Claim": "five_hour",
	}
	e := newEnvMode(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range want {
			w.Header().Set(k, v)
		}
		_, _ = io.WriteString(w, "{}")
	}), config.AuthClientPassthrough, nil)
	resp := e.do(t, "POST", "/v1/messages", "{}", passthroughAuth()...)
	_ = readAll(t, resp.Body)
	for k, v := range want {
		if g := resp.Header.Get(k); g != v {
			t.Errorf("%s = %q, want %q", k, g, v)
		}
	}
}

// syncBuffer is a log sink the handler goroutines and the test share.
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

// The passthrough credential must appear nowhere the proxy writes: logs at
// debug level, the call record, and what the tap seam is handed. The proxy has
// no metrics yet, and the store path takes the record that is checked here.
func TestPassthroughCredentialAppearsNowhereTheProxyWrites(t *testing.T) {
	var logs syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var tapped []byte
	e := newEnvModeTap(t, config.AuthClientPassthrough, logger, func(_ *core.CallRecord, request []byte, _ *http.Response) {
		tapped = request
	})
	resp := e.do(t, "POST", "/v1/messages?beta=true", `{"model":"m"}`, passthroughAuth(
		"X-Api-Key", oauthToken, "Cookie", "c="+oauthToken)...)
	_ = readAll(t, resp.Body)
	rec := e.rec.next(t)
	if b, _ := json.Marshal(rec); bytes.Contains(b, []byte(oauthToken)) || bytes.Contains(b, []byte(clientKey)) {
		t.Errorf("call record holds a credential: %s", b)
	}
	if bytes.Contains(tapped, []byte(oauthToken)) {
		t.Errorf("tap request holds a credential: %s", tapped)
	}
	// The access log line is written after the handler returns, which can be
	// after the record is finished. Close waits for the handler to complete.
	e.proxy.Close()
	out := logs.String()
	if out == "" {
		t.Fatal("no logs captured")
	}
	for _, secret := range []string{oauthToken, clientKey, "PASSTHROUGH-MARKER", "lp_alice"} {
		if strings.Contains(out, secret) {
			t.Errorf("logs hold %q:\n%s", secret, out)
		}
	}
}
