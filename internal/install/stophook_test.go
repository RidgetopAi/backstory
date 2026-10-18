package install_test

import (
	"os"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// The Stop handoff nudge is retired. Install over a settings.json holding an
// older Backstory Stop entry plus a user's own Stop hook removes only the
// Backstory entry (the user's bytes untouched), registers no Stop hook,
// --check no longer lists one, and --remove still leaves user hooks
// byte-identical.
func TestInstallRetiresBackstoryStopHook(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/opt/backstory/bin/backstory"}

	// Canonical json.MarshalIndent form, so the bytes are comparable.
	const userStop = `    "Stop": [
      {
        "hooks": [
          {
            "command": "user-stop-hook",
            "timeout": 5,
            "type": "command"
          }
        ],
        "matcher": ""
      }
    ]`
	seeded := `{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "command": "user-stop-hook",
            "timeout": 5,
            "type": "command"
          }
        ],
        "matcher": ""
      },
      {
        "hooks": [
          {
            "command": "/opt/backstory/bin/backstory hook stop",
            "timeout": 5,
            "type": "command"
          }
        ],
        "matcher": ""
      }
    ]
  }
}
`
	mustWriteFile(t, paths.SettingsJSON, seeded)

	if err := install.Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.SettingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), userStop) {
		t.Errorf("user Stop hook is not byte-identical after install:\n%s", data)
	}
	if strings.Contains(string(data), "hook stop") {
		t.Errorf("a Backstory Stop entry survived install:\n%s", data)
	}

	items, err := install.Check(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if strings.Contains(it.Name, "stop") {
			t.Errorf("--check lists %q; the Stop hook is retired", it.Name)
		}
	}

	// A second install changes nothing.
	if err := install.Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(paths.SettingsJSON)
	if string(again) != string(data) {
		t.Error("second install rewrote settings.json")
	}

	if err := install.Remove(paths, opts); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(paths.SettingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"hooks\": {\n" + userStop + "\n  }\n}\n"
	if string(after) != want {
		t.Errorf("settings.json after remove is not just the user's Stop hook:\n%s", after)
	}
}
