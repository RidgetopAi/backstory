package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
)

func mustAppendShellEvent(t *testing.T, s *Store, sessionID, cmd string, exit int) int64 {
	t.Helper()
	b, err := json.Marshal(payload.ShellCommand{Cmd: cmd, CWD: "/proj", Exit: exit})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AppendEvent(Event{TS: time.Now(), Kind: payload.KindShellCommand, SessionID: sessionID, Source: "shell", Payload: string(b)})
	if err != nil {
		t.Fatalf("AppendEvent command: %v", err)
	}
	return id
}

// mustAppendAgentFailure appends an agent tool.use (Bash cmd) plus its
// failing tool.result, returning the result's event id.
func mustAppendAgentFailure(t *testing.T, s *Store, sessionID, useID, cmd string, exit int) int64 {
	t.Helper()
	use, _ := json.Marshal(payload.ToolUse{ToolUseID: useID, Name: "Bash", Command: cmd})
	if _, err := s.AppendEvent(Event{TS: time.Now(), Kind: payload.KindToolUse, SessionID: sessionID, Source: "posttooluse", Payload: string(use)}); err != nil {
		t.Fatalf("AppendEvent tool.use: %v", err)
	}
	res, _ := json.Marshal(payload.ToolResult{ToolUseID: useID, IsError: true, Exit: &exit})
	id, err := s.AppendEvent(Event{TS: time.Now(), Kind: payload.KindToolResult, SessionID: sessionID, Source: "posttooluse", Payload: string(res)})
	if err != nil {
		t.Fatalf("AppendEvent tool.result: %v", err)
	}
	return id
}

func failureReasons(t *testing.T, s *Store, h Record, workspaces []string) []FreshnessReason {
	t.Helper()
	reasons, err := s.HandoffFreshness(h, workspaces)
	if err != nil {
		t.Fatalf("HandoffFreshness: %v", err)
	}
	var out []FreshnessReason
	for _, r := range reasons {
		if r.Kind == FreshnessLaterFailure {
			out = append(out, r)
		}
	}
	return out
}

// DONE WHEN (1): human shell-capture noise (cd typo exit 1, ssh exit 127,
// grep-style exit 1) after a handoff never produces a later-failure reason.
func TestLaterFailureIgnoresShellCaptureEvents(t *testing.T) {
	s, sess := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sess, nil)
	mustAppendShellEvent(t, s, sess, "cd nowhere", 1)
	mustAppendShellEvent(t, s, sess, "ssh ovh", 127)
	mustAppendShellEvent(t, s, sess, "cargo run --release", 101)
	if got := failureReasons(t, s, h, nil); len(got) != 0 {
		t.Fatalf("shell-capture failures produced later-failure reasons: %+v", got)
	}
}

// DONE WHEN (2): an agent tool.result failure in the handoff's own project
// does, citing its event id and saying what failed.
func TestLaterFailureCountsAgentToolResultInOwnProject(t *testing.T) {
	s, sess := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sess, nil)
	mustAppendShellEvent(t, s, sess, "cd nowhere", 1)
	id := mustAppendAgentFailure(t, s, sess, "tu1", "cargo test", 101)

	got := failureReasons(t, s, h, nil)
	if len(got) != 1 || fmt.Sprint(got[0].EventIDs) != fmt.Sprint([]int64{id}) {
		t.Fatalf("later-failure reasons = %+v, want one citing event %d", got, id)
	}
	f := got[0].Failures
	if len(f) != 1 || f[0].Command != "cargo test" || f[0].Tool != "Bash" || f[0].Exit == nil || *f[0].Exit != 101 {
		t.Fatalf("failure detail = %+v, want Bash `cargo test` exit 101", f)
	}

	// An is_error result with no exit code counts too.
	s2, sess2 := newFreshnessFixture(t)
	h2 := mustInsertHandoff(t, s2, sess2, nil)
	b, _ := json.Marshal(payload.ToolResult{ToolUseID: "x", IsError: true})
	id2, err := s2.AppendEvent(Event{TS: time.Now(), Kind: payload.KindToolResult, SessionID: sess2, Source: "posttooluse", Payload: string(b)})
	if err != nil {
		t.Fatal(err)
	}
	if got := failureReasons(t, s2, h2, nil); len(got) != 1 || got[0].EventIDs[0] != id2 {
		t.Fatalf("is_error result without exit: %+v", got)
	}
}

// DONE WHEN (3): in workspace W with repos R and S, an agent failure in S
// flags neither R's handoff nor W's workspace-homed one; a failure located at
// W itself flags W's handoff.
func TestLaterFailureSameProjectOnlyAcrossWorkspace(t *testing.T) {
	wsDir := "/tmp/fixture-failure/projects"
	workspaces := []string{wsDir}
	w := "workspace:" + wsDir
	r := wsDir + "/r"
	sKey := wsDir + "/s"

	s := mustOpen(t, tempDBPath(t))
	for _, k := range []string{w, r, sKey} {
		mustUpsertProject(t, s, k)
	}
	start := func(cwd, key string) string {
		id, err := s.StartSession(StartSessionParams{Agent: "claude", CWD: cwd, ProjectKey: key, StartedAt: time.Now(), Origin: OriginLive})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sessW, sessR, sessS := start(wsDir, w), start(r, r), start(sKey, sKey)
	insert := func(sess, key string) Record {
		id, err := s.InsertRecord(InsertRecordParams{Identity: Identity{Kind: IdentityAgent}, Kind: KindHandoff, Text: "h " + key, SessionID: sess, ProjectKey: key})
		if err != nil {
			t.Fatal(err)
		}
		rec, err := s.GetRecord(id)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	hW, hR := insert(sessW, w), insert(sessR, r)

	failS := mustAppendAgentFailure(t, s, sessS, "s1", "cargo test", 101)
	if got := failureReasons(t, s, hR, workspaces); len(got) != 0 {
		t.Fatalf("S failure flagged R's handoff: %+v", got)
	}
	if got := failureReasons(t, s, hW, workspaces); len(got) != 0 {
		t.Fatalf("S failure (event %d) flagged W's workspace handoff: %+v", failS, got)
	}

	failW := mustAppendAgentFailure(t, s, sessW, "w1", "make", 2)
	got := failureReasons(t, s, hW, workspaces)
	if len(got) != 1 || fmt.Sprint(got[0].EventIDs) != fmt.Sprint([]int64{failW}) {
		t.Fatalf("W-located failure: %+v, want one citing %d", got, failW)
	}
	if got := failureReasons(t, s, hR, workspaces); len(got) != 0 {
		t.Fatalf("W failure flagged R's handoff: %+v", got)
	}
}

// DONE WHEN (6, store half): a human-declared affirm clears the later-failure
// flag; a NEW agent failure afterwards flags again; an agent's confirm affirm
// is never human-declared.
func TestLaterFailureHumanAffirmClearsUntilNewFailure(t *testing.T) {
	s, sess := newFreshnessFixture(t)
	h := mustInsertHandoff(t, s, sess, nil)
	mustAppendAgentFailure(t, s, sess, "a", "go test", 1)
	if got := failureReasons(t, s, h, nil); len(got) != 1 {
		t.Fatalf("before affirm: %+v", got)
	}

	affirmID, err := s.Confirm(ConfirmParams{
		Identity: Identity{Kind: IdentityHuman, Actor: "human"}, Action: ConfirmAffirm, RecordID: h.ID, ProjectKey: h.ProjectKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.GetRecord(affirmID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tier != TierHumanDeclared {
		t.Fatalf("human affirm tier = %q", rec.Tier)
	}
	if got := failureReasons(t, s, h, nil); len(got) != 0 {
		t.Fatalf("after human affirm still flagged: %+v", got)
	}

	id := mustAppendAgentFailure(t, s, sess, "b", "go test", 1)
	if got := failureReasons(t, s, h, nil); len(got) != 1 || got[0].EventIDs[0] != id {
		t.Fatalf("new failure after affirm: %+v, want one citing %d", got, id)
	}

	agentAffirm, err := s.GetRecord(mustAffirm(t, s, sess, h.ID))
	if err != nil {
		t.Fatal(err)
	}
	if agentAffirm.Tier == TierHumanDeclared {
		t.Fatal("an agent confirm affirm produced a human-declared record")
	}
}
