package block_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

func mustInsertHandoffWithNext(t *testing.T, s *store.Store, sessionID, text, next string) store.Record {
	t.Helper()
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: text,
		SessionID: sessionID, ProjectKey: testProjectKey, Next: next,
	}); err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}
	rec, ok, err := s.LatestRecord(testProjectKey, store.KindHandoff)
	if err != nil || !ok {
		t.Fatalf("LatestRecord: ok=%v err=%v", ok, err)
	}
	return rec
}

func renderFor(t *testing.T, s *store.Store, self string) string {
	t.Helper()
	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

// Task 91860fb0 clause 1: the handoff's next step renders on its own line
// right after the Resume line; a handoff with no next renders no Next line.
func TestRenderResumeSlotShowsNextImmediatelyAfterResume(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	h := mustInsertHandoffWithNext(t, s, self, "did the first part", "NEXTMARK add the thing")

	out := renderFor(t, s, self)
	want := "Resume: (id " + h.ID + ") did the first part\nNext: NEXTMARK add the thing"
	if !strings.Contains(out, want) {
		t.Fatalf("want Next line immediately after Resume line %q; got:\n%s", want, out)
	}
}

func TestRenderResumeSlotOmitsNextWhenHandoffHasNone(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	mustInsertHandoffWithNext(t, s, self, "did the first part", "")

	if out := renderFor(t, s, self); strings.Contains(out, "Next:") {
		t.Fatalf("handoff with no next must render no Next line; got:\n%s", out)
	}
}

// Clause 3: at a budget that fits slot 1 but nothing after it, both the
// Resume line and the Next line survive.
func TestBudgetKeepsNextWithResumeWhenOnlySlotOneFits(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	h := mustInsertHandoffWithNext(t, s, self, "did the first part", "NEXTMARK add the thing")
	mustInsertDraft(t, s, self, "", nil) // slot 4 present, so something must be cut
	other := mustStartSession(t, s, "codex", "/elsewhere", 200)
	mustAppendToolUse(t, s, other, h.TS.Add(1), "f.go") // slot 2 present

	slot1 := "Resume: (id " + h.ID + ") did the first part\nNext: NEXTMARK add the thing"
	budget := block.EstimateTokens(block.HeaderLine + "\n\n" + slot1 + "\n\n" + block.FinalLineFor(h.ID))
	if err := s.SetSetting(block.SettingBudgetKey, strconv.Itoa(budget)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	out := renderFor(t, s, self)
	if !strings.Contains(out, slot1) {
		t.Fatalf("Resume and Next lines must both survive; got:\n%s", out)
	}
	if strings.Contains(out, "Delta:") || strings.Contains(out, "Attention:") {
		t.Fatalf("test fixture bug: later slots were not cut; got:\n%s", out)
	}
}

// Clause 4: a real MODE line with trailing words still renders.
func TestRenderResumeSlotShowsModeLineWithTrailingWords(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	mustInsertHandoffWithNext(t, s, self, "status\nMODE: debug/verify. extra words\nmore", "")

	out := renderFor(t, s, self)
	if !strings.Contains(out, "\nMODE: debug/verify\n") && !strings.HasSuffix(strings.Split(out, "\n\n")[1], "MODE: debug/verify") {
		t.Fatalf("want a MODE line for %q; got:\n%s", "MODE: debug/verify. extra words", out)
	}
}
