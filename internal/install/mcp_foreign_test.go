package install_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// TestClaudeForeignNpxMCPServerUnderBackstoryKey: an entry with Backstory's
// shape (stdio, args ["mcp"]) but a command that is not a backstory binary is
// foreign: install reports it and leaves it, --check says foreign-conflict,
// --remove keeps it.
func TestClaudeForeignNpxMCPServerUnderBackstoryKey(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/usr/bin/backstory"}
	const seed = `{"mcpServers":{"backstory":{"type":"stdio","command":"npx","args":["mcp"]}}}`
	mustWriteFile(t, paths.ClaudeJSON, seed)

	if err := install.Install(paths, opts); !errors.Is(err, install.ErrForeignConflict) {
		t.Fatalf("Install err = %v, want a foreign-conflict", err)
	}
	if got := readFile(t, paths.ClaudeJSON); got != seed {
		t.Errorf("~/.claude.json changed by install: %s", got)
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
		t.Errorf("~/.claude.json changed by remove: %s", got)
	}
}

// TestCodexForeignNpxCommandInsideBackstoryBlock: a [mcp_servers.backstory]
// table inside Backstory's markers whose command was changed to a
// non-backstory binary is foreign: install refuses and writes nothing,
// --check says foreign-conflict, --remove keeps the table. Backstory's own
// table under an older backstory path is still rewritten.
func TestCodexForeignNpxCommandInsideBackstoryBlock(t *testing.T) {
	home := t.TempDir()
	cp := install.DefaultCodexPaths(home)
	old := install.Options{Prefix: t.TempDir(), BinaryPath: "/old/bin/backstory"}
	cur := install.Options{Prefix: old.Prefix, BinaryPath: "/new/bin/backstory"}

	if err := install.InstallCodex(cp, old); err != nil {
		t.Fatalf("InstallCodex(old): %v", err)
	}
	if err := install.InstallCodex(cp, cur); err != nil {
		t.Fatalf("InstallCodex(cur) over our older table: %v", err)
	}
	toml := readFile(t, cp.ConfigTOML)
	if !strings.Contains(toml, `command = "/new/bin/backstory"`) || strings.Contains(toml, "/old/bin/") {
		t.Fatalf("older backstory table not rewritten:\n%s", toml)
	}

	foreign := strings.Replace(toml, `command = "/new/bin/backstory"`, `command = "npx"`, 1)
	if err := os.WriteFile(cp.ConfigTOML, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := install.InstallCodex(cp, cur); !errors.Is(err, install.ErrForeignConflict) {
		t.Fatalf("InstallCodex err = %v, want a foreign-conflict", err)
	}
	if got := readFile(t, cp.ConfigTOML); got != foreign {
		t.Errorf("config.toml changed by install:\n%s", got)
	}
	items, err := install.CheckCodex(cp, cur)
	if err != nil {
		t.Fatal(err)
	}
	if st := statusMap(t, items)[install.ItemCodexMCPServer]; st != install.StatusForeign {
		t.Errorf("--check codex mcp-server = %s, want foreign-conflict", st)
	}
	if err := install.RemoveCodex(cp, cur); err != nil {
		t.Fatalf("RemoveCodex: %v", err)
	}
	if got := readFile(t, cp.ConfigTOML); !strings.Contains(got, "[mcp_servers.backstory]\ncommand = \"npx\"\nargs = [\"mcp\"]\n") {
		t.Errorf("remove dropped the foreign table:\n%s", got)
	}
}
