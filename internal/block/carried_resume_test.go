package block_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

// Task 23bd05fe: a Resume handoff carried in from another location (the
// workspace root's newest repo handoff, decision dc0a4387, carried into a
// non-git direct child with none of its own, decision 7a2556b6) must not be
// named as the reader's supersedes. Measured 2026-11-26: a session in
// ~/projects/ridgey was told "supersedes = <ridgey-weather's handoff>".

// carriedFixture is a workspace with a non-git child "ridgey" and a git
// sibling "ridgey-weather" whose toplevel lies under the workspace.
type carriedFixture struct {
	*rootFixture
	git     commonDirFakeGit
	ridgey  string
	weather string
}

func newCarriedFixture(t *testing.T) *carriedFixture {
	f := newRootFixture(t)
	weather := f.workspace + "/ridgey-weather"
	mustUpsertProject(t, f.s, weather) // Toplevel = weather, under the workspace
	return &carriedFixture{rootFixture: f, git: commonDirFakeGit{weather: true}, ridgey: f.workspace + "/ridgey", weather: weather}
}

func (f *carriedFixture) renderAt(cwd, key string) string {
	f.t.Helper()
	out, err := block.Render(block.Params{
		Store: f.s, ProcFS: fakeProcFS{}, ProjectKey: key, SessionID: "caller",
		CWD: cwd, Git: f.git, WorkspaceDirs: []string{f.workspace}, Now: time.Now(),
	})
	if err != nil {
		f.t.Fatalf("Render: %v", err)
	}
	return out
}

// wantCarried asserts out resumes id, labelled label, and that slot 5 starts
// a new chain instead of naming id as supersedes.
func wantCarried(t *testing.T, out, id, label string) {
	t.Helper()
	if !strings.Contains(out, "(id "+id) || !strings.Contains(out, "\nLocation: "+label) {
		t.Fatalf("block does not resume %s at Location %s:\n%s", id, label, out)
	}
	last := lastLine(out)
	if strings.Contains(last, id) {
		t.Fatalf("slot 5 names the carried handoff %s: %s", id, last)
	}
	if want := block.FinalLineOtherLocation(label); last != want {
		t.Fatalf("slot 5 = %q, want %q", last, want)
	}
	if !block.EndsWithFinalLine(out) {
		t.Fatalf("EndsWithFinalLine false for %q", last)
	}
}

// wantOwn asserts out's slot 5 is unchanged: supersedes = id.
func wantOwn(t *testing.T, out, id string) {
	t.Helper()
	if last, want := lastLine(out), block.FinalLineFor(id); last != want {
		t.Fatalf("slot 5 = %q, want %q", last, want)
	}
}

// The measured case: the sibling's handoff is filed under its repo key
// (dc0a4387), so the root's newest handoff is it, and the child carries it.
func TestCarriedSiblingHandoffIsNotTheChildsToSupersede(t *testing.T) {
	f := newCarriedFixture(t)
	w := f.handoffHomedAt(f.weather, f.weather, f.weather, "weather radar shipped")

	wantCarried(t, f.renderAt(f.ridgey, f.home), w, "projects/ridgey-weather")
	// Control: inside the sibling its own handoff is still superseded.
	wantOwn(t, f.renderAt(f.weather, f.weather), w)
	// Once ridgey writes its own, it supersedes that one (unchanged).
	r := f.handoff(f.ridgey, f.home, "ridgey's own handoff")
	wantOwn(t, f.renderAt(f.ridgey, f.home), r)
}

// The workspace root: a root-written handoff is the root's own; a newer repo
// handoff shown there (dc0a4387) is carried; a non-git child carrying the
// root's own handoff is carried too (it is the root's chain, not the child's).
func TestWorkspaceRootSupersedesOnlyItsOwnHandoff(t *testing.T) {
	f := newCarriedFixture(t)
	a := f.handoff(f.workspace, f.home, "root handoff A")
	wantOwn(t, f.renderAt(f.workspace, f.home), a)
	wantCarried(t, f.renderAt(f.ridgey, f.home), a, "projects")

	w := f.handoffHomedAt(f.weather, f.weather, f.weather, "weather newest")
	wantCarried(t, f.renderAt(f.workspace, f.home), w, "projects/ridgey-weather")
}

// A done (Finished) handoff follows the same rule: the reader's own still
// names it, a carried one does not.
func TestDoneHandoffSupersedesFollowLocation(t *testing.T) {
	f := newCarriedFixture(t)
	w := f.handoffHomedAt(f.weather, f.weather, f.weather, "weather finished")
	if _, err := f.s.Confirm(store.ConfirmParams{
		Identity: store.Identity{Kind: store.IdentityHuman, Actor: "human"}, Action: store.ConfirmDone,
		RecordID: w, ProjectKey: f.weather,
	}); err != nil {
		t.Fatal(err)
	}
	child := f.renderAt(f.ridgey, f.home)
	if !strings.Contains(child, "Finished: (id "+w) {
		t.Fatalf("child block does not show the Finished handoff:\n%s", child)
	}
	wantCarried(t, child, w, "projects/ridgey-weather")
	wantOwn(t, f.renderAt(f.weather, f.weather), w)
}

// An imported (paired-device) handoff homed on the sibling repo is that
// repo's own inside it, and carried at the root.
func TestImportedHandoffSupersedesFollowLocation(t *testing.T) {
	f := newCarriedFixture(t)
	if err := f.s.UpsertProject(store.Project{Key: f.weather, RemoteURL: "git@github.com:acme/weather.git", Toplevel: f.weather, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.PairSyncPeer("dev-laptop", "laptop", "laptop.local"); err != nil {
		t.Fatal(err)
	}
	const id = "imported-weather-0001"
	if _, err := f.s.ImportSyncChanges(store.Identity{Kind: store.IdentityHuman, Actor: "human"}, store.SyncPeer{DeviceID: "dev-laptop"},
		[]store.SyncChange{{Kind: store.SyncKindRecord, Record: &store.SyncRecord{
			ID: id, TS: time.Now().UnixNano(), Kind: string(store.KindHandoff), Tier: string(store.TierHumanDeclared),
			Text: "laptop weather handoff", ProjectSyncID: "github.com/acme/weather", OriginDevice: "dev-laptop",
		}}}); err != nil {
		t.Fatal(err)
	}
	in := f.renderAt(f.weather, f.weather)
	if !strings.Contains(in, "(id "+id+", from laptop)") {
		t.Fatalf("in-repo block does not resume the imported handoff:\n%s", in)
	}
	wantOwn(t, in, id)

	root := f.renderAt(f.workspace, f.home)
	if !strings.Contains(root, "(id "+id+", from laptop)") {
		t.Fatalf("root block does not resume the imported handoff:\n%s", root)
	}
	if last := lastLine(root); strings.Contains(last, id) || last != block.FinalLineOtherLocation("") {
		t.Fatalf("root slot 5 = %q, want %q", last, block.FinalLineOtherLocation(""))
	}
}

func TestFinalLineOtherLocationHasNoID(t *testing.T) {
	l := block.FinalLineOtherLocation("projects/ridgey-weather")
	if !strings.Contains(l, "supersedes = nothing (the Resume handoff is from projects/ridgey-weather; your handoff starts this folder's chain)") {
		t.Fatalf("FinalLineOtherLocation = %q", l)
	}
	if !block.EndsWithFinalLine("x\n" + l) {
		t.Fatalf("EndsWithFinalLine does not recognize %q", l)
	}
}
