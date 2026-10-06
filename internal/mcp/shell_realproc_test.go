package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

const shellEmitHelperEnv = "BACKSTORY_TEST_SHELL_EMIT_SOCK"

// TestShellEmitHelper is not a test: when shellEmitHelperEnv is set the test
// binary re-executes itself as `backstory shell emit` — one fresh connection,
// one shell_emit request — and exits.
func TestShellEmitHelper(t *testing.T) {
	sock := os.Getenv(shellEmitHelperEnv)
	if sock == "" {
		t.Skip("helper process only")
	}
	params, _ := json.Marshal(ShellEmitParams{Cmd: os.Getenv("BACKSTORY_TEST_SHELL_EMIT_CMD")})
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sock) }) //nolint:gosec // test socket path from t.TempDir via env
	defer func() { _ = s.Close() }()
	if _, rerr := s.callDaemon(DaemonMethodShellEmit, params); rerr != nil {
		t.Fatalf("shell_emit: %v", rerr)
	}
}

// realShell starts bash as its own session leader (the way a terminal or
// tmux pane starts an interactive shell) and returns a function that makes it
// run n emits launched exactly like the shipped snippet does: backgrounded
// inside a subshell, so the emit's parent has exited before the daemon looks.
func realShell(t *testing.T, sock string) (emit func(cmd string), stop func()) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `while IFS= read -r c; do ( BACKSTORY_TEST_SHELL_EMIT_CMD="$c" "$BS_BIN" -test.run='^TestShellEmitHelper$' >/dev/null 2>&1 & ); done`)
	cmd.Env = append(os.Environ(), shellEmitHelperEnv+"="+sock, "BS_BIN="+bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bash: %v", err)
	}
	emit = func(c string) { _, _ = io.WriteString(in, c+"\n") }
	stop = func() { _ = in.Close(); _ = cmd.Wait() }
	return emit, stop
}

// TestRealShellEmitsShareOneSessionUnderRealProc: under the REAL /proc, emits
// launched from a session-leader bash as `( cmd & )` (parent already gone,
// reparented to init or a subreaper) land in exactly one shell session; a
// second bash session makes a second one.
func TestRealShellEmitsShareOneSessionUnderRealProc(t *testing.T) {
	st := mustOpenStore(t)
	sock := testDaemonRealProcFS(t, st, "shell-proj-key")

	emit, stop := realShell(t, sock)
	for i := 0; i < 5; i++ {
		emit(fmt.Sprintf("cmd %d", i))
	}
	waitEvents(t, st, 5)
	if got := countSessionsByAgent(t, st, ident.HarnessShell); got != 1 {
		t.Fatalf("shell sessions after 5 emits = %d, want 1", got)
	}
	if got := countSessionsByAgent(t, st, "unknown"); got != 0 {
		t.Fatalf("per-emit unknown sessions = %d, want 0", got)
	}
	var total int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total sessions = %d, want 1", total)
	}

	emit2, stop2 := realShell(t, sock)
	emit2("other shell")
	waitEvents(t, st, 6)
	if got := countSessionsByAgent(t, st, ident.HarnessShell); got != 2 {
		t.Fatalf("shell sessions after a second bash session = %d, want 2", got)
	}
	stop()
	stop2()
}

func waitEvents(t *testing.T, st *store.Store, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for countTimelineEvents(t, st) < n {
		if time.Now().After(deadline) {
			t.Fatalf("timeline events = %d, want %d", countTimelineEvents(t, st), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
