package hermes

import (
	"path/filepath"
	"testing"
	"time"
)

// TestImportHonoursCaptureOffAndPauseWindows is DONE WHEN clause 3 for
// Hermes: capture off imports nothing; sessions started inside a closed
// pause window are not imported once capture is back on, while the same
// fixture with a window elsewhere imports both.
func TestImportHonoursCaptureOffAndPauseWindows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sessions, messages := twoLinkedSessionsFixture(t0)
	newFixtureDB(t, dbPath, sessions, messages)
	on := func() (bool, error) { return false, nil }

	off := mustOpenStore(t)
	res, err := Import(off, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}, CaptureOff: func() (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionsCreated != 0 || countAllEvents(t, off) != 0 || countAllSessions(t, off) != 0 {
		t.Errorf("capture off: Result = %+v, want nothing imported", res)
	}

	paused := mustOpenStore(t)
	if err := paused.BeginCapturePause(t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := paused.EndCapturePause(t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(paused, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}, CaptureOff: on}); err != nil {
		t.Fatal(err)
	}
	if n := countAllSessions(t, paused); n != 0 {
		t.Errorf("sessions started inside the pause: %d imported, want 0", n)
	}

	elsewhere := mustOpenStore(t)
	if err := elsewhere.BeginCapturePause(t0.Add(48 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := elsewhere.EndCapturePause(t0.Add(72 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(elsewhere, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}, CaptureOff: on}); err != nil {
		t.Fatal(err)
	}
	if n := countAllSessions(t, elsewhere); n != 2 {
		t.Errorf("control with a window elsewhere: %d sessions, want 2", n)
	}
}
