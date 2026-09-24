package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// recallFixtureProject is the project key every recall golden test seeds
// and queries against. Fixed rather than derived from a temp dir's git
// state, so goldens are stable regardless of where the checkout lives.
const recallFixtureProject = "acme-widgets"

// recallFixtureLongText is deliberately longer than summaryBodyChars (240)
// so headline (whole text, one line), summary (truncated body) and full
// (untruncated body) all render it differently — the fixture this punch's
// critic clause 5 mutation ("drop the --altitude flag's effect") must fail
// against.
const recallFixtureLongText = "The recall engine walks a project's whole ledger and renders it at an altitude under a token budget, the recall_thread model PLAN.md describes: headline is one line per item, summary is a short body per item, and full is each item's complete text with no truncation at all regardless of length."

// buildRecallFixtureStore opens a fresh store at $XDG_DATA_HOME/backstory/
// backstory.db under dataDir (storePath()'s own layout) and seeds it with
// three decision/note records for recallFixtureProject: a superseded
// decision, the decision that supersedes it, and a long note — enough to
// exercise every recall.Status this punch's DONE WHEN clause 1 requires
// output for (current, superseded) and both truncating and non-truncating
// altitudes. Record ids and timestamps are fixed literals, not the public
// API's random UUID / time.Now(), so golden output is deterministic byte
// for byte.
func buildRecallFixtureStore(t *testing.T, dataDir string) {
	t.Helper()
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertProject(store.Project{
		Key: recallFixtureProject, Toplevel: recallFixtureProject, FirstSeen: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const (
		oldDecisionID = "aaaa1111-0000-4000-8000-000000000001"
		longNoteID    = "bbbb2222-0000-4000-8000-000000000002"
		newDecisionID = "cccc3333-0000-4000-8000-000000000003"
	)
	mustInsertFixtureRecord(t, st, oldDecisionID, base, store.KindDecision, store.TierHumanDeclared,
		"Ship v1 with Go for the CLI binary.")
	mustInsertFixtureRecord(t, st, longNoteID, base.Add(time.Minute), store.KindNote, store.TierAgentDeclared,
		recallFixtureLongText)
	mustInsertFixtureRecord(t, st, newDecisionID, base.Add(2*time.Minute), store.KindDecision, store.TierHumanDeclared,
		"Actually ship v1 with Go and a Rust FFI for search.")

	if err := st.LinkEdge(newDecisionID, oldDecisionID, store.EdgeSupersedes, "human-cli-fixture"); err != nil {
		t.Fatalf("LinkEdge supersedes: %v", err)
	}
}

// mustInsertFixtureRecord inserts a records row with a caller-chosen id and
// ts, bypassing InsertRecord (random UUID, ts = time.Now()) — the same
// technique internal/recall's own tests use (mustInsertRecordAtTS) — so a
// golden fixture's ids and ordering are fixed literals, not test-run
// dependent. session_id is left NULL: recall's project anchor and altitude
// rendering never read it, and NULL satisfies the FK trivially.
func mustInsertFixtureRecord(t *testing.T, st *store.Store, id string, ts time.Time, kind store.RecordKind, tier store.Tier, text string) {
	t.Helper()
	_, err := st.DB().Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at, tombstoned_at)
		VALUES (?, ?, ?, ?, ?, '[]', NULL, ?, '[]', NULL, NULL, NULL, NULL)`,
		id, ts.UTC().UnixNano(), string(kind), string(tier), text, recallFixtureProject)
	if err != nil {
		t.Fatalf("insert fixture record %s: %v", id, err)
	}
}

// runRecallCLI runs `backstory recall` in-process (main's own run, not a
// subprocess) against dataDir's store, with XDG_DATA_HOME pointed at
// dataDir. It never sets XDG_RUNTIME_DIR or HOME: recall opens the store
// directly and never dials the daemon socket, so neither variable is ever
// consulted (DONE WHEN clause 4 holds trivially for this command, not by
// omission).
func runRecallCLI(t *testing.T, dataDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	var outBuf, errBuf bytes.Buffer
	code = run(append([]string{"recall"}, args...), bytes.NewReader(nil), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

func goldenPath(name string) string {
	return filepath.Join("testdata", "recall", name)
}

// compareGolden compares got against the committed golden file testdata/
// recall/name. Set RA_UPDATE_GOLDEN=1 to (re)write it from got instead of
// comparing — the mechanism this punch's goldens were themselves produced
// with, kept so a deliberate future rendering change can regenerate them
// rather than hand-editing committed text.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := goldenPath(name)
	if os.Getenv("RA_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // test fixture output, not sensitive
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // path is this test's own fixed testdata path
	if err != nil {
		t.Fatalf("read golden %s: %v (run with RA_UPDATE_GOLDEN=1 to create it)", path, err)
	}
	if got != string(want) {
		t.Fatalf("output does not match golden %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// TestRecallGoldenHeadline is the punch's DONE WHEN clause 1: `backstory
// recall --altitude headline` against the fixture store matches the
// committed golden.
func TestRecallGoldenHeadline(t *testing.T) {
	dataDir := t.TempDir()
	buildRecallFixtureStore(t, dataDir)
	stdout, stderr, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject, "--altitude", "headline")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	compareGolden(t, "headline.golden", stdout)
}

// TestRecallGoldenSummary covers recall's default altitude (no --altitude
// flag at all).
func TestRecallGoldenSummary(t *testing.T) {
	dataDir := t.TempDir()
	buildRecallFixtureStore(t, dataDir)
	stdout, stderr, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	compareGolden(t, "summary.golden", stdout)
}

// TestRecallGoldenFull covers --altitude full: recallFixtureLongText must
// appear complete, with no "…" truncation marker.
func TestRecallGoldenFull(t *testing.T) {
	dataDir := t.TempDir()
	buildRecallFixtureStore(t, dataDir)
	stdout, stderr, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject, "--altitude", "full")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	compareGolden(t, "full.golden", stdout)
	if strings.Contains(stdout, "…") {
		t.Fatalf("--altitude full output contains a truncation marker, want the complete text:\n%s", stdout)
	}
	if !strings.Contains(stdout, recallFixtureLongText) {
		t.Fatalf("--altitude full output is missing the fixture's full long text:\n%s", stdout)
	}
}

// TestRecallGoldenJSON is the punch's DONE WHEN clause 1's --json half:
// output parses and every item carries id, kind, tier and status.
func TestRecallGoldenJSON(t *testing.T) {
	dataDir := t.TempDir()
	buildRecallFixtureStore(t, dataDir)
	stdout, stderr, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject, "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	compareGolden(t, "json.golden", stdout)

	var parsed struct {
		ProjectKey string `json:"project_key"`
		Altitude   string `json:"altitude"`
		Items      []struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Tier   string `json:"tier"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if parsed.ProjectKey != recallFixtureProject {
		t.Fatalf("project_key = %q, want %q", parsed.ProjectKey, recallFixtureProject)
	}
	if len(parsed.Items) != 3 {
		t.Fatalf("items = %d, want 3: %+v", len(parsed.Items), parsed.Items)
	}
	for _, item := range parsed.Items {
		if item.ID == "" || item.Kind == "" || item.Tier == "" || item.Status == "" {
			t.Fatalf("item missing a required field (id/kind/tier/status): %+v", item)
		}
	}
	var sawCurrent, sawSuperseded bool
	for _, item := range parsed.Items {
		switch item.Status {
		case "current":
			sawCurrent = true
		case "superseded":
			sawSuperseded = true
		}
	}
	if !sawCurrent || !sawSuperseded {
		t.Fatalf("items = %+v, want at least one current and one superseded status", parsed.Items)
	}
}

// TestRecallAltitudeFlagChangesRendering is the direct guard for this
// punch's critic clause 5 mutation ("drop the --altitude flag's effect"):
// headline, summary and full must all render recallFixtureLongText
// differently from one another. If --altitude stopped reaching recall.Build
// (e.g. the flag were parsed but never passed through), all three calls
// would render at the same fixed altitude and at least one of these
// comparisons would start passing where it must fail.
func TestRecallAltitudeFlagChangesRendering(t *testing.T) {
	dataDir := t.TempDir()
	buildRecallFixtureStore(t, dataDir)

	headline, _, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject, "--altitude", "headline")
	if code != 0 {
		t.Fatalf("headline: exit code = %d", code)
	}
	summary, _, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject, "--altitude", "summary")
	if code != 0 {
		t.Fatalf("summary: exit code = %d", code)
	}
	full, _, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject, "--altitude", "full")
	if code != 0 {
		t.Fatalf("full: exit code = %d", code)
	}

	if headline == summary {
		t.Fatalf("--altitude headline and --altitude summary produced identical output, want different renderings")
	}
	if summary == full {
		t.Fatalf("--altitude summary and --altitude full produced identical output, want different renderings")
	}
	if strings.Contains(summary, "…") == false {
		t.Fatalf("--altitude summary output has no truncation marker for a %d-char body, want one", len(recallFixtureLongText))
	}
	if strings.Contains(full, "…") {
		t.Fatalf("--altitude full output contains a truncation marker, want the complete text")
	}
}

// TestRecallInvalidAltitudeRejected guards the flag's own validation: an
// altitude outside headline/summary/full is a usage error (exit 2), not a
// silent fallback to the default.
func TestRecallInvalidAltitudeRejected(t *testing.T) {
	dataDir := t.TempDir()
	buildRecallFixtureStore(t, dataDir)
	_, stderr, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject, "--altitude", "orbital")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "orbital") {
		t.Fatalf("stderr = %q, want it to name the rejected value", stderr)
	}
}

// TestRecallEmptyProjectPrintsEmptyStateNamingProject is the punch's DONE
// WHEN clause 3: a project with nothing in the store gets an honest empty
// state naming it, and still exits 0.
func TestRecallEmptyProjectPrintsEmptyStateNamingProject(t *testing.T) {
	dataDir := t.TempDir()
	stdout, stderr, code := runRecallCLI(t, dataDir, "--project", "empty-project")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	want := "no records for project empty-project\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

// TestRecallEmptyProjectJSONNamesProjectWithNoItems covers the same empty
// state through --json: project_key is set, items is an empty array (never
// null, so a caller's JSON decoder never has to special-case it).
func TestRecallEmptyProjectJSONNamesProjectWithNoItems(t *testing.T) {
	dataDir := t.TempDir()
	stdout, stderr, code := runRecallCLI(t, dataDir, "--project", "empty-project", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	want := `{"project_key":"empty-project","altitude":"summary","items":[]}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

// TestRecallRecordAnchorByIDPrefix covers the CLI's own id-vs-text
// disambiguation (recallAnchor): a unique id prefix resolves to that
// record's own anchor, not a free-text search.
func TestRecallRecordAnchorByIDPrefix(t *testing.T) {
	dataDir := t.TempDir()
	buildRecallFixtureStore(t, dataDir)

	stdout, stderr, code := runRecallCLI(t, dataDir, "--project", recallFixtureProject, "--json", "cccc3333")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	var parsed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	// The record anchor walks edges too, so the superseded record it points
	// at via `supersedes` comes back alongside it.
	ids := map[string]bool{}
	for _, item := range parsed.Items {
		ids[item.ID] = true
	}
	if !ids["cccc3333-0000-4000-8000-000000000003"] || !ids["aaaa1111-0000-4000-8000-000000000001"] {
		t.Fatalf("record anchor result = %+v, want the anchor record and its supersedes neighbour", parsed.Items)
	}
	if ids["bbbb2222-0000-4000-8000-000000000002"] {
		t.Fatalf("record anchor result = %+v, want the unrelated note excluded", parsed.Items)
	}
}
