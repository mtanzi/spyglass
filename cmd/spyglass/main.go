// Command spyglass is the LLM proxy server binary.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mtanzi/spyglass/internal/cli"
)

// version is set at build time: go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `usage: spyglass [command] [flags]

commands:
  serve    run the proxy
  keygen   generate a virtual key and its config entry
  backfill-projects
           give older calls a project from their key or session
  version  print the build version

Run spyglass --help for the admin client commands.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a subcommand and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return cli.Run(nil, stdout, stderr, os.Getenv)
	}
	switch args[0] {
	case "describe", "config", "sessions", "calls", "help", "-h", "--help":
		return cli.Run(args, stdout, stderr, os.Getenv)
	case "keygen":
		return runKeygen(args[1:], stdout, stderr)
	case "backfill-projects":
		return runBackfillProjects(args[1:], stdout, stderr)
	case "version":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case "serve":
		return runServe(args[1:], stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "spyglass: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
