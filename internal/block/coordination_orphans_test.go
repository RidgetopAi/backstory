package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

func coordinationOf(t *testing.T, s *store.Store, procfs fakeProcFS, self string) string {
	t.Helper()
	out := mustRender(t, s, procfs, self)
	i := strings.Index(out, "Coordination:")
	if i < 0 {
		return ""
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestCoordinationCollapsesRowsByPid is task 6d68ac6f's DONE WHEN clause 1
// (block half): six live rows left by earlier daemon runs for one alive
// pid list that process once.
//
// RA-MUTATION-PROBE: drop the seenPID collapse in coordinationSlot -> RED.
func TestCoordinationCollapsesRowsByPid(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	for i := 0; i < 6; i++ {
		mustStartSession(t, s, "claude", "/proj", 16873)
	}
	got := coordinationOf(t, s, fakeProcFS{alive: map[int]bool{100: true, 16873: true}, comm: map[int]string{100: "claude", 16873: "claude"}}, self)
	if n := strings.Count(got, "- claude in /proj"); n != 1 {
		t.Fatalf("pid 16873 listed %d times, want 1; got:\n%s", n, got)
	}
}

// TestCoordinationNeverListsSelfAndChecksComm is DONE WHEN clause 2: the
// reader's own session and pid are absent from its slot, and an alive pid
// whose comm is not the recorded harness is not listed.
//
// RA-MUTATION-PROBE: drop the comm check (st.Name == agent) -> RED; drop
// the selfPID skip -> RED.
func TestCoordinationNeverListsSelfAndChecksComm(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj-self", 100)
	mustStartSession(t, s, "claude", "/proj-self-old-row", 100) // same pid, other row
	mustStartSession(t, s, "codex", "/proj-recycled", 300)      // pid alive, now a bash
	mustStartSession(t, s, "codex", "/proj-real", 400)
	procfs := fakeProcFS{
		alive: map[int]bool{100: true, 300: true, 400: true},
		comm:  map[int]string{100: "claude", 300: "bash", 400: "codex"},
	}
	got := coordinationOf(t, s, procfs, self)
	for _, bad := range []string{"/proj-self", "/proj-recycled"} {
		if strings.Contains(got, bad) {
			t.Errorf("slot lists %s; got:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "- codex in /proj-real") {
		t.Errorf("slot misses the real other session; got:\n%s", got)
	}
}

// TestCoordinationDetachedHeadOmitsBranch is DONE WHEN clause 3.
//
// RA-MUTATION-PROBE: drop the `branch != detachedHead` test -> RED.
func TestCoordinationDetachedHeadOmitsBranch(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	other := mustStartSession(t, s, "codex", "/proj-detached", 200)
	mustAppendEvent(t, s, other, payload.KindSessionStart, time.Now(), payload.SessionStart{GitBranch: "HEAD"})
	got := coordinationOf(t, s, fakeProcFS{alive: map[int]bool{200: true}}, self)
	if !strings.Contains(got, "/proj-detached") || strings.Contains(got, "(branch HEAD)") {
		t.Fatalf("want a line without (branch HEAD); got:\n%s", got)
	}
}
