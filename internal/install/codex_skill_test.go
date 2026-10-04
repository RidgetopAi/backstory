package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/skill"
)

func codexSkillStatus(t *testing.T, home string, opts Options) ItemStatus {
	t.Helper()
	items, err := (codexAdapter{}).Check(home, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Name == ItemCodexSkill {
			return it.Status
		}
	}
	t.Fatal("Check reported no codex-skill item")
	return ""
}

func TestInstallCodexWritesSkillAndReportsPresent(t *testing.T) {
	home := t.TempDir()
	opts := Options{Prefix: t.TempDir()}
	if err := (codexAdapter{}).Install(home, opts); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".codex", "skills", "backstory", "SKILL.md")) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(skill.Embedded) {
		t.Error("codex skill differs from the embedded skill")
	}
	if s := codexSkillStatus(t, home, opts); s != StatusPresent {
		t.Errorf("codex-skill = %s, want present", s)
	}
}

func TestCodexForeignSkillLeftAloneAndRemoveKeepsIt(t *testing.T) {
	home := t.TempDir()
	opts := Options{Prefix: t.TempDir()}
	dest := DefaultCodexPaths(home).SkillPath
	writePiFile(t, dest, "someone else's skill\n")
	a := codexAdapter{}
	if err := a.Install(home, opts); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "someone else's skill\n" { //nolint:gosec // test temp dir
		t.Errorf("foreign skill was modified: %q", b)
	}
	if s := codexSkillStatus(t, home, opts); s != StatusForeign || string(s) != "foreign-conflict" {
		t.Errorf("codex-skill = %s, want foreign-conflict", s)
	}
	if err := a.Remove(home, opts); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "someone else's skill\n" { //nolint:gosec // test temp dir
		t.Errorf("remove touched the foreign skill: %q", b)
	}
}

func TestCodexRemoveDeletesOwnSkill(t *testing.T) {
	home := t.TempDir()
	opts := Options{Prefix: t.TempDir()}
	a := codexAdapter{}
	if err := a.Install(home, opts); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(home, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DefaultCodexPaths(home).SkillPath); !os.IsNotExist(err) {
		t.Errorf("skill still present after remove: %v", err)
	}
}

func TestCodexReplacesOutdatedSkill(t *testing.T) {
	home := t.TempDir()
	opts := Options{Prefix: t.TempDir()}
	dest := DefaultCodexPaths(home).SkillPath
	writePiFile(t, dest, skillAt462e93f)
	if s := codexSkillStatus(t, home, opts); s != StatusOutdated {
		t.Fatalf("codex-skill = %s, want outdated", s)
	}
	if err := (codexAdapter{}).Install(home, opts); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != string(skill.Embedded) { //nolint:gosec // test temp dir
		t.Error("outdated codex skill was not replaced")
	}
}

func TestCodexStubNamesTheSkill(t *testing.T) {
	home := t.TempDir()
	if err := (codexAdapter{}).Install(home, Options{Prefix: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(DefaultCodexPaths(home).AgentsMD) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`backstory` skill", "skills/backstory/SKILL.md"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("codex AGENTS.md stub lacks %q:\n%s", want, b)
		}
	}
}
