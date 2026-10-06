package export

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// exportFixtureProject is the project key every export golden test seeds
// and builds against. Fixed rather than derived from a temp dir's git
// state, so goldens are stable regardless of where the checkout lives —
// the same technique cmd/backstory/recall_test.go's fixture uses.
const exportFixtureProject = "acme-export"

// exportFixtureNow is Build's Params.Now for every golden test: fixed, not
// time.Now(), so the header's "Generated: ..." line is byte-for-byte
// reproducible.
var exportFixtureNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// exportFixtureLongText is deliberately longer than summaryBodyChars (240)
// so headline (first line only) and summary (truncated body) render n1
// differently from one another — the same technique recall's own golden
// fixture uses for recallFixtureLongText.
const exportFixtureLongText = "The export mirror renders a project's latest handoff, its open Attention items, and its recent decisions and notes at a chosen altitude, via the recall engine's own trust-annotated ledger, so another tool's memory file can read a bounded, honest summary instead of the whole store."

// exportFixtureTombstonedText must never appear anywhere in Build's output
// (DONE WHEN clause 2): it is the text of a tombstoned note.
const exportFixtureTombstonedText = "SECRET: this text must never leave the ledger."

const (
	fixtureHandoffID  = "10000000-0000-4000-8000-000000000001"
	fixtureDecisionID = "20000000-0000-4000-8000-000000000002"
	fixtureLongNoteID = "30000000-0000-4000-8000-000000000003"
	fixtureDraftID    = "40000000-0000-4000-8000-000000000004"
	fixtureTombID     = "50000000-0000-4000-8000-000000000005"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// buildExportFixtureStore seeds a fresh store with five records for
// exportFixtureProject, inserted in this exact order so rowid ordering
// (newest first, SCHEMA.md invariant 10) is deterministic:
//
//  1. fixtureHandoffID  - the latest handoff, about ["src/main.go"]
//  2. fixtureDecisionID - a decision sharing that about[] path, inserted
//     after the handoff, so store.HandoffFreshness flags the handoff
//     possibly-stale (FreshnessLaterRecord) - DONE WHEN clause 1's "stale
//     flag on a flagged handoff"
//  3. fixtureLongNoteID - a note longer than summaryBodyChars, tier
//     agent-declared, exercising altitude-dependent truncation
//  4. fixtureDraftID    - an inferred-tier note with no promoter/expiry:
//     counts toward the Attention section's unconfirmed draft count
//  5. fixtureTombID     - a tombstoned note whose text must never reach
//     the rendered mirror (DONE WHEN clause 2)
func buildExportFixtureStore(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.UpsertProject(store.Project{
		Key: exportFixtureProject, Toplevel: exportFixtureProject, FirstSeen: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	mustInsertExportFixtureRecord(t, st, fixtureHandoffID, base, store.KindHandoff, store.TierAgentDeclared,
		"Ship v1, then decide whether the Rust FFI is worth it.", []string{"src/main.go"}, nil)
	mustInsertExportFixtureRecord(t, st, fixtureDecisionID, base.Add(time.Minute), store.KindDecision, store.TierHumanDeclared,
		"Use Go throughout the CLI; skip the Rust FFI for v1.", []string{"src/main.go"}, nil)
	mustInsertExportFixtureRecord(t, st, fixtureLongNoteID, base.Add(2*time.Minute), store.KindNote, store.TierAgentDeclared,
		exportFixtureLongText, nil, nil)
	mustInsertExportFixtureRecord(t, st, fixtureDraftID, base.Add(3*time.Minute), store.KindNote, store.TierInferred,
		"Maybe worth trying embeddings later.", nil, nil)
	tombstonedAt := base.Add(5 * time.Minute)
	mustInsertExportFixtureRecord(t, st, fixtureTombID, base.Add(4*time.Minute), store.KindNote, store.TierHumanDeclared,
		exportFixtureTombstonedText, nil, &tombstonedAt)
}

// mustInsertExportFixtureRecord inserts a records row directly via SQL,
// bypassing InsertRecord (random UUID, ts = time.Now(), no way to
// pre-tombstone a row in the same insert) - the same technique
// cmd/backstory/recall_test.go's mustInsertFixtureRecord and
// internal/recall's own fixture helpers use, so golden output is
// deterministic byte for byte. promoter and expires_at are always NULL:
// fixtureDraftID's tier (inferred) plus these two NULLs is exactly what
// store.UnconfirmedDraftCount counts.
func mustInsertExportFixtureRecord(t *testing.T, st *store.Store, id string, ts time.Time, kind store.RecordKind, tier store.Tier, text string, about []string, tombstonedAt *time.Time) {
	t.Helper()
	aboutJSON, err := json.Marshal(about)
	if err != nil {
		t.Fatalf("marshal about: %v", err)
	}
	var tombstonedNanos any
	if tombstonedAt != nil {
		tombstonedNanos = tombstonedAt.UTC().UnixNano()
	}
	_, err = st.DB().Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at, tombstoned_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?, '[]', NULL, NULL, NULL, ?)`,
		id, ts.UTC().UnixNano(), string(kind), string(tier), text, string(aboutJSON), exportFixtureProject, tombstonedNanos)
	if err != nil {
		t.Fatalf("insert fixture record %s: %v", id, err)
	}
}

func goldenPath(name string) string {
	return filepath.Join("testdata", name)
}

// compareGolden compares got against the committed golden file
// testdata/name. Set RA_UPDATE_GOLDEN=1 to (re)write it from got instead of
// comparing - the same mechanism cmd/backstory's own golden tests use.
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

// TestBuildGoldenHeadline is DONE WHEN clause 1's headline half: the fixture
// project at AltitudeHeadline matches the committed golden, including the
// generated-mirror header, tiers, and the stale flag on the flagged
// handoff.
func TestBuildGoldenHeadline(t *testing.T) {
	st := newTestStore(t)
	buildExportFixtureStore(t, st)

	got, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: AltitudeHeadline, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	compareGolden(t, "headline.golden", got)
}

// TestBuildGoldenSummary is DONE WHEN clause 1's summary half (export's
// default altitude).
func TestBuildGoldenSummary(t *testing.T) {
	st := newTestStore(t)
	buildExportFixtureStore(t, st)

	got, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: AltitudeSummary, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	compareGolden(t, "summary.golden", got)
}

// TestBuildHeaderNamesBackstoryProjectAndGenerationTime guards the header's
// three required facts directly (decision 262cf929), independent of the
// golden byte comparison, so a future rendering change that keeps these
// facts but reshuffles surrounding text still has an explicit check.
func TestBuildHeaderNamesBackstoryProjectAndGenerationTime(t *testing.T) {
	st := newTestStore(t)
	buildExportFixtureStore(t, st)

	got, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: AltitudeSummary, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	header := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(header, "Backstory") {
		t.Errorf("header = %q, want it to name Backstory", header)
	}
	if !strings.Contains(header, exportFixtureProject) {
		t.Errorf("header = %q, want it to name the project %q", header, exportFixtureProject)
	}
	if !strings.Contains(header, exportFixtureNow.Format(time.RFC3339)) {
		t.Errorf("header = %q, want it to name the generation time %s", header, exportFixtureNow.Format(time.RFC3339))
	}
	if !strings.Contains(strings.ToLower(header), "generated mirror") || !strings.Contains(strings.ToLower(header), "overwritten") {
		t.Errorf("header = %q, want it to say the file is a generated mirror that gets overwritten", header)
	}
}

// TestBuildTombstonedTextNeverAppears is DONE WHEN clause 2: at both
// altitudes, exportFixtureTombstonedText is absent from Build's output,
// even though the tombstoned record's id still appears (recall omits a
// tombstoned record's text, keeps everything else - SCHEMA.md invariant
// 1), proving the omission is the text specifically, not the whole record
// silently vanishing.
func TestBuildTombstonedTextNeverAppears(t *testing.T) {
	st := newTestStore(t)
	buildExportFixtureStore(t, st)

	for _, altitude := range []Altitude{AltitudeHeadline, AltitudeSummary} {
		got, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: altitude, Now: exportFixtureNow})
		if err != nil {
			t.Fatalf("Build(%s): %v", altitude, err)
		}
		if strings.Contains(got, exportFixtureTombstonedText) {
			t.Fatalf("Build(%s) output contains the tombstoned record's text:\n%s", altitude, got)
		}
		if !strings.Contains(got, fixtureTombID) {
			t.Fatalf("Build(%s) output is missing the tombstoned record's id %s entirely, want it present with its text omitted:\n%s", altitude, fixtureTombID, got)
		}
		if !strings.Contains(got, "tombstoned") {
			t.Fatalf("Build(%s) output does not mark %s as tombstoned:\n%s", altitude, fixtureTombID, got)
		}
	}
}

// TestBuildStaleHandoffFlaggedInResumeAndAttention guards the stale flag
// directly: the fixture handoff is flagged possibly-stale by a later
// decision sharing its about[] path (store.HandoffFreshness's
// FreshnessLaterRecord reason), and Build must surface that in both the
// Resume section (next to the handoff itself) and the Attention section
// (as an open attention item).
func TestBuildStaleHandoffFlaggedInResumeAndAttention(t *testing.T) {
	st := newTestStore(t)
	buildExportFixtureStore(t, st)

	got, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: AltitudeSummary, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	resume := got[strings.Index(got, "## Resume"):strings.Index(got, "## Attention")]
	if !strings.Contains(resume, "possibly stale") {
		t.Errorf("Resume section = %q, want a possibly-stale marker", resume)
	}
	attention := got[strings.Index(got, "## Attention"):strings.Index(got, "## Decisions & notes")]
	if !strings.Contains(attention, "possibly stale") {
		t.Errorf("Attention section = %q, want it to flag the possibly-stale handoff", attention)
	}
	if !strings.Contains(attention, "1 unconfirmed draft") {
		t.Errorf("Attention section = %q, want 1 unconfirmed draft (fixtureDraftID)", attention)
	}
}

// TestBuildTiersShown guards DONE WHEN clause 1's "tiers shown": every one
// of the fixture's three distinct tiers appears in Build's output.
func TestBuildTiersShown(t *testing.T) {
	st := newTestStore(t)
	buildExportFixtureStore(t, st)

	got, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: AltitudeSummary, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, tier := range []store.Tier{store.TierHumanDeclared, store.TierAgentDeclared, store.TierInferred} {
		if !strings.Contains(got, string(tier)) {
			t.Errorf("output missing tier %q:\n%s", tier, got)
		}
	}
}

// TestBuildAltitudeChangesRendering guards the --altitude plumbing itself,
// the same way cmd/backstory/recall_test.go's
// TestRecallAltitudeFlagChangesRendering guards recall's: headline and
// summary must render the long note (fixtureLongNoteID) differently.
func TestBuildAltitudeChangesRendering(t *testing.T) {
	st := newTestStore(t)
	buildExportFixtureStore(t, st)

	headline, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: AltitudeHeadline, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build(headline): %v", err)
	}
	summary, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: AltitudeSummary, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build(summary): %v", err)
	}
	if headline == summary {
		t.Fatalf("headline and summary produced identical output, want different renderings")
	}
	if !strings.Contains(summary, "…") {
		t.Fatalf("summary output has no truncation marker for a %d-char body, want one", len(exportFixtureLongText))
	}
}

// TestBuildEmptyProjectHasNoHandoffAndNoItems covers a project with no
// records: Build still returns a well-formed mirror rather than erroring or
// panicking (no handoff to index into, no decisions/notes to filter).
func TestBuildEmptyProjectHasNoHandoffAndNoItems(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertProject(store.Project{Key: "empty-project", Toplevel: "empty-project", FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	got, err := Build(Params{Store: st, ProjectKey: "empty-project", Altitude: AltitudeSummary, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(got, "no handoff yet") {
		t.Errorf("output = %q, want an honest empty Resume section", got)
	}
	if !strings.Contains(got, "none.") {
		t.Errorf("output = %q, want an honest empty Decisions & notes section", got)
	}
}

// --- WriteAtomic ---

// TestWriteAtomicWritesFile is WriteAtomic's happy path: data lands at path
// verbatim.
func TestWriteAtomicWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mirror.md")
	if err := WriteAtomic(path, []byte("mirror content\n")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "mirror content\n" {
		t.Fatalf("file content = %q, want %q", got, "mirror content\n")
	}
}

// TestWriteAtomicNoPartialFileOnInjectedWriteFailure is DONE WHEN clause 3:
// a write failure injected mid-write (via the writeTempData seam - the temp
// file's random suffix from os.CreateTemp makes any other fault-injection
// technique unreliable) must leave the destination path exactly as it was
// before the call, and must not leave a stray temp file behind. This is
// also clause 4's second mutation target: reverting WriteAtomic to a direct
// os.WriteFile(path, data, ...) call - never calling writeTempData, never
// stopping short of touching path on a mid-write failure - makes this test
// fail, because the pre-existing content would be clobbered regardless of
// the injected error.
func TestWriteAtomicNoPartialFileOnInjectedWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mirror.md")
	const preexisting = "existing mirror content\n"
	if err := os.WriteFile(path, []byte(preexisting), 0o644); err != nil { //nolint:gosec // test fixture, not sensitive
		t.Fatalf("seed preexisting file: %v", err)
	}

	orig := writeTempData
	injectedErr := errors.New("injected write failure")
	writeTempData = func(f *os.File, data []byte) error { return injectedErr }
	t.Cleanup(func() { writeTempData = orig })

	err := WriteAtomic(path, []byte("new content that must never land"))
	if !errors.Is(err, injectedErr) {
		t.Fatalf("WriteAtomic error = %v, want it to wrap %v", err, injectedErr)
	}

	got, readErr := os.ReadFile(path) //nolint:gosec // test-owned temp path
	if readErr != nil {
		t.Fatalf("ReadFile after failed write: %v", readErr)
	}
	if string(got) != preexisting {
		t.Fatalf("path content = %q after a failed write, want it untouched: %q", got, preexisting)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "mirror.md" {
			t.Fatalf("leftover file %s in %s after a failed write, want the temp file cleaned up", e.Name(), dir)
		}
	}
}

// TestWriteAtomicNoPartialFileWhenDestinationDidNotExist covers the other
// half of "no partial file": when path did not exist before an injected
// failure, it must still not exist after.
func TestWriteAtomicNoPartialFileWhenDestinationDidNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mirror.md")

	orig := writeTempData
	writeTempData = func(f *os.File, data []byte) error { return errors.New("injected write failure") }
	t.Cleanup(func() { writeTempData = orig })

	if err := WriteAtomic(path, []byte("new content")); err == nil {
		t.Fatalf("WriteAtomic: want an error, got nil")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%s) error = %v, want IsNotExist (no partial file created)", path, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("dir %s = %v after a failed write, want empty (temp file cleaned up)", dir, entries)
	}
}

// Task 91860fb0 clause 2: the Resume section includes the handoff's next.
func TestBuildResumeSectionIncludesNext(t *testing.T) {
	st := newTestStore(t)
	buildExportFixtureStore(t, st)
	if _, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: "newest handoff",
		ProjectKey: exportFixtureProject, Next: "NEXTMARK add the thing",
	}); err != nil {
		t.Fatalf("insert handoff: %v", err)
	}

	got, err := Build(Params{Store: st, ProjectKey: exportFixtureProject, Altitude: AltitudeSummary, Now: exportFixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(got, "Next: NEXTMARK add the thing") {
		t.Fatalf("export Resume section lacks next:\n%s", got)
	}
}
