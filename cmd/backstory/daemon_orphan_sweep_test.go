package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestDaemonStartEndsSessionsLeftLiveByPreviousRun is task 6d68ac6f clause 1
// at the wiring point: starting the real daemon binary against a store that
// holds live session rows from a previous run ends those rows. It guards the
// mcp.EndOrphanedSessions call in runDaemon, which the internal/mcp tests
// (calling the function directly) cannot.
func TestDaemonStartEndsSessionsLeftLiveByPreviousRun(t *testing.T) {
	bin := buildBackstory(t)
	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("seed: store.Open: %v", err)
	}
	const key = "wobble-party"
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	pid := 16873
	var ids []string
	for i := 0; i < 6; i++ {
		id, err := st.StartSession(store.StartSessionParams{
			Agent: "claude", CWD: "/home/brian/wobble-party", ProjectKey: key, PID: &pid,
			StartedAt: time.Now().Add(-time.Duration(i+1) * time.Hour), Origin: store.OriginLive,
		})
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		ids = append(ids, id)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built
	cmd.Env = testXDGEnv("XDG_RUNTIME_DIR="+runtimeDir, "XDG_DATA_HOME="+dataDir)
	var out safeBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()

	waitForFile(t, filepath.Join(runtimeDir, "backstory", "sock"), 2*time.Second)

	// Read through a second connection; the sweep ran before Listen.
	ro, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = ro.Close() }()
	for _, id := range ids {
		var ended *int64
		if err := ro.DB().QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, id).Scan(&ended); err != nil {
			t.Fatal(err)
		}
		if ended == nil {
			t.Errorf("session %s still live after daemon start\ndaemon output:\n%s", id, out.String())
		}
	}
}

// TestDaemonStartRecordsGitStateForSessionsLeftLive is task 78ca0350 DONE
// WHEN (3): the real daemon binary's start-up sweep records session.git_state
// for a left-open agent session in a repo with 3 uncommitted files, so
// removing the call (or its git-state recording) at runDaemon turns it RED.
func TestDaemonStartRecordsGitStateForSessionsLeftLive(t *testing.T) {
	bin := buildBackstory(t)
	runtimeDir := t.TempDir()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "c1"}} {
		c := exec.Command("git", args...) //nolint:gosec // literal args
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, f := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("seed: store.Open: %v", err)
	}
	const key = "orphan-git"
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: repo, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	id, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: repo, ProjectKey: key, StartedAt: time.Now().Add(-time.Hour), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "daemon") //nolint:gosec // bin is the binary this test just built
	cmd.Env = testXDGEnv("XDG_RUNTIME_DIR="+runtimeDir, "XDG_DATA_HOME="+dataDir)
	var out safeBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()
	waitForFile(t, filepath.Join(runtimeDir, "backstory", "sock"), 2*time.Second)

	ro, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = ro.Close() }()
	var ended *int64
	if err := ro.DB().QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, id).Scan(&ended); err != nil || ended == nil {
		t.Fatalf("session not ended (%v, %v)\ndaemon output:\n%s", ended, err, out.String())
	}
	var raw string
	if err := ro.DB().QueryRow(`SELECT payload FROM timeline_events WHERE kind = ? AND session_id = ?`,
		payload.KindSessionGitState, id).Scan(&raw); err != nil {
		t.Fatalf("no session.git_state after daemon start: %v\ndaemon output:\n%s", err, out.String())
	}
	var gs payload.SessionGitState
	if err := json.Unmarshal([]byte(raw), &gs); err != nil {
		t.Fatal(err)
	}
	if gs.CouldNotObserve || gs.UncommittedCount == nil || *gs.UncommittedCount != 3 {
		t.Errorf("git_state = %s; want uncommitted_count 3", raw)
	}
}
