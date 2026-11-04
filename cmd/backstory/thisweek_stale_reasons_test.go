package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func staleItemsJSON(t *testing.T, dataDir string) []attentionItemJSON {
	t.Helper()
	stdout, stderr, code := runThisWeekCLI(t, dataDir, "--json")
	if code != 0 {
		t.Fatalf("this-week --json exit %d: %s", code, stderr)
	}
	var parsed thisWeekOutputJSON
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatal(err)
	}
	var out []attentionItemJSON
	for _, it := range parsed.Attention {
		if it.Kind == "possibly-stale-handoff" {
			out = append(out, it)
		}
	}
	return out
}

// DONE WHEN (1): a possibly-stale handoff item carries the handoff's time and
// one-line summary and one reason entry per freshness reason with kind, when,
// where and text; a failed command longer than 60 characters appears in full.
func TestThisWeekStaleHandoffCarriesStructuredReasons(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	long := "cargo run --release -p omarcade-warden --example art_review -- " + strings.Repeat("x", 80)
	appendWorkspaceRootAgentFailure(t, dataDir, "tu-1", long, 101)

	var rootItem *attentionItemJSON
	var failed *staleReasonJSON
	items := staleItemsJSON(t, dataDir)
	for i := range items {
		it := &items[i]
		if it.HandoffID == workspaceRootHandoff {
			rootItem = it
		}
	}
	if rootItem == nil {
		t.Fatalf("no stale item for %s in %+v", workspaceRootHandoff, items)
	}
	if rootItem.HandoffTime == "" || rootItem.HandoffSummary == "" {
		t.Fatalf("handoff_time %q / handoff_summary %q must both be set", rootItem.HandoffTime, rootItem.HandoffSummary)
	}
	if strings.Contains(rootItem.HandoffSummary, "\n") {
		t.Fatalf("handoff_summary %q must be one line", rootItem.HandoffSummary)
	}
	for i := range rootItem.Reasons {
		if rootItem.Reasons[i].Kind == "later-failure" {
			failed = &rootItem.Reasons[i]
		}
	}
	if failed == nil {
		t.Fatalf("no later-failure reason in %+v", rootItem.Reasons)
	}
	if !strings.Contains(failed.Text, long) || !strings.Contains(failed.Text, "exit 101") {
		t.Fatalf("failure text = %q, want the full command and exit 101", failed.Text)
	}
	if failed.When == "" || failed.Where == "" {
		t.Fatalf("failure reason when %q / where %q must both be set", failed.When, failed.Where)
	}
	if strings.HasPrefix(failed.Where, "/") {
		t.Fatalf("where %q must be workspace-relative", failed.Where)
	}
	// reason stays as it was, truncated.
	if strings.Contains(rootItem.Reason, long) {
		t.Fatalf("reason %q must keep its truncation", rootItem.Reason)
	}
}

// A later-record reason carries the newer note's first line.
func TestThisWeekStaleHandoffLaterRecordCarriesNoteFirstLine(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	var found bool
	for _, it := range staleItemsJSON(t, dataDir) {
		if it.HandoffID != "handoff-0001" {
			continue
		}
		for _, r := range it.Reasons {
			if r.Kind == "later-record" {
				found = true
				if r.Text != "Switched to a different approach" || r.When == "" {
					t.Fatalf("later-record reason = %+v, want the newer note's first line and a time", r)
				}
			}
		}
		if it.HandoffSummary != "Resume: ship the CLI" {
			t.Fatalf("handoff_summary = %q", it.HandoffSummary)
		}
	}
	if !found {
		t.Fatal("no later-record reason on handoff-0001")
	}
}
