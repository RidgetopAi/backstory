package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// captureBackfillEnv points the data dir, runtime dir and Claude transcript
// root at temp dirs and writes one transcript whose only timestamp is ts.
func captureBackfillEnv(t *testing.T, sessionID string, ts time.Time) (root string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(tmp, "run"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	root = filepath.Join(tmp, "projects")
	t.Setenv("BACKSTORY_CLAUDE_ROOT", root)
	writeClaudeTranscript(t, root, sessionID, ts)
	return root
}

func writeClaudeTranscript(t *testing.T, root, sessionID string, ts time.Time) {
	t.Helper()
	dir := filepath.Join(root, "-home-zed-proj")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"user","uuid":"%[1]s-u1","parentUuid":null,"sessionId":"%[1]s","cwd":"/home/zed/proj","version":"2.1.0","timestamp":%[2]q,"isMeta":false,"message":{"role":"user","content":"hello"}}`+"\n",
		sessionID, ts.UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
}

func storeCounts(t *testing.T) (sessions, events int) {
	t.Helper()
	dbPath, err := storePath()
	if err != nil {
		t.Fatal(err)
	}
	st, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	return sessions, events
}

func mustRunCmd(t *testing.T, name string, fn func(args []string, out, errw io.Writer) int, args ...string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := fn(args, &out, &errb); code != 0 {
		t.Fatalf("%s %v = %d, stderr: %s", name, args, code, errb.String())
	}
}

// TestCaptureOffStopsCLIBackfillAndDaemonStartupBackfill is DONE WHEN clause
// 1: with capture off, `backfill claude` and the daemon's start-up run both
// import nothing.
func TestCaptureOffStopsCLIBackfillAndDaemonStartupBackfill(t *testing.T) {
	captureBackfillEnv(t, "sess-off", time.Now().Add(-time.Hour))
	mustRunCmd(t, "capture", func(a []string, o, e io.Writer) int { return runCapture(a, o, e) }, "off")

	var out, errb bytes.Buffer
	if code := runBackfill([]string{"claude"}, &out, &errb); code != 0 {
		t.Fatalf("backfill claude = %d: %s", code, errb.String())
	}
	if s, e := storeCounts(t); s != 0 || e != 0 {
		t.Fatalf("after CLI backfill with capture off: %d sessions, %d events, want 0/0", s, e)
	}

	dbPath, _ := storePath()
	st, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	runClaudeBackfillOnce(st, log.New(io.Discard, "", 0))
	_ = st.Close()
	if s, e := storeCounts(t); s != 0 || e != 0 {
		t.Fatalf("after daemon start-up backfill with capture off: %d sessions, %d events, want 0/0", s, e)
	}
}

// TestPausedSessionNotImportedAfterCaptureOn is clause 2 end to end through
// the CLI: capture off records a window; a transcript started inside it is
// not imported after capture is back on, one started after is.
func TestPausedSessionNotImportedAfterCaptureOn(t *testing.T) {
	root := captureBackfillEnv(t, "sess-paused", time.Now().Add(200*time.Millisecond))
	mustRunCmd(t, "capture", func(a []string, o, e io.Writer) int { return runCapture(a, o, e) }, "off")
	time.Sleep(500 * time.Millisecond)
	mustRunCmd(t, "capture", func(a []string, o, e io.Writer) int { return runCapture(a, o, e) }, "on")
	writeClaudeTranscript(t, root, "sess-after", time.Now().Add(time.Second))

	mustRunCmd(t, "backfill", runBackfill, "claude")

	dbPath, _ := storePath()
	st, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var paused, after int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'sess-paused'`).Scan(&paused)
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'sess-after'`).Scan(&after)
	if paused != 0 {
		t.Errorf("session started during the pause was imported")
	}
	if after != 1 {
		t.Errorf("session started after capture came back on: %d imported, want 1", after)
	}
	ps, err := st.CapturePauses()
	if err != nil || len(ps) != 1 || ps[0].End == nil {
		t.Fatalf("CapturePauses = %+v, %v; want one closed window", ps, err)
	}
}
