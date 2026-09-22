package block_test

import (
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
