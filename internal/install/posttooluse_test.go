package install_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// TestPostToolUseHookAddedKeepsForeignEntryAndRemoveOnlyRemovesOwn is task
// 04b1cb40's DONE WHEN clause 5: `backstory install claude` adds the
// PostToolUse hook, keeps a foreign PostToolUse hook already in
// settings.json, --check reports it present, and --remove removes only
// Backstory's entries.
func TestPostToolUseHookAddedKeepsForeignEntryAndRemoveOnlyRemovesOwn(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir()}

	seededSettingsJSON := `{"hooks":{"PostToolUse":[{"matcher":"Edit|Write","hooks":[{"type":"command","command":"foreign-lint-hook","timeout":5}]}]}}`
	mustWriteFile(t, paths.SettingsJSON, seededSettingsJSON)

	items, err := install.Check(paths, opts)
	if err != nil {
		t.Fatalf("Check before install: %v", err)
	}
	if status := statusMap(t, items)[install.ItemPostToolUseHook]; status != install.StatusAbsent {
		t.Errorf("post-tool-use-hook status before install = %s, want absent (a foreign matcher is not backstory's entry)", status)
	}

	if err := install.Install(paths, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}

	data, err := os.ReadFile(paths.SettingsJSON)
	if err != nil {
		t.Fatalf("read settings.json after install: %v", err)
	}
	var settings struct {
		Hooks struct {
			PostToolUse []map[string]any `json:"PostToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json after install: %v", err)
	}
	if len(settings.Hooks.PostToolUse) != 2 {
		t.Fatalf("hooks.PostToolUse has %d entries after install, want 2 (foreign kept + backstory's own): %#v",
			len(settings.Hooks.PostToolUse), settings.Hooks.PostToolUse)
	}
	foundForeign, foundOwn := false, false
	for _, e := range settings.Hooks.PostToolUse {
		if e["matcher"] == "Edit|Write" {
			foundForeign = true
		}
		if e["matcher"] == install.HookMatcherPostToolUse {
			hooks, _ := e["hooks"].([]any)
			if len(hooks) == 1 {
				if h, ok := hooks[0].(map[string]any); ok && h["command"] == install.HookCommandPostToolUse {
					foundOwn = true
				}
			}
		}
	}
	if !foundForeign {
		t.Errorf("foreign PostToolUse entry (matcher Edit|Write) missing after install: %#v", settings.Hooks.PostToolUse)
	}
	if !foundOwn {
		t.Errorf("backstory's own PostToolUse entry missing after install: %#v", settings.Hooks.PostToolUse)
	}

	items, err = install.Check(paths, opts)
	if err != nil {
		t.Fatalf("Check after install: %v", err)
	}
	if status := statusMap(t, items)[install.ItemPostToolUseHook]; status != install.StatusPresent {
		t.Errorf("post-tool-use-hook status after install = %s, want present", status)
	}

	if err := install.Remove(paths, opts); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	data, err = os.ReadFile(paths.SettingsJSON)
	if err != nil {
		t.Fatalf("read settings.json after remove: %v", err)
	}
	var afterRemove struct {
		Hooks struct {
			PostToolUse []map[string]any `json:"PostToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &afterRemove); err != nil {
		t.Fatalf("parse settings.json after remove: %v", err)
	}
	if len(afterRemove.Hooks.PostToolUse) != 1 {
		t.Fatalf("hooks.PostToolUse has %d entries after remove, want 1 (only the foreign entry survives): %#v",
			len(afterRemove.Hooks.PostToolUse), afterRemove.Hooks.PostToolUse)
	}
	if afterRemove.Hooks.PostToolUse[0]["matcher"] != "Edit|Write" {
		t.Errorf("surviving PostToolUse entry after remove = %#v, want the foreign matcher Edit|Write entry untouched",
			afterRemove.Hooks.PostToolUse[0])
	}
}
