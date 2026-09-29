package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const codexFixtureTOML = `# hand-written by the desk
model = "gpt-5"

[mcp_servers.mandrel]
url = "http://100.1.2.3:8080/mcp"   # tailnet

[projects."/x"]
trust_level = "trusted"

# trailing comment
`

// Canonical json.MarshalIndent form: hooks.json is JSON, so it is rewritten
// through the same marshaler the Claude adapter uses (semantic, not
// byte-for-byte, preservation); a canonical fixture makes bytes comparable.
const codexFixtureHooks = `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "command": "other-tool start",
            "type": "command"
          }
        ],
        "matcher": "startup"
      }
    ]
  }
}
`

func codexFixture(t *testing.T, toml string) (CodexPaths, string) {
	t.Helper()
	home := t.TempDir()
	p := DefaultCodexPaths(home)
	if err := os.MkdirAll(filepath.Dir(p.ConfigTOML), 0o750); err != nil {
		t.Fatal(err)
	}
	if toml != "" {
		writeFile(t, p.ConfigTOML, toml)
	}
	return p, home
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Clause 1: every original byte survives; file minus the marked block is
// the original.
func TestCodexInstallPreservesConfigTOMLBytes(t *testing.T) {
	p, _ := codexFixture(t, codexFixtureTOML)
	if err := InstallCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p.ConfigTOML)
	if !strings.Contains(got, "[mcp_servers.backstory]\ncommand = \"backstory\"\nargs = [\"mcp\"]\n") {
		t.Fatalf("backstory table missing:\n%s", got)
	}
	b, e, ok, err := tomlBlockSpan("x", got)
	if err != nil || !ok {
		t.Fatalf("no block: %v", err)
	}
	if got[:b]+got[e:] != codexFixtureTOML {
		t.Errorf("file minus block != original:\n%s", got)
	}
	if !strings.HasPrefix(got, codexFixtureTOML) {
		t.Errorf("original is not a byte prefix")
	}
}

// Clause 2: second install writes nothing; remove restores pre-install
// bytes or absence.
func TestCodexInstallIdempotentAndRemoveRestores(t *testing.T) {
	for _, tc := range []struct{ name, toml string }{
		{"existing", codexFixtureTOML},
		{"no-trailing-newline", "model = \"x\""},
		{"absent", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := codexFixture(t, tc.toml)
			hooksPre := codexFixtureHooks
			writeFile(t, p.HooksJSON, hooksPre)
			agentsPre := "# my agents\n"
			writeFile(t, p.AgentsMD, agentsPre)
			if tc.name == "absent" {
				_ = os.Remove(p.HooksJSON)
				_ = os.Remove(p.AgentsMD)
				hooksPre, agentsPre = "", ""
			}

			if err := InstallCodex(p, Options{}); err != nil {
				t.Fatal(err)
			}
			files := []string{p.ConfigTOML, p.HooksJSON, p.AgentsMD}
			bytes1, mt1 := map[string]string{}, map[string]time.Time{}
			for _, f := range files {
				bytes1[f] = readFile(t, f)
				st, _ := os.Stat(f)
				mt1[f] = st.ModTime()
			}
			time.Sleep(20 * time.Millisecond)
			if err := InstallCodex(p, Options{}); err != nil {
				t.Fatal(err)
			}
			for _, f := range files {
				st, _ := os.Stat(f)
				if readFile(t, f) != bytes1[f] || !st.ModTime().Equal(mt1[f]) {
					t.Errorf("%s changed on second install", f)
				}
			}

			if err := RemoveCodex(p, Options{}); err != nil {
				t.Fatal(err)
			}
			for f, want := range map[string]string{p.ConfigTOML: tc.toml, p.HooksJSON: hooksPre, p.AgentsMD: agentsPre} {
				b, err := os.ReadFile(f)
				if want == "" {
					if !os.IsNotExist(err) {
						t.Errorf("%s should be absent after remove", f)
					}
					continue
				}
				if err != nil || string(b) != want {
					t.Errorf("%s after remove = %q (%v), want %q", f, b, err, want)
				}
			}
		})
	}
}

// Clause 3: foreign table is reported, untouched, other items still install.
func TestCodexForeignBackstoryTableLeftUntouched(t *testing.T) {
	for _, foreign := range []string{
		"[mcp_servers.backstory]\ncommand = \"other\"\n",
		"[mcp_servers.\"backstory\"]\ncommand = \"other\"\n",
		"[mcp_servers.backstory.env]\nK = \"v\"\n",
		"mcp_servers.backstory.command = \"other\"\n",
	} {
		p, _ := codexFixture(t, codexFixtureTOML+foreign)
		before := readFile(t, p.ConfigTOML)
		err := InstallCodex(p, Options{})
		if !errors.Is(err, ErrForeignConflict) || !strings.Contains(err.Error(), ItemCodexMCPServer) {
			t.Fatalf("err = %v, want foreign-conflict on mcp-server (%q)", err, foreign)
		}
		if readFile(t, p.ConfigTOML) != before {
			t.Errorf("config.toml modified despite foreign conflict")
		}
		items, _ := CheckCodex(p, Options{})
		for _, it := range items {
			want := StatusPresent
			if it.Name == ItemCodexMCPServer {
				want = StatusForeign
			}
			if it.Status != want {
				t.Errorf("%s = %s, want %s", it.Name, it.Status, want)
			}
		}
		if err := RemoveCodex(p, Options{}); err != nil || readFile(t, p.ConfigTOML) != before {
			t.Errorf("remove touched foreign table: %v", err)
		}
	}
}

// Clause 4: hooks.json parses, holds both hooks, keeps pre-existing entries.
func TestCodexHooksJSON(t *testing.T) {
	p, _ := codexFixture(t, "")
	writeFile(t, p.HooksJSON, codexFixtureHooks)
	if err := InstallCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	var root map[string]map[string][]map[string]any
	if err := json.Unmarshal([]byte(readFile(t, p.HooksJSON)), &root); err != nil {
		t.Fatal(err)
	}
	hooks := root["hooks"]
	if len(hooks["SessionStart"]) != 2 || len(hooks["PostToolUse"]) != 1 {
		t.Fatalf("hooks = %v", hooks)
	}
	if !strings.Contains(readFile(t, p.HooksJSON), "other-tool start") {
		t.Errorf("pre-existing hook lost")
	}
	for ev, cmd := range map[string]string{"SessionStart": "backstory hook session-start --harness codex", "PostToolUse": "backstory hook post-tool-use --harness codex"} {
		found := false
		for _, e := range hooks[ev] {
			if strings.Contains(toJSON(t, e), cmd) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing %q", ev, cmd)
		}
	}
	if err := RemoveCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p.HooksJSON); !strings.Contains(got, "other-tool start") || strings.Contains(got, "--harness codex") {
		t.Errorf("after remove: %s", got)
	}
}

func TestCodexMalformedHooksJSONWritesNothing(t *testing.T) {
	p, _ := codexFixture(t, codexFixtureTOML)
	writeFile(t, p.HooksJSON, "{nope")
	if err := InstallCodex(p, Options{}); !errors.Is(err, ErrMalformedCodexHooksJSON) {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, p.ConfigTOML) != codexFixtureTOML {
		t.Errorf("config.toml written despite malformed hooks.json")
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
