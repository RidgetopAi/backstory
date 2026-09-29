package mcp

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// toggleCapture is a captureOff callback a test can flip between dials.
// atomic.Bool because ServeDaemonConn reads it from its own per-connection
// goroutine while the test goroutine that started the daemon flips it
// between dials (Makefile's `test` target runs `go test -race`).
type toggleCapture struct{ off atomic.Bool }

func (c *toggleCapture) fn() (bool, error) { return c.off.Load(), nil }

// testDaemonWithCapture is testDaemon's (note_test.go) counterpart for a
// test that must control captureOff itself: task 9c62f9dc's DONE WHEN
// clauses 1 and 2 are about whether the daemon's post_tool_use handler and
// its session start honour capture-off, which the fixed captureNeverOff
// every other daemon in this package uses can never exercise.
func testDaemonWithCapture(t *testing.T, st *store.Store, harness, cwd, projectKey string, captureOff func() (bool, error)) string {
	t.Helper()
	selfPID := os.Getpid()
	procfs := fakeProcFS{
		status: map[int]ident.Status{selfPID: {PPid: 1, Name: harness}},
		cwd:    map[int]string{selfPID: cwd},
	}
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(string) string { return projectKey }}

	sessions := NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, fakeGit{}, nil, sessions, captureOff, nil)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return sockPath
}

func countTimelineEvents(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&n); err != nil {
		t.Fatalf("count timeline_events: %v", err)
	}
	return n
}

// TestPostToolUseCaptureOffInsertsNoEventsCaptureOnInsertsOne is task
// 9c62f9dc's DONE WHEN clause 1: a socket peer dialing the daemon directly
// with post_tool_use (no hook client in the loop, so cmd/backstory/hook.go's
// client-side captureOff fast path never runs) gets an error response and
// inserts no timeline_events row while capture is off; the identical
// request, once capture is switched back on, inserts exactly one row (a
// non-Bash tool_name, so DONE WHEN's "one row" is a single tool.use event,
// not a tool.use+tool.result pair).
//
// RA-MUTATION-PROBE: delete handlePostToolUse's captureOff check (task
// 9c62f9dc's fix) -> RED (the capture-off call below succeeds and inserts a
// row instead of being refused); restored -> GREEN.
func TestPostToolUseCaptureOffInsertsNoEventsCaptureOnInsertsOne(t *testing.T) {
	st := mustOpenStore(t)
	var capture toggleCapture
	capture.off.Store(true)
	sockPath := testDaemonWithCapture(t, st, "claude", "/home/brian/proj", "proj-key", capture.fn)

	params, err := json.Marshal(PostToolUseParams{ToolName: "Edit", Path: "/home/brian/proj/f.go"})
	if err != nil {
		t.Fatalf("marshal PostToolUseParams: %v", err)
	}

	if got := countTimelineEvents(t, st); got != 0 {
		t.Fatalf("timeline_events before any request = %d, want 0", got)
	}

	shim := dialShim(t, sockPath)
	_, rerr := shim.callDaemon(DaemonMethodPostToolUse, params)
	if rerr == nil {
		t.Fatal("post_tool_use with capture off succeeded, want a capture-off error")
	}
	if !strings.Contains(rerr.Message, "capture") {
		t.Errorf("error message = %q, want it to mention capture", rerr.Message)
	}
	if got := countTimelineEvents(t, st); got != 0 {
		t.Fatalf("timeline_events after post_tool_use with capture off = %d, want 0", got)
	}

	capture.off.Store(false)
	shim2 := dialShim(t, sockPath)
	if _, rerr := shim2.callDaemon(DaemonMethodPostToolUse, params); rerr != nil {
		t.Fatalf("post_tool_use with capture on: %v", rerr)
	}
	if got := countTimelineEvents(t, st); got != 1 {
		t.Fatalf("timeline_events after post_tool_use with capture on = %d, want 1", got)
	}
}

// TestCaptureOffConnectionCreatesNoSessionsRow is task 9c62f9dc's DONE WHEN
// clause 2: a brand-new connection, while capture is off, never writes a
// sessions row at all — not even a bare "session started" row with nothing
// recorded against it yet. Once capture is back on, the next new connection
// does start one.
//
// RA-MUTATION-PROBE: revert ServeDaemonConn's start closure to call
// startSession unconditionally (drop the captureOff gate) -> RED (the first
// connection below, made while capture is off, leaves a sessions row
// behind); restored -> GREEN.
func TestCaptureOffConnectionCreatesNoSessionsRow(t *testing.T) {
	st := mustOpenStore(t)
	var capture toggleCapture
	capture.off.Store(true)
	sockPath := testDaemonWithCapture(t, st, "claude", "/home/brian/proj", "proj-key", capture.fn)

	shim := dialShim(t, sockPath)
	raw, rerr := shim.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status) with capture off: %v", rerr)
	}
	var result StatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal StatusResult: %v", err)
	}
	if result.CaptureOn {
		t.Error("StatusResult.CaptureOn = true, want false while capture is off")
	}
	if got := countSessions(t, st); got != 0 {
		t.Fatalf("sessions after a connection with capture off = %d, want 0", got)
	}

	capture.off.Store(false)
	shim2 := dialShim(t, sockPath)
	if _, rerr := shim2.CallTool(ToolStatus, nil); rerr != nil {
		t.Fatalf("CallTool(status) with capture on: %v", rerr)
	}
	if got := countSessions(t, st); got != 1 {
		t.Fatalf("sessions after a connection with capture on = %d, want 1", got)
	}
}

// TestPostToolUseBashOutputRedactedBeforeExcerpt is task c9ab6d28's DONE
// WHEN clause 3 through the daemon: a PEM block straddling the excerpt cut
// is stored as the redaction marker, never as partial key text.
//
// RA-MUTATION-PROBE: truncate before redacting in handlePostToolUse -> RED.
func TestPostToolUseBashOutputRedactedBeforeExcerpt(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemonWithCapture(t, st, "claude", "/home/brian/proj", "proj-key", (&toggleCapture{}).fn)

	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("MIIEowIBAAKCAQEAuVJTUt9Us8cKjMzE\n", 60) + "-----END RSA PRIVATE KEY-----"
	out := strings.Repeat("p", payload.ToolOutputExcerptMaxRunes/2-50) + pem + strings.Repeat("s", 3000)
	params, err := json.Marshal(PostToolUseParams{ToolUseID: "toolu_pem", ToolName: "Bash", Command: "cat key", Output: out})
	if err != nil {
		t.Fatal(err)
	}
	shim := dialShim(t, sockPath)
	if _, rerr := shim.callDaemon(DaemonMethodPostToolUse, params); rerr != nil {
		t.Fatalf("post_tool_use: %v", rerr)
	}

	found, ok, err := st.FindToolResult("toolu_pem")
	if err != nil || !ok {
		t.Fatal(err)
	}
	c := found.Payload.Content
	if !strings.Contains(c, "[redacted:pem-block]") {
		t.Errorf("content lacks the pem-block marker: %.120q", c)
	}
	if strings.Contains(c, "PRIVATE KEY") || strings.Contains(c, "MIIEow") {
		t.Errorf("content leaks partial key text")
	}
}
