// Command spyglass-report prints the cost per task and the cost per merged PR
// (ADR 0040). It reads a task list through a TaskSource adapter and the call
// log through the admin API, and joins them with costreport.Attribute.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/mtanzi/spyglass/internal/costreport"
	"github.com/mtanzi/spyglass/internal/costreport/firstmate"
	"github.com/mtanzi/spyglass/internal/costreport/taskfile"
)

const defaultAdminURL = "http://127.0.0.1:9090"

const usage = `usage: spyglass-report --tasks firstmate:<ledger> | file:<tasks.csv|tasks.json> [flags]

Prints the cost per task and the cost per merged PR. Calls reach the report
from the admin API (SPYGLASS_ADMIN_URL, default ` + defaultAdminURL + `).

flags:
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv, time.Now)
	stop()
	os.Exit(code)
}

// sourceFor builds the TaskSource named by a --tasks value: kind:path.
func sourceFor(spec string) (costreport.TaskSource, error) {
	kind, path, ok := strings.Cut(spec, ":")
	if !ok || path == "" {
		return nil, fmt.Errorf("--tasks %q: want firstmate:<path> or file:<path>", spec)
	}
	switch kind {
	case "firstmate":
		return firstmate.Ledger{Path: path}, nil
	case "file":
		return taskfile.File{Path: path}, nil
	}
	return nil, fmt.Errorf("--tasks %q: unknown source %q, want firstmate or file", spec, kind)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string, now func() time.Time) int {
	fs := flag.NewFlagSet("spyglass-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tasksFlag := fs.String("tasks", "", "task list: firstmate:<path to fleet-ledger.jsonl> or file:<path to .csv or .json>")
	from := fs.String("from", "", "start of the call window, RFC 3339 (default: the earliest task start)")
	to := fs.String("to", "", "end of the call window, RFC 3339 (default: now)")
	format := fs.String("format", "table", "output format: "+costreport.FormatNames)
	admin := fs.String("admin-url", "", "admin API base URL (default: $SPYGLASS_ADMIN_URL, else "+defaultAdminURL+")")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fail := func(code int, err error) int {
		_, _ = fmt.Fprintf(stderr, "spyglass-report: %v\n", err)
		return code
	}
	if fs.NArg() > 0 {
		return fail(2, fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	if *tasksFlag == "" {
		fs.Usage()
		return 2
	}
	write, ok := costreport.Formatters[*format]
	if !ok {
		return fail(2, fmt.Errorf("--format %q: want one of %s", *format, costreport.FormatNames))
	}
	src, err := sourceFor(*tasksFlag)
	if err != nil {
		return fail(2, err)
	}
	base := *admin
	if base == "" {
		base = strings.TrimSpace(getenv("SPYGLASS_ADMIN_URL"))
	}
	if base == "" {
		base = defaultAdminURL
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fail(2, fmt.Errorf("admin URL %q is not an http or https URL", base))
	}

	list, err := src.Tasks(ctx)
	if err != nil {
		return fail(1, err)
	}
	end, start := now().UTC(), time.Time{}
	// The window is half-open and the API reads whole seconds: a window that
	// ends now rounds up, so a call recorded in this second is not dropped.
	end = end.Truncate(time.Second).Add(time.Second)
	if *to != "" {
		if end, err = time.Parse(time.RFC3339, *to); err != nil {
			return fail(2, fmt.Errorf("--to %q is not an RFC 3339 time", *to))
		}
	}
	if *from != "" {
		if start, err = time.Parse(time.RFC3339, *from); err != nil {
			return fail(2, fmt.Errorf("--from %q is not an RFC 3339 time", *from))
		}
	} else {
		for _, t := range list.Tasks {
			if start.IsZero() || t.Start.Before(start) {
				start = t.Start
			}
		}
	}
	var calls []costreport.Call
	if start.IsZero() {
		_, _ = fmt.Fprintln(stderr, "spyglass-report: the task list is empty: no calls to read")
	} else {
		if !start.Before(end) {
			return fail(2, fmt.Errorf("the call window is empty: from %s is not before to %s", start.UTC().Format(time.RFC3339), end.Format(time.RFC3339)))
		}
		reader := adminCalls{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 60 * time.Second}}
		if calls, err = reader.Calls(ctx, start.UTC().Truncate(time.Second), end); err != nil {
			return fail(1, err)
		}
	}

	rep := costreport.Attribute(list.Tasks, calls)
	rep.Warnings = list.Warnings
	if err := write(stdout, rep); err != nil {
		return fail(1, err)
	}
	return 0
}
