package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/homesnap"
	"github.com/RidgetopAi/backstory/internal/install"
)

// backstoryInstallInventory is every path under $HOME that `backstory
// install` / `--remove` (each harness, `install bash`, `install sync`)
// writes, replaces or deletes, and what an edited, symlinked or shipped copy
// gets. TestBackstoryInstallInventory observes the real runs and fails on a
// path missing here or a row no run touched.
// Its population is what those runs do: a write on a branch they do not take
// is not seen, so a new branch that writes under $HOME needs a step here.
var backstoryInstallInventory = []homesnap.Row{
	{Pattern: ".claude.json", Writer: "install claude", OnInstall: "JSON merge: only mcpServers.backstory is written; an entry of ours you edited, or a foreign one, is a foreign-conflict and the harness is skipped; a symlink is written through", OnRemove: "deletes only our unedited entry; the file is otherwise kept"},
	{Pattern: ".claude/settings.json", Writer: "install claude", OnInstall: "JSON merge: our hook's command is updated, its matcher, timeout and other keys are yours (legacy SessionStart default excepted); foreign hooks untouched; symlink written through", OnRemove: "removes only our hook objects"},
	{Pattern: ".claude/CLAUDE.md", Writer: "install claude", OnInstall: "one stub line between backstory:begin/end markers; text outside them is yours and kept; the marked block is Backstory's and is rewritten", OnRemove: "removes the marked block only; an emptied file is removed (emptied in place if a symlink)"},
	{Pattern: ".claude/skills/**", Writer: "install claude (skill)", OnInstall: "skill rule: a shipped older SKILL.md is backed up (SKILL.md.bak-<hash>) then replaced; unknown content is a foreign-conflict and kept", OnRemove: "deletes a current or shipped SKILL.md only; backups and unknown files stay"},
	{Pattern: ".codex", Writer: "install codex", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".codex/config.toml", Writer: "install codex", OnInstall: "marked backstory block; lines you add inside it are moved out after the end marker and kept; a foreign mcp_servers.backstory is a conflict", OnRemove: "removes the marked block only"},
	{Pattern: ".codex/hooks.json", Writer: "install codex", OnInstall: "JSON merge, same hook rule as settings.json", OnRemove: "removes only our hook objects; an emptied file is removed"},
	{Pattern: ".codex/AGENTS.md", Writer: "install codex", OnInstall: "marked stub block, as CLAUDE.md", OnRemove: "as CLAUDE.md"},
	{Pattern: ".codex/skills/**", Writer: "install codex (skill)", OnInstall: "skill rule", OnRemove: "skill rule; empty skill dirs pruned"},
	{Pattern: ".config", Writer: "install agents / install sync", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".config/AGENTS.md", Writer: "install agents", OnInstall: "marked stub block, as CLAUDE.md", OnRemove: "as CLAUDE.md"},
	{Pattern: ".agents", Writer: "install agents", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".agents/mcp.json", Writer: "install agents", OnInstall: "JSON merge: only mcpServers.backstory; an edited or foreign entry is a conflict", OnRemove: "deletes only our entry; an emptied file is removed"},
	{Pattern: ".hermes", Writer: "install hermes", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".hermes/config.yaml", Writer: "install hermes", OnInstall: "sets memory.provider with a backstory trailer (original value recorded) or a marked block; another provider is a conflict", OnRemove: "restores the recorded original value or removes the marked block"},
	{Pattern: ".hermes/plugins/**", Writer: "install hermes", OnInstall: "managedFile rule (plugin.yaml, __init__.py): shipped older -> replaced, edited -> both kept and named, no marker -> conflict; __pycache__ is Hermes' compile cache of our plugin", OnRemove: "deletes current or shipped files only; __pycache__ only once __init__.py is gone; empty dirs pruned"},
	{Pattern: ".hermes/skills/**", Writer: "install hermes (skill)", OnInstall: "skill rule", OnRemove: "skill rule; empty dirs pruned"},
	{Pattern: ".pi/**", Writer: "install pi", OnInstall: "extension index.ts: managedFile rule; AGENTS.md: marked stub block; skills: skill rule; dirs created when missing", OnRemove: "extension: current or shipped only, edited kept and named; stub block removed; empty dirs pruned"},
	{Pattern: ".bashrc", Writer: "install bash", OnInstall: "appends the marked two-line block when the marker is absent; with the marker present nothing is written; a symlink to your own regular file is written through", OnRemove: "removes the marked block only; an emptied file is removed"},
	{Pattern: ".config/systemd/**", Writer: "install sync", OnInstall: "managedFile rule for backstory-sync.service (ExecStart binary undone before hashing) and .timer: shipped -> written, edited -> kept and named; dirs created when missing", OnRemove: "deletes current or shipped units only; edited kept and named; a symlinked unit is left in place"},
}

var inventoryProbe = os.Getenv("INVENTORY_PRINT") != ""

func TestBackstoryInstallInventory(t *testing.T) {
	rows := backstoryInstallInventory
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(install.HermesHomeEnv, "")
	oldSM := install.ServiceManager
	install.ServiceManager = func(...string) error { return nil }
	t.Cleanup(func() { install.ServiceManager = oldSM })
	opts := install.Options{BinaryPath: "/opt/backstory/bin/backstory", Prefix: t.TempDir()}
	// An older shipped skill, so install's backup of it is observed.
	skillPath := install.DefaultPaths(home).SkillPath
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte(bc94d31Skill), 0o600); err != nil {
		t.Fatal(err)
	}

	var seen []string
	step := func(name string, run func()) {
		t.Helper()
		before, err := homesnap.Take(home)
		if err != nil {
			t.Fatal(err)
		}
		run()
		after, err := homesnap.Take(home)
		if err != nil {
			t.Fatal(err)
		}
		changed := homesnap.Changed(before, after)
		if inventoryProbe {
			t.Logf("%s:\n%s", name, strings.Join(changed, "\n"))
		}
		seen = append(seen, changed...)
		for _, p := range homesnap.Unlisted(changed, rows) {
			t.Errorf("%s touched %s, which is not in the backstory install inventory", name, p)
		}
	}
	step("install every harness, bash and sync", func() {
		for _, o := range install.InstallAll(install.Adapters(), home, opts) {
			if o.Err != nil {
				t.Errorf("install %s: %v", o.Adapter.Name(), o.Err)
			}
		}
		if err := install.InstallBashrcBinary(install.DefaultBashrcPath(home), opts.BinaryPath); err != nil {
			t.Error(err)
		}
		if _, err := install.InstallSyncTimer(home); err != nil {
			t.Error(err)
		}
	})
	step("remove every harness, bash and sync", func() {
		for _, a := range install.Adapters() {
			if err := a.Remove(home, opts); err != nil {
				t.Errorf("remove %s: %v", a.Name(), err)
			}
		}
		if err := install.RemoveBashrc(install.DefaultBashrcPath(home)); err != nil {
			t.Error(err)
		}
		if _, err := install.RemoveSyncTimer(home); err != nil {
			t.Error(err)
		}
	})
	for _, r := range homesnap.Unseen(seen, rows) {
		t.Errorf("inventory row %s was touched by no run", r.Pattern)
	}
}
