package install

import (
	"strings"
	"testing"
)

const codexProjectsTrust = "[projects.\"/home/u/work\"]\ntrust_level = \"trusted\"\n"

func codexAppendedTables(p CodexPaths) string {
	return "\n" + trustEntries(p) + codexProjectsTrust
}

// codexInsideBlock inserts text where Codex's toml_edit lands it: after the
// backstory table, before the end marker.
func codexInsideBlock(t *testing.T, p CodexPaths, text string) {
	t.Helper()
	c := readFile(t, p.ConfigTOML)
	i := strings.Index(c, "\n"+CodexTOMLMarkerEnd)
	if i < 0 {
		t.Fatal("no end marker")
	}
	writeFile(t, p.ConfigTOML, c[:i+1]+text+c[i+1:])
}

func installedWithCodexTables(t *testing.T, seed string) (CodexPaths, string) {
	t.Helper()
	p := codexFixture(t, seed)
	if err := InstallCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	tables := codexAppendedTables(p)
	codexInsideBlock(t, p, tables)
	return p, tables
}

// Clause 1: --check reads TOML, so trust tables inside the markers neither
// hide the MCP server nor the hook trust.
func TestCodexCheckWithTablesInsideBlock(t *testing.T) {
	p, _ := installedWithCodexTables(t, codexFixtureTOML)
	st := codexHookStatuses(t, p)
	for _, n := range []string{ItemCodexMCPServer, ItemSessionStartHook, ItemPostToolUseHook} {
		if st[n] != StatusPresent {
			t.Errorf("%s = %s, want present", n, st[n])
		}
	}
}

// Clause 2: reinstall and remove keep Codex's tables byte-identical.
func TestCodexReinstallAndRemoveKeepAppendedTables(t *testing.T) {
	p, tables := installedWithCodexTables(t, codexFixtureTOML)
	if err := InstallCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	c := readFile(t, p.ConfigTOML)
	if !strings.Contains(c, tables) {
		t.Fatalf("reinstall lost appended tables:\n%s", c)
	}
	if n := strings.Count(c, "[mcp_servers.backstory]"); n != 1 {
		t.Errorf("backstory tables = %d", n)
	}
	if st := codexHookStatuses(t, p); st[ItemCodexMCPServer] != StatusPresent || st[ItemSessionStartHook] != StatusPresent {
		t.Errorf("after reinstall: %v", st)
	}
	if err := RemoveCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	c = readFile(t, p.ConfigTOML)
	if !strings.Contains(c, tables) {
		t.Fatalf("remove lost appended tables:\n%s", c)
	}
	if strings.Contains(c, "backstory:") || strings.Contains(c, "mcp_servers.backstory") {
		t.Errorf("remove left backstory lines:\n%s", c)
	}
	if want := codexFixtureTOML + tables; c != want {
		t.Errorf("after remove:\n%q\nwant\n%q", c, want)
	}
}

// Same, when the appended tables land after the end marker, and with an
// older block (different binary path) holding foreign lines.
func TestCodexTablesAfterMarkerAndOlderBlock(t *testing.T) {
	p := codexFixture(t, codexFixtureTOML)
	if err := InstallCodex(p, Options{BinaryPath: "/old/backstory"}); err != nil {
		t.Fatal(err)
	}
	tables := codexAppendedTables(p)
	codexInsideBlock(t, p, codexProjectsTrust)
	writeFile(t, p.ConfigTOML, readFile(t, p.ConfigTOML)+tables)
	if err := InstallCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	c := readFile(t, p.ConfigTOML)
	if !strings.Contains(c, codexProjectsTrust) || !strings.Contains(c, tables) || strings.Contains(c, "/old/backstory") {
		t.Fatalf("bad rewrite:\n%s", c)
	}
	if err := RemoveCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if want := codexFixtureTOML + codexProjectsTrust + tables; readFile(t, p.ConfigTOML) != want {
		t.Errorf("after remove:\n%s", readFile(t, p.ConfigTOML))
	}
}

// Clause 3: unrelated tables still round-trip install then remove exactly.
func TestCodexUnrelatedTablesRoundTrip(t *testing.T) {
	for _, seed := range []string{codexFixtureTOML, "[a]\nb = 1\n[other.c]\nd = \"x\"", "# only a comment\n"} {
		p := codexFixture(t, seed)
		if err := InstallCodex(p, Options{}); err != nil {
			t.Fatal(err)
		}
		if err := RemoveCodex(p, Options{}); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, p.ConfigTOML); got != seed {
			t.Errorf("round trip changed %q -> %q", seed, got)
		}
	}
}
