package install_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// TestStopHookInstalledCheckedAndRemoved: `install claude` registers a Stop
// hook for the absolute binary, --check reports it, and --remove removes it
// leaving every user hook (a foreign Stop group included) byte-identical.
func TestStopHookInstalledCheckedAndRemoved(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/opt/backstory/bin/backstory"}

	// Canonical json.MarshalIndent form, so the bytes are comparable.
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
      }
    ]
  }
}
`
	mustWriteFile(t, paths.SettingsJSON, seeded)

	items, err := install.Check(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := statusMap(t, items)[install.ItemStopHook]; got != install.StatusAbsent {
		t.Errorf("before install status = %s, want absent", got)
	}

	if err := install.Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.SettingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Hooks struct {
			Stop []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, g := range st.Hooks.Stop {
		for _, h := range g.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	if len(cmds) != 2 || cmds[0] != "user-stop-hook" || cmds[1] != "/opt/backstory/bin/backstory hook stop" {
		t.Errorf("Stop commands after install = %q, want the user hook then the absolute-binary hook stop", cmds)
	}

	items, _ = install.Check(paths, opts)
	if got := statusMap(t, items)[install.ItemStopHook]; got != install.StatusPresent {
		t.Errorf("after install status = %s, want present", got)
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
	if string(after) != seeded {
		t.Errorf("settings.json after remove differs from the user's original:\n%s", after)
	}
	if strings.Contains(string(after), "hook stop") {
		t.Error("backstory's Stop hook survived remove")
	}
	items, _ = install.Check(paths, opts)
	if got := statusMap(t, items)[install.ItemStopHook]; got != install.StatusAbsent {
		t.Errorf("after remove status = %s, want absent", got)
	}
}
