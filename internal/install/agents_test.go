package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Canonical json.MarshalIndent form so bytes are comparable.
const agentsFixtureMCP = `{
  "mcpServers": {
    "other": {
      "args": [
        "--flag",
        "x"
      ],
      "command": "other-server",
      "env": {
        "K": "v"
      }
    }
  }
}
`

func agentsFixture(t *testing.T, mcp, md string) AgentsPaths {
	t.Helper()
	p := DefaultAgentsPaths(t.TempDir())
	for path, s := range map[string]string{p.MCPJSON: mcp, p.AgentsMD: md} {
		if s == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, s)
	}
	return p
}

// Clause 1: other server preserved, exactly one stub line added.
func TestAgentsInstallPreservesOtherServerAndAddsOneStub(t *testing.T) {
	p := agentsFixture(t, agentsFixtureMCP, "# my notes\n")
	if err := InstallAgents(p, Options{}); err != nil {
		t.Fatal(err)
	}
	root, _, err := loadJSONObject(p.MCPJSON, ErrMalformedAgentsMCPJSON)
	if err != nil {
		t.Fatal(err)
	}
	servers := root["mcpServers"].(map[string]any)
	var orig map[string]any
	origRoot, _, _ := loadJSONObject(writeTemp(t, agentsFixtureMCP), ErrMalformedAgentsMCPJSON)
	orig = origRoot["mcpServers"].(map[string]any)
	if !jsonDeepEqual(servers["other"], orig["other"]) {
		t.Errorf("other server changed: %v", servers["other"])
	}
	if !jsonDeepEqual(servers[MCPServerName], map[string]any{"command": "backstory", "args": []any{"mcp"}}) {
		t.Errorf("backstory entry = %v", servers[MCPServerName])
	}
	md := readFile(t, p.AgentsMD)
	if n := strings.Count(md, AgentsStubLine); n != 1 {
		t.Errorf("stub line count = %d, want 1:\n%s", n, md)
	}
	if !strings.HasPrefix(md, "# my notes\n") {
		t.Errorf("existing AGENTS.md content lost: %q", md)
	}
}

func writeTemp(t *testing.T, s string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.json")
	writeFile(t, path, s)
	return path
}

// Clause 2: idempotent second install; remove restores bytes.
func TestAgentsSecondInstallWritesNothingAndRemoveRestores(t *testing.T) {
	p := agentsFixture(t, agentsFixtureMCP, "# my notes\n")
	if err := InstallAgents(p, Options{}); err != nil {
		t.Fatal(err)
	}
	mcp1, md1 := readFile(t, p.MCPJSON), readFile(t, p.AgentsMD)
	if err := InstallAgents(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p.MCPJSON) != mcp1 || readFile(t, p.AgentsMD) != md1 {
		t.Error("second install changed bytes")
	}
	if strings.Count(readFile(t, p.AgentsMD), AgentsStubLine) != 1 {
		t.Error("stub duplicated on second install")
	}
	if err := RemoveAgents(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p.MCPJSON); got != agentsFixtureMCP {
		t.Errorf("mcp.json after remove:\n%s", got)
	}
	if got := readFile(t, p.AgentsMD); got != "# my notes\n" {
		t.Errorf("AGENTS.md after remove: %q", got)
	}
}

func TestAgentsRemoveRestoresAbsence(t *testing.T) {
	p := agentsFixture(t, "", "")
	if err := InstallAgents(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAgents(p, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p.MCPJSON, p.AgentsMD} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s should be absent after remove: %v", f, err)
		}
	}
}

func TestAgentsForeignBackstoryEntryLeftUntouched(t *testing.T) {
	const foreign = "{\n  \"mcpServers\": {\n    \"backstory\": {\n      \"command\": \"something-else\"\n    }\n  }\n}\n"
	p := agentsFixture(t, foreign, "")
	err := InstallAgents(p, Options{})
	if !errors.Is(err, ErrForeignConflict) {
		t.Fatalf("err = %v, want ErrForeignConflict", err)
	}
	if readFile(t, p.MCPJSON) != foreign {
		t.Error("foreign entry modified by install")
	}
	items, _ := CheckAgents(p, Options{})
	if items[0].Status != StatusForeign {
		t.Errorf("status = %v, want foreign", items[0].Status)
	}
	if err := RemoveAgents(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p.MCPJSON) != foreign {
		t.Error("foreign entry modified by remove")
	}
}

func TestAgentsMalformedMCPJSONWritesNothing(t *testing.T) {
	p := agentsFixture(t, "{nope", "")
	if err := InstallAgents(p, Options{}); !errors.Is(err, ErrMalformedAgentsMCPJSON) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(p.AgentsMD); !os.IsNotExist(err) {
		t.Error("AGENTS.md written despite malformed mcp.json")
	}
}

func TestAgentsCheckStatuses(t *testing.T) {
	p := agentsFixture(t, "", "")
	items, err := CheckAgents(p, Options{})
	if err != nil || items[0].Status != StatusAbsent || items[1].Status != StatusAbsent {
		t.Fatalf("pre: %v %v", items, err)
	}
	if err := InstallAgents(p, Options{}); err != nil {
		t.Fatal(err)
	}
	items, _ = CheckAgents(p, Options{})
	if items[0].Status != StatusPresent || items[1].Status != StatusPresent {
		t.Errorf("post: %v", items)
	}
}
