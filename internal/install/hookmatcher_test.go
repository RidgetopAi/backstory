package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Install, an upgrade to another --binary, and --check treat a hook group of
// ours that the owner narrowed (or added keys to) as the owner's: only the
// command follows the binary, and --check calls it present, so no later
// install widens the matcher back to every event.

// hookGroups parses path's hooks.<event> array.
func hookGroups(t *testing.T, path, event string) []map[string]any {
	t.Helper()
	var root struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &root); err != nil {
		t.Fatal(err)
	}
	return root.Hooks[event]
}

// ownerHook is a hook group of ours as an owner edited it: a narrowed
// matcher, a longer timeout, and a key on the group and on the hook.
func ownerHook(matcher, command string) map[string]any {
	return map[string]any{
		"matcher": matcher,
		"note":    "owner's",
		"hooks": []any{map[string]any{
			"type": "command", "command": command, "timeout": float64(30), "statusMessage": "owner's",
		}},
	}
}

func writeHooks(t *testing.T, path string, events map[string][]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"hooks": events})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(b))
}

// assertOwnerGroup: event holds exactly one group, it is ownerHook(matcher)
// with command bin+" "+sub.
func assertOwnerGroup(t *testing.T, path, event, matcher, bin, sub string) {
	t.Helper()
	groups := hookGroups(t, path, event)
	if len(groups) != 1 {
		t.Fatalf("%s: %d groups, want 1 (no match-all group added): %#v", event, len(groups), groups)
	}
	if !jsonDeepEqual(groups[0], ownerHook(matcher, bin+" "+sub)) {
		t.Errorf("%s group = %#v\nwant the owner's group with command %q", event, groups[0], bin+" "+sub)
	}
}

func checkStatus(t *testing.T, items []Item) map[string]Item {
	t.Helper()
	m := map[string]Item{}
	for _, it := range items {
		m[it.Name] = it
	}
	return m
}

func TestInstallKeepsOwnersNarrowedClaudeHooks(t *testing.T) {
	const oldBin, newBin = "/old/place/backstory", "/new/place/backstory"
	cases := []struct{ event, item, sub, matcher string }{
		{"SessionStart", ItemSessionStartHook, subSessionStart, "startup"},
		{"PostToolUse", ItemPostToolUseHook, subPostToolUse, "Bash|Edit"},
		{"PostToolUseFailure", ItemPostToolUseFailureHook, subPostToolUseFailure, "Bash"},
	}
	home := t.TempDir()
	paths := DefaultPaths(home)
	events := map[string][]any{}
	for _, c := range cases {
		events[c.event] = []any{ownerHook(c.matcher, oldBin+" "+c.sub)}
	}
	writeHooks(t, paths.SettingsJSON, events)
	prefix := t.TempDir()

	for _, bin := range []string{oldBin, newBin} { // re-install, then upgrade to another --binary
		opts := Options{BinaryPath: bin, Prefix: prefix}
		if err := Install(paths, opts); err != nil {
			t.Fatal(err)
		}
		items, err := Check(paths, opts)
		if err != nil {
			t.Fatal(err)
		}
		st := checkStatus(t, items)
		for _, c := range cases {
			assertOwnerGroup(t, paths.SettingsJSON, c.event, c.matcher, bin, c.sub)
			if st[c.item].Status != StatusPresent || st[c.item].Binary != bin {
				t.Errorf("binary %s: --check %s = %+v, want present at %s", bin, c.item, st[c.item], bin)
			}
		}
	}
	before := readFile(t, paths.SettingsJSON)
	if err := Install(paths, Options{BinaryPath: newBin, Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, paths.SettingsJSON); got != before {
		t.Errorf("a current install rewrote settings.json:\n%s\nwant:\n%s", got, before)
	}
}

// A hook of ours the owner put in a group with a hook of their own: install
// updates it there instead of adding a match-all group, and --remove takes
// out only ours.
func TestInstallKeepsOurHookInsideOwnersSharedGroup(t *testing.T) {
	home := t.TempDir()
	paths := DefaultPaths(home)
	shared := func(bin string) map[string]any {
		return map[string]any{"matcher": "Edit", "hooks": []any{
			map[string]any{"type": "command", "command": "owner-lint"},
			map[string]any{"type": "command", "command": bin + " " + subPostToolUse, "timeout": float64(10)},
		}}
	}
	writeHooks(t, paths.SettingsJSON, map[string][]any{"PostToolUse": {shared("/old/backstory")}})
	opts := Options{BinaryPath: testBinary, Prefix: t.TempDir()}
	if err := Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	if groups := hookGroups(t, paths.SettingsJSON, "PostToolUse"); len(groups) != 1 || !jsonDeepEqual(groups[0], shared(testBinary)) {
		t.Errorf("PostToolUse = %#v, want only the owner's shared group with our command updated", groups)
	}
	items, err := Check(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := checkStatus(t, items)[ItemPostToolUseHook]; st.Status != StatusPresent {
		t.Errorf("--check post-tool-use = %+v, want present", st)
	}
	if err := Remove(paths, opts); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"matcher": "Edit", "hooks": []any{map[string]any{"type": "command", "command": "owner-lint"}}}
	if groups := hookGroups(t, paths.SettingsJSON, "PostToolUse"); len(groups) != 1 || !jsonDeepEqual(groups[0], want) {
		t.Errorf("after remove PostToolUse = %#v, want the owner's hook alone", groups)
	}
}

// --remove takes out a narrowed group of ours and keeps a foreign one.
func TestRemoveDropsNarrowedOursKeepsForeign(t *testing.T) {
	home := t.TempDir()
	paths := DefaultPaths(home)
	foreign := map[string]any{"matcher": "Edit|Write", "hooks": []any{map[string]any{"type": "command", "command": "foreign-lint-hook"}}}
	writeHooks(t, paths.SettingsJSON, map[string][]any{"PostToolUse": {ownerHook("Bash|Edit", testBinary+" "+subPostToolUse), foreign}})
	if err := Remove(paths, Options{BinaryPath: testBinary, Prefix: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if groups := hookGroups(t, paths.SettingsJSON, "PostToolUse"); len(groups) != 1 || !jsonDeepEqual(groups[0], foreign) {
		t.Errorf("after remove PostToolUse = %#v, want the foreign group alone", groups)
	}
}

// A fresh install writes the default matchers; a matcher an earlier release
// wrote as its default moves to the current one.
func TestInstallWritesDefaultMatchersAndUpgradesLegacyDefault(t *testing.T) {
	home := t.TempDir()
	paths := DefaultPaths(home)
	opts := Options{BinaryPath: testBinary, Prefix: t.TempDir()}
	if err := Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	for event, want := range map[string]map[string]any{
		"SessionStart":       wantSessionStartEntry(opts),
		"PostToolUse":        wantPostToolUseEntry(opts),
		"PostToolUseFailure": wantPostToolUseFailureEntry(opts),
	} {
		if groups := hookGroups(t, paths.SettingsJSON, event); len(groups) != 1 || !jsonDeepEqual(groups[0], want) {
			t.Errorf("fresh %s = %#v, want %#v", event, groups, want)
		}
	}

	writeHooks(t, paths.SettingsJSON, map[string][]any{"SessionStart": {hookEntry("startup|resume|clear", "backstory "+subSessionStart, 10)}})
	if err := Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	if groups := hookGroups(t, paths.SettingsJSON, "SessionStart"); len(groups) != 1 || !jsonDeepEqual(groups[0], wantSessionStartEntry(opts)) {
		t.Errorf("legacy SessionStart after install = %#v, want %#v", groups, wantSessionStartEntry(opts))
	}
}

func TestInstallCodexKeepsOwnersNarrowedHooks(t *testing.T) {
	const oldBin, newBin = "/old/place/backstory", "/new/place/backstory"
	p := codexFixture(t, "")
	writeHooks(t, p.HooksJSON, map[string][]any{
		"SessionStart": {ownerHook("startup", oldBin+" "+subSessionStartCodex)},
		"PostToolUse":  {ownerHook("Bash", oldBin+" "+subPostToolUseCodex)},
	})
	writeFile(t, p.ConfigTOML, trustEntries(p))
	for _, bin := range []string{oldBin, newBin} {
		opts := Options{BinaryPath: bin}
		if err := InstallCodex(p, opts); err != nil {
			t.Fatal(err)
		}
		assertOwnerGroup(t, p.HooksJSON, "SessionStart", "startup", bin, subSessionStartCodex)
		assertOwnerGroup(t, p.HooksJSON, "PostToolUse", "Bash", bin, subPostToolUseCodex)
		items, err := CheckCodex(p, opts)
		if err != nil {
			t.Fatal(err)
		}
		st := checkStatus(t, items)
		for _, item := range []string{ItemSessionStartHook, ItemPostToolUseHook} {
			if st[item].Status != StatusPresent {
				t.Errorf("binary %s: --check %s = %+v, want present (narrowed and trusted)", bin, item, st[item])
			}
		}
	}
	if err := RemoveCodex(p, Options{BinaryPath: newBin}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.HooksJSON); os.IsNotExist(err) {
		return // removed with nothing else in it
	}
	for _, event := range []string{"SessionStart", "PostToolUse"} {
		if groups := hookGroups(t, p.HooksJSON, event); len(groups) != 0 {
			t.Errorf("after remove %s = %#v, want none", event, groups)
		}
	}
}
