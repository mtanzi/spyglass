package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mtanzi/spyglass/internal/api"
	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/httpmw"
	"github.com/mtanzi/spyglass/internal/keyring"
	"github.com/mtanzi/spyglass/internal/proxy"
	"github.com/mtanzi/spyglass/internal/recorder"
	"github.com/mtanzi/spyglass/internal/store/postgres"
)

// This file is the composition root (ADR 0028): the one place that knows the
// concrete types. The wiring is plain constructor calls.

// storeAdapter is the store as the composition root uses it. *postgres.Store
// implements it; the wiring test uses a fake.
type storeAdapter interface {
	core.CallWriter
	core.CallReader
	// Ping is the admin readiness check.
	Ping(ctx context.Context) error
	// Close closes the connections. It is shutdown's last step.
	Close()
	// StartRetention and StopRetention run the daily retention job (ADR 0022).
	StartRetention(ctx context.Context) error
	StopRetention(ctx context.Context) error
	// RetentionCollector exports the retention metrics (ADR 0026).
	RetentionCollector() prometheus.Collector
}

var _ storeAdapter = (*postgres.Store)(nil)

// runServe is the serve command. It returns the process exit code. Each
// startup step that can fail prints one line that names the step.
func runServe(args []string, stderr io.Writer) int {
	fail := func(step string, err error) int {
		_, _ = fmt.Fprintf(stderr, "spyglass serve: %s: %v\n", step, err)
		return 1
	}

	// Step 1: flags, config and the first snapshot, with the keyring.
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgFlag := fs.String("config", "", "path to the config file (default: $"+config.EnvConfigPath+")")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "spyglass serve: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	opts := config.Options{
		BuildKeys: func(keys []core.Key) (config.KeyIndex, error) { return keyring.New(keys) },
	}
	path, err := config.Path(*cfgFlag, opts)
	if err != nil {
		return fail("config", err)
	}
	// The logger settings live in the config, and the holder wants its logger
	// at construction, so the file is read once to configure logging and then
	// again by the holder. Both reads validate.
	cfg, err := config.Load(path, opts)
	if err != nil {
		return fail("load config", err)
	}

	// Step 2: logging with redaction.
	log := newLogger(cfg.Log, stderr)

	holder := config.NewHolder(path, opts, log)
	if err := holder.Load(); err != nil {
		return fail("load config", err)
	}
	cfg = holder.Current().Config()

	// Step 3: the store. SIGINT and SIGTERM cancel the startup retry.
	startCtx, stopStart := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	st, err := openStore(startCtx, storeConfig(cfg), log, defaultStartupRetry())
	stopStart()
	if err != nil {
		return fail("database", err)
	}

	// Steps 4 to 7: recorder, proxy, admin plane, retention.
	a, err := buildApp(holder, st, log)
	if err != nil {
		st.Close()
		return fail("build", err)
	}

	// Step 8: the listeners, only now that the store has migrated.
	dataLn, err := net.Listen("tcp", cfg.Listen.Data)
	if err != nil {
		a.abort()
		return fail("listen.data", err)
	}
	adminLn, err := net.Listen("tcp", cfg.Listen.Admin)
	if err != nil {
		_ = dataLn.Close()
		a.abort()
		return fail("listen.admin", err)
	}
	log.Info("serving", "data", dataLn.Addr().String(), "admin", adminLn.Addr().String(),
		"tls", cfg.Listen.TLS.Enabled(), "version", version)

	// Step 9: SIGHUP reloads the config, SIGTERM and SIGINT stop the process.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	term := make(chan os.Signal, 2)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(hup)
	defer signal.Stop(term)

	if err := a.run(dataLn, adminLn, term, hup); err != nil {
		return fail("run", err)
	}
	return 0
}

// app is the wired binary, ready to serve.
type app struct {
	servers  *Servers
	registry *prometheus.Registry
	holder   *config.Holder
	recorder *recorder.Recorder
	store    storeAdapter
	shutdown *Shutdown
	log      *slog.Logger
}

// buildApp wires steps 4 to 7 of ADR 0028 from the loaded holder and a store
// that has migrated: the recorder with the store as its writer, the proxy
// handler, the admin plane with the store as its reader, the servers and the
// retention job. It opens no listener. On an error it leaves the store open
// for the caller.
func buildApp(holder *config.Holder, st storeAdapter, log *slog.Logger) (*app, error) {
	snap := holder.Current()
	if snap == nil {
		return nil, errors.New("config not loaded")
	}
	cfg := snap.Config()

	// Step 4: the recorder starts its batch writer.
	rec := recorder.New(st, recorder.Options{QueueCapacity: cfg.QueueCapacity, Logger: log})

	// Step 5: the proxy handler. The keyring travels with each snapshot.
	pm := proxy.NewMetrics()
	handler := proxy.NewHandler(holder, proxy.Options{Recorder: rec, Logger: log, Metrics: pm})
	drain := &httpmw.Drain{}

	// Step 6: the admin plane. The UI (ADR 0029) mounts at /ui/ in api.NewMux;
	// the live tail (ADR 0025) subscribes to the recorder.
	reg := api.NewRegistry(pm, rec.Collector(), holder.Collector(), st.RetentionCollector())
	admin := api.NewMux(api.Options{
		Metrics: reg,
		Ping:    st.Ping,
		Drain:   drain,
		Calls:   st,
		Live:    rec,
		Config:  func() config.View { return holder.Current().Config().View() },
	})

	servers, err := BuildServers(ServerOptions{
		Listen:          cfg.Listen,
		Data:            proxy.NewMux(handler, drain),
		Admin:           admin,
		MaxRequestBytes: maxRequestBytes(cfg, holder),
		Drain:           drain,
		Inflight:        handler.InflightStreams,
		InflightCalls:   handler.InflightCalls,
		Logger:          log,
	})
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), recorder.DefaultCloseTimeout)
		defer cancel()
		_ = rec.Close(closeCtx)
		return nil, err
	}

	// Step 7: the retention job. Shutdown stops it.
	var retention RetentionStopper
	if err := st.StartRetention(context.Background()); err != nil {
		log.Error("retention job not started", "error", err)
	} else {
		retention = st
	}

	return &app{
		servers:  servers,
		registry: reg,
		holder:   holder,
		recorder: rec,
		store:    st,
		log:      log,
		shutdown: &Shutdown{
			Servers:      servers,
			Recorder:     rec,
			Retention:    retention,
			Store:        st,
			DrainTimeout: time.Duration(cfg.Server.DrainTimeout),
			Log:          log,
		},
	}, nil
}

// abort undoes buildApp when the process cannot go on to serve.
func (a *app) abort() {
	ctx, cancel := context.WithTimeout(context.Background(), recorder.DefaultCloseTimeout)
	defer cancel()
	_ = a.recorder.Close(ctx)
	_ = a.store.StopRetention(ctx)
	a.store.Close()
}

// run serves both planes on the given listeners and blocks until the process
// stops. The first value on term starts the graceful shutdown (ADR 0027); a
// second one forces the exit. Each value on hup reloads the config (ADR 0005).
// A listener that fails also starts the shutdown, and run returns its error.
func (a *app) run(dataLn, adminLn net.Listener, term chan os.Signal, hup <-chan os.Signal) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.holder.WatchReload(ctx, hup)

	serveErr := make(chan error, 2)
	go func() { serveErr <- wrapServeErr("data listener", a.servers.ServeData(dataLn)) }()
	go func() { serveErr <- wrapServeErr("admin listener", a.servers.ServeAdmin(adminLn)) }()

	done := make(chan error, 1)
	go func() { done <- a.shutdown.Run(term) }()

	var listenErr error
	for {
		select {
		case err := <-done:
			return errors.Join(listenErr, err)
		case err := <-serveErr:
			if err == nil || listenErr != nil {
				continue
			}
			listenErr = err
			a.log.Error("listener failed, shutting down", "error", err)
			select {
			case term <- syscall.SIGTERM:
			default: // a signal is already waiting
			}
		}
	}
}

// wrapServeErr names the listener in an error and treats the close that
// shutdown causes as no error.
func wrapServeErr(name string, err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}

// newLogger returns the process logger for the log section of the config. A
// log attribute named after a credential header is redacted, whatever its
// value; config secrets already redact themselves when logged.
func newLogger(c config.Log, w io.Writer) *slog.Logger {
	ho := &slog.HandlerOptions{
		Level: parseLevel(c.Level),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if httpmw.IsCredentialHeader(a.Key) {
				return slog.String(a.Key, httpmw.Redacted)
			}
			return a
		},
	}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(w, ho))
	}
	return slog.New(slog.NewJSONHandler(w, ho))
}

func parseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}

// startupRetry bounds the wait for the first database connection (ADR 0028).
type startupRetry struct {
	// Timeout is how long serve keeps trying before it gives up.
	Timeout time.Duration
	// Initial is the first pause between attempts; each pause doubles up to Max.
	Initial, Max time.Duration
	// Attempt bounds one ping.
	Attempt time.Duration
	// Now and Sleep are test hooks. Nil uses the clock.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

func defaultStartupRetry() startupRetry {
	return startupRetry{Timeout: 30 * time.Second, Initial: 250 * time.Millisecond, Max: 4 * time.Second, Attempt: 5 * time.Second}
}

// storeConfig is the adapter's settings from the config file: the database
// section and the retention period.
func storeConfig(cfg *config.Config) postgres.Config {
	return postgres.Config{
		URL:       cfg.Database.URL.Secret().Reveal(),
		MaxConns:  cfg.Database.MaxConns,
		Retention: postgres.Retention{Keep: time.Duration(cfg.Retention.Calls)},
	}
}

// openStore is step 3: open the pool, wait for the database within the retry
// bound, then migrate under goose's lock. A failed migration is a failed start.
func openStore(ctx context.Context, pc postgres.Config, log *slog.Logger, retry startupRetry) (*postgres.Store, error) {
	st, err := postgres.Open(ctx, pc)
	if err != nil {
		return nil, err
	}
	if err := waitForDatabase(ctx, st.Ping, log, retry); err != nil {
		st.Close()
		return nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, err
	}
	log.Info("database migrated")
	return st, nil
}

// waitForDatabase pings until the database answers or the retry bound passes.
// The last ping error is part of the returned error.
func waitForDatabase(ctx context.Context, ping func(context.Context) error, log *slog.Logger, r startupRetry) error {
	now, sleep := r.Now, r.Sleep
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepCtx
	}
	start := now()
	delay := r.Initial
	for attempt := 1; ; attempt++ {
		actx, cancel := ctx, context.CancelFunc(func() {})
		if r.Attempt > 0 {
			actx, cancel = context.WithTimeout(ctx, r.Attempt)
		}
		err := ping(actx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("stopped while waiting for the database: %w", ctx.Err())
		}
		if now().Add(delay).Sub(start) > r.Timeout {
			return fmt.Errorf("not reachable after %d attempts over %s: %w", attempt, now().Sub(start).Round(time.Second), err)
		}
		log.Warn("database not reachable, retrying", "attempt", attempt, "retry_in", delay, "error", err)
		if err := sleep(ctx, delay); err != nil {
			return fmt.Errorf("stopped while waiting for the database: %w", err)
		}
		delay = min(delay*2, r.Max)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
