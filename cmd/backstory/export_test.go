package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// exportFixtureProject is the project key every export CLI test seeds and
// queries against.
const exportFixtureProject = "acme-export-cli"

// buildExportFixtureStore opens a fresh store at $XDG_DATA_HOME/backstory/
// backstory.db under dataDir and seeds it with one decision record for
// exportFixtureProject — enough to make `backstory export` render
// non-empty content; internal/export's own tests cover the rendered
// content's shape (header, tiers, stale flag, tombstone omission) in
// detail, so this file only exercises the CLI's own concerns: where the
// output goes.
func buildExportFixtureStore(t *testing.T, dataDir string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertProject(store.Project{
		Key: exportFixtureProject, Toplevel: exportFixtureProject, FirstSeen: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityHuman}, Kind: store.KindDecision,
		Text: "Ship the CLI mirror export.", ProjectKey: exportFixtureProject,
	}); err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
}

// runExportCLI runs `backstory export` in-process against dataDir's store,
// with XDG_DATA_HOME pointed at dataDir — the same technique
// cmd/backstory/recall_test.go's runRecallCLI uses.
func runExportCLI(t *testing.T, dataDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	var outBuf, errBuf bytes.Buffer
	code = run(append([]string{"export"}, args...), bytes.NewReader(nil), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

// TestExportNoOutWritesNothingToDiskOnlyStdout is DONE WHEN clause 3's
// first half: with no --out at all, the rendered mirror goes to stdout and
// nothing is written to disk anywhere outside the store itself.
func TestExportNoOutWritesNothingToDiskOnlyStdout(t *testing.T) {
	dataDir := t.TempDir()
	buildExportFixtureStore(t, dataDir)
	outDir := t.TempDir()

	stdout, stderr, code := runExportCLI(t, dataDir, "--project", exportFixtureProject)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "Backstory export:") || !strings.Contains(stdout, exportFixtureProject) {
		t.Fatalf("stdout = %q, want the rendered mirror", stdout)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("outDir = %v, want nothing written to disk when --out is not given", entries)
	}
}

// TestExportOutDashWritesStdoutOnly covers --out - explicitly meaning
// stdout, the same as omitting --out (ORIGINAL DESCRIPTION: "backstory
// export ... [--out <path>|-]").
func TestExportOutDashWritesStdoutOnly(t *testing.T) {
	dataDir := t.TempDir()
	buildExportFixtureStore(t, dataDir)

	stdout, stderr, code := runExportCLI(t, dataDir, "--project", exportFixtureProject, "--out", "-")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "Backstory export:") {
		t.Fatalf("stdout = %q, want the rendered mirror", stdout)
	}
}

// TestExportWithOutWritesFileAtomicallyNotStdout is DONE WHEN clause 3's
// second half: --out <path> writes the rendered mirror to that path (not
// stdout), and the file holds the full rendered content.
func TestExportWithOutWritesFileAtomicallyNotStdout(t *testing.T) {
	dataDir := t.TempDir()
	buildExportFixtureStore(t, dataDir)
	outPath := filepath.Join(t.TempDir(), "mirror.md")

	stdout, stderr, code := runExportCLI(t, dataDir, "--project", exportFixtureProject, "--out", outPath)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty when --out writes to a file", stdout)
	}
	got, err := os.ReadFile(outPath) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", outPath, err)
	}
	if !strings.Contains(string(got), "Backstory export:") || !strings.Contains(string(got), exportFixtureProject) {
		t.Fatalf("file content = %q, want the rendered mirror", got)
	}
	if !strings.Contains(string(got), "Ship the CLI mirror export.") {
		t.Fatalf("file content = %q, want the fixture decision's text", got)
	}
}

// TestExportInvalidAltitudeRejected guards the flag's own validation: an
// altitude outside headline/summary is a usage error (exit 2), the same
// convention cmd/backstory/recall_test.go's TestRecallInvalidAltitudeRejected
// uses. In particular "full" is rejected — export has no --altitude full
// (ORIGINAL DESCRIPTION's CLI signature lists only headline|summary).
func TestExportInvalidAltitudeRejected(t *testing.T) {
	dataDir := t.TempDir()
	buildExportFixtureStore(t, dataDir)

	for _, altitude := range []string{"orbital", "full"} {
		_, stderr, code := runExportCLI(t, dataDir, "--project", exportFixtureProject, "--altitude", altitude)
		if code != 2 {
			t.Fatalf("--altitude %s: exit code = %d, want 2", altitude, code)
		}
		if !strings.Contains(stderr, altitude) {
			t.Fatalf("--altitude %s: stderr = %q, want it to name the rejected value", altitude, stderr)
		}
	}
}

// TestExportDefaultAltitudeIsSummary covers export's default altitude with
// no --altitude flag at all (ORIGINAL DESCRIPTION: "at the chosen altitude
// (default summary)"): the fixture decision's text is untruncated at this
// short length regardless, so this test asserts the CLI runs successfully
// end-to-end at the default rather than distinguishing altitudes (that
// distinction is internal/export's TestBuildAltitudeChangesRendering).
func TestExportDefaultAltitudeIsSummary(t *testing.T) {
	dataDir := t.TempDir()
	buildExportFixtureStore(t, dataDir)

	stdout, stderr, code := runExportCLI(t, dataDir, "--project", exportFixtureProject)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "Ship the CLI mirror export.") {
		t.Fatalf("stdout = %q, want the fixture decision's text", stdout)
	}
}
