package install_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// dotfile writes content to dir/name and symlinks link to it, the way GNU
// stow or a dotfiles repo manages a config file. It returns the target path.
func dotfile(t *testing.T, dir, name, link, content string) string {
	t.Helper()
	target := filepath.Join(dir, name)
	mustWriteFile(t, target, content)
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return target
}

// assertStillLinked fails unless link is still a symlink pointing at target.
func assertStillLinked(t *testing.T, link, target string) {
	t.Helper()
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat %s: %v", link, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s was replaced by a plain file; want it to stay a symlink to %s", link, target)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("%s -> %q (%v), want -> %q", link, got, err, target)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a t.TempDir() path this test built
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestInstallAndRemoveWriteThroughSymlinkedClaudeConfigs: ~/.claude.json,
// ~/.claude/settings.json and ~/.claude/CLAUDE.md symlinked into a dotfiles
// tree stay symlinks through install and --remove; the targets get the
// content, and --remove restores the targets' seeded bytes.
func TestInstallAndRemoveWriteThroughSymlinkedClaudeConfigs(t *testing.T) {
	home, dots := t.TempDir(), t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/usr/bin/backstory"}

	const seedClaude = `{"mcpServers":{"other":{"type":"stdio","command":"other","args":[]}}}`
	const seedSettings = `{"theme":"dark"}`
	const seedMD = "# my notes\n"
	claudeT := dotfile(t, dots, "claude.json", paths.ClaudeJSON, seedClaude)
	settingsT := dotfile(t, dots, "settings.json", paths.SettingsJSON, seedSettings)
	mdT := dotfile(t, dots, "CLAUDE.md", paths.ClaudeMD, seedMD)

	if err := install.Install(paths, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for link, target := range map[string]string{paths.ClaudeJSON: claudeT, paths.SettingsJSON: settingsT, paths.ClaudeMD: mdT} {
		assertStillLinked(t, link, target)
	}
	if got := readFile(t, claudeT); !strings.Contains(got, `"backstory"`) || !strings.Contains(got, `"other"`) {
		t.Errorf("claude.json target lacks the backstory entry or lost other: %s", got)
	}
	if got := readFile(t, settingsT); !strings.Contains(got, "hook session-start") || !strings.Contains(got, `"theme"`) {
		t.Errorf("settings.json target lacks the hook or lost theme: %s", got)
	}
	if got := readFile(t, mdT); !strings.HasPrefix(got, seedMD) || !strings.Contains(got, install.StubMarkerBegin) {
		t.Errorf("CLAUDE.md target = %q, want seed + stub", got)
	}
	for _, d := range []string{home, filepath.Join(home, ".claude"), dots} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".backstory-tmp-") {
				t.Errorf("stray temp file %s left in %s", e.Name(), d)
			}
		}
	}

	if err := install.Remove(paths, opts); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for link, target := range map[string]string{paths.ClaudeJSON: claudeT, paths.SettingsJSON: settingsT, paths.ClaudeMD: mdT} {
		assertStillLinked(t, link, target)
	}
	if got := readFile(t, mdT); got != seedMD {
		t.Errorf("CLAUDE.md target after remove = %q, want %q", got, seedMD)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(readFile(t, claudeT)), &root); err != nil {
		t.Fatal(err)
	}
	if servers, _ := root["mcpServers"].(map[string]any); servers["backstory"] != nil || servers["other"] == nil {
		t.Errorf("claude.json target after remove: %v", root)
	}
}

// TestRemoveEmptiesSymlinkedStubFileInsteadOfDeletingTheLink: a symlinked
// CLAUDE.md whose target held nothing before install is emptied by --remove;
// neither the link nor its target is deleted.
func TestRemoveEmptiesSymlinkedStubFileInsteadOfDeletingTheLink(t *testing.T) {
	home, dots := t.TempDir(), t.TempDir()
	paths := install.DefaultPaths(home)
	target := dotfile(t, dots, "CLAUDE.md", paths.ClaudeMD, "")
	if err := install.InstallStub(paths.ClaudeMD); err != nil {
		t.Fatal(err)
	}
	if err := install.Remove(paths, install.Options{Prefix: t.TempDir()}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	assertStillLinked(t, paths.ClaudeMD, target)
	if got := readFile(t, target); got != "" {
		t.Errorf("target after remove = %q, want empty", got)
	}
}

// TestInstallRefusesDanglingSymlinkAndWritesNothing: a symlinked
// settings.json whose target is missing fails the install with
// ErrDanglingSymlink before any file is written.
func TestInstallRefusesDanglingSymlinkAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	if err := os.MkdirAll(filepath.Dir(paths.SettingsJSON), 0o750); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone", "settings.json")
	if err := os.Symlink(missing, paths.SettingsJSON); err != nil {
		t.Fatal(err)
	}
	err := install.Install(paths, install.Options{Prefix: t.TempDir()})
	if !errors.Is(err, install.ErrDanglingSymlink) {
		t.Fatalf("Install err = %v, want ErrDanglingSymlink", err)
	}
	if _, err := os.Lstat(paths.ClaudeJSON); !os.IsNotExist(err) {
		t.Errorf("~/.claude.json was written despite the refusal (lstat err %v)", err)
	}
	if _, err := os.Lstat(paths.ClaudeMD); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md was written despite the refusal (lstat err %v)", err)
	}
	if got, err := os.Readlink(paths.SettingsJSON); err != nil || got != missing {
		t.Errorf("dangling link changed: -> %q (%v)", got, err)
	}
}

// TestRemoveKeepsSymlinkedSkillDirectory: ~/.claude/skills/backstory linked
// into a dotfiles tree loses Backstory's SKILL.md on --remove, but the link
// itself is never pruned (os.Remove would unlink it).
func TestRemoveKeepsSymlinkedSkillDirectory(t *testing.T) {
	home, dots := t.TempDir(), t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir()}
	realDir := filepath.Join(dots, "skill")
	if err := os.MkdirAll(realDir, 0o750); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Dir(paths.SkillPath)
	if err := os.MkdirAll(filepath.Dir(linkDir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	if err := install.Install(paths, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(realDir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not written into the linked dir: %v", err)
	}
	if err := install.Remove(paths, opts); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	assertStillLinked(t, linkDir, realDir)
	if _, err := os.Stat(filepath.Join(realDir, "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("SKILL.md survived remove: %v", err)
	}
}

// TestCodexAndAgentsWriteThroughSymlinks: config.toml, ~/.codex/AGENTS.md and
// ~/.agents/mcp.json symlinked into a dotfiles tree stay symlinks through
// install and --remove, with their seeded bytes restored; an mcp.json that
// held nothing else is emptied to {} rather than deleted.
func TestCodexAndAgentsWriteThroughSymlinks(t *testing.T) {
	home, dots := t.TempDir(), t.TempDir()
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/usr/bin/backstory"}
	cp := install.DefaultCodexPaths(home)
	ap := install.DefaultAgentsPaths(home)
	const seedTOML = "model = \"o3\"\n"
	const seedMD = "# agents\n"
	tomlT := dotfile(t, dots, "config.toml", cp.ConfigTOML, seedTOML)
	mdT := dotfile(t, dots, "AGENTS.md", cp.AgentsMD, seedMD)
	mcpT := dotfile(t, dots, "mcp.json", ap.MCPJSON, "{}\n")

	if err := install.InstallCodex(cp, opts); err != nil {
		t.Fatalf("InstallCodex: %v", err)
	}
	if err := install.InstallAgents(ap, opts); err != nil {
		t.Fatalf("InstallAgents: %v", err)
	}
	assertStillLinked(t, cp.ConfigTOML, tomlT)
	assertStillLinked(t, cp.AgentsMD, mdT)
	assertStillLinked(t, ap.MCPJSON, mcpT)
	if got := readFile(t, tomlT); !strings.Contains(got, "[mcp_servers.backstory]") {
		t.Errorf("config.toml target lacks the backstory table: %q", got)
	}
	if got := readFile(t, mcpT); !strings.Contains(got, `"backstory"`) {
		t.Errorf("mcp.json target lacks the backstory entry: %q", got)
	}

	if err := install.RemoveCodex(cp, opts); err != nil {
		t.Fatalf("RemoveCodex: %v", err)
	}
	if err := install.RemoveAgents(ap, opts); err != nil {
		t.Fatalf("RemoveAgents: %v", err)
	}
	assertStillLinked(t, cp.ConfigTOML, tomlT)
	assertStillLinked(t, cp.AgentsMD, mdT)
	assertStillLinked(t, ap.MCPJSON, mcpT)
	if got := readFile(t, tomlT); got != seedTOML {
		t.Errorf("config.toml target after remove = %q, want %q", got, seedTOML)
	}
	if got := readFile(t, mdT); got != seedMD {
		t.Errorf("AGENTS.md target after remove = %q, want %q", got, seedMD)
	}
	if got := readFile(t, mcpT); got != "{}\n" {
		t.Errorf("mcp.json target after remove = %q, want {}", got)
	}
}

// TestClaudeForeignMCPServerLeftUntouched: an mcpServers.backstory Backstory
// did not write (another shape) survives install and --remove byte for byte;
// install reports it as a foreign-conflict and writes nothing else for the
// harness, and --check reports foreign-conflict.
func TestClaudeForeignMCPServerLeftUntouched(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/usr/bin/backstory"}
	const seed = `{"mcpServers":{"backstory":{"type":"http","url":"https://example.invalid/mcp"}}}`
	mustWriteFile(t, paths.ClaudeJSON, seed)

	err := install.Install(paths, opts)
	if !errors.Is(err, install.ErrForeignConflict) || !install.IsConflict(err) {
		t.Fatalf("Install err = %v, want a foreign-conflict", err)
	}
	if !strings.Contains(err.Error(), install.ItemMCPServer) {
		t.Errorf("conflict does not name the item: %v", err)
	}
	if got := readFile(t, paths.ClaudeJSON); got != seed {
		t.Errorf("~/.claude.json changed by install:\n got %s\nwant %s", got, seed)
	}
	for _, p := range []string{paths.SettingsJSON, paths.ClaudeMD, paths.SkillPath} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s written despite the conflict (lstat err %v)", p, err)
		}
	}
	items, err := install.Check(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := statusMap(t, items)[install.ItemMCPServer]; st != install.StatusForeign {
		t.Errorf("--check mcp-server = %s, want foreign-conflict", st)
	}

	if err := install.Remove(paths, opts); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := readFile(t, paths.ClaudeJSON); got != seed {
		t.Errorf("~/.claude.json changed by remove:\n got %s\nwant %s", got, seed)
	}
}

// TestClaudeOwnOlderMCPServerIsUpdatedAndRemoved: Backstory's own entry
// naming another binary path is recognised as ours, so install rewrites it
// to the current binary and --remove deletes it.
func TestClaudeOwnOlderMCPServerIsUpdatedAndRemoved(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/new/bin/backstory"}
	mustWriteFile(t, paths.ClaudeJSON, `{"keep":1,"mcpServers":{"backstory":{"type":"stdio","command":"/old/bin/backstory","args":["mcp"]}}}`)

	if err := install.Install(paths, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(readFile(t, paths.ClaudeJSON)), &root); err != nil {
		t.Fatal(err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	entry, _ := servers["backstory"].(map[string]any)
	if entry["command"] != "/new/bin/backstory" {
		t.Errorf("mcpServers.backstory.command = %v, want /new/bin/backstory", entry["command"])
	}

	if err := install.Remove(paths, opts); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := readFile(t, paths.ClaudeJSON); strings.Contains(got, "backstory") || !strings.Contains(got, `"keep"`) {
		t.Errorf("~/.claude.json after remove = %s, want only keep", got)
	}
}

// TestRemoveBashrcKeepsSymlinkedTarget: a symlinked ~/.bashrc whose target
// held only Backstory's block is emptied by --remove; the target is not
// deleted out from under the link.
func TestRemoveBashrcKeepsSymlinkedTarget(t *testing.T) {
	home, dots := t.TempDir(), t.TempDir()
	path := install.DefaultBashrcPath(home)
	target := dotfile(t, dots, "bashrc", path, "")
	if err := install.InstallBashrc(path); err != nil {
		t.Fatalf("InstallBashrc: %v", err)
	}
	if err := install.RemoveBashrc(path); err != nil {
		t.Fatalf("RemoveBashrc: %v", err)
	}
	assertStillLinked(t, path, target)
	if got := readFile(t, target); got != "" {
		t.Errorf("bashrc target after remove = %q, want empty", got)
	}
}
