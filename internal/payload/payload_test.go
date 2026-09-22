package payload

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// exemptFiles are non-test .go files in the scanned packages that
// legitimately declare json-tagged structs for something other than a
// Backstory timeline event payload, so a hit there is not a violation.
//
// line.go decodes Claude Code's OWN transcript line format
// (~/.claude/projects/<slug>/<sessionId>.jsonl) — an external file format
// Backstory reads but does not own the shape of. It is not a payload this
// package's contract governs.
var exemptFiles = map[string]bool{
	filepath.Join("backfill", "claude", "line.go"): true,
}

// TestNoPrivateJSONTaggedPayloadStructsOutsideThisPackage is the punch's
// clause 1 (task 8ba5487a): internal/block and internal/backfill/claude
// must build and decode every timeline event payload through this
// package's types — SessionStart, SessionEnd, ToolUse, ToolResult — never
// through a private json-tagged struct of their own. A hit here means one
// of those packages just reintroduced the class of bug this package exists
// to close: two independent struct definitions for the same wire shape,
// free to drift apart silently.
func TestNoPrivateJSONTaggedPayloadStructsOutsideThisPackage(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// thisFile is .../internal/payload/payload_test.go; internalDir is
	// .../internal, the parent of every package this test scans.
	internalDir := filepath.Dir(filepath.Dir(thisFile))

	packages := []string{
		filepath.Join("block"),
		filepath.Join("backfill", "claude"),
	}

	for _, pkgRel := range packages {
		pkgDir := filepath.Join(internalDir, pkgRel)
		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			t.Fatalf("ReadDir(%s): %v", pkgDir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if exemptFiles[filepath.Join(pkgRel, name)] {
				continue
			}
			path := filepath.Join(pkgDir, name)
			b, err := os.ReadFile(path) //nolint:gosec // path is built from a fixed, hardcoded package list, not external input
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", path, err)
			}
			if strings.Contains(string(b), `json:"`) {
				t.Errorf("%s declares a json-tagged struct outside package payload; every timeline event payload must be built from payload.SessionStart / SessionEnd / ToolUse / ToolResult", filepath.Join(pkgRel, name))
			}
		}
	}
}
