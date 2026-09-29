// Command backstory is the OS memory daemon for Omarchy.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/RidgetopAi/backstory/internal/version"
)

const usage = `usage: backstory <command>

commands:
  version    print the build version
  daemon     run the memory daemon
  mcp        run the MCP stdio shim
  status     print daemon status (project, capture, budget); non-zero if unreachable
  hook       run a harness hook (session-start)
  shell      shell integration ("shell init bash", "shell emit")
  install    install or remove a harness integration ("install claude|codex|hermes|pi|agents"), or shell capture ("install bash")
  backfill   import transcripts from another tool (e.g. "backfill claude", "backfill codex")
  delete     tombstone a record by id (human-only; "delete <id> [--yes]")
  capture    pause or resume capture ("capture off|on|status")
  recall     print a project's trust-annotated ledger narrative, read-only
  timeline   print a project's observed events, read-only
  records    list what Backstory saved for a project, with status and tier, read-only
  group      set/clear/list project groups (human-only; "group set|clear|list")
  export     write a project's markdown mirror for another tool to read
  this-week  print Attention, Where you left off, and The week, read-only
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "daemon":
		return runDaemon(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], stdin, stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "hook":
		return runHook(args[1:], stdin, stdout, stderr)
	case "shell":
		return runShell(args[1:], stdout, stderr)
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "backfill":
		return runBackfill(args[1:], stdout, stderr)
	case "delete":
		return runDelete(args[1:], stdin, stdout, stderr)
	case "capture":
		return runCapture(args[1:], stdout, stderr)
	case "recall":
		return runRecall(args[1:], stdout, stderr)
	case "timeline":
		return runTimeline(args[1:], stdout, stderr)
	case "records":
		return runRecords(args[1:], stdout, stderr)
	case "group":
		return runGroup(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "this-week":
		return runThisWeek(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "backstory: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print version, go, os, and arch as a JSON object")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if !*jsonOut {
		_, _ = fmt.Fprintln(stdout, version.Version)
		return 0
	}

	b, err := json.Marshal(version.Get())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(b))
	return 0
}
