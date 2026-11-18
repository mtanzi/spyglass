package httpmw

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtanzi/spyglass/internal/core"
)

var update = flag.Bool("update", false, "rewrite golden files")

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRequestIDIsUUIDv7AndKeepsClientID(t *testing.T) {
	var gotID core.ID
	var gotClient string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotID, gotClient = CallID(r.Context()), ClientRequestID(r.Context())
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-Id", "client-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	hdr := rec.Header().Get("x-request-id")
	if hdr == "" || hdr == "client-abc" {
		t.Fatalf("x-request-id = %q, want a new id", hdr)
	}
	parsed, err := core.ParseID(hdr)
	if err != nil {
		t.Fatalf("x-request-id %q: %v", hdr, err)
	}
	if parsed != gotID {
		t.Errorf("header id %s != context id %s", parsed, gotID)
	}
	if parsed[6]>>4 != 7 {
		t.Errorf("id %s is not a UUIDv7", parsed)
	}
	if gotClient != "client-abc" {
		t.Errorf("ClientRequestID = %q, want client-abc", gotClient)
	}
}

func TestRecoverReturnsProtocolError(t *testing.T) {
	h := Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }), Options{Logger: discard()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	id := rec.Header().Get("x-request-id")
	var body struct {
		Type      string `json:"type"`
		Error     struct{ Type string }
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	if body.Type != "error" || body.Error.Type != "api_error" || body.RequestID != id || id == "" {
		t.Errorf("body = %+v, header id = %q", body, id)
	}
}

func TestMaxBytes(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			if !WriteBodyError(w, err) {
				t.Errorf("unexpected read error: %v", err)
			}
			return
		}
		w.WriteHeader(204)
	})
	h := Wrap(read, Options{Logger: discard(), MaxRequestBytes: func() int64 { return 10 }})

	t.Run("declared length", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 11))))
		checkOversize(t, rec)
	})
	t.Run("unknown length", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/", io.NopCloser(strings.NewReader(strings.Repeat("x", 11))))
		req.ContentLength = -1
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		checkOversize(t, rec)
	})
	t.Run("at the limit", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 10))))
		if rec.Code != 204 {
			t.Errorf("status = %d, want 204", rec.Code)
		}
	})
}

func checkOversize(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != 413 {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"request_too_large"`) {
		t.Errorf("body = %s", rec.Body)
	}
	if rec.Header().Get("x-request-id") == "" {
		t.Error("413 has no x-request-id")
	}
}

// TestAccessLogRedactionGolden logs a request that carries every credential
// header and a body, and compares the debug line to a golden file. Ids and
// timings are masked. The line must hold no credential value and no body.
func TestAccessLogRedactionGolden(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), Options{Logger: log})

	req := httptest.NewRequest("POST", "/v1/messages?api_key=in-query", strings.NewReader("SECRET-BODY"))
	req.RemoteAddr = "192.0.2.1:5555"
	req.Header.Set("Authorization", "Bearer sk-secret")
	req.Header.Set("x-api-key", "key-secret")
	req.Header.Set("x-spyglass-key", "lp_secret")
	req.Header.Set("x-llm-proxy-key", "lp_legacy_secret")
	req.Header.Set("Cookie", "session=cookie-secret")
	req.Header.Set("x-request-id", "client-1")
	req.Header.Set("User-Agent", "test-agent")
	h.ServeHTTP(httptest.NewRecorder(), req)

	line := buf.String()
	for _, secret := range []string{"sk-secret", "key-secret", "lp_secret", "lp_legacy_secret", "cookie-secret", "SECRET-BODY", "in-query"} {
		if strings.Contains(line, secret) {
			t.Errorf("log line contains %q:\n%s", secret, line)
		}
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line %q: %v", line, err)
	}
	for _, k := range []string{"time", "duration_ms", "call_id"} {
		if rec[k] == nil {
			t.Errorf("log line has no %q", k)
		}
		rec[k] = "masked:" + k
	}
	got, _ := json.MarshalIndent(rec, "", "  ")
	got = append(got, '\n')

	path := filepath.Join("testdata", "accesslog.golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("log line differs from %s (run with -update):\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}

func TestAccessLogLogsPanicAsServerError(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }), Options{Logger: log})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(buf.String(), `"status":500`) {
		t.Errorf("no status 500 in log:\n%s", buf.String())
	}
}

func TestInnerStepsRunInOrder(t *testing.T) {
	var order []string
	step := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }),
		Options{Logger: discard(), Inner: []Middleware{step("auth"), step("correlation")}})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got := strings.Join(order, ","); got != "auth,correlation,handler" {
		t.Errorf("order = %s", got)
	}
}

func TestRecoverAbortsAStartedResponse(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: chunk\n\n"))
		w.(http.Flusher).Flush()
		panic("boom")
	}), Options{Logger: log})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Errorf("read completed cleanly, want an aborted response; body %q", body)
	}
	if strings.Contains(string(body), "api_error") {
		t.Errorf("error body written into a started response: %q", body)
	}
	// The access log is written when the handler chain returns, which can be
	// after the client sees the abort. Close blocks until the handler is done,
	// so the buffer has no writer left when it is read.
	srv.Close()
	if !strings.Contains(buf.String(), `"status":200`) {
		t.Errorf("access log does not record the sent status 200:\n%s", buf.String())
	}
}
