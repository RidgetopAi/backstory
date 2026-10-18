package install

import (
	"strings"
	"testing"
)

// codexInstalledHooksJSON is the hooks.json `install codex` wrote before the
// Codex post-commit ask existed. The ask is a hook-side change only, so a
// re-install over it must leave it byte-identical — Codex keys its trust
// hashes on the hook command, and a changed command would revoke approval.
const codexInstalledHooksJSON = `{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "command": "backstory hook post-tool-use --harness codex",
            "timeout": 10,
            "type": "command"
          }
        ],
        "matcher": ""
      }
    ],
    "SessionStart": [
      {
        "hooks": [
          {
            "command": "backstory hook session-start --harness codex",
            "timeout": 10,
            "type": "command"
          }
        ],
        "matcher": ""
      }
    ]
  }
}
`

func TestInstallCodexOverCurrentInstallKeepsHooksJSONAndTrust(t *testing.T) {
	p := codexFixture(t, "")
	seed := codexFixtureTOML + "\n" + trustEntries(p)
	writeFile(t, p.HooksJSON, codexInstalledHooksJSON)
	writeFile(t, p.ConfigTOML, seed)

	if err := InstallCodex(p, Options{BinaryPath: "backstory"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p.HooksJSON); got != codexInstalledHooksJSON {
		t.Errorf("hooks.json changed:\n%s\nwant:\n%s", got, codexInstalledHooksJSON)
	}
	if got := readFile(t, p.ConfigTOML); !strings.Contains(got, trustEntries(p)) {
		t.Errorf("config.toml lost the trust entries:\n%s", got)
	}
	st := codexHookStatuses(t, p)
	for _, item := range []string{ItemSessionStartHook, ItemPostToolUseHook} {
		if st[item] != StatusPresent {
			t.Errorf("%s = %s, want present (trust keys still match)", item, st[item])
		}
	}
}
