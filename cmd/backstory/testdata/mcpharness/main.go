// Command mcpharness is mcp_idle_test.go's stand-in for the real-world
// harness process that launches `backstory mcp` as a child and leaves it
// running for the whole session (task 40008eea). It is built to a path
// literally named "claude" (or another entry in internal/ident.KnownHarnesses)
// and exec'd directly, never through a shell, so Linux sets its comm from
// that basename; it then execs the real `backstory mcp` binary as its own
// child, inheriting stdin/stdout/stderr untouched, so the test's pipes reach
// the actual shim subprocess while the daemon's /proc ancestry walk finds
// this process's harness name one level up — exactly the shape a live
// Claude Code session has.
//
// Usage: mcpharness <backstory-binary> <args-for-backstory>...
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mcpharness <backstory-binary> <args-for-backstory>...")
		os.Exit(2)
	}

	cmd := exec.Command(os.Args[1], os.Args[2:]...) //nolint:gosec // os.Args[1] is the binary this test built, not external input
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "mcpharness: run backstory:", err)
		os.Exit(1)
	}
}
