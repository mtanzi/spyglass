package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxCapture bounds what one body keeps in memory.
const maxCapture = 64 << 20

// credentialHeaders never reach disk, not even in a raw capture.
// x-llm-proxy-key is no longer read by the proxy (ADR 0054); it stays so a key
// sent by an old client is still redacted.
var credentialHeaders = map[string]bool{
	"authorization": true, "x-api-key": true, "x-spyglass-key": true, "x-llm-proxy-key": true,
	"proxy-authorization": true, "cookie": true, "set-cookie": true,
}

const redacted = "[redacted]"

// rawMeta is the meta.json of a raw capture and of a fixture: the shape the
// extractor goldens read.
type rawMeta struct {
	Endpoint   string `json:"endpoint"`
	Status     int    `json:"status"`
	Streamed   bool   `json:"streamed"`
	TTFBMs     int    `json:"ttfb_ms"`
	TTFTMs     int    `json:"ttft_ms"`
	DurationMs int    `json:"duration_ms"`
}

func runRecord(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:8081", "address to listen on")
	upstream := fs.String("upstream", "https://api.anthropic.com", "the real API")
	out := fs.String("out", "", "directory for raw captures (required, outside the repository)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "fixturecap record: -out is required")
		return 2
	}
	if err := refuseRepoPath(*out); err != nil {
		_, _ = fmt.Fprintf(stderr, "fixturecap record: %v\n", err)
		return 2
	}
	target, err := url.Parse(*upstream)
	if err != nil || target.Host == "" {
		_, _ = fmt.Fprintf(stderr, "fixturecap record: -upstream %q is not a URL\n", *upstream)
		return 2
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		_, _ = fmt.Fprintf(stderr, "fixturecap record: %v\n", err)
		return 1
	}
	rec := &recorder{out: *out, log: stderr}
	_, _ = fmt.Fprintf(stderr, "fixturecap: recording %s to %s, raw captures hold real prompts: keep them out of git\n", target, *out)
	srv := &http.Server{Addr: *listen, Handler: rec.handler(target), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		_, _ = fmt.Fprintf(stderr, "fixturecap record: %v\n", err)
		return 1
	}
	return 0
}

// refuseRepoPath refuses an output directory inside a git checkout: a raw
// capture committed by accident would publish real prompts.
func refuseRepoPath(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for p := abs; ; p = filepath.Dir(p) {
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
			return fmt.Errorf("%s is inside the git checkout %s; raw captures hold real prompts, use a directory outside it", dir, p)
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

type recorder struct {
	out string
	log io.Writer
	n   atomic.Int64
}

func (r *recorder) handler(target *url.URL) http.Handler {
	return &httputil.ReverseProxy{
		FlushInterval: -1, // stream chunks as they come
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			// Let the transport negotiate gzip itself and decode it, so the
			// capture is plain text.
			pr.Out.Header.Del("Accept-Encoding")
		},
		Transport: &capturingTransport{rec: r, next: http.DefaultTransport},
	}
}

// capturingTransport records each exchange around the real round trip.
type capturingTransport struct {
	rec  *recorder
	next http.RoundTripper
}

func (c *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ex := &exchange{rec: c.rec, id: int(c.rec.n.Add(1)), start: time.Now(), path: req.URL.Path}
	ex.reqHeader = redactHeaders(req.Header)
	if req.Body != nil {
		body, err := io.ReadAll(io.LimitReader(req.Body, maxCapture))
		if err != nil {
			return nil, err
		}
		ex.reqBody = body
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	resp, err := c.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	ex.status = resp.StatusCode
	ex.ttfb = time.Since(ex.start)
	ex.respHeader = redactHeaders(resp.Header)
	ex.streamed = strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
	resp.Body = &teeBody{src: resp.Body, ex: ex}
	return resp, nil
}

type exchange struct {
	rec        *recorder
	id         int
	start      time.Time
	path       string
	reqHeader  http.Header
	reqBody    []byte
	status     int
	respHeader http.Header
	streamed   bool
	ttfb, ttft time.Duration

	mu       sync.Mutex
	respBody bytes.Buffer
	done     bool
}

// teeBody copies the response body into the exchange as the client reads it.
type teeBody struct {
	src io.ReadCloser
	ex  *exchange
}

func (b *teeBody) Read(p []byte) (int, error) {
	n, err := b.src.Read(p)
	if n > 0 {
		b.ex.add(p[:n])
	}
	if err != nil {
		b.ex.finish()
	}
	return n, err
}

func (b *teeBody) Close() error {
	err := b.src.Close()
	b.ex.finish()
	return err
}

func (e *exchange) add(p []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ttft == 0 && bytes.Contains(p, []byte("event: content_block_delta")) {
		e.ttft = time.Since(e.start)
	}
	if room := maxCapture - e.respBody.Len(); room > 0 {
		e.respBody.Write(p[:min(len(p), room)])
	}
}

// finish writes the exchange once, when its response body ends.
func (e *exchange) finish() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return
	}
	e.done = true
	if err := e.write(); err != nil {
		_, _ = fmt.Fprintf(e.rec.log, "fixturecap: exchange %04d not written: %v\n", e.id, err)
		return
	}
	_, _ = fmt.Fprintf(e.rec.log, "fixturecap: exchange %04d %s status %d\n", e.id, e.path, e.status)
}

func (e *exchange) write() error {
	dir := filepath.Join(e.rec.out, fmt.Sprintf("%04d", e.id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body := "response.json"
	if e.streamed {
		body = "response.sse"
	}
	meta := rawMeta{Endpoint: e.path, Status: e.status, Streamed: e.streamed,
		TTFBMs: int(e.ttfb.Milliseconds()), TTFTMs: int(e.ttft.Milliseconds()), DurationMs: int(time.Since(e.start).Milliseconds())}
	files := []struct {
		name string
		data []byte
	}{
		{"request.json", e.reqBody},
		{body, e.respBody.Bytes()},
		{"request.headers.json", mustJSON(e.reqHeader)},
		{"response.headers.json", mustJSON(e.respHeader)},
		{"meta.json", mustJSON(meta)},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// redactHeaders copies h with credential headers blanked.
func redactHeaders(h http.Header) http.Header {
	out := http.Header{}
	for k, v := range h {
		if credentialHeaders[strings.ToLower(k)] {
			out[k] = []string{redacted}
			continue
		}
		out[k] = append([]string(nil), v...)
	}
	return out
}

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}
