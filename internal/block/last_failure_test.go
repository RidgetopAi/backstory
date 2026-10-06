package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/payload"
)

func renderAfterHandoff(t *testing.T, build func(s func(kind string, pl any))) string {
	t.Helper()
	st := newTestStore(t)
	mustUpsertProject(t, st, testProjectKey)
	self := mustStartSession(t, st, "claude", "/proj", 100)
	handoff := mustInsertHandoff(t, st, self, "resume here")
	ts := handoff.TS
	build(func(kind string, pl any) {
		ts = ts.Add(time.Second)
		mustAppendEvent(t, st, self, kind, ts, pl)
	})
	out, err := block.Render(block.Params{
		Store: st, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
		Now: ts.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

// TestLastFailureClearedByLaterNonErrorResultForSameCommand is task
// b2613e7a's clause 3: a failing `python -m unittest` (is_error, as the
// PostToolUseFailure hook records it) followed by a non-error result for the
// same command leaves no Last failure line; with no later success it shows.
func TestLastFailureClearedByLaterNonErrorResultForSameCommand(t *testing.T) {
	const cmd = "python -m unittest"
	exit1 := 1
	failing := func(s func(string, any)) {
		s(payload.KindToolUse, payload.ToolUse{ToolUseID: "t1", Name: "Bash", Command: cmd})
		s(payload.KindToolResult, payload.ToolResult{ToolUseID: "t1", IsError: true, Exit: &exit1})
	}

	out := renderAfterHandoff(t, func(s func(string, any)) {
		failing(s)
		s(payload.KindToolUse, payload.ToolUse{ToolUseID: "t2", Name: "Bash", Command: cmd})
		s(payload.KindToolResult, payload.ToolResult{ToolUseID: "t2", IsError: false})
	})
	if strings.Contains(out, "Last failure") {
		t.Errorf("failure was fixed by a later non-error result, still shown:\n%s", out)
	}

	out = renderAfterHandoff(t, func(s func(string, any)) {
		failing(s)
		s(payload.KindToolUse, payload.ToolUse{ToolUseID: "t2", Name: "Bash", Command: "go build ./..."})
		s(payload.KindToolResult, payload.ToolResult{ToolUseID: "t2", IsError: false})
	})
	if !strings.Contains(out, "Last failure: python -m unittest exit 1") {
		t.Errorf("a different passing command must not clear the failure:\n%s", out)
	}

	out = renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindToolUse, payload.ToolUse{ToolUseID: "t1", Name: "Bash", Command: cmd})
		s(payload.KindToolResult, payload.ToolResult{ToolUseID: "t1", IsError: true})
	})
	if !strings.Contains(out, "Last failure: python -m unittest failed") {
		t.Errorf("is_error failure with no exit and no later success must show:\n%s", out)
	}
}

// TestLastFailureClearedByLaterShellExitZero covers the shell-event half of
// clearing, and that the newest of several uncleared failures is shown.
func TestLastFailureClearedByLaterShellExitZero(t *testing.T) {
	out := renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "make check", Exit: 2})
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "make check", Exit: 0})
	})
	if strings.Contains(out, "Last failure") {
		t.Errorf("shell exit 0 should clear the failure:\n%s", out)
	}

	out = renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "make lint", Exit: 3})
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "make check", Exit: 2})
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "make check", Exit: 0})
	})
	if !strings.Contains(out, "Last failure: make lint exit 3") {
		t.Errorf("the newest uncleared failure should show:\n%s", out)
	}
}
