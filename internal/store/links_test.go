package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedRemoteProject(t *testing.T, s *Store, key, remote string) {
	t.Helper()
	if err := s.UpsertProject(Project{Key: key, RemoteURL: remote, Toplevel: "/tmp/" + key, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", key, err)
	}
}

// Two projects at different local paths with equivalent remotes share one
// sync identity; a project without a remote has none until linked; a link
// overrides the remote; clearing it restores the remote's identity.
func TestSyncIdentityRemoteLinkAndOverride(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	human := Identity{Kind: IdentityHuman, Actor: humanActor}

	seedRemoteProject(t, s, "/home/me/code/app", "git@github.com:X/App.git")
	seedRemoteProject(t, s, "/home/me/projects/app", "https://github.com/x/app")
	seedRemoteProject(t, s, "/home/me/scratch", "")
	seedRemoteProject(t, s, "/home/me/local", "/srv/git/local.git")

	a, _ := s.SyncIdentityOf("/home/me/code/app")
	b, _ := s.SyncIdentityOf("/home/me/projects/app")
	if a.ID != "github.com/x/app" || a.ID != b.ID || a.Source != SyncSourceRemote {
		t.Fatalf("remote identities = %+v, %+v", a, b)
	}
	for _, k := range []string{"/home/me/scratch", "/home/me/local"} {
		if id, _ := s.SyncIdentityOf(k); id.ID != "" || id.Source != SyncSourceNone {
			t.Fatalf("%s identity = %+v, want none", k, id)
		}
	}

	if err := s.SetProjectLink(human, "scratchpad", "/home/me/scratch"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.SyncIdentityOf("/home/me/scratch"); id.ID != "scratchpad" || id.Source != SyncSourceLink {
		t.Fatalf("linked identity = %+v", id)
	}
	if err := s.SetProjectLink(human, "other-id", "/home/me/code/app"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.SyncIdentityOf("/home/me/code/app"); id.ID != "other-id" || id.Source != SyncSourceLink {
		t.Fatalf("link must override remote, got %+v", id)
	}
	all, err := s.ListSyncIdentities()
	if err != nil || len(all) != 4 {
		t.Fatalf("ListSyncIdentities = %v, %v", all, err)
	}
	if err := s.ClearProjectLink(human, "/home/me/code/app"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.SyncIdentityOf("/home/me/code/app"); id.ID != "github.com/x/app" {
		t.Fatalf("after clear = %+v", id)
	}
	if err := s.SetProjectLink(human, "x", "/no/such"); err == nil {
		t.Fatal("link on unknown project must fail")
	}
	if err := s.SetProjectLink(human, "  ", "/home/me/scratch"); err == nil {
		t.Fatal("blank sync id must fail")
	}
}

// A non-human identity cannot write a link, and nothing reachable from the
// socket calls the link writers.
func TestProjectLinkRefusesNonHumanAndSocketHasNoWrite(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	seedRemoteProject(t, s, "/p/scratch", "")

	for _, id := range []Identity{
		{Kind: IdentityAgent, Actor: agentActor},
		{Kind: IdentityInference, Actor: "daemon"},
	} {
		if err := s.SetProjectLink(id, "evil", "/p/scratch"); !errors.Is(err, ErrProjectLinkRequiresHuman) {
			t.Fatalf("SetProjectLink(%v) = %v, want ErrProjectLinkRequiresHuman", id.Kind, err)
		}
	}
	human := Identity{Kind: IdentityHuman, Actor: humanActor}
	if err := s.SetProjectLink(human, "mine", "/p/scratch"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearProjectLink(Identity{Kind: IdentityAgent, Actor: agentActor}, "/p/scratch"); !errors.Is(err, ErrProjectLinkRequiresHuman) {
		t.Fatalf("ClearProjectLink by agent = %v", err)
	}
	if id, _ := s.SyncIdentityOf("/p/scratch"); id.ID != "mine" {
		t.Fatalf("refused clear changed the link: %+v", id)
	}

	for _, dir := range []string{"../mcp", "../socket"} {
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			b, _ := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // fixed sibling package dirs, not caller input
			if strings.Contains(string(b), "ProjectLink") {
				t.Errorf("%s/%s references a project link writer; links are human-only", dir, e.Name())
			}
		}
	}
}
