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
  version   print the build version
  daemon    run the memory daemon
  mcp       run the MCP stdio shim
  hook      run a harness hook (session-start)
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
	case "hook":
		return runHook(args[1:], stdin, stdout, stderr)
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
