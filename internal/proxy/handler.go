package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/family/anthropic"
	"github.com/mtanzi/spyglass/internal/httpmw"
)

// Header names the handler sets or removes.
const (
	headerProxyKey        = "X-Spyglass-Key"
	headerLegacyProxyKey  = "X-Llm-Proxy-Key" // no longer read; still removed from the upstream request so an old client's key never leaves
	headerAPIKey          = "X-Api-Key"
	headerAuthorization   = "Authorization"
	headerAcceptEncoding  = "Accept-Encoding"
	headerUpstreamRequest = "Request-Id"
)

// Snapshots gives the current config snapshot. *config.Holder implements it.
type Snapshots interface {
	Current() *config.Snapshot
}

// Recorder receives each finished call. Finished must not block: the handler
// calls it on the request goroutine. The recorder (ADR 0016) implements it.
type Recorder interface {
	Finished(core.CallRecord)
}

// Tap is an extra ModifyResponse hook. It runs once per upstream response,
// before the response reaches the client, after the built-in tap (ADR 0013)
// has wrapped resp.Body. It may replace resp.Body
// with a reader that scans the stream and passes every byte on unchanged, and
// it may fill rec as the exchange proceeds. request is the one read of the
// request body. The handler finishes rec only after the response body is
// closed, so everything the tap wrote is in the record by then.
type Tap func(rec *core.CallRecord, request []byte, resp *http.Response)

// Options configures a Handler. The zero value forwards and records nothing.
type Options struct {
	Recorder  Recorder  // nil drops finished calls
	Extractor Extractor // nil uses anthropic.Extractor
	Tap       Tap       // nil adds no hook after the built-in tap
	Logger    *slog.Logger
	Metrics   *Metrics // nil records no metrics
}

// Handler forwards the Anthropic data-plane endpoints to the configured
// provider. It authenticates the virtual key, reads the request body once,
// and streams the response back unchanged.
type Handler struct {
	snaps      Snapshots
	transports *Transports
	recorder   Recorder
	extractor  Extractor
	tap        Tap
	log        *slog.Logger
	metrics    *Metrics
	streams    atomic.Int64 // streaming requests being forwarded now
	calls      atomic.Int64 // forwarded calls whose record is not yet sent
}

// InflightStreams is the number of streaming requests being forwarded now. It
// is the value of spyglass_inflight_streams, kept even when the handler has no
// Metrics; graceful shutdown (ADR 0027) logs it when the drain deadline passes.
func (h *Handler) InflightStreams() int64 { return h.streams.Load() }

// InflightCalls is the number of forwarded calls, streaming or not, whose
// record has not yet reached the recorder. Graceful shutdown waits for it to
// reach zero after cutting streams, so their records are not lost.
func (h *Handler) InflightCalls() int64 { return h.calls.Load() }

// NewHandler returns a Handler that reads its providers and keys from snaps.
func NewHandler(snaps Snapshots, o Options) *Handler {
	h := &Handler{snaps: snaps, transports: NewTransports(), recorder: o.Recorder, extractor: o.Extractor, tap: o.Tap, log: o.Logger, metrics: o.Metrics}
	if h.extractor == nil {
		h.extractor = anthropic.Extractor{}
	}
	if h.log == nil {
		h.log = slog.New(slog.DiscardHandler)
	}
	return h
}

// forwarded reports whether the proxy forwards method and path. The query
// string is not part of the path, so /v1/messages?beta=true matches.
func forwarded(method, path string) bool {
	switch path {
	case "/v1/messages", "/v1/messages/count_tokens":
		return method == http.MethodPost
	case "/v1/models":
		return method == http.MethodGet
	}
	return false
}

// Hello answers HEAD /api/hello locally. It is a connection-warming probe and
// is not logged as a call.
func Hello(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// NotFound answers a path or method the proxy does not serve with the
// protocol 404.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	httpmw.WriteError(w, anthropic.NotFoundError(w.Header().Get(httpmw.HeaderRequestID)))
}

// ServeHTTP runs the hot path: auth, body read, correlation, forward.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !forwarded(r.Method, r.URL.Path) {
		NotFound(w, r)
		return
	}
	id := httpmw.CallID(r.Context())
	reqID := id.String()
	w.Header().Set(httpmw.HeaderRequestID, reqID)

	// One snapshot for the whole request: a reload never changes it midway.
	snap := h.snaps.Current()
	provider, ok := pickProvider(snap)
	if !ok {
		h.log.Error("no usable provider in the config snapshot", "call_id", reqID)
		httpmw.WriteError(w, anthropic.InternalError(reqID))
		return
	}
	keys := snap.Keys()
	if keys == nil {
		h.log.Error("config snapshot has no key index", "call_id", reqID)
		httpmw.WriteError(w, anthropic.InternalError(reqID))
		return
	}
	keyName, errResp, ok := Authenticate(r.Header, keys, provider.AuthMode, reqID)
	if !ok {
		httpmw.WriteError(w, errResp)
		return
	}
	target, err := url.Parse(provider.BaseURL)
	if err != nil {
		h.log.Error("provider base_url does not parse", "call_id", reqID, "provider", provider.Name)
		httpmw.WriteError(w, anthropic.InternalError(reqID))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		if !httpmw.WriteBodyError(w, err) {
			httpmw.WriteError(w, anthropic.MalformedRequestError(reqID))
		}
		return
	}

	start := time.Now()
	rec := core.CallRecord{
		ID:              id,
		TS:              start,
		KeyName:         keyName,
		ClientRequestID: httpmw.ClientRequestID(r.Context()),
		Family:          "anthropic",
		Endpoint:        r.URL.Path,
		AuthMode:        provider.AuthMode,
		Provider:        provider.Name,
	}
	Correlate(r.Header, &rec)

	f := &flight{h: h, rec: &rec, body: body, start: start, reqID: reqID, snap: snap, arrival: httpmw.Arrival(r.Context())}
	h.calls.Add(1)
	defer func() {
		p := recover()
		if p == http.ErrAbortHandler && f.failure == core.FailureNone && r.Context().Err() == nil {
			f.failure = core.FailureUpstreamUnreachable
		}
		f.finish(r.Context())
		if p != nil {
			panic(p)
		}
	}()

	// A stream outlasts any fixed write deadline.
	_ = httpmw.ClearWriteDeadline(w)

	// The idle watchdog cancels the upstream request through this context.
	// The client's own context stays on r, so a client abort is still told
	// apart from a timeout. Before the headers it watches only a streaming
	// request, whose headers come at once; a non-streaming request sends no
	// header until the model has finished, so the transport's response-header
	// timeout bounds that wait.
	upstreamCtx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	f.wd = newWatchdog(provider.IdleTimeout.Std(), cancel)
	defer f.wd.Disarm()
	if anthropic.RequestStreams(body) {
		f.wd.Arm()
		f.h.streams.Add(1)
		f.h.metrics.streamStarted()
		f.inflight = true
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	rp := &httputil.ReverseProxy{
		Rewrite:        func(pr *httputil.ProxyRequest) { rewrite(pr, target, provider) },
		Transport:      h.transports.For(provider),
		FlushInterval:  -1, // flush after every write: SSE chunks reach the client at once
		ModifyResponse: f.modifyResponse,
		ErrorHandler:   f.errorHandler,
	}
	outCtx := upstreamCtx
	if tr := f.clientTrace(); tr != nil {
		outCtx = httptrace.WithClientTrace(upstreamCtx, tr)
	}
	rp.ServeHTTP(w, r.WithContext(outCtx))
}

// pickProvider returns the one provider of v1. Config validation guarantees
// exactly one, of kind anthropic; the length check guards a snapshot built
// without validation.
func pickProvider(snap *config.Snapshot) (config.Provider, bool) {
	if snap == nil || len(snap.Config().Providers) != 1 {
		return config.Provider{}, false
	}
	return snap.Config().Providers[0], true
}

// rewrite points the outbound request at the provider and applies the
// credential. Every header it does not name is forwarded unchanged, so
// anthropic-version, anthropic-beta and the other anthropic-* headers keep
// their values. A virtual key never goes upstream: x-spyglass-key and the
// x-llm-proxy-key of release 0.1.0 are removed in every mode, so a key sent by
// an old client does not reach the upstream either. In proxy_key mode the
// client credential, which is the virtual key, is replaced by the provider key. In client_passthrough mode the client's
// own Authorization and x-api-key are forwarded as they came, and the proxy
// holds and injects no credential of its own. Accept-Encoding is set to
// identity in every mode, whatever the client sent.
func rewrite(pr *httputil.ProxyRequest, target *url.URL, p config.Provider) {
	pr.SetURL(target)
	out := pr.Out.Header
	out.Del(headerProxyKey)
	out.Del(headerLegacyProxyKey)
	// Ask for an uncompressed body so the tap can scan every response
	// (ADR 0050). The client's Accept-Encoding is not forwarded.
	out.Set(headerAcceptEncoding, "identity")
	if p.AuthMode == config.AuthClientPassthrough {
		return
	}
	out.Del(headerAuthorization)
	out.Del(headerAPIKey)
	if key := p.Credential.Ref.Secret().Reveal(); key != "" {
		out.Set(headerAPIKey, key)
	}
}

// flight is the state of one forwarded call. The handler goroutine owns it:
// ReverseProxy calls modifyResponse and errorHandler on that goroutine.
type flight struct {
	h     *Handler
	rec   *core.CallRecord
	body  []byte
	start time.Time
	reqID string
	wd    *watchdog
	snap  *config.Snapshot

	failure   core.FailureKind
	extracted bool // the tap ran the extractor
	inflight  bool // counted in spyglass_inflight_streams until finish

	// Proxy overhead (ADR 0026): arrival at the chain to the upstream request
	// headers written. The transport calls the trace hook on its own goroutine.
	arrival time.Time
	sent    sync.Once
}

// clientTrace measures spyglass_proxy_overhead_seconds. It observes once, when
// the headers of the first upstream request attempt are written.
func (f *flight) clientTrace() *httptrace.ClientTrace {
	if f.h.metrics == nil {
		return nil
	}
	return &httptrace.ClientTrace{
		WroteHeaders: func() {
			f.sent.Do(func() { f.h.metrics.observeOverhead(time.Since(f.arrival)) })
		},
	}
}

// modifyResponse records the upstream facts and installs the tap. The headers
// have arrived, so the watchdog rests until the tap reads. Response
// headers otherwise pass through. The proxy's call id replaces any
// x-request-id the upstream sent; the upstream's own id is kept as
// upstream_request_id.
func (f *flight) modifyResponse(resp *http.Response) error {
	f.rec.Status = resp.StatusCode
	f.rec.TTFB = time.Since(f.start)
	f.rec.UpstreamRequestID = resp.Header.Get(headerUpstreamRequest)
	f.rec.Streamed = strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
	resp.Header.Del(httpmw.HeaderRequestID)
	f.wd.Disarm()
	resp.Body = newTap(f, resp)
	if f.h.tap != nil {
		f.h.tap(f.rec, f.body, resp)
	}
	return nil
}

// errorHandler answers a failure to get a response from the upstream with an
// ADR 0007 body. A client that went away gets nothing it can read, but the
// call is still logged.
func (f *flight) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case f.wd.Fired():
		f.failure = core.FailureIdleTimeout
		f.rec.Status = http.StatusGatewayTimeout
		httpmw.WriteError(w, anthropic.UpstreamTimeoutError(f.reqID))
	case r.Context().Err() != nil:
		f.failure = core.FailureClientAbort
		f.rec.Status = 499
		w.WriteHeader(499)
	case isTimeout(err):
		f.failure = core.FailureUpstreamTimeout
		f.rec.Status = http.StatusGatewayTimeout
		httpmw.WriteError(w, anthropic.UpstreamTimeoutError(f.reqID))
	default:
		f.failure = core.FailureUpstreamUnreachable
		f.rec.Status = http.StatusBadGateway
		httpmw.WriteError(w, anthropic.UpstreamUnreachableError(f.reqID))
	}
	f.h.log.Warn("upstream request failed", "call_id", f.reqID,
		"provider", f.rec.Provider, "endpoint", f.rec.Endpoint, "status", f.rec.Status,
		"error_class", string(core.ClassifyOutcome(f.rec.Status, f.failure)))
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// finish completes the record and hands it to the recorder. It runs when
// ReverseProxy has returned, so the upstream body is closed and the tap is
// done. It also runs while a panic unwinds, so an aborted stream is logged: a
// stream cut by the upstream after the headers counts as an upstream failure.
// The order is the one of ADR 0014: extract, classify, price, drop the request
// bytes, send Finished.
func (f *flight) finish(ctx context.Context) {
	rec := f.rec
	now := time.Now()
	rec.Duration = now.Sub(f.start)
	rec.EndedAt = now
	if !f.extracted {
		// No response body reached the tap: the upstream was never reached or
		// the client left first. The request facts are still worth keeping.
		rec.Streamed = anthropic.RequestStreams(f.body)
		anthropic.ExtractRequest(rec, f.body)
	}
	if f.failure == core.FailureNone && ctx.Err() != nil {
		f.failure = core.FailureClientAbort
	}
	if rec.ErrorClass == "" {
		rec.ErrorClass = string(core.ClassifyOutcome(rec.Status, f.failure))
	}
	if rec.ErrorClass == string(core.ErrorClassClientAbort) {
		rec.UsageEstimated = true
	}
	f.price()
	f.body = nil
	if f.inflight {
		f.h.streams.Add(-1)
		f.h.metrics.streamEnded()
	}
	f.h.metrics.finished(rec.Family, rec.Provider, rec.Status, rec.ErrorClass, rec.Duration, rec.TTFT)
	if f.h.recorder != nil {
		f.h.recorder.Finished(*rec)
	}
	f.h.calls.Add(-1)
}

// price sets the cost and the pricing version from the snapshot the request
// started with (ADR 0015). A record without usage, or with a model no price
// entry matches, keeps a nil cost: an unknown cost is not zero.
func (f *flight) price() {
	rec := f.rec
	if len(rec.UsageRaw) == 0 || f.snap == nil {
		return
	}
	rec.Cost, rec.PricingVersion = core.CostAt(f.snap.Config().PriceVersions(), rec.Provider, rec.Model, rec.TS, rec.Usage)
}
