package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

func (f *syncedFixture) attempt(result, detail string, ago time.Duration) {
	f.t.Helper()
	if err := f.s.AppendSyncAttempt(store.SyncAttempt{TS: time.Now().Add(-ago), Peer: "laptop", DeviceID: "dev-laptop", Result: result, Detail: detail}); err != nil {
		f.t.Fatalf("AppendSyncAttempt: %v", err)
	}
}

// While sync is failing, the SessionStart block for any project carries the
// warning line, and it survives a budget that drops every slot but the header.
func TestSyncFailingWarningLeadsAndSurvivesTinyBudget(t *testing.T) {
	f := newSyncedFixture(t)
	f.attempt("ok", "", 3*time.Hour)
	f.attempt("error", "remote backstory speaks sync proto 2, this one 1", time.Hour)
	mustInsertHandoff(t, f.s, f.sess, "local handoff")

	const wantStart = "⚠ Backstory sync with laptop is failing (remote backstory speaks sync proto 2, this one 1); work from laptop since "
	const wantEnd = " may be missing. Tell the user before resuming."
	for _, budget := range []string{"", "1"} {
		out := f.render(budget)
		if !strings.Contains(out, wantStart) || !strings.Contains(out, wantEnd) {
			t.Fatalf("budget %q: block lacks the sync warning:\n%s", budget, out)
		}
		if !strings.HasPrefix(out, block.HeaderLine+"\n\n⚠ Backstory sync") {
			t.Fatalf("budget %q: warning is not right after the header:\n%s", budget, out)
		}
	}
	if out := f.render("1"); strings.Contains(out, "Resume:") || strings.Contains(out, block.FinalLine) {
		t.Fatalf("tiny budget kept other slots:\n%s", out)
	}

	// An empty project (no slot at all) carries it too.
	out, err := block.Render(block.Params{Store: f.s, ProjectKey: "/some/other/project", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, wantStart) {
		t.Fatalf("empty-project block lacks the sync warning:\n%s", out)
	}
}

func TestSyncWarningAbsentWhenHealthy(t *testing.T) {
	f := newSyncedFixture(t)
	f.attempt("ok", "", time.Hour)
	mustInsertHandoff(t, f.s, f.sess, "local handoff")
	if out := f.render(""); strings.Contains(out, "Backstory sync") {
		t.Fatalf("healthy sync produced a warning:\n%s", out)
	}
	// A timer installed but not active makes it fail.
	out, err := block.Render(block.Params{Store: f.s, ProjectKey: testProjectKey, Now: time.Now(), SyncTimer: bsync.TimerInactive})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sync timer is installed but not active") {
		t.Fatalf("inactive timer produced no warning:\n%s", out)
	}
}
