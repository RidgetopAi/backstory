package pi

import (
	"os"
	"path/filepath"
	"testing"
)

// TestImportSameRunTwiceAfterEndYieldsOneSession is task a757b754's DONE WHEN
// clause 2 for Pi: importing the same finished run again (cursor gone) yields
// one session and no duplicate events.
//
// RA-MUTATION-PROBE: drop the RunSessionByHarnessSessionID match -> RED.
func TestImportSameRunTwiceAfterEndYieldsOneSession(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "--home-zed-proj--")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"root-d","parentId":"","timestamp":"2026-05-01T00:00:00Z","type":"session","cwd":"/home/zed/proj"}
{"id":"u1","parentId":"root-d","timestamp":"2026-05-01T00:00:01Z","type":"message","role":"user","text":"go"}
{"id":"a1","parentId":"u1","timestamp":"2026-05-01T00:00:02Z","type":"message","role":"assistant","content":[{"type":"toolCall","id":"c1","name":"Bash"}]}
`
	if err := os.WriteFile(filepath.Join(dir, "2026-05-01T00-00-00Z_root-d.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`DELETE FROM backfill_cursors`); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatal(err)
	}
	var sessions, after int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'root-d'`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Errorf("sessions = %d, want 1", sessions)
	}
	if after != events {
		t.Errorf("events after re-import = %d, want %d", after, events)
	}
}
