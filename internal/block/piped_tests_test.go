package block_test

import (
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/payload"
)

const pipedUnittest = "python -m unittest discover -s tests 2>&1 | tail -3"

// A piped run's exit is tail's, not the runner's: it must not clear a
// failure nor render as a pass.
func TestPipedTestRunExitZeroDoesNotClearFailure(t *testing.T) {
	out := renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "python -m unittest", Exit: 1})
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: pipedUnittest, Exit: 0})
	})
	if !strings.Contains(out, "Last failure: python -m unittest exit 1") {
		t.Errorf("piped exit 0 cleared the failure:\n%s", out)
	}
	if strings.Contains(out, "last exit 0") {
		t.Errorf("Tests line claims exit 0 for a piped run:\n%s", out)
	}
	if !strings.Contains(out, "exit not observed (piped)") {
		t.Errorf("Tests line should say the exit was not observed:\n%s", out)
	}
}

func TestPipefailMakesPipedExitObserved(t *testing.T) {
	const cmd = "set -o pipefail; python -m unittest 2>&1 | tail -3"
	out := renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "python -m unittest", Exit: 1})
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: cmd, Exit: 0})
	})
	if strings.Contains(out, "Last failure") {
		t.Errorf("pipefail exit 0 should clear the failure:\n%s", out)
	}
	if !strings.Contains(out, "Tests: "+cmd+" (last exit 0") {
		t.Errorf("want Tests line with last exit 0:\n%s", out)
	}

	out = renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: cmd, Exit: 1})
	})
	if !strings.Contains(out, "Last failure: "+cmd+" exit 1") {
		t.Errorf("piped non-zero exit must show as Last failure:\n%s", out)
	}
}

func TestMultiLineCommandGetsTestsLine(t *testing.T) {
	out := renderAfterHandoff(t, func(s func(string, any)) {
		s(payload.KindShellCommand, payload.ShellCommand{Cmd: "cd repo\npython -m unittest -v", Exit: 0})
	})
	if !strings.Contains(out, "Tests: ") || !strings.Contains(out, "python -m unittest") || !strings.Contains(out, "last exit 0") {
		t.Errorf("multi-line command should produce a Tests line:\n%s", out)
	}
}
