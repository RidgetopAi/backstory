package main

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// TestRecallAndTimelineWorkWithAndWithoutRuntimeDirOrHome is the punch's
// DONE WHEN clause 4: recall and timeline open the store directly and never
// dial the daemon socket, so — unlike daemon/hook/mcp — they never consult
// XDG_RUNTIME_DIR or HOME at all when --project is given explicitly (they
// only ever read XDG_DATA_HOME, via storePath()). This runs both commands
// as real subprocesses under two env shapes to prove that directly: one
// with XDG_RUNTIME_DIR and HOME present in the invoking environment (the
// common case), one with both stripped out entirely (temp dirs injected
// for the one thing each command does need, XDG_DATA_HOME) — same fixture,
// same expected output either way.
func TestRecallAndTimelineWorkWithAndWithoutRuntimeDirOrHome(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	seedHumanPathEnvFixture(t, dataDir)

	cases := []struct {
		name string
		env  []string
	}{
		{
			name: "with XDG_RUNTIME_DIR and HOME set",
			env: append(envWithout(testXDGEnv(), "XDG_DATA_HOME"),
				"XDG_DATA_HOME="+dataDir,
				"XDG_RUNTIME_DIR="+t.TempDir(),
			),
		},
		{
			name: "without XDG_RUNTIME_DIR or HOME set",
			env: append(envWithout(testXDGEnv(), "XDG_DATA_HOME", "HOME"),
				"XDG_DATA_HOME="+dataDir,
			),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recallCmd := exec.Command(bin, "recall", "--project", "env-fixture", "--json") //nolint:gosec // bin is this test's own built binary
			recallCmd.Env = tc.env
			recallOut, err := recallCmd.Output()
			if err != nil {
				t.Fatalf("backstory recall: %v", err)
			}
			wantRecall := `{"project_key":"env-fixture","altitude":"summary","items":[{"id":"dddd4444-0000-4000-8000-000000000004","kind":"note","tier":"human-declared","status":"current","text":"env fixture note"}]}` + "\n"
			if string(recallOut) != wantRecall {
				t.Fatalf("backstory recall --json = %q, want %q", recallOut, wantRecall)
			}

			timelineCmd := exec.Command(bin, "timeline", "--project", "env-fixture", "--json") //nolint:gosec // bin is this test's own built binary
			timelineCmd.Env = tc.env
			timelineOut, err := timelineCmd.Output()
			if err != nil {
				t.Fatalf("backstory timeline: %v", err)
			}
			wantTimeline := `{"project_key":"env-fixture","events":[{"id":1,"ts":"2026-01-01T00:00:00Z","kind":"session.start","session_id":"sess-env-fixture","source":"shell","payload":{}}]}` + "\n"
			if string(timelineOut) != wantTimeline {
				t.Fatalf("backstory timeline --json = %q, want %q", timelineOut, wantTimeline)
			}
		})
	}
}

// seedHumanPathEnvFixture seeds a minimal, deterministic store for
// TestRecallAndTimelineWorkWithAndWithoutRuntimeDirOrHome: one record, one
// session, one event, all under project "env-fixture".
func seedHumanPathEnvFixture(t *testing.T, dataDir string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertProject(store.Project{Key: "env-fixture", Toplevel: "env-fixture", FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at, tombstoned_at)
		VALUES ('dddd4444-0000-4000-8000-000000000004', ?, 'note', 'human-declared', 'env fixture note', '[]', NULL, 'env-fixture', '[]', NULL, NULL, NULL, NULL)`,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()); err != nil {
		t.Fatalf("insert fixture record: %v", err)
	}

	sessionID, err := st.StartSession(store.StartSessionParams{
		ID: "sess-env-fixture", Agent: "claude", CWD: "/repo", ProjectKey: "env-fixture",
		StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := st.AppendEvent(store.Event{
		TS: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Kind: "session.start", SessionID: sessionID, Source: "shell", Payload: "{}",
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
}
