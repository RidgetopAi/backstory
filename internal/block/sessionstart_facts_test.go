package block_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// Task 6833d843: the SessionStart block states the facts the daemon already
// observed — repo state, the last failing command, ledger counts — and tells
// the agent to write a handoff.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixtureRepo makes a git repo with one commit and returns its dir and HEAD.
func fixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "first")
	return dir, gitIn(t, dir, "rev-parse", "HEAD")
}

func upsertProjectAt(t *testing.T, s *store.Store, toplevel string) {
	t.Helper()
	if err := s.UpsertProject(store.Project{Key: testProjectKey, Toplevel: toplevel, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

func insertStampedHandoff(t *testing.T, s *store.Store, sessionID, head string, about []string) store.Record {
	t.Helper()
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: "stopped mid-way",
		Next: "finish the parser", About: about, SessionID: sessionID, ProjectKey: testProjectKey, GitHead: head,
	}); err != nil {
		t.Fatalf("InsertRecord handoff: %v", err)
	}
	rec, ok, err := s.LatestRecord(testProjectKey, store.KindHandoff)
	if err != nil || !ok {
		t.Fatalf("LatestRecord: ok=%v err=%v", ok, err)
	}
	return rec
}

func lineWith(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func renderAt(t *testing.T, s *store.Store, self string, now time.Time) string {
	t.Helper()
	out, err := block.Render(block.Params{
		Store: s, ProcFS: fakeProcFS{}, ProjectKey: testProjectKey, SessionID: self, Harness: "claude", Now: now,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

// DONE WHEN (1): one Repo line naming the branch, the uncommitted count and
// the commits since the handoff; with could_not_observe, no uncommitted number.
func TestRepoStateLine(t *testing.T) {
	setup := func(t *testing.T, gs payload.SessionGitState) (string, time.Time) {
		dir, head := fixtureRepo(t)
		s := newTestStore(t)
		upsertProjectAt(t, s, dir)
		self := mustStartSession(t, s, "claude", dir, 100)
		earlier := mustStartSession(t, s, "claude", dir, 101)
		h := insertStampedHandoff(t, s, earlier, head, nil)
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "second")
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "third")
		mustAppendEvent(t, s, earlier, payload.KindSessionGitState, h.TS.Add(time.Minute), gs)
		return renderAt(t, s, self, h.TS.Add(time.Hour)), h.TS
	}

	three := 3
	out, _ := setup(t, payload.SessionGitState{Branch: "feat", UncommittedCount: &three})
	if got, want := lineWith(out, "Repo:"), "Repo: branch feat · 3 uncommitted · 2 commits since the handoff"; got != want {
		t.Errorf("Repo line = %q, want %q; block:\n%s", got, want, out)
	}
	if n := strings.Count(out, "Repo:"); n != 1 {
		t.Errorf("want exactly one Repo line, got %d", n)
	}

	out, _ = setup(t, payload.SessionGitState{CouldNotObserve: true})
	line := lineWith(out, "Repo:")
	if line == "" || strings.Contains(line, "uncommitted") || strings.Contains(line, "feat") {
		t.Errorf("could_not_observe Repo line = %q, want commits only and no uncommitted number", line)
	}
	if !strings.Contains(line, "2 commits since the handoff") {
		t.Errorf("could_not_observe Repo line = %q, want the commit count", line)
	}
}

// DONE WHEN (2): Last failure from shell command events, an Attention item for
// it, nothing when no non-zero exit, and no "last exit codes" text.
func TestLastFailureFromShellCommands(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	sh := mustStartSession(t, s, "shell", "/proj", 300)
	h := mustInsertHandoff(t, s, self, "handoff")
	mustAppendEvent(t, s, sh, payload.KindShellCommand, h.TS.Add(time.Minute), payload.ShellCommand{Cmd: "python -m unittest", Exit: 1})
	mustAppendEvent(t, s, sh, payload.KindShellCommand, h.TS.Add(2*time.Minute), payload.ShellCommand{Cmd: "ls", Exit: 0})

	out := renderAt(t, s, self, h.TS.Add(time.Hour))
	if !strings.Contains(out, "Last failure: python -m unittest exit 1 (") {
		t.Errorf("missing Last failure line; got:\n%s", out)
	}
	if line := lineWith(out, "Attention:"); !strings.Contains(line, "failed") {
		t.Errorf("missing Attention item for the failure; got:\n%s", out)
	}
	assertAbsent(t, out, "last exit codes")

	// A failure from before the handoff is not "since the handoff".
	s2 := newTestStore(t)
	mustUpsertProject(t, s2, testProjectKey)
	self2 := mustStartSession(t, s2, "claude", "/proj", 100)
	sh2 := mustStartSession(t, s2, "shell", "/proj", 300)
	mustAppendEvent(t, s2, sh2, payload.KindShellCommand, time.Now(), payload.ShellCommand{Cmd: "make", Exit: 2})
	h2 := mustInsertHandoff(t, s2, self2, "handoff")
	mustAppendEvent(t, s2, sh2, payload.KindShellCommand, h2.TS.Add(time.Minute), payload.ShellCommand{Cmd: "make", Exit: 0})
	out = renderAt(t, s2, self2, h2.TS.Add(time.Hour))
	assertAbsent(t, out, "Last failure")
	assertAbsent(t, out, "failed")
	assertAbsent(t, out, "last exit codes")
}

// A tool.result carrying an exit also counts, its command read from the tool.use.
func TestLastFailureFromToolResult(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	other := mustStartSession(t, s, "codex", "/proj", 200)
	h := mustInsertHandoff(t, s, self, "handoff")
	mustAppendEvent(t, s, other, payload.KindToolUse, h.TS.Add(time.Minute), payload.ToolUse{ToolUseID: "t1", Name: "Bash", Command: "go test ./..."})
	exit := 2
	mustAppendEvent(t, s, other, payload.KindToolResult, h.TS.Add(time.Minute), payload.ToolResult{ToolUseID: "t1", Exit: &exit})
	out := renderAt(t, s, self, h.TS.Add(time.Hour))
	if !strings.Contains(out, "Last failure: go test ./... exit 2") {
		t.Errorf("missing tool.result failure; got:\n%s", out)
	}
}

// DONE WHEN (3): Delta excludes the reading session; no "0 files touched".
func TestDeltaExcludesReadingSessionAndZeroFiles(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	other := mustStartSession(t, s, "codex", "/proj", 200)
	h := mustInsertHandoff(t, s, self, "handoff")
	mustAppendEvent(t, s, self, payload.KindSessionStart, h.TS.Add(time.Second), payload.SessionStart{})
	mustAppendEvent(t, s, other, payload.KindToolUse, h.TS.Add(time.Minute), payload.ToolUse{Name: "Bash", Command: "echo hi"})

	out := renderAt(t, s, self, h.TS.Add(time.Hour))
	if got := lineWith(out, "Delta:"); got != "Delta: 1 sessions" {
		t.Errorf("Delta line = %q, want %q (reader excluded, no 0 files phrase); got:\n%s", got, "Delta: 1 sessions", out)
	}
	assertAbsent(t, out, "0 files touched")
}

// DONE WHEN (4): the Ledger line.
func TestLedgerLine(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	out := renderAt(t, s, self, time.Now())
	assertAbsent(t, out, "Ledger:")

	mustInsertDecision(t, s, self, "first")
	mustInsertDecision(t, s, self, "second")
	if _, err := s.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindOutcome, Text: "it passed",
		SessionID: self, ProjectKey: testProjectKey,
	}); err != nil {
		t.Fatalf("InsertRecord outcome: %v", err)
	}
	out = renderAt(t, s, self, time.Now().Add(3*time.Hour))
	want := "Ledger: 2 decisions · 1 outcomes · 0 claims (newest 3h ago) — recall for them"
	if got := lineWith(out, "Ledger:"); got != want {
		t.Errorf("Ledger line = %q, want %q; got:\n%s", got, want, out)
	}
}

// DONE WHEN (5): a relative about path is flagged by a later edit of its
// absolute path under the project toplevel.
func TestRelativeAboutPathIsFlaggedStale(t *testing.T) {
	s := newTestStore(t)
	upsertProjectAt(t, s, "/repo")
	self := mustStartSession(t, s, "claude", "/repo", 100)
	h := mustInsertHandoffAbout(t, s, self, "resume at a.go", []string{"src/a.go"})
	mustAppendToolUseNamed(t, s, self, h.TS.Add(time.Minute), "Edit", "/other/src/a.go")
	assertAbsent(t, renderAt(t, s, self, h.TS.Add(time.Hour)), "possibly stale")

	mustAppendToolUseNamed(t, s, self, h.TS.Add(2*time.Minute), "Edit", "/repo/src/a.go")
	out := renderAt(t, s, self, h.TS.Add(time.Hour))
	if !strings.Contains(out, "⚠ possibly stale: 1 later edit(s) to files it names") {
		t.Errorf("relative about path was not flagged; got:\n%s", out)
	}
}

// DONE WHEN (6): the final line carries the handoff rule and the Resume id.
func TestFinalLineCarriesHandoffRule(t *testing.T) {
	s := newTestStore(t)
	mustUpsertProject(t, s, testProjectKey)
	self := mustStartSession(t, s, "claude", "/proj", 100)
	mustStartSession(t, s, "codex", "/proj2", 200)
	out := mustRender(t, s, fakeProcFS{alive: map[int]bool{200: true}}, self)
	last := out[strings.LastIndex(out, "\n")+1:]
	for _, w := range []string{"note handoff", "next", "supersedes"} {
		if !strings.Contains(last, w) {
			t.Errorf("final line %q lacks %q", last, w)
		}
	}

	h := mustInsertHandoff(t, s, self, "done")
	out = mustRender(t, s, fakeProcFS{}, self)
	last = out[strings.LastIndex(out, "\n")+1:]
	for _, w := range []string{"note handoff", "next", "supersedes = " + h.ID} {
		if !strings.Contains(last, w) {
			t.Errorf("final line %q lacks %q", last, w)
		}
	}
	if !block.EndsWithFinalLine(out) {
		t.Errorf("EndsWithFinalLine false for %q", last)
	}
}

// DONE WHEN (7): everything at once renders within the default budget with
// the Resume and Next lines intact.
func TestEverythingFitsDefaultBudget(t *testing.T) {
	dir, head := fixtureRepo(t)
	s := newTestStore(t)
	upsertProjectAt(t, s, dir)
	self := mustStartSession(t, s, "claude", dir, 100)
	prev := mustStartSession(t, s, "claude", dir, 101)
	sh := mustStartSession(t, s, "shell", dir, 300)
	h := insertStampedHandoff(t, s, prev, head, []string{"src/a.go"})
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "second")
	n := 3
	mustAppendEvent(t, s, prev, payload.KindSessionGitState, h.TS.Add(time.Minute), payload.SessionGitState{Branch: "feat", UncommittedCount: &n})
	mustAppendToolUseNamed(t, s, prev, h.TS.Add(time.Minute), "Edit", filepath.Join(dir, "src/a.go"))
	mustAppendEvent(t, s, sh, payload.KindShellCommand, h.TS.Add(2*time.Minute), payload.ShellCommand{Cmd: "python -m unittest", Exit: 1})
	mustInsertDecision(t, s, prev, "a decision")
	mustInsertDraft(t, s, prev, "", nil)
	mustStartSession(t, s, "codex", "/proj2", 200)

	out := mustRender(t, s, fakeProcFS{alive: map[int]bool{200: true}}, self)
	if got := block.EstimateTokens(out); got > block.DefaultBudgetTokens {
		t.Fatalf("block is %d tokens, over the default %d; got:\n%s", got, block.DefaultBudgetTokens, out)
	}
	assertPresent(t, out, "Resume: (id "+h.ID+")", "Next: finish the parser", "Repo:", "Delta:", "Last failure:", "Ledger:",
		"Coordination:", "Attention:", "⚠ possibly stale", "supersedes = "+h.ID)
}
