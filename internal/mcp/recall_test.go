package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

// EstimateTokensSum sums block.EstimateTokens over each of texts, matching
// assembleRecall's own per-item accounting (recall.go) so a test can compute
// an exact budget boundary without duplicating or guessing at its formula.
func EstimateTokensSum(texts ...string) int {
	total := 0
	for _, s := range texts {
		total += block.EstimateTokens(s)
	}
	return total
}

// noteText inserts a kind=kind record with text through the shim and
// returns its id, failing the test on any error.
func noteText(t *testing.T, shim *Server, kind, text string) string {
	t.Helper()
	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(fmt.Sprintf(`{"kind":%q,"text":%q}`, kind, text)))
	if rerr != nil {
		t.Fatalf("CallTool(note, kind=%s): %v", kind, rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	return result.ID
}

func recall(t *testing.T, shim *Server, args json.RawMessage) RecallResult {
	t.Helper()
	raw, rerr := shim.CallTool(ToolRecall, args)
	if rerr != nil {
		t.Fatalf("CallTool(recall): %v", rerr)
	}
	var result RecallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal RecallResult: %v", err)
	}
	return result
}

func sessionOf(t *testing.T, shim *Server) string {
	t.Helper()
	raw, rerr := shim.CallTool(ToolStatus, nil)
	if rerr != nil {
		t.Fatalf("CallTool(status): %v", rerr)
	}
	var result StatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal StatusResult: %v", err)
	}
	return result.Session
}

func appendEvent(t *testing.T, st *store.Store, sessionID string) {
	t.Helper()
	if _, err := st.AppendEvent(store.Event{
		TS:        time.Now(),
		Kind:      "test.event",
		SessionID: sessionID,
		Source:    "test",
		Payload:   "{}",
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
}

// TestRecallReturnsHandoffThenDecisionsNewestFirst is DONE WHEN clause 1:
// recall with no anchor returns the project's latest handoff first, then
// its decision records newest first, each carrying its id and provenance
// tier.
func TestRecallReturnsHandoffThenDecisionsNewestFirst(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	decision1ID := noteText(t, shim, "decision", "chose approach A")
	decision2ID := noteText(t, shim, "decision", "chose approach B")
	handoffID := noteText(t, shim, "handoff", "state: ready for review")

	result := recall(t, shim, nil)

	if result.ProjectKey != "proj-key" {
		t.Errorf("ProjectKey = %q, want proj-key", result.ProjectKey)
	}
	if result.Handoff == nil {
		t.Fatal("Handoff is nil, want the latest handoff")
	}
	if result.Handoff.ID != handoffID {
		t.Errorf("Handoff.ID = %q, want %q", result.Handoff.ID, handoffID)
	}
	if result.Handoff.Text != "state: ready for review" {
		t.Errorf("Handoff.Text = %q, want %q", result.Handoff.Text, "state: ready for review")
	}
	if result.Handoff.Tier != string(store.TierAgentDeclared) {
		t.Errorf("Handoff.Tier = %q, want %q", result.Handoff.Tier, store.TierAgentDeclared)
	}

	if len(result.Decisions) != 2 {
		t.Fatalf("len(Decisions) = %d, want 2: %+v", len(result.Decisions), result.Decisions)
	}
	if result.Decisions[0].ID != decision2ID {
		t.Errorf("Decisions[0].ID = %q, want %q (the newest decision first)", result.Decisions[0].ID, decision2ID)
	}
	if result.Decisions[1].ID != decision1ID {
		t.Errorf("Decisions[1].ID = %q, want %q (the older decision second)", result.Decisions[1].ID, decision1ID)
	}
	for _, d := range result.Decisions {
		if d.Tier != string(store.TierAgentDeclared) {
			t.Errorf("decision %s tier = %q, want %q", d.ID, d.Tier, store.TierAgentDeclared)
		}
	}
}

// TestRecallExcludesOtherProjectRecords is DONE WHEN clause 1's isolation
// requirement: a record from a different project seeded in the same store
// does not appear in another project's recall.
func TestRecallExcludesOtherProjectRecords(t *testing.T) {
	st := mustOpenStore(t)
	sockPathA := testDaemon(t, st, "claude", "/home/brian/proj-a", "proj-a")
	sockPathB := testDaemon(t, st, "claude", "/home/brian/proj-b", "proj-b")
	shimA := dialShim(t, sockPathA)
	shimB := dialShim(t, sockPathB)

	noteText(t, shimA, "handoff", "A's handoff")
	noteText(t, shimA, "decision", "A's decision")
	noteText(t, shimB, "handoff", "B's private handoff")
	noteText(t, shimB, "decision", "B's private decision")

	resultA := recall(t, shimA, nil)
	if resultA.ProjectKey != "proj-a" {
		t.Errorf("ProjectKey = %q, want proj-a", resultA.ProjectKey)
	}
	if resultA.Handoff == nil || resultA.Handoff.Text != "A's handoff" {
		t.Fatalf("Handoff = %+v, want A's own handoff", resultA.Handoff)
	}
	if len(resultA.Decisions) != 1 || resultA.Decisions[0].Text != "A's decision" {
		t.Fatalf("Decisions = %+v, want exactly A's own decision", resultA.Decisions)
	}

	raw, err := json.Marshal(resultA)
	if err != nil {
		t.Fatalf("marshal resultA: %v", err)
	}
	if strings.Contains(string(raw), "B's private") {
		t.Errorf("recall for proj-a leaked B's project content: %s", raw)
	}
}

// TestRecallOnEmptyProjectReturnsHonestEmptyResult is DONE WHEN clause 3: a
// project with no records and no events returns an honest empty result
// naming the project, not an error and not not-implemented.
func TestRecallOnEmptyProjectReturnsHonestEmptyResult(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/empty-proj", "empty-proj-key")
	shim := dialShim(t, sockPath)

	result := recall(t, shim, nil)

	if result.ProjectKey != "empty-proj-key" {
		t.Errorf("ProjectKey = %q, want empty-proj-key", result.ProjectKey)
	}
	if result.Handoff != nil {
		t.Errorf("Handoff = %+v, want nil", result.Handoff)
	}
	if len(result.Decisions) != 0 {
		t.Errorf("Decisions = %+v, want empty", result.Decisions)
	}
	if result.Timeline != "" {
		t.Errorf("Timeline = %q, want empty", result.Timeline)
	}
}

// TestRecallBudgetCutsTimelineBeforeAnyDecision is DONE WHEN clause 2's cut
// order: when cutting the timeline summary alone already fits the budget,
// no decision is cut, even though the timeline summary is textually shorter
// than a decision might be — timeline is cut first purely by priority, not
// by size.
func TestRecallBudgetCutsTimelineBeforeAnyDecision(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	noteText(t, shim, "handoff", "resume: keep going")
	sessionID := sessionOf(t, shim)
	appendEvent(t, st, sessionID)
	noteText(t, shim, "decision", "decision one, the oldest")
	noteText(t, shim, "decision", "decision two, the middle")
	noteText(t, shim, "decision", "decision three, the newest")

	full := recall(t, shim, nil)
	if full.Timeline == "" {
		t.Fatal("full recall's Timeline is empty; strengthen the fixture (need a seeded event)")
	}
	if len(full.Decisions) != 3 {
		t.Fatalf("full recall has %d decisions, want 3", len(full.Decisions))
	}

	budget := EstimateTokensSum(full.Handoff.Text, full.Decisions[0].Text, full.Decisions[1].Text, full.Decisions[2].Text)

	args, err := json.Marshal(RecallParams{BudgetTokens: budget})
	if err != nil {
		t.Fatalf("marshal RecallParams: %v", err)
	}
	trimmed := recall(t, shim, args)

	if trimmed.Timeline != "" {
		t.Errorf("Timeline = %q, want cut (budget only fits handoff + all 3 decisions)", trimmed.Timeline)
	}
	if len(trimmed.Decisions) != 3 {
		t.Errorf("Decisions = %+v, want all 3 to survive (cutting the timeline alone was enough)", trimmed.Decisions)
	}
	if trimmed.Handoff == nil {
		t.Error("Handoff is nil, want it to survive")
	}
}

// TestRecallBudgetCutsOldestDecisionsAfterTimelineHandoffSurvives is DONE
// WHEN clause 2's main path: a budget smaller than the seeded content
// truncates from the end — timeline first, then the oldest decisions — and
// the handoff always survives.
func TestRecallBudgetCutsOldestDecisionsAfterTimelineHandoffSurvives(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	noteText(t, shim, "handoff", "resume: keep going")
	sessionID := sessionOf(t, shim)
	appendEvent(t, st, sessionID)
	noteText(t, shim, "decision", "decision one, the oldest")
	noteText(t, shim, "decision", "decision two, the middle")
	noteText(t, shim, "decision", "decision three, the newest")

	full := recall(t, shim, nil)
	if full.Timeline == "" {
		t.Fatal("full recall's Timeline is empty; strengthen the fixture (need a seeded event)")
	}
	if len(full.Decisions) != 3 {
		t.Fatalf("full recall has %d decisions, want 3", len(full.Decisions))
	}

	// Just enough for the handoff plus the two newest decisions: timeline is
	// cut (priority 1), then the single oldest decision is cut (priority 2)
	// to fit; the two newest decisions and the handoff survive.
	budget := EstimateTokensSum(full.Handoff.Text, full.Decisions[0].Text, full.Decisions[1].Text)

	args, err := json.Marshal(RecallParams{BudgetTokens: budget})
	if err != nil {
		t.Fatalf("marshal RecallParams: %v", err)
	}
	trimmed := recall(t, shim, args)

	if trimmed.Timeline != "" {
		t.Errorf("Timeline = %q, want cut", trimmed.Timeline)
	}
	if trimmed.Handoff == nil || trimmed.Handoff.Text != "resume: keep going" {
		t.Fatalf("Handoff = %+v, want it to survive unchanged", trimmed.Handoff)
	}
	if len(trimmed.Decisions) != 2 {
		t.Fatalf("Decisions = %+v, want exactly the 2 newest to survive", trimmed.Decisions)
	}
	for _, d := range trimmed.Decisions {
		if d.Text == "decision one, the oldest" {
			t.Errorf("the oldest decision survived a cut that should have dropped it first: %+v", trimmed.Decisions)
		}
	}
}

// TestRecallHandoffSurvivesEvenUnderAnImpossiblyTightBudget is DONE WHEN
// clause 2's floor: the handoff is never dropped, no matter how small the
// budget.
func TestRecallHandoffSurvivesEvenUnderAnImpossiblyTightBudget(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	noteText(t, shim, "handoff", "resume: this must survive no matter what")
	sessionID := sessionOf(t, shim)
	appendEvent(t, st, sessionID)
	noteText(t, shim, "decision", "a decision that should be cut")

	args, err := json.Marshal(RecallParams{BudgetTokens: 1})
	if err != nil {
		t.Fatalf("marshal RecallParams: %v", err)
	}
	trimmed := recall(t, shim, args)

	if trimmed.Handoff == nil || trimmed.Handoff.Text != "resume: this must survive no matter what" {
		t.Fatalf("Handoff = %+v, want it to survive intact even under a 1-token budget", trimmed.Handoff)
	}
	if trimmed.Timeline != "" {
		t.Errorf("Timeline = %q, want cut under a 1-token budget", trimmed.Timeline)
	}
	if len(trimmed.Decisions) != 0 {
		t.Errorf("Decisions = %+v, want all cut under a 1-token budget", trimmed.Decisions)
	}
}
