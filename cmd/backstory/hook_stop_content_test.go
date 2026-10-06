package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// dirtyStartFixture is a git project whose tree is ALREADY dirty — a modified
// tracked file and an untracked file — before the session starts, the shape of
// every "continue where we left off" session (task 53bd4866).
func dirtyStartFixture(t *testing.T) *stopFixture {
	t.Helper()
	f := newStopFixture(t)
	f.gitInitProject()
	f.sh("echo v1 > a.py && git add a.py && git -c user.email=t@example.com -c user.name=t commit -q -m a")
	f.sh("echo v2 > a.py")      // tracked, modified, uncommitted
	f.sh("echo u1 > notes.txt") // untracked
	return f
}

func decisionOf(t *testing.T, stdout string) (decision, reason string) {
	t.Helper()
	var d struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("stdout %q is not a JSON decision: %v", stdout, err)
	}
	return d.Decision, d.Reason
}

func TestHookStopSeesRewriteOfAlreadyDirtyFiles(t *testing.T) {
	t.Run("rewriting an already-modified tracked file blocks naming 1 file", func(t *testing.T) {
		f := dirtyStartFixture(t)
		if stdout, exit := f.stop("s-rewrite", false, ""); exit != "exit=0" || stdout != "" {
			t.Fatalf("start: stdout %q %s, want silent exit 0", stdout, exit)
		}
		f.sh("echo v3 > a.py") // no Edit/Write tool event
		stdout, exit := f.stop("s-rewrite", false, "")
		if exit != "exit=0" {
			t.Fatal(exit)
		}
		if dec, reason := decisionOf(t, stdout); dec != "block" || !strings.Contains(reason, "1 file(s)") {
			t.Errorf("decision %q reason %q, want block naming 1 file(s)", dec, reason)
		}
	})

	t.Run("read-only session on a dirty tree is silent", func(t *testing.T) {
		f := dirtyStartFixture(t)
		for i := 0; i < 2; i++ {
			f.sh("cat a.py notes.txt >/dev/null")
			if stdout, exit := f.stop("s-readonly", false, ""); exit != "exit=0" || stdout != "" {
				t.Fatalf("stop %d: stdout %q %s, want silent exit 0", i, stdout, exit)
			}
		}
	})

	t.Run("editing an untracked file that existed at start counts as 1", func(t *testing.T) {
		f := dirtyStartFixture(t)
		if stdout, _ := f.stop("s-untracked", false, ""); stdout != "" {
			t.Fatalf("start: stdout %q, want silent", stdout)
		}
		f.sh("echo u2 > notes.txt")
		stdout, _ := f.stop("s-untracked", false, "")
		if dec, reason := decisionOf(t, stdout); dec != "block" || !strings.Contains(reason, "1 file(s)") {
			t.Errorf("decision %q reason %q, want block naming 1 file(s)", dec, reason)
		}
	})
}

// TestSessionGitStateStoresHashesNotContent: the stored fingerprint is paths
// and hashes only; no file content reaches the database.
func TestSessionGitStateStoresHashesNotContent(t *testing.T) {
	f := dirtyStartFixture(t)
	f.sh("echo SECRET-TOKEN-CONTENT > notes.txt")
	f.stop("s-nocontent", false, "")

	s := mustOpenTestStore(t, f.dbPath)
	rows, err := s.DB().Query(`SELECT payload FROM timeline_events WHERE kind = ?`, payload.KindSessionGitState)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		seen++
		if strings.Contains(raw, "SECRET-TOKEN-CONTENT") || strings.Contains(raw, "v2") {
			t.Errorf("git_state payload carries file content: %s", raw)
		}
		var gs payload.SessionGitState
		if err := json.Unmarshal([]byte(raw), &gs); err != nil {
			t.Fatal(err)
		}
		if gs.Phase == payload.GitStatePhaseStart {
			if len(gs.Hashes) != 2 || gs.Hashes["a.py"] == "" || gs.Hashes["notes.txt"] == "" {
				t.Errorf("start hashes = %v, want hashes for a.py and notes.txt", gs.Hashes)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no session.git_state event recorded")
	}
}
