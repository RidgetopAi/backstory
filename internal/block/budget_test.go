package block_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

// overflowingScenario builds a store whose four data slots, combined,
// clearly exceed a 200-token (~800 char) budget, but whose slot 1 (resume)
// plus the fixed final line stay comfortably under it on their own — so a
// 200 token budget is satisfiable by cutting slot 2 (delta) and slot 4
// (attention) alone, leaving slot 3 (coordination) and slot 1 to survive.
// Coordination carries the bulk of the excess (many other live sessions
// with long cwds) since it is the one that must NOT need cutting in the
// punch's clause-3 scenario.
func overflowingScenario(t *testing.T) (*store.Store, string) {
	t.Helper()
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	handoff := mustInsertHandoff(t, s, self, "resume: "+strings.Repeat("word ", 20))

	for i := 0; i < 8; i++ {
		pid := 200 + i
		cwd := "/workspace/very-long-project-path-for-testing-budget-overflow-" + itoa(i)
		other := mustStartSession(t, s, "codex", cwd, pid)
		exit := i % 2
		mustAppendEvent(t, s, other, handoff.TS.Add(time.Duration(i+1)*time.Second),
			map[string]any{"path": "file" + itoa(i) + ".go", "exit": exit})
	}

	mustInsertDraft(t, s, self, "", nil)

	return s, self
}

// overflowProcFS reports every pid overflowingScenario started (200..207)
// as alive.
func overflowProcFS() fakeProcFS {
	alive := map[int]bool{}
	for pid := 200; pid < 208; pid++ {
		alive[pid] = true
	}
	return fakeProcFS{alive: alive}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// TestBudgetCutsDeltaBeforeAttentionBeforeCoordination is the punch's DONE
// WHEN clause 3: at a 200 token budget with overflowing content, the output
// measures <= 200 by block.EstimateTokens, slot 2 (delta) and slot 4
// (attention) are cut but slot 3 (coordination) is not, and slots 1 (resume)
// and 5 (the final line) survive.
//
// Mutation probe (assemble's `if EstimateTokens(join()) <= budgetTokens {
// return join() }` short-circuited to an unconditional `return join()`,
// budget.go): "budget_test.go:98: EstimateTokens(out) = 220, want <= 200"
// (this test) -- restoring the budget check turns it back GREEN.
func TestBudgetCutsDeltaBeforeAttentionBeforeCoordination(t *testing.T) {
	s, self := overflowingScenario(t)

	out, err := block.Render(block.Params{
		Store: s, ProcFS: overflowProcFS(), ProjectKey: testProjectKey,
		SessionID: self, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if block.EstimateTokens(out) <= budgetTokens {
		t.Fatalf("fixture does not overflow a %d token budget before the setting is applied (got %d tokens); strengthen the fixture",
			budgetTokens, block.EstimateTokens(out))
	}

	if err := s.SetSetting(block.SettingBudgetKey, "200"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	out, err = block.Render(block.Params{
		Store: s, ProcFS: overflowProcFS(), ProjectKey: testProjectKey,
		SessionID: self, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if got := block.EstimateTokens(out); got > 200 {
		t.Fatalf("EstimateTokens(out) = %d, want <= 200; got:\n%s", got, out)
	}
	if strings.Contains(out, "Delta:") {
		t.Errorf("slot 2 (delta) was not cut; got:\n%s", out)
	}
	if strings.Contains(out, "Attention:") {
		t.Errorf("slot 4 (attention) was not cut; got:\n%s", out)
	}
	if !strings.Contains(out, "Coordination:") {
		t.Errorf("slot 3 (coordination) was cut but should have survived (only slots 2 and 4 were needed); got:\n%s", out)
	}
	if !strings.Contains(out, "Resume:") {
		t.Errorf("slot 1 (resume) was cut but should survive; got:\n%s", out)
	}
	if !strings.Contains(out, block.FinalLine) {
		t.Errorf("slot 5 (final line) is missing but must always survive; got:\n%s", out)
	}
}

const budgetTokens = 200

// orderPinScenario builds a fixture for the cut-order pinning test: one
// other live session (a small, constant slot 3), optionally events for
// slot 2 (delta) and optionally a draft for slot 4 (attention). Render is
// deterministic given these flags, so calling it with each flag toggled off
// in turn reconstructs exactly what assemble would produce after cutting
// that one slot — without hardcoding any of its formatting.
func orderPinScenario(t *testing.T, includeDelta, includeAttention bool) string {
	t.Helper()
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	mustStartSession(t, s, "codex", "/proj2", 200)

	handoff := mustInsertHandoff(t, s, self, "keep going")
	if includeDelta {
		mustAppendEvent(t, s, self, handoff.TS.Add(time.Second), map[string]any{"path": "a.go", "exit": 0})
		mustAppendEvent(t, s, self, handoff.TS.Add(2*time.Second), map[string]any{"path": "b.go", "exit": 1})
	}
	if includeAttention {
		mustInsertDraft(t, s, self, "", nil)
	}

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{alive: map[int]bool{200: true}}, ProjectKey: testProjectKey,
		SessionID: self, Harness: "claude", Now: handoff.TS.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

// TestBudgetCutOrderPinnedWhenOnlyDeltaAloneMustBeCut is the punch's DONE
// WHEN clause 3's cut-order pin: a fixture that overflows by only slot 2's
// (delta's) worth — cutting delta alone fits the budget, but cutting
// attention alone does not — must end up with delta cut and attention
// intact. Swapping the cut order (budget.go's `[]int{1, 3, 2}` to try
// attention before delta) makes this go RED: attention would also get cut
// even though it alone never needed to be.
//
// Mutation probe: swapping assemble's cut-order slice from
// []int{1, 3, 2} to []int{3, 1, 2} (budget.go) turns this test RED
// (attention gets cut too); restoring the original order turns it back
// GREEN.
func TestBudgetCutOrderPinnedWhenOnlyDeltaAloneMustBeCut(t *testing.T) {
	full := orderPinScenario(t, true, true)
	noDelta := orderPinScenario(t, false, true)
	noAttention := orderPinScenario(t, true, false)

	budget := block.EstimateTokens(noDelta)

	if block.EstimateTokens(full) <= budget {
		t.Fatalf("fixture does not overflow a %d token budget before the setting is applied; strengthen the fixture", budget)
	}
	if block.EstimateTokens(noAttention) <= budget {
		t.Fatalf("cutting attention alone already fits a %d token budget; fixture must require cutting delta specifically, not attention", budget)
	}

	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	mustStartSession(t, s, "codex", "/proj2", 200)
	handoff := mustInsertHandoff(t, s, self, "keep going")
	mustAppendEvent(t, s, self, handoff.TS.Add(time.Second), map[string]any{"path": "a.go", "exit": 0})
	mustAppendEvent(t, s, self, handoff.TS.Add(2*time.Second), map[string]any{"path": "b.go", "exit": 1})
	mustInsertDraft(t, s, self, "", nil)

	if err := s.SetSetting(block.SettingBudgetKey, strconv.Itoa(budget)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{alive: map[int]bool{200: true}}, ProjectKey: testProjectKey,
		SessionID: self, Harness: "claude", Now: handoff.TS.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if got := block.EstimateTokens(out); got > budget {
		t.Fatalf("EstimateTokens(out) = %d, want <= %d; got:\n%s", got, budget, out)
	}
	if strings.Contains(out, "Delta:") {
		t.Errorf("slot 2 (delta) was not cut; got:\n%s", out)
	}
	if !strings.Contains(out, "Attention:") {
		t.Errorf("slot 4 (attention) was cut but should have survived (cutting delta alone was enough); got:\n%s", out)
	}
	if !strings.Contains(out, "Coordination:") {
		t.Errorf("slot 3 (coordination) was cut but should survive; got:\n%s", out)
	}
	if !strings.Contains(out, "Resume:") {
		t.Errorf("slot 1 (resume) was cut but should survive; got:\n%s", out)
	}
	if !strings.Contains(out, block.FinalLine) {
		t.Errorf("slot 5 (final line) is missing but must always survive; got:\n%s", out)
	}
}

// TestBudgetTruncatesAResumeSlotThatAloneExceedsTheBudget is the punch's
// DONE WHEN clause 3's slot-1 truncation requirement: with every other slot
// empty, a resume slot long enough to alone exceed the budget is truncated
// to fit, never dropped and never left over budget.
func TestBudgetTruncatesAResumeSlotThatAloneExceedsTheBudget(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	longResume := strings.Repeat("word ", 400)
	mustInsertHandoff(t, s, self, longResume)

	const smallBudget = 50 // ~200 chars: far smaller than the resume text alone
	if err := s.SetSetting(block.SettingBudgetKey, strconv.Itoa(smallBudget)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if got := block.EstimateTokens(out); got > smallBudget {
		t.Fatalf("EstimateTokens(out) = %d, want <= %d (resume must be truncated to fit); got:\n%s", got, smallBudget, out)
	}
	if !strings.Contains(out, "Resume:") {
		t.Errorf("slot 1 (resume) was dropped entirely rather than truncated; got:\n%s", out)
	}
	if strings.Contains(out, longResume) {
		t.Errorf("slot 1 (resume) was not truncated at all; got:\n%s", out)
	}
	if !strings.Contains(out, block.FinalLine) {
		t.Errorf("slot 5 (final line) is missing but must always survive; got:\n%s", out)
	}
}

// TestBudgetSmallerThanFinalLineReturnsExactlyTheFinalLine is the punch's
// DONE WHEN clause 3's degenerate case: a budget too small even for the
// final line alone still completes (no panic, no infinite loop) and
// returns exactly the final line once every cuttable slot has been cut and
// slot 1 was already empty.
func TestBudgetSmallerThanFinalLineReturnsExactlyTheFinalLine(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	mustStartSession(t, s, "codex", "/proj2", 200) // gives slot 3 content so Render doesn't short-circuit to EmptyProjectLine

	const tinyBudget = 1
	if err := s.SetSetting(block.SettingBudgetKey, strconv.Itoa(tinyBudget)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{alive: map[int]bool{200: true}}, ProjectKey: testProjectKey,
		SessionID: self, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != block.FinalLine {
		t.Fatalf("Render with a %d token budget = %q, want exactly %q", tinyBudget, out, block.FinalLine)
	}
}

// TestBudgetPerHarnessOverrideWinsOverGlobal is the punch's DONE WHEN
// clause 3's per-harness override requirement.
func TestBudgetPerHarnessOverrideWinsOverGlobal(t *testing.T) {
	s, self := overflowingScenario(t)

	if err := s.SetSetting(block.SettingBudgetKey, "1500"); err != nil {
		t.Fatalf("SetSetting global: %v", err)
	}
	if err := s.SetSetting(block.SettingBudgetKey+".claude", "200"); err != nil {
		t.Fatalf("SetSetting per-harness: %v", err)
	}

	outClaude, err := block.Render(block.Params{
		Store: s, ProcFS: overflowProcFS(), ProjectKey: testProjectKey,
		SessionID: self, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render(claude): %v", err)
	}
	if got := block.EstimateTokens(outClaude); got > 200 {
		t.Fatalf("Render(claude) = %d tokens, want <= 200 (the per-harness override must win over the 1500 global): got:\n%s",
			got, outClaude)
	}

	outCodex, err := block.Render(block.Params{
		Store: s, ProcFS: overflowProcFS(), ProjectKey: testProjectKey,
		SessionID: self, Harness: "codex",
	})
	if err != nil {
		t.Fatalf("Render(codex): %v", err)
	}
	if got := block.EstimateTokens(outCodex); got <= 200 {
		t.Fatalf("Render(codex) = %d tokens, want > 200 (no per-harness override for codex, so the 1500 global applies and nothing should be cut)",
			got)
	}
}
