package codex

import (
	"path/filepath"
	"testing"
	"time"
)

// TestImportHonoursCaptureOffAndPauseWindows is DONE WHEN clause 3 for
// Codex: capture off imports nothing; with capture back on, the rollouts
// that started inside the pause window (main + its subagent, 2026-01-15)
// are not imported, the one started after (2026-01-16) is.
func TestImportHonoursCaptureOffAndPauseWindows(t *testing.T) {
	root := filepath.Join("testdata", "import", "sessions")

	off := mustOpenStore(t)
	res, err := Import(off, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}, CaptureOff: func() (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionsCreated != 0 || res.EventsCreated != 0 || res.FilesScanned != 0 {
		t.Errorf("capture off: Result = %+v, want nothing imported", res)
	}

	st := mustOpenStore(t)
	if err := st.BeginCapturePause(time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := st.EndCapturePause(time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	res, err = Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}, CaptureOff: func() (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionsCreated != 1 {
		t.Errorf("SessionsCreated = %d, want 1 (only the 2026-01-16 rollout)", res.SessionsCreated)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'main-thread-uuid'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the rollout that started inside the pause was imported")
	}
}
