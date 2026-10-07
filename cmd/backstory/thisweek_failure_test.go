package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

const workspaceRootHandoff = "handoff-workspace-root"

// appendWorkspaceRootAgentFailure appends an agent Bash tool.use + failing
// tool.result to the fixture's workspace-root session, after its handoff.
func appendWorkspaceRootAgentFailure(t *testing.T, dataDir, useID, cmd string, exit int) int64 {
	t.Helper()
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	use, _ := json.Marshal(payload.ToolUse{ToolUseID: useID, Name: "Bash", Command: cmd})
	mustAppendEvent(t, st, store.Event{TS: date(4, 9, 0), Kind: payload.KindToolUse, SessionID: "sess-workspace-root-1", Source: "posttooluse", Payload: string(use)})
	res, _ := json.Marshal(payload.ToolResult{ToolUseID: useID, IsError: true, Exit: &exit})
	return mustAppendEvent(t, st, store.Event{TS: date(4, 9, 1), Kind: payload.KindToolResult, SessionID: "sess-workspace-root-1", Source: "posttooluse", Payload: string(res)})
}

func workspaceRootItems(t *testing.T, dataDir string) (items []attentionItemJSON, text string, rows int) {
	t.Helper()
	stdout, stderr, code := runThisWeekCLI(t, dataDir, "--json")
	if code != 0 {
		t.Fatalf("this-week --json exit %d: %s", code, stderr)
	}
	var parsed thisWeekOutputJSON
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatal(err)
	}
	for _, it := range parsed.Attention {
		if it.HandoffID == workspaceRootHandoff {
			items = append(items, it)
		}
	}
	for _, row := range parsed.WhereLeftOff {
		if row.Project != nil && row.Project.HandoffID == workspaceRootHandoff {
			rows++
		}
	}
	text, stderr, code = runThisWeekCLI(t, dataDir)
	if code != 0 {
		t.Fatalf("this-week exit %d: %s", code, stderr)
	}
	return items, text, rows
}

// DONE WHEN (4) and (5): a later agent failure names the project label, the
// failed command (truncated) and the exit code in text and JSON (evidence ids
// kept), and the workspace handoff — reached through two label rows — is ONE
// attention item.
func TestThisWeekLaterFailureNamesWhatFailedAndListsHandoffOnce(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	long := "cargo run --release -p omarcade-warden --example art_review -- " + strings.Repeat("x", 80)
	id := appendWorkspaceRootAgentFailure(t, dataDir, "tu-1", long, 101)

	items, text, rows := workspaceRootItems(t, dataDir)
	if rows != 2 {
		t.Fatalf("workspace handoff reached through %d rows, want 2 (omarcade, vidflow) for the dedupe to mean anything", rows)
	}
	if len(items) != 1 {
		t.Fatalf("handoff listed %d times in attention, want exactly 1: %+v", len(items), items)
	}
	it := items[0]
	trunc := long[:60] + "…"
	if !strings.HasPrefix(it.Reason, "projects/") || !strings.Contains(it.Reason, trunc+" failed (exit 101) after it") || strings.Contains(it.Reason, long) {
		t.Fatalf("JSON reason = %q, want a projects/<label> prefix and the truncated command + exit 101", it.Reason)
	}
	if len(it.EvidenceIDs) < 1 || it.EvidenceIDs[len(it.EvidenceIDs)-1] != strconv.FormatInt(id, 10) {
		t.Fatalf("evidence_ids = %v, want to end with the failing result event %d", it.EvidenceIDs, id)
	}
	if !strings.Contains(text, "handoff possibly stale — "+trunc+" failed (exit 101) after it") || !strings.Contains(text, "projects/") {
		t.Fatalf("text attention lacks label/command/exit:\n%s", text)
	}
	if n := strings.Count(text, "possibly stale — "+trunc); n != 1 {
		t.Fatalf("text lists the failure %d times, want 1:\n%s", n, text)
	}
}

// DONE WHEN (6): `backstory affirm <id>` writes a human-declared affirm that
// clears the item; a NEW agent failure flags the handoff again.
func TestAffirmCLIClearsStaleHandoffUntilNewAgentFailure(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	appendWorkspaceRootAgentFailure(t, dataDir, "tu-1", "cargo test", 101)
	if items, _, _ := workspaceRootItems(t, dataDir); len(items) != 1 {
		t.Fatalf("before affirm: %d items, want 1", len(items))
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"affirm", workspaceRootHandoff}, bytes.NewReader(nil), &out, &errOut); code != 0 {
		t.Fatalf("affirm exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "affirmed "+workspaceRootHandoff) {
		t.Fatalf("affirm stdout = %q", out.String())
	}

	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	edges, err := st.EdgesTouching(workspaceRootHandoff)
	if err != nil {
		t.Fatal(err)
	}
	var human bool
	for _, e := range edges {
		if e.Type != store.EdgeInforms || e.ToID != workspaceRootHandoff {
			continue
		}
		rec, err := st.GetRecord(e.FromID)
		if err != nil {
			t.Fatal(err)
		}
		human = rec.Kind == store.KindConfirm && rec.Tier == store.TierHumanDeclared
	}
	_ = st.Close()
	if !human {
		t.Fatalf("no human-declared confirm informing the handoff; edges %+v", edges)
	}

	if items, text, _ := workspaceRootItems(t, dataDir); len(items) != 0 {
		t.Fatalf("after affirm still flagged: %+v\n%s", items, text)
	}

	appendWorkspaceRootAgentFailure(t, dataDir, "tu-2", "make", 2)
	items, _, _ := workspaceRootItems(t, dataDir)
	if len(items) != 1 || !strings.Contains(items[0].Reason, "make failed (exit 2) after it") {
		t.Fatalf("new failure after affirm: %+v, want one item naming make", items)
	}
}

func TestAffirmCLIRejectsBadTargets(t *testing.T) {
	dataDir := t.TempDir()
	buildThisWeekFixtureStore(t, dataDir)
	t.Setenv("XDG_DATA_HOME", dataDir)
	for _, args := range [][]string{{"affirm"}, {"affirm", "no-such-record"}, {"affirm", "claim001"}} {
		var out, errOut bytes.Buffer
		if code := run(args, bytes.NewReader(nil), &out, &errOut); code == 0 {
			t.Errorf("run(%v) exit 0, want failure (stdout %q)", args, out.String())
		}
	}
}
