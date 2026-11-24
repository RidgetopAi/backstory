package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A hook command is Backstory's only when it is exactly `<one path word>
// hook <sub>`. A command that wraps our hook in shell (an environment
// prefix, `||`, `;`) is the owner's: install leaves it as it is and adds no
// default group beside it, --check reports the slot foreign-conflict, and
// --remove keeps it.

const wrapBin = "/opt/backstory/bin/backstory"

// ownerWrappers are commands that run (or skip) our hook through shell the
// owner wrote, for hook subcommand sub.
func ownerWrappers(sub string) map[string]string {
	return map[string]string{
		"env opt-out":       "BACKSTORY_OFF=1 " + wrapBin + " " + sub,
		"or chain":          "true || " + wrapBin + " " + sub,
		"exit then bare":    "exit 0; backstory " + sub,
		"assignment word":   "X=" + wrapBin + " " + sub,
		"redirect suffix":   wrapBin + " " + sub + " 2>/dev/null",
		"quoted then more":  "'" + wrapBin + "' " + sub + "; true",
		"two quoted words":  "'/opt/x' '" + wrapBin + "' " + sub,
		"subshell wrapper":  "(" + wrapBin + " " + sub + ")",
		"tab-separated env": "BACKSTORY_OFF=1\t" + wrapBin + " " + sub,
	}
}

func TestInstallLeavesOwnersHookWrapperAlone(t *testing.T) {
	events := []struct{ event, item, sub string }{
		{"SessionStart", ItemSessionStartHook, subSessionStart},
		{"PostToolUse", ItemPostToolUseHook, subPostToolUse},
		{"PostToolUseFailure", ItemPostToolUseFailureHook, subPostToolUseFailure},
	}
	for _, ev := range events {
		for name, cmd := range ownerWrappers(ev.sub) {
			t.Run(ev.event+"/"+name, func(t *testing.T) {
				home := t.TempDir()
				paths := DefaultPaths(home)
				group := map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": cmd}}}
				writeHooks(t, paths.SettingsJSON, map[string][]any{ev.event: {group}})
				opts := Options{BinaryPath: wrapBin, Prefix: t.TempDir()}
				if err := Install(paths, opts); err != nil {
					t.Fatal(err)
				}
				if groups := hookGroups(t, paths.SettingsJSON, ev.event); len(groups) != 1 || !jsonDeepEqual(groups[0], group) {
					t.Errorf("after install %s = %#v\nwant only the owner's group, unchanged", ev.event, groups)
				}
				items, err := Check(paths, opts)
				if err != nil {
					t.Fatal(err)
				}
				if st := checkStatus(t, items)[ev.item]; st.Status != StatusForeign {
					t.Errorf("--check %s = %+v, want %s", ev.item, st, StatusForeign)
				}
				if err := Remove(paths, opts); err != nil {
					t.Fatal(err)
				}
				if groups := hookGroups(t, paths.SettingsJSON, ev.event); len(groups) != 1 || !jsonDeepEqual(groups[0], group) {
					t.Errorf("after remove %s = %#v\nwant the owner's group kept", ev.event, groups)
				}
			})
		}
	}
}

func TestInstallCodexLeavesOwnersHookWrapperAlone(t *testing.T) {
	events := []struct{ event, item, sub string }{
		{"SessionStart", ItemSessionStartHook, subSessionStartCodex},
		{"PostToolUse", ItemPostToolUseHook, subPostToolUseCodex},
	}
	for _, ev := range events {
		for name, cmd := range ownerWrappers(ev.sub) {
			t.Run(ev.event+"/"+name, func(t *testing.T) {
				p := codexFixture(t, "")
				group := map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": cmd}}}
				writeHooks(t, p.HooksJSON, map[string][]any{ev.event: {group}})
				opts := Options{BinaryPath: wrapBin}
				if err := InstallCodex(p, opts); err != nil {
					t.Fatal(err)
				}
				if groups := hookGroups(t, p.HooksJSON, ev.event); len(groups) != 1 || !jsonDeepEqual(groups[0], group) {
					t.Errorf("after install %s = %#v\nwant only the owner's group, unchanged", ev.event, groups)
				}
				items, err := CheckCodex(p, opts)
				if err != nil {
					t.Fatal(err)
				}
				if st := checkStatus(t, items)[ev.item]; st.Status != StatusForeign {
					t.Errorf("--check %s = %+v, want %s", ev.item, st, StatusForeign)
				}
				if err := RemoveCodex(p, opts); err != nil {
					t.Fatal(err)
				}
				if groups := hookGroups(t, p.HooksJSON, ev.event); len(groups) != 1 || !jsonDeepEqual(groups[0], group) {
					t.Errorf("after remove %s = %#v\nwant the owner's group kept", ev.event, groups)
				}
			})
		}
	}
}

// Our hook in two groups the owner narrowed differently: install updates
// the command in each and keeps both, so neither tool set stops recording.
func TestInstallKeepsOurHookInEveryNarrowedGroup(t *testing.T) {
	const oldBin, newBin = "/old/place/backstory", "/new/place/backstory"
	t.Run("claude", func(t *testing.T) {
		paths := DefaultPaths(t.TempDir())
		writeHooks(t, paths.SettingsJSON, map[string][]any{"PostToolUse": {
			ownerHook("Bash", oldBin+" "+subPostToolUse),
			ownerHook("Edit", oldBin+" "+subPostToolUse),
		}})
		opts := Options{BinaryPath: newBin, Prefix: t.TempDir()}
		if err := Install(paths, opts); err != nil {
			t.Fatal(err)
		}
		want := []any{ownerHook("Bash", newBin+" "+subPostToolUse), ownerHook("Edit", newBin+" "+subPostToolUse)}
		if groups := hookGroups(t, paths.SettingsJSON, "PostToolUse"); !jsonDeepEqual(toAny(groups), want) {
			t.Errorf("PostToolUse = %#v\nwant both narrowed groups with the new command", groups)
		}
	})
	t.Run("codex", func(t *testing.T) {
		p := codexFixture(t, "")
		writeHooks(t, p.HooksJSON, map[string][]any{"PostToolUse": {
			ownerHook("Bash", oldBin+" "+subPostToolUseCodex),
			ownerHook("Edit", oldBin+" "+subPostToolUseCodex),
		}})
		if err := InstallCodex(p, Options{BinaryPath: newBin}); err != nil {
			t.Fatal(err)
		}
		want := []any{ownerHook("Bash", newBin+" "+subPostToolUseCodex), ownerHook("Edit", newBin+" "+subPostToolUseCodex)}
		if groups := hookGroups(t, p.HooksJSON, "PostToolUse"); !jsonDeepEqual(toAny(groups), want) {
			t.Errorf("PostToolUse = %#v\nwant both narrowed groups with the new command", groups)
		}
	})
}

// Codex runs a hook only with a trusted_hash for its own position: a second
// group of ours that is untrusted makes the item not-trusted.
func TestCodexCheckNeedsTrustForEveryGroupOfOurs(t *testing.T) {
	p := codexFixture(t, "")
	writeHooks(t, p.HooksJSON, map[string][]any{
		"SessionStart": {ownerHook("startup", wrapBin+" "+subSessionStartCodex)},
		"PostToolUse": {
			ownerHook("Bash", wrapBin+" "+subPostToolUseCodex),
			ownerHook("Edit", wrapBin+" "+subPostToolUseCodex),
		},
	})
	writeFile(t, p.ConfigTOML, trustEntries(p))
	opts := Options{BinaryPath: wrapBin}
	items, err := CheckCodex(p, opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := checkStatus(t, items)[ItemPostToolUseHook]; st.Status != StatusNotTrusted {
		t.Errorf("second group untrusted: --check = %+v, want %s", st, StatusNotTrusted)
	}
	writeFile(t, p.ConfigTOML, trustEntries(p)+"[hooks.state.\""+p.HooksJSON+":"+codexEventPostToolUse+":1:0\"]\ntrusted_hash = \"sha256:abc\"\n")
	items, err = CheckCodex(p, opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := checkStatus(t, items)[ItemPostToolUseHook]; st.Status != StatusPresent {
		t.Errorf("both groups trusted: --check = %+v, want present", st)
	}
}

func toAny(gs []map[string]any) []any {
	out := make([]any, len(gs))
	for i, g := range gs {
		out[i] = g
	}
	return out
}

// --check present means install changes nothing, and so does
// foreign-conflict (the slot is the owner's); any other status means install
// changes the event's groups, for every fixture below. Each fixture names one binary,
// and install runs with that binary (--check calls an entry for another
// --binary present on purpose; TestCheckRecognisesInstallMadeWithBinary).
func TestHookCheckPresentIffInstallIsNoOp(t *testing.T) {
	const bin = "/opt/fix/backstory"
	type fixture map[string][]any // event -> groups
	cmd := func(sub string) string { return bin + " " + sub }
	claude := map[string]fixture{
		"empty":      {},
		"default":    {"PostToolUse": {wantPostToolUseEntry(Options{BinaryPath: bin})}},
		"narrowed":   {"PostToolUse": {ownerHook("Bash", cmd(subPostToolUse))}},
		"two groups": {"PostToolUse": {ownerHook("Bash", cmd(subPostToolUse)), ownerHook("Edit", cmd(subPostToolUse))}},
		"same group x2": {"PostToolUse": {map[string]any{"matcher": "Bash", "hooks": []any{
			map[string]any{"type": "command", "command": cmd(subPostToolUse)},
			map[string]any{"type": "command", "command": cmd(subPostToolUse)},
		}}}},
		"identical groups": {"PostToolUse": {ownerHook("Bash", cmd(subPostToolUse)), ownerHook("Bash", cmd(subPostToolUse))}},
		"wrapper only":     {"PostToolUse": {ownerHook("Bash", "BACKSTORY_OFF=1 "+cmd(subPostToolUse))}},
		"wrapper and ours": {"PostToolUse": {ownerHook("Bash", "BACKSTORY_OFF=1 "+cmd(subPostToolUse)), ownerHook("Edit", cmd(subPostToolUse))}},
		"legacy matcher":   {"SessionStart": {hookEntry("startup|resume|clear", cmd(subSessionStart), 10)}},
		"foreign only":     {"PostToolUse": {ownerHook("Bash", "lint "+subPostToolUse)}},
	}
	claudeEvents := map[string]string{"SessionStart": ItemSessionStartHook, "PostToolUse": ItemPostToolUseHook, "PostToolUseFailure": ItemPostToolUseFailureHook}
	for name, fx := range claude {
		t.Run("claude/"+name, func(t *testing.T) {
			paths := DefaultPaths(t.TempDir())
			writeHooks(t, paths.SettingsJSON, fx)
			opts := Options{BinaryPath: bin, Prefix: t.TempDir()}
			items, err := Check(paths, opts)
			if err != nil {
				t.Fatal(err)
			}
			st := checkStatus(t, items)
			before := map[string][]map[string]any{}
			for ev := range claudeEvents {
				before[ev] = hookGroups(t, paths.SettingsJSON, ev)
			}
			if err := Install(paths, opts); err != nil {
				t.Fatal(err)
			}
			for ev, item := range claudeEvents {
				unchanged := jsonDeepEqual(toAny(before[ev]), toAny(hookGroups(t, paths.SettingsJSON, ev)))
				leaves := st[item].Status == StatusPresent || st[item].Status == StatusForeign
				if leaves != unchanged {
					t.Errorf("%s: --check %s, install changed it: %v", ev, st[item].Status, !unchanged)
				}
			}
		})
	}
	codex := map[string]fixture{
		"empty":            {},
		"narrowed":         {"PostToolUse": {ownerHook("Bash", cmd(subPostToolUseCodex))}},
		"two groups":       {"PostToolUse": {ownerHook("Bash", cmd(subPostToolUseCodex)), ownerHook("Edit", cmd(subPostToolUseCodex))}},
		"identical groups": {"PostToolUse": {ownerHook("Bash", cmd(subPostToolUseCodex)), ownerHook("Bash", cmd(subPostToolUseCodex))}},
		"wrapper only":     {"PostToolUse": {ownerHook("Bash", "true || "+cmd(subPostToolUseCodex))}},
	}
	codexEvents := map[string]string{"SessionStart": ItemSessionStartHook, "PostToolUse": ItemPostToolUseHook}
	for name, fx := range codex {
		t.Run("codex/"+name, func(t *testing.T) {
			p := codexFixture(t, "")
			writeHooks(t, p.HooksJSON, fx)
			opts := Options{BinaryPath: bin}
			items, err := CheckCodex(p, opts)
			if err != nil {
				t.Fatal(err)
			}
			st := checkStatus(t, items)
			before := map[string][]map[string]any{}
			for ev := range codexEvents {
				before[ev] = hookGroups(t, p.HooksJSON, ev)
			}
			if err := InstallCodex(p, opts); err != nil {
				t.Fatal(err)
			}
			for ev, item := range codexEvents {
				unchanged := jsonDeepEqual(toAny(before[ev]), toAny(hookGroups(t, p.HooksJSON, ev)))
				// not-trusted is "installed, waiting for approval": the
				// file is as install leaves it.
				leaves := st[item].Status == StatusPresent || st[item].Status == StatusNotTrusted || st[item].Status == StatusForeign
				if leaves != unchanged {
					t.Errorf("%s: --check %s, install changed it: %v", ev, st[item].Status, !unchanged)
				}
			}
		})
	}
}

// A sync unit that is a symlink into the owner's dotfiles is the owner's
// (as ops/install.sh treats it): install writes nothing through it, names
// it as kept, and --check does not call it something install rewrites.
func TestInstallSyncTimerKeepsSymlinkedOlderUnit(t *testing.T) {
	home, dots := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	oldSM, oldExe := ServiceManager, executablePath
	ServiceManager = func(...string) error { return nil }
	executablePath = func() (string, error) { return "/opt/tmp/backstory", nil }
	t.Cleanup(func() { ServiceManager, executablePath = oldSM, oldExe })

	dir := SyncUnitDir(home)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dots, SyncServiceName)
	if err := os.WriteFile(target, []byte(SyncServiceUnit), 0o600); err != nil { // the older %h/.local/bin version
		t.Fatal(err)
	}
	link := filepath.Join(dir, SyncServiceName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, SyncTimerName)
	if err := os.Symlink(filepath.Join(dots, "missing.timer"), dangling); err != nil {
		t.Fatal(err)
	}

	kept, err := InstallSyncTimer(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != SyncServiceUnit {
		t.Errorf("install wrote through the symlink into the owner's file:\n%s", got)
	}
	if _, err := os.Lstat(filepath.Join(dots, "missing.timer")); !os.IsNotExist(err) {
		t.Errorf("install created the dangling symlink's target (lstat err %v)", err)
	}
	if strings.Join(kept, "\n") != dangling+"\n"+link && strings.Join(kept, "\n") != link+"\n"+dangling {
		t.Errorf("kept = %q, want both symlinked units named", kept)
	}
	if s := SyncTimerStatus(home); s != StatusEdited {
		t.Errorf("--check = %s, want %s", s, StatusEdited)
	}
	for _, p := range kept {
		if n := EditedNotice("sync", p, false); !strings.Contains(n, "symlink") {
			t.Errorf("notice for %s does not say it is a symlink: %q", p, n)
		}
	}
}
