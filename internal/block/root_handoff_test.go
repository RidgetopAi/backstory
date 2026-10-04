package block_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
)

// rootFixture seeds a workspace W with sessions + handoffs written in order.
type rootFixture struct {
	t         *testing.T
	s         *store.Store
	workspace string
	home      string
	git       resumeHomeFakeGit
}

func newRootFixture(t *testing.T) *rootFixture {
	w := "/home/fixture/projects"
	s := newTestStore(t)
	mustUpsertProject(t, s, "workspace:"+w)
	return &rootFixture{t: t, s: s, workspace: w, home: "workspace:" + w, git: resumeHomeFakeGit{}}
}

// handoff writes a handoff (stored under the workspace key) from a session
// whose cwd/project_key is sessionKey; returns the record id.
func (f *rootFixture) handoff(sessionCWD, sessionKey, text string) string {
	f.t.Helper()
	sess, err := f.s.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: sessionCWD, ProjectKey: sessionKey, StartedAt: time.Now(), Origin: store.OriginLive,
	})
	if err != nil {
		f.t.Fatalf("StartSession: %v", err)
	}
	id, err := f.s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff,
		Text: text, SessionID: sess, ProjectKey: f.home,
	})
	if err != nil {
		f.t.Fatalf("InsertRecord: %v", err)
	}
	return id
}

func (f *rootFixture) render(cwd, key string) string {
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

// DONE WHEN 1 and 3: the root serves the root-written handoff A, not the
// newer in-repo B; the in-repo block still carries B; a non-git direct child
// shares the root and carries A.
func TestRootServesRootWrittenHandoffNotNewerProjectOne(t *testing.T) {
	f := newRootFixture(t)
	repo := f.workspace + "/wobble"
	f.git[repo] = true
	mustUpsertProject(t, f.s, repo)
	a := f.handoff(f.workspace, f.home, "GPU lockups debug")
	b := f.handoff(repo, repo, "wobble v0.2.0 shipped")

	out := f.render(f.workspace, f.home)
	if !strings.Contains(out, "Resume: (id "+a+")") || strings.Contains(out, b) || strings.Contains(out, "wobble v0.2.0") {
		t.Fatalf("root block = %q, want A (%s) and nothing of B (%s)", out, a, b)
	}
	if strings.Contains(out, "Handoffs exist in projects") {
		t.Fatalf("root block = %q, pointer must not appear when a root handoff exists", out)
	}

	in := f.render(repo, repo)
	if !strings.Contains(in, "Resume: (id "+b+")") {
		t.Fatalf("in-repo block = %q, want B (%s)", in, b)
	}

	notes := f.workspace + "/notes" // non-git direct child: key == home
	child := f.render(notes, f.home)
	if !strings.Contains(child, "Resume: (id "+a+")") {
		t.Fatalf("non-git child block = %q, want A (%s)", child, a)
	}
}

// DONE WHEN 2: with only project-written handoffs the root carries no body
// and exactly one pointer line naming each project and its latest handoff
// id, ending in "+N more" past the cap.
func TestRootPointerLineWhenOnlyProjectHandoffs(t *testing.T) {
	f := newRootFixture(t)
	var latest []string
	for i := 0; i < 5; i++ {
		repo := fmt.Sprintf("%s/proj%d", f.workspace, i)
		f.git[repo] = true
		mustUpsertProject(t, f.s, repo)
		f.handoff(repo, repo, fmt.Sprintf("old body %d", i)) // superseded by the next
		latest = append(latest, f.handoff(repo, repo, fmt.Sprintf("body %d", i)))
	}

	out := f.render(f.workspace, f.home)
	if strings.Contains(out, "Resume:") || strings.Contains(out, "body ") {
		t.Fatalf("root block = %q, must carry no handoff body", out)
	}
	var ptr []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Handoffs exist in projects") {
			ptr = append(ptr, l)
		}
	}
	if len(ptr) != 1 {
		t.Fatalf("root block = %q, want exactly one pointer line, got %d", out, len(ptr))
	}
	// Newest-first, capped at 3, each at its project's LATEST handoff id.
	for _, i := range []int{4, 3, 2} {
		if !strings.Contains(ptr[0], fmt.Sprintf("projects/proj%d (id %s)", i, latest[i])) {
			t.Fatalf("pointer = %q, want proj%d with id %s", ptr[0], i, latest[i])
		}
	}
	if strings.Contains(ptr[0], "proj1") || !strings.HasSuffix(ptr[0], "; +2 more") {
		t.Fatalf("pointer = %q, want cap of 3 and suffix '; +2 more'", ptr[0])
	}
}
