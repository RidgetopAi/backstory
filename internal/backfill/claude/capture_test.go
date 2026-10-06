package claude

import (
	"path/filepath"
	"testing"
	"time"
)

func mustCounts(t *testing.T, sessions, events *int, q func(string, *int)) {
	t.Helper()
	q(`SELECT COUNT(*) FROM sessions`, sessions)
	q(`SELECT COUNT(*) FROM timeline_events`, events)
}

// TestImportWithCaptureOffImportsNothing is DONE WHEN clause 1 (importer
// half): capture off → 0 sessions and 0 events.
func TestImportWithCaptureOffImportsNothing(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "import", "projects")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}, CaptureOff: func() (bool, error) { return true, nil }})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.SessionsCreated != 0 || res.EventsCreated != 0 {
		t.Errorf("Result = %+v, want no sessions/events", res)
	}
	var sessions, events int
	mustCounts(t, &sessions, &events, func(q string, dst *int) {
		if err := st.DB().QueryRow(q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	})
	if sessions != 0 || events != 0 {
		t.Errorf("store holds %d sessions, %d events, want 0/0", sessions, events)
	}
}

// TestImportSkipsSessionStartedInsidePauseWindow is DONE WHEN clause 2:
// sess-b (2026-02-01) started during a closed pause window and capture is
// back on; sess-a (before) and sess-c (after) import.
func TestImportSkipsSessionStartedInsidePauseWindow(t *testing.T) {
	st := mustOpenStore(t)
	if err := st.BeginCapturePause(time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := st.EndCapturePause(time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("testdata", "import", "projects")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}, CaptureOff: func() (bool, error) { return false, nil }})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.SessionsCreated != 2 {
		t.Errorf("SessionsCreated = %d, want 2 (sess-a and sess-c)", res.SessionsCreated)
	}
	for id, want := range map[string]int{"sess-a-uuid": 1, "sess-b-uuid": 0, "sess-c-uuid": 1} {
		var n int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("sessions for %s = %d, want %d", id, n, want)
		}
	}

	// A rerun must keep skipping it (no cursor was written for the skipped file).
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'sess-b-uuid'`).Scan(&n)
	if n != 0 {
		t.Errorf("rerun imported the paused session")
	}
}
