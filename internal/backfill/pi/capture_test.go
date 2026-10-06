package pi

import (
	"path/filepath"
	"testing"
	"time"
)

// TestImportHonoursCaptureOffAndPauseWindows is DONE WHEN clause 3 for Pi:
// capture off imports nothing; root-b (2026-05-02) started inside a closed
// pause window and is not imported, root-a (2026-05-01) is.
func TestImportHonoursCaptureOffAndPauseWindows(t *testing.T) {
	root := filepath.Join("testdata", "import", "sessions")

	off := mustOpenStore(t)
	res, err := Import(off, Options{Root: root, Git: fakeGit{}, CaptureOff: func() (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionsCreated != 0 || res.EventsCreated != 0 || res.FilesScanned != 0 {
		t.Errorf("capture off: Result = %+v, want nothing imported", res)
	}

	st := mustOpenStore(t)
	if err := st.BeginCapturePause(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := st.EndCapturePause(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	res, err = Import(st, Options{Root: root, Git: fakeGit{}, CaptureOff: func() (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionsCreated != 1 {
		t.Errorf("SessionsCreated = %d, want 1 (root-a only)", res.SessionsCreated)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'root-b'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the session that started inside the pause was imported")
	}
}
