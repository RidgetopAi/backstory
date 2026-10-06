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
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/opt/backstory/bin/backstory"}

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
				if h, ok := hooks[0].(map[string]any); ok && h["command"] == opts.BinaryPath+" hook post-tool-use" {
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

// TestPostToolUseFailureHookInstalledCheckedAndRemoved is task b2613e7a's
// clause 1: install writes a PostToolUseFailure entry for the same absolute
// binary, --check reports it, --remove removes it (and only it).
func TestPostToolUseFailureHookInstalledCheckedAndRemoved(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir(), BinaryPath: "/opt/backstory/bin/backstory"}
	mustWriteFile(t, paths.SettingsJSON, `{"hooks":{"PostToolUseFailure":[{"matcher":"","hooks":[{"type":"command","command":"foreign-fail-hook"}]}]}}`)

	items, err := install.Check(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := statusMap(t, items)[install.ItemPostToolUseFailureHook]; got != install.StatusAbsent {
		t.Errorf("before install status = %s, want absent", got)
	}
	if err := install.Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	read := func() []map[string]any {
		data, err := os.ReadFile(paths.SettingsJSON)
		if err != nil {
			t.Fatal(err)
		}
		var st struct {
			Hooks struct {
				F []map[string]any `json:"PostToolUseFailure"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(data, &st); err != nil {
			t.Fatal(err)
		}
		return st.Hooks.F
	}
	entries := read()
	if len(entries) != 2 {
		t.Fatalf("PostToolUseFailure has %d entries after install, want 2: %#v", len(entries), entries)
	}
	own := false
	for _, e := range entries {
		hooks, _ := e["hooks"].([]any)
		if h, ok := hooks[0].(map[string]any); ok && h["command"] == opts.BinaryPath+" hook post-tool-use-failure" {
			own = true
		}
	}
	if !own {
		t.Errorf("own PostToolUseFailure entry missing: %#v", entries)
	}
	items, _ = install.Check(paths, opts)
	if got := statusMap(t, items)[install.ItemPostToolUseFailureHook]; got != install.StatusPresent {
		t.Errorf("after install status = %s, want present", got)
	}
	if err := install.Remove(paths, opts); err != nil {
		t.Fatal(err)
	}
	entries = read()
	if len(entries) != 1 {
		t.Fatalf("after remove %d entries, want only the foreign one: %#v", len(entries), entries)
	}
}
