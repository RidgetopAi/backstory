package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// The Repo line's uncommitted count is the last session end's observation and
// says so, with its age.
func TestRepoLineLabelsUncommittedAsAtLastSessionEnd(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	earlier := mustStartSession(t, s, "claude", "/proj", 101)
	h := mustInsertHandoff(t, s, earlier, "handoff")
	two := 2
	mustAppendEvent(t, s, earlier, payload.KindSessionGitState, h.TS.Add(time.Minute), payload.SessionGitState{Branch: "main", UncommittedCount: &two})
	out := renderAt(t, s, self, h.TS.Add(3*time.Hour+time.Minute))
	line := lineWith(out, "Repo:")
	if !strings.Contains(line, "2 uncommitted at last session end (3h ago)") {
		t.Errorf("Repo line = %q, want the at-last-session-end label with an age", line)
	}
}

// A failure cleared by an attributable pass is no longer possibly-stale
// evidence; a later edit to a path the handoff names still is.
func TestClearedFailureIsNotStaleEvidence(t *testing.T) {
	const cmd = "go test ./..."
	exit1 := 1
	st := newTestStore(t)
	mustUpsertProject(t, st, testProjectKey)
	self := mustStartSession(t, st, "claude", "/proj", 100)
	h := mustInsertHandoffAbout(t, st, self, "resume at main.go", []string{"main.go"})
	ts := h.TS
	add := func(kind string, pl any) {
		ts = ts.Add(time.Second)
		mustAppendEvent(t, st, self, kind, ts, pl)
	}
	add(payload.KindToolUse, payload.ToolUse{ToolUseID: "t1", Name: "Bash", Command: cmd})
	add(payload.KindToolResult, payload.ToolResult{ToolUseID: "t1", IsError: true, Exit: &exit1})

	out := mustRender(t, st, fakeProcFS{}, self)
	if !strings.Contains(out, "⚠ possibly stale") || !strings.Contains(out, "Last failure") {
		t.Fatalf("open failure must mark the handoff stale:\n%s", out)
	}

	add(payload.KindToolUse, payload.ToolUse{ToolUseID: "t2", Name: "Bash", Command: cmd})
	add(payload.KindToolResult, payload.ToolResult{ToolUseID: "t2"})
	out = mustRender(t, st, fakeProcFS{}, self)
	if strings.Contains(out, "possibly stale") || strings.Contains(out, "possibly-stale") || strings.Contains(out, "Last failure") {
		t.Fatalf("cleared failure still marks/attends:\n%s", out)
	}

	mustAppendToolUseNamed(t, st, self, ts.Add(time.Second), "Edit", "main.go")
	out = mustRender(t, st, fakeProcFS{}, self)
	if !strings.Contains(out, "⚠ possibly stale") {
		t.Fatalf("a later edit of a named path must still mark it:\n%s", out)
	}
}
