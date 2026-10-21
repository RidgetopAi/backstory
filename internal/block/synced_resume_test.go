package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

// syncedFixture is a store whose project testProjectKey has remote
// syncedRemote, paired with device "laptop", so an imported record is homed
// on testProjectKey exactly as ImportSyncChanges does for a real peer.
type syncedFixture struct {
	t    *testing.T
	s    *store.Store
	sess string
}

func newSyncedFixture(t *testing.T) *syncedFixture {
	t.Helper()
	s := newTestStore(t)
	if err := s.UpsertProject(store.Project{Key: testProjectKey, RemoteURL: "git@github.com:acme/widgets.git", Toplevel: testProjectKey, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.PairSyncPeer("dev-laptop", "laptop", "laptop.local"); err != nil {
		t.Fatalf("PairSyncPeer: %v", err)
	}
	return &syncedFixture{t: t, s: s, sess: mustStartSession(t, s, "claude", "/work", 0)}
}

func (f *syncedFixture) importHandoff(id string, ts time.Time, text string, edges ...store.SyncChange) {
	f.t.Helper()
	ch := []store.SyncChange{{Kind: store.SyncKindRecord, Record: &store.SyncRecord{
		ID: id, TS: ts.UnixNano(), Kind: string(store.KindHandoff), Tier: string(store.TierHumanDeclared), Text: text,
		ProjectSyncID: "github.com/acme/widgets", OriginDevice: "dev-laptop",
	}}}
	ch = append(ch, edges...)
	human := store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	if _, err := f.s.ImportSyncChanges(human, store.SyncPeer{DeviceID: "dev-laptop"}, ch); err != nil {
		f.t.Fatalf("ImportSyncChanges: %v", err)
	}
}

func (f *syncedFixture) render(budget string) string {
	f.t.Helper()
	if budget != "" {
		if err := f.s.SetSetting(block.SettingBudgetKey, budget); err != nil {
			f.t.Fatalf("SetSetting: %v", err)
		}
	}
	out, err := block.Render(block.Params{Store: f.s, ProjectKey: testProjectKey, Now: time.Now()})
	if err != nil {
		f.t.Fatalf("Render: %v", err)
	}
	return out
}

// TestResumePicksNewestImportedHandoffAndNamesLocalInAlso is the punch's
// DONE WHEN clause 1: the imported handoff is newer, so it is the Resume
// line (with `from laptop`) and the local one rides on an also line; under a
// tight budget the also line drops first, before the Resume line.
func TestResumePicksNewestImportedHandoffAndNamesLocalInAlso(t *testing.T) {
	f := newSyncedFixture(t)
	local := mustInsertHandoff(t, f.s, f.sess, "local desktop handoff\nsecond line")
	f.importHandoff("imported-handoff-0001", time.Now().Add(time.Hour), "laptop handoff text")

	out := f.render("")
	if !strings.Contains(out, "Resume: (id imported-handoff-0001, from laptop) laptop handoff text") {
		t.Fatalf("Resume is not the imported handoff:\n%s", out)
	}
	self, _ := f.s.SelfDevice()
	wantAlso := "also: " + self.Name + " handoff (id " + local.ID[:8] + ") local desktop handoff"
	if !strings.Contains(out, wantAlso+"\n") {
		t.Fatalf("also line %q missing:\n%s", wantAlso, out)
	}
	if strings.Contains(out, "second line") {
		t.Fatalf("also line quotes more than the first line:\n%s", out)
	}

	// The tightest budget that still holds the Resume line without the also
	// line: the also line must go, the Resume line must stay.
	full := block.EstimateTokens(out)
	alsoTokens := block.EstimateTokens(wantAlso + "\n\n")
	tight := f.render(itoa(full - alsoTokens))
	if strings.Contains(tight, "also:") {
		t.Fatalf("also line survived a budget that drops it:\n%s", tight)
	}
	if !strings.Contains(tight, "Resume: (id imported-handoff-0001, from laptop)") {
		t.Fatalf("Resume line dropped before the also line:\n%s", tight)
	}
}

// TestResumeLocalNewerKeepsLocalAndAlsoNamesImported: the same rule the other
// way round, and a superseded loser earns no also line.
func TestResumeLocalNewerKeepsLocalAndAlsoNamesImported(t *testing.T) {
	f := newSyncedFixture(t)
	f.importHandoff("imported-handoff-0001", time.Now().Add(-time.Hour), "old laptop handoff")
	local := mustInsertHandoff(t, f.s, f.sess, "fresh local handoff")
	out := f.render("")
	if !strings.Contains(out, "Resume: (id "+local.ID+") written by claude: fresh local handoff") {
		t.Fatalf("Resume is not the local handoff:\n%s", out)
	}
	if !strings.Contains(out, "also: laptop handoff (id imported) old laptop handoff") {
		t.Fatalf("also line for the imported handoff missing:\n%s", out)
	}
}

func TestResumeAlsoLineOmittedWhenLoserIsSuperseded(t *testing.T) {
	f := newSyncedFixture(t)
	local := mustInsertHandoff(t, f.s, f.sess, "local desktop handoff")
	f.importHandoff("imported-handoff-0001", time.Now().Add(time.Hour), "laptop handoff text",
		store.SyncChange{Kind: store.SyncKindEdge, Edge: &store.SyncEdge{
			FromID: "imported-handoff-0001", ToID: local.ID, Type: string(store.EdgeSupersedes), DeclaredBy: "human"}})
	out := f.render("")
	if !strings.Contains(out, "Resume: (id imported-handoff-0001, from laptop)") || strings.Contains(out, "also:") {
		t.Fatalf("want imported Resume and no also line:\n%s", out)
	}
}

// TestDeltaAfterImportedHandoffCountsOnlyLocalEventsAfterImport is the punch's
// DONE WHEN clause 2: the imported handoff's event_cursor is the local
// timeline position at import, so events before the import are not "since
// the handoff" and events after it are.
func TestDeltaAfterImportedHandoffCountsOnlyLocalEventsAfterImport(t *testing.T) {
	f := newSyncedFixture(t)
	mustAppendToolUse(t, f.s, f.sess, time.Now().Add(-time.Minute), "/work/before.go")
	f.importHandoff("imported-handoff-0001", time.Now().Add(-time.Hour), "laptop handoff text")
	sess2 := mustStartSession(t, f.s, "claude", "/work", 0)
	mustAppendToolUse(t, f.s, sess2, time.Now(), "/work/after.go")

	out := f.render("")
	if !strings.Contains(out, "1 file touched") && !strings.Contains(out, "1 files touched") {
		t.Fatalf("delta does not count exactly the one post-import event:\n%s", out)
	}
	if strings.Contains(out, "before.go") {
		t.Fatalf("pre-import event leaked into the delta:\n%s", out)
	}
}
