package ident

// HarnessShell is the agent label of a session minted for one interactive
// shell's captured commands (`backstory shell emit`). It is deliberately not
// in KnownHarnesses: a shell is never matched by the ancestry walk, and its
// sessions do not count as agent work.
const HarnessShell = "shell"

// KnownHarnesses is the named table of harness process names the /proc
// ancestry walk recognizes (PLAN.md §Phase 1: "harness table lives in named
// config, not literals in the walk"). The value reported by ProcFS.Status's
// Name field (i.e. /proc/<pid>/status "Name:", the process's comm) is
// matched against this table exactly; a miss all the way to pid 1 leaves
// Harness at HarnessUnknown.
var KnownHarnesses = []string{
	"claude",
	"codex",
	"copilot",
	"hermes",
	"opencode",
	"pi",
}

// matchHarness reports whether procName is a known harness binary, and its
// canonical identifier when it is.
func matchHarness(procName string) (string, bool) {
	for _, h := range KnownHarnesses {
		if procName == h {
			return h, true
		}
	}
	return "", false
}
