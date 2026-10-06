package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestDaemonStartEndsSessionsLeftLiveByPreviousRun is task 6d68ac6f's DONE
// WHEN clause 1: a store holding six live rows for one alive pid (previous
// daemon runs) renders that pid at most once in a new reader's Coordination
// slot after the start-up sweep, and the previous rows have ended_at set.
//
// RA-MUTATION-PROBE: skip EndOrphanedSessions -> RED (rows stay live and
// the live-session list is not empty); drop the pid collapse -> the
// pre-sweep render lists the pid six times.
func TestDaemonStartEndsSessionsLeftLiveByPreviousRun(t *testing.T) {
	st := mustOpenStore(t)
	const key, pid = "wobble-party", 16873
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	var old []string
	for i := 0; i < 6; i++ {
		p := pid
		id, err := st.StartSession(store.StartSessionParams{
			Agent: "claude", CWD: "/home/brian/wobble-party", ProjectKey: key, PID: &p,
			StartedAt: time.Now().Add(-time.Duration(i+1) * time.Hour), Origin: store.OriginLive,
		})
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		old = append(old, id)
	}
	// A backfilled row is not a daemon's to end.
	imp, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: "/x", ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginBackfilled,
	})
	if err != nil {
		t.Fatalf("StartSession backfill: %v", err)
	}

	procfs := fakeProcFS{status: map[int]ident.Status{pid: {Name: "claude"}, 4242: {Name: "claude"}}}
	render := func(reader string) string {
		out, err := block.Render(block.Params{
			Store: st, ProcFS: procfs, ProjectKey: key, SessionID: reader, Harness: "claude", Now: time.Now(),
		})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		return out
	}

	now := time.Now()
	n, err := EndOrphanedSessions(st, fakeGit{}, nil, captureNeverOff)
	if err != nil || n != 6 {
		t.Fatalf("EndOrphanedSessions = %d, %v; want 6, nil", n, err)
	}
	readerPID := 4242
	reader, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: "/home/brian/wobble-party", ProjectKey: key, PID: &readerPID,
		StartedAt: now, Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession reader: %v", err)
	}

	out := render(reader)
	if c := strings.Count(out, "- claude in /home/brian/wobble-party"); c > 1 {
		t.Errorf("pid listed %d times; got:\n%s", c, out)
	}
	live, err := st.LiveSessionsInProject(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range live {
		if s.ID != reader {
			t.Errorf("session %s still live after the sweep", s.ID)
		}
	}
	for _, id := range old {
		var ended *int64
		var kind *string
		if err := st.DB().QueryRow(`SELECT ended_at, exit_kind FROM sessions WHERE id = ?`, id).Scan(&ended, &kind); err != nil {
			t.Fatal(err)
		}
		if ended == nil || kind == nil || *kind != ReasonDaemonRestarted {
			t.Errorf("session %s ended_at=%v exit_kind=%v", id, ended, kind)
		}
	}
	var ended *int64
	if err := st.DB().QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, imp).Scan(&ended); err != nil || ended != nil {
		t.Errorf("backfilled session was ended: %v %v", ended, err)
	}
}

func startLeftOpen(t *testing.T, st *store.Store, agent, cwd, key string) string {
	t.Helper()
	id, err := st.StartSession(store.StartSessionParams{
		Agent: agent, CWD: cwd, ProjectKey: key, StartedAt: time.Now().Add(-time.Hour), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return id
}

func sessionEnded(t *testing.T, st *store.Store, id string) bool {
	t.Helper()
	var ended *int64
	if err := st.DB().QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, id).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	return ended != nil
}

// TestOrphanSweepRecordsGitState is task 78ca0350 DONE WHEN (1): a live
// agent session left open in a git repo with 3 uncommitted files ends with a
// session.git_state of uncommitted_count 3, and the next SessionStart block
// for that repo shows "3 uncommitted".
//
// RA-MUTATION-PROBE: drop the git-state recording from the sweep -> RED.
func TestOrphanSweepRecordsGitState(t *testing.T) {
	st := mustOpenStore(t)
	repo := newTempRepo(t)
	for _, f := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const key = "orphan-repo"
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: repo, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	old := startLeftOpen(t, st, "claude", repo, key)

	if n, err := EndOrphanedSessions(st, project.RealGit{}, nil, captureNeverOff); err != nil || n != 1 {
		t.Fatalf("EndOrphanedSessions = %d, %v; want 1, nil", n, err)
	}
	if !sessionEnded(t, st, old) {
		t.Fatal("session not ended")
	}
	evs := gitStateEventsForSession(t, st, old)
	if len(evs) != 1 || evs[0].CouldNotObserve || evs[0].UncommittedCount == nil || *evs[0].UncommittedCount != 3 {
		t.Fatalf("git_state events = %+v; want one with uncommitted_count 3", evs)
	}

	reader := startLeftOpen(t, st, "claude", repo, key)
	out, err := block.Render(block.Params{
		Store: st, ProcFS: fakeProcFS{}, ProjectKey: key, SessionID: reader, Harness: "claude", Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "3 uncommitted") {
		t.Errorf("block lacks \"3 uncommitted\":\n%s", out)
	}
}

// TestOrphanSweepGitStateEdgeCases is DONE WHEN (2): a non-git cwd records
// could_not_observe with no uncommitted_count; a shell session records none;
// capture-off records none (the row still ends).
//
// RA-MUTATION-PROBE: record uncommitted_count 0 on a non-repo cwd -> RED.
func TestOrphanSweepGitStateEdgeCases(t *testing.T) {
	st := mustOpenStore(t)
	const key = "orphan-edge"
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	nonRepo := t.TempDir()
	agent := startLeftOpen(t, st, "claude", nonRepo, key)
	shell := startLeftOpen(t, st, ident.HarnessShell, nonRepo, key)

	if n, err := EndOrphanedSessions(st, project.RealGit{}, nil, captureNeverOff); err != nil || n != 2 {
		t.Fatalf("EndOrphanedSessions = %d, %v; want 2, nil", n, err)
	}
	evs := gitStateEventsForSession(t, st, agent)
	if len(evs) != 1 || !evs[0].CouldNotObserve || evs[0].UncommittedCount != nil {
		t.Errorf("non-repo git_state = %+v; want one could_not_observe with no count", evs)
	}
	if evs := gitStateEventsForSession(t, st, shell); len(evs) != 0 {
		t.Errorf("shell session got git_state %+v", evs)
	}
	if !sessionEnded(t, st, shell) {
		t.Error("shell session not ended")
	}

	off := startLeftOpen(t, st, "claude", nonRepo, key)
	if _, err := EndOrphanedSessions(st, project.RealGit{}, nil, func() (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	if !sessionEnded(t, st, off) || len(gitStateEventsForSession(t, st, off)) != 0 {
		t.Error("capture-off sweep must end the row with no git_state")
	}
}
