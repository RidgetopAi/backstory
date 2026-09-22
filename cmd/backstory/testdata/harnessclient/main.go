// Command harnessclient is daemon_test.go's self-spawned stand-in for a
// recognised harness process (punch e96d1a21's clause 4). It is built to a
// path literally named "claude" (or another entry in
// internal/ident.KnownHarnesses) and exec'd with a cwd the test controls, so
// the daemon's /proc-ancestry identity walk finds it at distance zero
// regardless of whatever process tree happened to launch `go test` — the
// harness ancestor the test asserts on is one it created, never one it
// inherited.
//
// Usage: harnessclient <socket-path> <session-id>
// It dials the daemon socket, issues one DaemonMethodBlock request for
// session-id, and prints the rendered block text to stdout. A failure at
// any step is reported on stderr with a non-zero exit.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: harnessclient <socket-path> <session-id>")
		os.Exit(2)
	}
	sockPath, sessionID := os.Args[1], os.Args[2]

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial daemon socket:", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	req := mcp.DaemonRequest{Session: sessionID, Method: mcp.DaemonMethodBlock}
	b, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal block request:", err)
		os.Exit(1)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "write block request:", err)
		os.Exit(1)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "read block response:", err)
		os.Exit(1)
	}
	var resp mcp.DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		fmt.Fprintln(os.Stderr, "decode block response:", err)
		os.Exit(1)
	}
	if resp.Error != nil {
		fmt.Fprintln(os.Stderr, "daemon returned error for block request:", resp.Error.Message)
		os.Exit(1)
	}
	var result mcp.BlockResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		fmt.Fprintln(os.Stderr, "decode block result:", err)
		os.Exit(1)
	}
	fmt.Print(result.Block)
}
