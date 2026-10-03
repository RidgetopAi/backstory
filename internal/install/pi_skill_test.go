package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/skill"
)

// skillAt462e93f is SKILL.md as embedded at 462e93f, the version before the
// frontmatter was added, inlined verbatim.
const skillAt462e93f = "`description:` claims the intents — \"what was I doing\", \"resume\", \"why did we\", \"backstory\", \"history\", \"last session\".\nif a SessionStart block is present, do not re-fetch; else call `recall` for this project once; if the Resume line carries a possibly-stale marker, verify it before acting and `confirm affirm` it if still true.\nbefore changing a file whose recall shows a declared decision, read it.\nwhen you choose between alternatives, `note decision` in one line — no ceremony.\nclaim \"done\" only with `note outcome` pointing at a `timeline` event id; a claim without evidence is recorded as a claim.\nend with `note handoff`: what is true now, what is next, what not to do. Put the single next step in the handoff's optional `next` (one line, at most 200 characters; handoff only) so the panel can show it. Set `supersedes` to the Resume slot's id when it showed one; whenever any record replaces or corrects an earlier one, set `supersedes` to its id (or `confirm supersede`).\ninferred records are hints; declared records are claims; the timeline is fact.\nnever put a secret in a note.\n"

func TestInstallPiWritesSkillAndReportsPresent(t *testing.T) {
	home := t.TempDir()
	opts := Options{Prefix: t.TempDir()}
	a := piAdapter{}
	if err := a.Install(home, opts); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "skills", "backstory", "SKILL.md")) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(skill.Embedded) {
		t.Error("pi skill differs from the embedded skill")
	}
	items, err := a.Check(home, opts)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, it := range items {
		if it.Name == ItemPiSkill {
			found = true
			if it.Status != StatusPresent {
				t.Errorf("pi-skill = %s, want present", it.Status)
			}
		}
	}
	if !found {
		t.Fatal("Check reported no pi-skill item")
	}
}

func TestInstallPiLeavesForeignSkillUntouched(t *testing.T) {
	home := t.TempDir()
	opts := Options{Prefix: t.TempDir()}
	dest := DefaultPiPaths(home).SkillPath
	writePiFile(t, dest, "someone else's skill\n")
	a := piAdapter{}
	if err := a.Install(home, opts); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "someone else's skill\n" { //nolint:gosec // test temp dir
		t.Errorf("foreign skill was modified: %q", b)
	}
	items, _ := a.Check(home, opts)
	for _, it := range items {
		if it.Name == ItemPiSkill && it.Status != StatusForeign {
			t.Errorf("pi-skill = %s, want foreign-conflict", it.Status)
		}
	}
}

func TestInstallPiReplacesOutdatedSkill(t *testing.T) {
	home := t.TempDir()
	opts := Options{Prefix: t.TempDir()}
	dest := DefaultPiPaths(home).SkillPath
	writePiFile(t, dest, skillAt462e93f)
	a := piAdapter{}
	items, _ := a.Check(home, opts)
	for _, it := range items {
		if it.Name == ItemPiSkill && it.Status != StatusOutdated {
			t.Errorf("pi-skill = %s, want outdated", it.Status)
		}
	}
	if err := a.Install(home, opts); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != string(skill.Embedded) { //nolint:gosec // test temp dir
		t.Error("outdated pi skill was not replaced")
	}
}

// A desk holding the 462e93f skill reads outdated and `install claude`
// replaces it with the frontmatter version.
func TestClaude462e93fSkillIsOutdatedAndReplaced(t *testing.T) {
	home := t.TempDir()
	paths := DefaultPaths(home)
	opts := Options{Prefix: t.TempDir()}
	writePiFile(t, paths.SkillPath, skillAt462e93f)
	items, err := Check(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Name == ItemSkill && it.Status != StatusOutdated {
			t.Fatalf("skill = %s, want outdated", it.Status)
		}
	}
	if err := Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(paths.SkillPath); string(b) != string(skill.Embedded) { //nolint:gosec // test temp dir
		t.Error("claude skill not replaced by install")
	}
}

func TestPiStubNamesTheSkill(t *testing.T) {
	home := t.TempDir()
	if err := (piAdapter{}).Install(home, Options{Prefix: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(DefaultPiPaths(home).AgentsMD) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`backstory` skill", "skills/backstory/SKILL.md"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("pi AGENTS.md stub lacks %q:\n%s", want, b)
		}
	}
}
