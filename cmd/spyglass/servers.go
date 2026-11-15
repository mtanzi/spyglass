package main

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/httpmw"
	"github.com/mtanzi/spyglass/internal/proxy"
)

// Server timeouts. There is deliberately no WriteTimeout: it would cut long
// SSE streams (ADR 0006).
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
)

// Servers are the two listeners of the binary. The caller starts them with
// ServeData and ServeAdmin and stops them with Shutdown.
type Servers struct {
	Data  *http.Server
	Admin *http.Server

	// Drain is the shutdown flag. Drain.Start makes both /readyz endpoints
	// report not-ready; graceful shutdown (ADR 0027) calls it first.
	Drain *httpmw.Drain
	// Inflight is the number of streams being forwarded now, from the proxy
	// handler. Shutdown logs it when the drain deadline passes.
	Inflight func() int64
	// InflightCalls is the number of forwarded calls, streaming or not, whose
	// record has not reached the recorder yet. Shutdown waits for it after
	// cutting streams.
	InflightCalls func() int64

	tls config.TLS
}

// ServerOptions is what BuildServers needs. The composition root (serve.go)
// builds every handler; BuildServers only puts each behind the request chain
// and into an http.Server.
type ServerOptions struct {
	// Listen holds the listener addresses and the data-plane TLS pair.
	Listen config.Listen
	// Data and Admin are the finished handlers of the two planes.
	Data, Admin http.Handler
	// MaxRequestBytes returns the request size limit, read once per request.
	MaxRequestBytes func() int64
	// Drain is the flag the readiness checks of both planes read. Shutdown
	// starts it.
	Drain *httpmw.Drain
	// Inflight and InflightCalls come from the proxy handler. Both may be nil.
	Inflight, InflightCalls func() int64
	Logger                  *slog.Logger
}

// BuildServers builds the data-plane and admin-plane servers, each behind the
// request chain. The admin plane also sets the browser security headers
// (httpmw.SecurityHeaders); the data plane does not. It refuses a non-loopback listen.admin, so the
// unauthenticated admin plane never leaves the host even when validation was
// skipped.
func BuildServers(o ServerOptions) (*Servers, error) {
	if !config.IsLoopback(o.Listen.Admin) {
		return nil, fmt.Errorf("listen.admin %q is not a loopback address; the admin plane must listen on 127.0.0.1, ::1 or localhost", o.Listen.Admin)
	}
	opts := httpmw.Options{Logger: o.Logger, MaxRequestBytes: o.MaxRequestBytes}
	s := &Servers{
		Data:          newServer(o.Listen.Data, httpmw.Wrap(o.Data, opts), o.Logger),
		Admin:         newServer(o.Listen.Admin, httpmw.SecurityHeaders(httpmw.Wrap(o.Admin, opts)), o.Logger),
		Drain:         o.Drain,
		Inflight:      o.Inflight,
		InflightCalls: o.InflightCalls,
		tls:           o.Listen.TLS,
	}
	if s.tls.Enabled() {
		s.Data.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return s, nil
}

// maxRequestBytes returns the size limit for each request: the current
// snapshot's server.max_request_bytes, so a SIGHUP reload applies to the next
// request. Before the first snapshot it is the value of cfg.
func maxRequestBytes(cfg *config.Config, snaps proxy.Snapshots) func() int64 {
	return func() int64 {
		if snap := snaps.Current(); snap != nil {
			return int64(snap.Config().Server.MaxRequestBytes)
		}
		return int64(cfg.Server.MaxRequestBytes)
	}
}

func newServer(addr string, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// ServeData serves the data plane on ln, over TLS when listen.tls is set.
func (s *Servers) ServeData(ln net.Listener) error {
	if s.tls.Enabled() {
		return s.Data.ServeTLS(ln, s.tls.CertFile, s.tls.KeyFile)
	}
	return s.Data.Serve(ln)
}

// ServeAdmin serves the admin plane on ln, always as plain HTTP.
func (s *Servers) ServeAdmin(ln net.Listener) error { return s.Admin.Serve(ln) }
