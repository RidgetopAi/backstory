package block_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// testProjectKey is the project every test in this package uses: nothing
// here exercises cross-project scoping (that lives in internal/store's own
// tests), so one fixed key keeps every fixture builder simple.
const testProjectKey = "proj-a"

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustUpsertProject(t *testing.T, s *store.Store, key string) {
	t.Helper()
	if err := s.UpsertProject(store.Project{Key: key, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

func mustStartSession(t *testing.T, s *store.Store, agent, cwd string, pid int) string {
	t.Helper()
	id, err := s.StartSession(store.StartSessionParams{
		Agent: agent, CWD: cwd, ProjectKey: testProjectKey, PID: &pid, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return id
}

// fakeProcFS reports exactly the pids in alive as live processes; every
// other pid (including one with no entry at all) is dead — the same shape
// production /proc reports for a pid that has exited.
//
// An alive pid's comm is comm[pid] when set, else defaultComm: the "other
// live session" every coordination test starts is a codex.
type fakeProcFS struct {
	alive map[int]bool
	comm  map[int]string
}

const defaultComm = "codex"

func (f fakeProcFS) Status(pid int) (ident.Status, error) {
	if f.alive[pid] {
		if name, ok := f.comm[pid]; ok {
			return ident.Status{Name: name}, nil
		}
		return ident.Status{Name: defaultComm}, nil
	}
	return ident.Status{}, fmt.Errorf("fakeProcFS: pid %d not alive", pid)
}
func (f fakeProcFS) Cwd(int) (string, error)       { return "", fmt.Errorf("fakeProcFS: no cwd") }
func (f fakeProcFS) Cmdline(int) ([]string, error) { return nil, nil }

func mustInsertHandoff(t *testing.T, s *store.Store, sessionID, text string) store.Record {
	t.Helper()
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: text,
		SessionID: sessionID, ProjectKey: testProjectKey,
	}); err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}
	rec, ok, err := s.LatestRecord(testProjectKey, store.KindHandoff)
	if err != nil || !ok {
		t.Fatalf("LatestRecord after insert handoff: ok=%v err=%v", ok, err)
	}
	return rec
}

func mustAppendEvent(t *testing.T, s *store.Store, sessionID, kind string, ts time.Time, pl any) int64 {
	t.Helper()
	b, err := json.Marshal(pl)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	id, err := s.AppendEvent(store.Event{TS: ts, Kind: kind, SessionID: sessionID, Source: "shell", Payload: string(b)})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	return id
}

// mustAppendToolUse appends a tool.use event for a mutating tool (Edit) —
// the shape the delta slot's file-touched count reads.
func mustAppendToolUse(t *testing.T, s *store.Store, sessionID string, ts time.Time, path string) {
	t.Helper()
	mustAppendToolUseNamed(t, s, sessionID, ts, "Edit", path)
}

// mustAppendToolUseNamed appends a tool.use event with an explicit tool
// name, for tests that need to distinguish a mutating tool (Edit, Write,
// MultiEdit, NotebookEdit) from a non-mutating one (Read) — only the
// former counts toward the delta slot's "files touched" figure.
func mustAppendToolUseNamed(t *testing.T, s *store.Store, sessionID string, ts time.Time, name, path string) {
	t.Helper()
	mustAppendEvent(t, s, sessionID, payload.KindToolUse, ts, payload.ToolUse{Name: name, Path: path})
}

// mustAppendToolResult appends a tool.result event carrying an exit code —
// the shape the delta slot's "last exit codes" reads.
func mustAppendToolResult(t *testing.T, s *store.Store, sessionID string, ts time.Time, exit int) {
	t.Helper()
	mustAppendEvent(t, s, sessionID, payload.KindToolResult, ts, payload.ToolResult{Exit: &exit})
}

func mustInsertNote(t *testing.T, s *store.Store, sessionID, text string) string {
	t.Helper()
	id, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindNote, Text: text,
		SessionID: sessionID, ProjectKey: testProjectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord note: %v", err)
	}
	return id
}

func mustInsertDraft(t *testing.T, s *store.Store, sessionID string, promoter string, expiresAt *time.Time) {
	t.Helper()
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityInference}, Kind: store.KindNote, Text: "an inferred draft",
		SessionID: sessionID, ProjectKey: testProjectKey, Promoter: promoter, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("InsertRecord draft: %v", err)
	}
}

// TestRenderAllFiveSlotsPopulatedInOrderWithModeLine is clause (a): every
// slot has content, they appear in fixed order, and the handoff's MODE line
// is surfaced.
func TestRenderAllFiveSlotsPopulatedInOrderWithModeLine(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)

	self := mustStartSession(t, s, "claude", "/proj", 100)
	other := mustStartSession(t, s, "codex", "/proj-worktree", 200)

	handoff := mustInsertHandoff(t, s, self, "shipped the delta slot\nMODE: review")

	mustAppendToolUse(t, s, other, handoff.TS.Add(time.Minute), "main.go")
	mustAppendToolResult(t, s, other, handoff.TS.Add(time.Minute), 1)

	mustInsertDraft(t, s, self, "", nil)
	target := mustInsertNote(t, s, self, "a claim")
	evidence := mustInsertNote(t, s, self, "contradicting evidence")
	if err := s.LinkEdge(evidence, target, store.EdgeContradicts, self); err != nil {
		t.Fatalf("LinkEdge: %v", err)
	}

	procfs := fakeProcFS{alive: map[int]bool{200: true}}
	out, err := block.Render(block.Params{
		Store: s, ProcFS: procfs, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
		Now: handoff.TS.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	idxHeader := strings.Index(out, block.HeaderLine)
	idxResume := strings.Index(out, "Resume:")
	idxMode := strings.Index(out, "MODE: review")
	idxDelta := strings.Index(out, "Delta:")
	idxCoord := strings.Index(out, "Coordination:")
	idxAttention := strings.Index(out, "Attention:")
	idxFinal := strings.Index(out, block.FinalLine)

	for name, idx := range map[string]int{
		"header": idxHeader, "Resume": idxResume, "MODE": idxMode, "Delta": idxDelta,
		"Coordination": idxCoord, "Attention": idxAttention, "final line": idxFinal,
	} {
		if idx < 0 {
			t.Fatalf("output missing %s; got:\n%s", name, out)
		}
	}
	if idxHeader != 0 {
		t.Fatalf("header is not the first line; got:\n%s", out)
	}
	inOrder := idxHeader < idxResume && idxResume < idxMode && idxMode < idxDelta && idxDelta < idxCoord &&
		idxCoord < idxAttention && idxAttention < idxFinal
	if !inOrder {
		t.Fatalf("slots out of order; got:\n%s", out)
	}
	if !strings.Contains(out, "Delta: 1 sessions, 1 files touched") || !strings.Contains(out, "Last failure:") {
		t.Errorf("delta slot missing expected counts; got:\n%s", out)
	}
	if !strings.Contains(out, "1 unconfirmed draft(s), 1 contradiction(s)") {
		t.Errorf("attention slot missing expected counts; got:\n%s", out)
	}
	if !strings.Contains(out, "- codex in /proj-worktree") {
		t.Errorf("coordination slot missing the other live session; got:\n%s", out)
	}
}

// TestRenderResumeSlotCarriesTheHandoffRecordID is the punch's DONE WHEN
// clause 1 (task 56317fe7, decision 1e53165a): the Resume slot names the
// stored handoff's own record id — read back from the store independently
// of Render, so the test cannot pass by coincidence — in a fixed "(id ...)"
// position right after the "Resume:" label, so an agent can lift it
// verbatim into note's supersedes.
//
// Mutation probe: drop rec.ID from resumeSlot's line (block.go) -> RED (out
// no longer contains handoff.ID); restore -> GREEN.
func TestRenderResumeSlotCarriesTheHandoffRecordID(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	handoff := mustInsertHandoff(t, s, self, "shipped the resume id")

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if handoff.ID == "" {
		t.Fatalf("test fixture bug: stored handoff has no id")
	}
	wantLine := "Resume: (id " + handoff.ID + ") shipped the resume id"
	if !strings.Contains(out, wantLine) {
		t.Fatalf("Resume slot does not carry the handoff's record id %q; got:\n%s", handoff.ID, out)
	}
}

// TestRenderEachSlotIndependentlyOmittedWhenEmpty is clause (b): with three
// of the four data slots populated and one deliberately empty, the empty
// one's label is absent and the other three remain, for all four slots in
// turn.
func TestRenderEachSlotIndependentlyOmittedWhenEmpty(t *testing.T) {
	t.Run("resume omitted", func(t *testing.T) {
		s := newTestStore(t)
		mustUpsertProject(t, s, testProjectKey)
		self := mustStartSession(t, s, "claude", "/proj", 100)
		other := mustStartSession(t, s, "codex", "/proj2", 200)
		mustAppendToolUse(t, s, other, time.Now(), "a.go")
		mustInsertDraft(t, s, self, "", nil)

		out := mustRender(t, s, fakeProcFS{alive: map[int]bool{200: true}}, self)
		assertAbsent(t, out, "Resume:")
		assertPresent(t, out, "Delta:", "Coordination:", "Attention:", block.FinalLine)
	})

	t.Run("delta omitted", func(t *testing.T) {
		s := newTestStore(t)
		mustUpsertProject(t, s, testProjectKey)
		self := mustStartSession(t, s, "claude", "/proj", 100)
		mustStartSession(t, s, "codex", "/proj2", 200)
		mustInsertHandoff(t, s, self, "no more events after this")
		mustInsertDraft(t, s, self, "", nil)

		out := mustRender(t, s, fakeProcFS{alive: map[int]bool{200: true}}, self)
		assertAbsent(t, out, "Delta:")
		assertPresent(t, out, "Resume:", "Coordination:", "Attention:", block.FinalLine)
	})

	t.Run("coordination omitted", func(t *testing.T) {
		s := newTestStore(t)
		mustUpsertProject(t, s, testProjectKey)
		self := mustStartSession(t, s, "claude", "/proj", 100)
		handoff := mustInsertHandoff(t, s, self, "no other live sessions")
		mustAppendToolUse(t, s, self, handoff.TS.Add(time.Minute), "a.go")
		mustInsertDraft(t, s, self, "", nil)

		out := mustRender(t, s, fakeProcFS{}, self)
		assertAbsent(t, out, "Coordination:")
		assertPresent(t, out, "Resume:", "Delta:", "Attention:", block.FinalLine)
	})

	t.Run("attention omitted", func(t *testing.T) {
		s := newTestStore(t)
		mustUpsertProject(t, s, testProjectKey)
		self := mustStartSession(t, s, "claude", "/proj", 100)
		mustStartSession(t, s, "codex", "/proj2", 200)
		handoff := mustInsertHandoff(t, s, self, "no drafts, no contradictions")
		mustAppendToolUse(t, s, self, handoff.TS.Add(time.Minute), "a.go")

		out := mustRender(t, s, fakeProcFS{alive: map[int]bool{200: true}}, self)
		assertAbsent(t, out, "Attention:")
		assertPresent(t, out, "Resume:", "Delta:", "Coordination:", block.FinalLine)
	})
}

// TestRenderDeltaCountsOnlyEventsAfterHandoff is clause (c): an event before
// the handoff never contributes to the delta's counts.
func TestRenderDeltaCountsOnlyEventsAfterHandoff(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	before := time.Now()
	mustAppendToolUse(t, s, self, before, "before.go")
	mustAppendToolResult(t, s, self, before, 9)

	handoff := mustInsertHandoff(t, s, self, "cut here")

	mustAppendToolUse(t, s, self, handoff.TS.Add(time.Minute), "after.go")
	mustAppendToolResult(t, s, self, handoff.TS.Add(time.Minute), 0)

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
		Now: handoff.TS.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "before.go") || strings.Contains(out, "exit 9") {
		t.Errorf("delta counted an event before the handoff; got:\n%s", out)
	}
	if !strings.Contains(out, "Delta: 1 files touched") || strings.Contains(out, "Last failure") {
		t.Errorf("delta did not count the after-handoff event correctly; got:\n%s", out)
	}
}

// TestRenderDeltaFilesTouchedCountsOnlyMutatingTools is the punch's clause
// 1 (task 393d174c, decision 1e53165a): a Read populates ToolUse.Path just
// like an Edit does (it is real history), but "files touched" must count
// files CHANGED, not files opened. Three Read paths and two Edit paths
// must render "2 files touched", never 5 — on Brian's real history the
// undiscriminating count was 411 distinct paths against 105 actually
// changed, a 3.9x overstatement of the block's headline number.
func TestRenderDeltaFilesTouchedCountsOnlyMutatingTools(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	handoff := mustInsertHandoff(t, s, self, "cut here")

	ts := handoff.TS.Add(time.Minute)
	mustAppendToolUseNamed(t, s, self, ts, "Read", "read1.go")
	mustAppendToolUseNamed(t, s, self, ts, "Read", "read2.go")
	mustAppendToolUseNamed(t, s, self, ts, "Read", "read3.go")
	mustAppendToolUseNamed(t, s, self, ts, "Edit", "edit1.go")
	mustAppendToolUseNamed(t, s, self, ts, "Edit", "edit2.go")

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
		Now: handoff.TS.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Delta: 2 files touched") {
		t.Errorf("delta counted Read paths toward files touched; want \"2 files touched\" (edit1.go, edit2.go only); got:\n%s", out)
	}
}

// TestRenderDeltaCountsABackfilledEventAppendedAfterTheHandoffDespiteALyingEarlierTS
// is the critic's exact case (critic T1 on 7d3954f0, SCHEMA.md invariant
// 10): a handoff is inserted, then an event is appended AFTER it (so its
// timeline_events.id is higher — it landed later in real sequence) but
// carrying a ts EARLIER than the handoff's ts, the way a backfilled
// transcript's file clock lies. The delta's membership test must be the
// handoff's event cursor (its position in the sequence at insert time), not
// a wall-clock comparison — a ts-based boundary drops this row entirely.
//
// Mutation probe: reinstating the ts comparison this test replaced
// (`if hasHandoff && e.TS.Before(handoff.TS) { continue }`, formerly in
// deltaSlot, block.go) turns this test RED (the Delta slot goes missing);
// removing it again turns it back GREEN.
func TestRenderDeltaCountsABackfilledEventAppendedAfterTheHandoffDespiteALyingEarlierTS(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	handoff := mustInsertHandoff(t, s, self, "resume here")

	backfilledTS := handoff.TS.Add(-time.Hour) // lying: earlier than the handoff, but appended after it
	mustAppendToolUse(t, s, self, backfilledTS, "backfilled.go")
	mustAppendToolResult(t, s, self, backfilledTS, 7)

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
		Now: handoff.TS.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Delta:") {
		t.Fatalf("delta slot missing entirely; a backfilled event appended after the handoff with a lying earlier ts must still be counted; got:\n%s", out)
	}
	if !strings.Contains(out, "Delta: 1 files touched") || !strings.Contains(out, "Last failure: (command not recorded) exit 7") {
		t.Errorf("delta slot did not count the backfilled event appended after the handoff; got:\n%s", out)
	}
}

// TestRenderCoordinationListsLiveOmitsEndedAndDeadPID is clause (d).
func TestRenderCoordinationListsLiveOmitsEndedAndDeadPID(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)

	mustStartSession(t, s, "codex", "/proj-live", 200)

	endedOther := mustStartSession(t, s, "codex", "/proj-ended", 201)
	if err := s.EndSession(endedOther, time.Now(), "exit"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	mustStartSession(t, s, "codex", "/proj-dead-pid", 202) // pid never in fakeProcFS.alive

	procfs := fakeProcFS{alive: map[int]bool{200: true}} // 201 ended (irrelevant), 202 dead
	out := mustRender(t, s, procfs, self)

	if !strings.Contains(out, "/proj-live") {
		t.Errorf("coordination missing the live session with an alive pid; got:\n%s", out)
	}
	if strings.Contains(out, "/proj-ended") {
		t.Errorf("coordination listed an ended session; got:\n%s", out)
	}
	if strings.Contains(out, "/proj-dead-pid") {
		t.Errorf("coordination listed a session whose pid is dead; got:\n%s", out)
	}
}

// TestRenderAttentionCountsDraftsAndContradictions is clause (e).
func TestRenderAttentionCountsDraftsAndContradictions(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	now := time.Now()

	mustInsertDraft(t, s, self, "", nil)              // counted
	mustInsertDraft(t, s, self, "some-promoter", nil) // promoted: excluded
	expired := now.Add(-time.Hour)
	mustInsertDraft(t, s, self, "", &expired) // expired: excluded

	targetA := mustInsertNote(t, s, self, "claim A")
	evidenceA := mustInsertNote(t, s, self, "evidence A")
	if err := s.LinkEdge(evidenceA, targetA, store.EdgeContradicts, self); err != nil {
		t.Fatalf("LinkEdge contradicts A: %v", err)
	}
	targetB := mustInsertNote(t, s, self, "claim B")
	supersederB := mustInsertNote(t, s, self, "superseding note")
	if err := s.LinkEdge(supersederB, targetB, store.EdgeSupersedes, self); err != nil {
		t.Fatalf("LinkEdge supersedes: %v", err)
	}

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude", Now: now,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Attention: 1 unconfirmed draft(s), 1 contradiction(s)") {
		t.Errorf("attention slot = wrong counts; got:\n%s", out)
	}
}

// TestRenderEmptyProjectYieldsExactlyTheEmptyStateConstant is clause (f),
// plus the punch's (task 6ae45e80, decision 1e53165a) DONE WHEN clause 1's
// empty-state half: the empty-state render opens with HeaderLine so an
// agent seeing only "no history yet" can still tell it came from Backstory
// and needs no recall call to re-fetch it.
func TestRenderEmptyProjectYieldsExactlyTheEmptyStateConstant(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, "proj-empty")

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: "proj-empty", SessionID: "self", Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := block.HeaderLine + "\n\n" + block.EmptyProjectLine
	if out != want {
		t.Fatalf("Render(empty project) = %q, want exactly %q", out, want)
	}
	if !strings.HasPrefix(out, block.HeaderLine) {
		t.Errorf("empty-state render does not open with HeaderLine; got:\n%s", out)
	}
	if !strings.Contains(out, "Backstory") {
		t.Errorf("empty-state header does not name Backstory as the source; got:\n%s", out)
	}
}

// TestRenderHeaderLineOpensBothThePopulatedAndEmptyStateBlock is the punch's
// (task 6ae45e80, decision 1e53165a) DONE WHEN clause 1, directly: Brian's
// desktop measurement showed an agent receiving "Delta: 50 sessions, 105
// files touched\n\nask backstory for more" could not tell the block came
// from Backstory and offered to re-fetch it with recall — AGENT-CONTRACT.md
// §The SessionStart block's "do not re-fetch it" rule was unobeyable because
// nothing named the source. The first line of every render, populated or
// empty-state, must name Backstory and say the block is already loaded so
// recall need not be called again.
func TestRenderHeaderLineOpensBothThePopulatedAndEmptyStateBlock(t *testing.T) {
	if !strings.Contains(block.HeaderLine, "Backstory") {
		t.Fatalf("HeaderLine %q does not contain the word Backstory", block.HeaderLine)
	}
	if !strings.Contains(strings.ToLower(block.HeaderLine), "already loaded") {
		t.Fatalf("HeaderLine %q does not say the block is already loaded", block.HeaderLine)
	}
	if !strings.Contains(strings.ToLower(block.HeaderLine), "recall") {
		t.Fatalf("HeaderLine %q does not mention recall need not be called to re-fetch it", block.HeaderLine)
	}
	if strings.Contains(block.HeaderLine, "\n") {
		t.Fatalf("HeaderLine %q is not one line", block.HeaderLine)
	}

	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	mustInsertHandoff(t, s, self, "populated case")

	populated, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render (populated): %v", err)
	}
	if firstLine := strings.SplitN(populated, "\n", 2)[0]; firstLine != block.HeaderLine {
		t.Errorf("populated block's first line = %q, want %q", firstLine, block.HeaderLine)
	}

	mustUpsertProject(t, s, "proj-empty-header")
	empty, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: "proj-empty-header", SessionID: "self", Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render (empty-state): %v", err)
	}
	if firstLine := strings.SplitN(empty, "\n", 2)[0]; firstLine != block.HeaderLine {
		t.Errorf("empty-state block's first line = %q, want %q", firstLine, block.HeaderLine)
	}
}

func mustRender(t *testing.T, s *store.Store, procfs ident.ProcFS, selfSessionID string) string {
	t.Helper()
	out, err := block.Render(block.Params{
		Store: s, ProcFS: procfs, ProjectKey: testProjectKey, SessionID: selfSessionID, Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func assertPresent(t *testing.T, out string, labels ...string) {
	t.Helper()
	for _, l := range labels {
		if !strings.Contains(out, l) {
			t.Errorf("output missing %q; got:\n%s", l, out)
		}
	}
}

func assertAbsent(t *testing.T, out string, label string) {
	t.Helper()
	if strings.Contains(out, label) {
		t.Errorf("output unexpectedly contains %q; got:\n%s", label, out)
	}
}

// task b4829f9a: the Resume line names the harness that wrote the handoff
// when it differs from the reader, after the fixed "(id ...)" part; an
// unknown author or the same harness renders exactly as before.
func TestRenderResumeLineNamesTheWritingHarness(t *testing.T) {
	render := func(t *testing.T, authorAgent, reader string) (string, string) {
		s := newTestStore(t)
		mustUpsertProject(t, s, testProjectKey)
		author := mustStartSession(t, s, authorAgent, "/proj", 100)
		h := mustInsertHandoff(t, s, author, "fixed the thing")
		out, err := block.Render(block.Params{
			Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: author, Harness: reader,
		})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		return out, h.ID
	}

	out, id := render(t, "claude", "codex")
	want := "Resume: (id " + id + ") written by claude: fixed the thing"
	if !strings.Contains(out, want) {
		t.Fatalf("want line %q; got:\n%s", want, out)
	}

	out, id = render(t, "", "codex")
	if want := "Resume: (id " + id + ") fixed the thing"; !strings.Contains(out, want) {
		t.Fatalf("unknown author: want byte-identical %q; got:\n%s", want, out)
	}
	if strings.Contains(out, "written by") {
		t.Fatalf("unknown author must not print an author; got:\n%s", out)
	}

	out, id = render(t, "claude", "claude")
	if want := "Resume: (id " + id + ") fixed the thing"; !strings.Contains(out, want) {
		t.Fatalf("same harness: want unchanged %q; got:\n%s", want, out)
	}
}

// TestRenderDeltaDoesNotCountShellSessions: one agent session and three shell
// sessions after the handoff render "1 sessions".
func TestRenderDeltaDoesNotCountShellSessions(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	handoff := mustInsertHandoff(t, s, self, "cut here")

	agent := mustStartSession(t, s, "codex", "/proj", 300)
	mustAppendToolUse(t, s, agent, handoff.TS.Add(time.Minute), "after.go")
	for i := 0; i < 3; i++ {
		sh := mustStartSession(t, s, "shell", "/proj", 200+i)
		mustAppendEvent(t, s, sh, "shell.command", handoff.TS.Add(2*time.Minute), map[string]any{"cmd": "ls"})
	}

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude",
		Now: handoff.TS.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Delta: 1 sessions, 1 files touched") {
		t.Errorf("delta counted shell sessions; got:\n%s", out)
	}
}
