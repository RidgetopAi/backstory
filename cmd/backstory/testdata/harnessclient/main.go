// Command harnessclient is daemon_test.go's self-spawned stand-in for a
// recognised harness process (punch e96d1a21's clause 4). It is built to a
// path literally named "claude" (or another entry in
// internal/ident.KnownHarnesses) and exec'd with a cwd the test controls, so
// the daemon's /proc-ancestry identity walk finds it at distance zero
// regardless of whatever process tree happened to launch `go test` — the
// harness ancestor the test asserts on is one it created, never one it
// inherited.
//
// Usage: harnessclient <socket-path> <session-id> [method] [params-json]
//
// With no method (or method == "block"), it issues one raw
// mcp.DaemonMethodBlock request for session-id and prints the rendered
// block text to stdout — unchanged from before task d6ddfce3, so
// requestBlockAsHarness's existing 2-argument call sites keep working.
//
// With method == "recall" (task d6ddfce3's clause 1: "through the MCP
// shim"), it drives the request through an actual mcp.Server built on this
// process's own connection — the same production code path
// cmd/backstory's `mcp` subcommand uses — rather than hand-rolling a
// DaemonRequest line the way the block path does, and prints the tool's raw
// JSON result to stdout.
//
// Any other method (e.g. "note") takes the same raw-DaemonRequest path as
// "block", but forwards params-json as the request's Params — task
// fd620482's capture-off test needs a real note request (kind/text), which
// an empty Params would always fail validation on regardless of capture
// state. A daemon error response (resp.Error != nil) is reported on stderr
// with a non-zero exit, same as every other raw-path failure here.
//
// A failure at any step is reported on stderr with a non-zero exit.
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
	if len(os.Args) < 3 || len(os.Args) > 5 {
		fmt.Fprintln(os.Stderr, "usage: harnessclient <socket-path> <session-id> [method] [params-json]")
		os.Exit(2)
	}
	sockPath, sessionID := os.Args[1], os.Args[2]
	method := mcp.DaemonMethodBlock
	if len(os.Args) >= 4 {
		method = os.Args[3]
	}
	var params json.RawMessage
	if len(os.Args) == 5 {
		params = json.RawMessage(os.Args[4])
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial daemon socket:", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	if method == mcp.ToolRecall {
		shim := mcp.NewServer(func() (net.Conn, error) { return conn, nil })
		result, rerr := shim.CallTool(mcp.ToolRecall, params)
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "recall call error:", rerr.Message)
			os.Exit(1)
		}
		fmt.Print(string(result))
		return
	}

	req := mcp.DaemonRequest{Session: sessionID, Method: method, Params: params}
	b, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal", method, "request:", err)
		os.Exit(1)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "write", method, "request:", err)
		os.Exit(1)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "read", method, "response:", err)
		os.Exit(1)
	}
	var resp mcp.DaemonResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		fmt.Fprintln(os.Stderr, "decode", method, "response:", err)
		os.Exit(1)
	}
	if resp.Error != nil {
		fmt.Fprintln(os.Stderr, "daemon returned error for", method, "request:", resp.Error.Message)
		os.Exit(1)
	}
	var result mcp.BlockResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		fmt.Fprintln(os.Stderr, "decode", method, "result:", err)
		os.Exit(1)
	}
	fmt.Print(result.Block)
}
