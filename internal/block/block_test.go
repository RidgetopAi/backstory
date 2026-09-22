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
	s, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"))
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
type fakeProcFS struct {
	alive map[int]bool
}

func (f fakeProcFS) Status(pid int) (ident.Status, error) {
	if f.alive[pid] {
		return ident.Status{Name: "proc"}, nil
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

// mustAppendToolUse appends a tool.use event carrying only Path — the
// shape the delta slot's file-touched count reads.
func mustAppendToolUse(t *testing.T, s *store.Store, sessionID string, ts time.Time, path string) int64 {
	t.Helper()
	return mustAppendEvent(t, s, sessionID, payload.KindToolUse, ts, payload.ToolUse{Path: path})
}

// mustAppendToolResult appends a tool.result event carrying an exit code —
// the shape the delta slot's "last exit codes" reads.
func mustAppendToolResult(t *testing.T, s *store.Store, sessionID string, ts time.Time, exit int) int64 {
	t.Helper()
	return mustAppendEvent(t, s, sessionID, payload.KindToolResult, ts, payload.ToolResult{Exit: &exit})
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

	idxResume := strings.Index(out, "Resume:")
	idxMode := strings.Index(out, "MODE: review")
	idxDelta := strings.Index(out, "Delta:")
	idxCoord := strings.Index(out, "Coordination:")
	idxAttention := strings.Index(out, "Attention:")
	idxFinal := strings.Index(out, block.FinalLine)

	for name, idx := range map[string]int{
		"Resume": idxResume, "MODE": idxMode, "Delta": idxDelta,
		"Coordination": idxCoord, "Attention": idxAttention, "final line": idxFinal,
	} {
		if idx < 0 {
			t.Fatalf("output missing %s; got:\n%s", name, out)
		}
	}
	inOrder := idxResume < idxMode && idxMode < idxDelta && idxDelta < idxCoord &&
		idxCoord < idxAttention && idxAttention < idxFinal
	if !inOrder {
		t.Fatalf("slots out of order; got:\n%s", out)
	}
	if !strings.Contains(out, "1 sessions, 1 files touched, last exit codes: 1") {
		t.Errorf("delta slot missing expected counts; got:\n%s", out)
	}
	if !strings.Contains(out, "1 unconfirmed draft(s), 1 contradiction(s)") {
		t.Errorf("attention slot missing expected counts; got:\n%s", out)
	}
	if !strings.Contains(out, "- codex in /proj-worktree") {
		t.Errorf("coordination slot missing the other live session; got:\n%s", out)
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
	if strings.Contains(out, "before.go") || strings.Contains(out, "exit codes: 9") {
		t.Errorf("delta counted an event before the handoff; got:\n%s", out)
	}
	if !strings.Contains(out, "1 sessions, 1 files touched, last exit codes: 0") {
		t.Errorf("delta did not count the after-handoff event correctly; got:\n%s", out)
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
	if !strings.Contains(out, "1 sessions, 1 files touched, last exit codes: 7") {
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

// TestRenderEmptyProjectYieldsExactlyTheEmptyStateConstant is clause (f).
func TestRenderEmptyProjectYieldsExactlyTheEmptyStateConstant(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, "proj-empty")

	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: "proj-empty", SessionID: "self", Harness: "claude",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != block.EmptyProjectLine {
		t.Fatalf("Render(empty project) = %q, want exactly %q", out, block.EmptyProjectLine)
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
