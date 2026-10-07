package block_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// TestLastFailureClearsOnNormalisedCommandKey: `-v` and a `2>&1` redirect
// are the same command; an unrelated passing command still leaves it.
func TestLastFailureClearsOnNormalisedCommandKey(t *testing.T) {
	out := renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "python -m unittest", Exit: 1})
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "python -m unittest -v 2>&1", Exit: 0})
	})
	if strings.Contains(out, "Last failure") {
		t.Errorf("same command with -v and a redirect passed, failure still shown:\n%s", out)
	}
	out = renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "python -m unittest", Exit: 1})
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "ls -la | head", Exit: 0})
	})
	if !strings.Contains(out, "Last failure: python -m unittest exit 1") {
		t.Errorf("an unrelated passing command must not clear the failure:\n%s", out)
	}
}

func TestPossiblyStaleOnLaterNonZeroExit(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, testProjectKey)
	self := mustStartSession(t, st, "claude", "/proj", 100)
	handoff := mustInsertHandoff(t, st, self, "tests pass")
	exit1 := 1
	id := mustAppendEvent(t, st, self, payload.KindToolResult, handoff.TS.Add(time.Second),
		payload.ToolResult{ToolUseID: "t1", IsError: true, Exit: &exit1})
	out := mustRender(t, st, fakeProcFS{}, self)
	if !strings.Contains(out, "⚠ possibly stale:") || !strings.Contains(out, "later non-zero exit(s) (ids "+strconv.FormatInt(id, 10)+")") {
		t.Errorf("failing exit after handoff must mark it possibly stale citing event %d:\n%s", id, out)
	}

	st = newTestStore(t)
	mustUpsertProject(t, st, testProjectKey)
	self = mustStartSession(t, st, "claude", "/proj", 100)
	h := mustInsertHandoff(t, st, self, "tests pass")
	mustAppendEvent(t, st, self, payload.KindShellCommand, h.TS.Add(time.Second),
		payload.ShellCommand{Cmd: "ls", Exit: 0})
	if out = mustRender(t, st, fakeProcFS{}, self); strings.Contains(out, "possibly stale") {
		t.Errorf("no later failure or edit, yet marked stale:\n%s", out)
	}
}

// TestDeltaExcludesHandoffWritingSession: a later session reading right
// after the session that wrote the handoff sees no "sessions" count.
func TestDeltaExcludesHandoffWritingSession(t *testing.T) {
	st := newTestStore(t)
	mustUpsertProject(t, st, testProjectKey)
	writer := mustStartSession(t, st, "claude", "/proj", 100)
	h := mustInsertHandoff(t, st, writer, "done")
	mustAppendEvent(t, st, writer, payload.KindSessionEnd, h.TS.Add(time.Second), payload.SessionEnd{Reason: "exit"})
	reader := mustStartSession(t, st, "claude", "/proj", 200)
	out := mustRender(t, st, fakeProcFS{}, reader)
	if strings.Contains(out, "sessions") && strings.Contains(out, "Delta:") {
		t.Errorf("handoff's own session counted in Delta:\n%s", out)
	}
}

func TestTestsLine(t *testing.T) {
	out := renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "python -m unittest", Exit: 0})
	})
	if !strings.Contains(out, "Tests: python -m unittest (last exit 0, ") {
		t.Errorf("missing Tests line:\n%s", out)
	}
	out = renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "go build ./...", Exit: 0})
	})
	if strings.Contains(out, "Tests:") {
		t.Errorf("Tests line with no test command observed:\n%s", out)
	}
}
