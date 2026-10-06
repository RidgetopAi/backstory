package codex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestImportSameRunTwiceAfterEndYieldsOneSession is task a757b754's DONE WHEN
// clause 2 for Codex: the run's session has ended; importing the same run
// again (its cursor gone, as when the store was rebuilt or the rollout was
// copied) attaches to it — one session, no duplicate events.
//
// RA-MUTATION-PROBE: drop the RunSessionByHarnessSessionID match -> RED.
func TestImportSameRunTwiceAfterEndYieldsOneSession(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "01", "15")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session_meta","timestamp":"2026-01-15T09:00:00Z","payload":{"id":"dup-thread","cwd":"/home/zed/proj","cli_version":"0.1"}}
{"type":"response_item","timestamp":"2026-01-15T09:00:02Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]}}
{"type":"response_item","timestamp":"2026-01-15T09:00:03Z","payload":{"type":"custom_tool_call","name":"apply_patch","input":"x","call_id":"c1"}}
`
	if err := os.WriteFile(filepath.Join(dir, "rollout-20260115T090000-dup000thread.jsonl"), []byte(body), 0o600); err != nil {
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
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness_session_id = 'dup-thread'`).Scan(&sessions); err != nil {
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
