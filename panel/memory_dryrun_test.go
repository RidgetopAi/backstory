package panel

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// dryRunGoldenPath is what `backstory purge --dry-run` prints for a project
// with backups and a paired device (cmd/backstory TestPurgeDryRunGolden
// writes and checks it): the same bytes the panel's Forget confirm reads.
const dryRunGoldenPath = "../cmd/backstory/testdata/purge/dry-run.golden"

type dryRunCounts struct {
	Sessions  int    `json:"sessions"`
	Events    int    `json:"events"`
	Records   int    `json:"records"`
	Backups   int    `json:"backups"`
	SyncPeers string `json:"syncPeers"`
}

func parseDryRunJS(t *testing.T, stdout string) *dryRunCounts {
	t.Helper()
	raw := evalJS(t, "parseDryRun("+jsStringLiteral(stdout)+")", "js/memory.js")
	var out *dryRunCounts
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

func readDryRunGolden(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(dryRunGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v", dryRunGoldenPath, err)
	}
	return string(raw)
}

// The confirm says everything the CLI's dry run says Forget does: the counts,
// the store-wide backups it deletes and the paired device it reaches, read
// from the CLI's own pinned output.
func TestForgetConfirmReadsBackupsAndSyncFromTheDryRun(t *testing.T) {
	golden := readDryRunGolden(t)
	got := parseDryRunJS(t, golden)
	want := dryRunCounts{Sessions: 1, Events: 1, Records: 0, Backups: 5, SyncPeers: "laptop"}
	if got == nil || *got != want {
		t.Fatalf("parseDryRun(golden) = %+v, want %+v", got, want)
	}
	raw := evalJS(t, "forgetConfirmText(Object.assign(parseDryRun("+jsStringLiteral(golden)+"), { displayName: \"acme\" }))", "js/memory.js")
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"from acme?", "all 5 Backstory backups", "every project", "forgotten on laptop at the next sync", "cannot be undone"} {
		if !strings.Contains(text, s) {
			t.Errorf("confirm %q lacks %q", text, s)
		}
	}

	// No backups and no paired device: the CLI says so, and the confirm
	// names neither.
	first := strings.SplitN(golden, "\n", 2)[0] + "\n"
	bare := first + "no backups made by Backstory hold this text\n"
	if got := parseDryRunJS(t, bare); got == nil || got.Backups != 0 || got.SyncPeers != "" {
		t.Fatalf("parseDryRun(no backups) = %+v", got)
	}
	// Neither backup line: what Forget deletes is unknown, so no confirm.
	if got := parseDryRunJS(t, first); got != nil {
		t.Fatalf("parseDryRun(counts only) = %+v, want null", got)
	}
}

// A dry run that finishes after the view moved to another folder offers
// nothing; the confirm's Forget runs only for the folder on show and never
// while a purge or delete is running.
func TestDryRunForAnotherFolderIsDiscarded(t *testing.T) {
	stdout := "would purge 3 sessions, 4 events, 5 records\nno backups made by Backstory hold this text\n"
	expr := `(function () {
  var a = purgeScopeKey("workspace:/w", "/w/a"), b = purgeScopeKey("workspace:/w", "/w/b")
  var run = { scopeKey: a, projectKey: "workspace:/w", location: "/w/a", displayName: "w/a", label: "Everything here", since: "", until: "U" }
  var here = dryRunOutcome(run, ` + jsStringLiteral(stdout) + `, a)
  return {
    moved: dryRunOutcome(run, ` + jsStringLiteral(stdout) + `, b).kind,
    here: here.kind,
    location: here.pending.location,
    until: here.pending.until,
    confirmHere: canConfirmPurge(here.pending, a, false),
    confirmMoved: canConfirmPurge(here.pending, b, false),
    confirmBusy: canConfirmPurge(here.pending, a, true),
    confirmNone: canConfirmPurge(null, a, false)
  }
})()`
	raw := evalJS(t, expr, "js/memory.js")
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	want := map[string]any{"moved": "discard", "here": "confirm", "location": "/w/a", "until": "U",
		"confirmHere": true, "confirmMoved": false, "confirmBusy": false, "confirmNone": false}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}
