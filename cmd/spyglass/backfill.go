package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/keyring"
)

// runBackfillProjects is the backfill-projects command (ADR 0059). It reads the
// same config file and database settings as serve, gives older calls with no
// project the project of their key and then the one project of their session,
// prints how many calls each step changed, and exits. It is safe to run again:
// it writes only calls that have no project.
func runBackfillProjects(args []string, stdout, stderr io.Writer) int {
	fail := func(step string, err error) int {
		_, _ = fmt.Fprintf(stderr, "spyglass backfill-projects: %s: %v\n", step, err)
		return 1
	}

	fs := flag.NewFlagSet("backfill-projects", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgFlag := fs.String("config", "", "path to the config file (default: $"+config.EnvConfigPath+")")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "spyglass backfill-projects: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	opts := config.Options{
		BuildKeys: func(keys []core.Key) (config.KeyIndex, error) { return keyring.New(keys) },
	}
	path, err := config.Path(*cfgFlag, opts)
	if err != nil {
		return fail("config", err)
	}
	cfg, err := config.Load(path, opts)
	if err != nil {
		return fail("load config", err)
	}
	log := newLogger(cfg.Log, stderr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	st, err := openStore(ctx, storeConfig(cfg), log, defaultStartupRetry())
	if err != nil {
		return fail("database", err)
	}
	defer st.Close()

	keyProjects := map[string]string{}
	for _, k := range cfg.Keys {
		if k.Project != "" {
			keyProjects[k.Name] = k.Project
		}
	}
	res, err := st.BackfillProjects(ctx, keyProjects)
	if err != nil {
		return fail("backfill", err)
	}
	_, _ = fmt.Fprintf(stdout, "%d calls got the project of their key, %d calls got the project of their session\n",
		res.ByKey, res.BySession)
	return 0
}
