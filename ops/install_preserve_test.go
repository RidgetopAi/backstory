package ops

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// What make install must leave alone on a re-run: a panel `omarchy plugin add`
// installed (a git checkout), a Hyprland file the user edited, and a panel the
// user disabled.

// gitPanel makes dir a git checkout holding the repo's manifest and a local
// edit, as `omarchy plugin add` leaves it once the user has changed a file.
func gitPanel(t *testing.T, dir string) map[string]string {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatalf("git init: %v\n%s", err, out)
	}
	manifest, err := os.ReadFile(filepath.Join("..", "panel", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"manifest.json": manifest, "Panel.qml": []byte("// my local edit\n")} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
			t.Fatal(err)
		}
	}
	return treeMap(t, dir)
}

func sameTree(t *testing.T, what, dir string, want map[string]string) {
	t.Helper()
	got := treeMap(t, dir)
	if len(got) != len(want) {
		t.Errorf("%s changed: %d entries, want %d", what, len(got), len(want))
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s: %s changed or gone", what, k)
		}
	}
}

func TestInstallAndUninstallLeaveAGitManagedPanelAlone(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	before := gitPanel(t, f.panelDir())

	out := f.mustMake("install")
	sameTree(t, "git-managed panel after install", f.panelDir(), before)
	mustNotExist(t, f.panelBackup())
	mustExist(t, f.path(".local", "bin", "backstory"))
	if !strings.Contains(out, "omarchy plugin update ridgetopai.backstory") {
		t.Errorf("install does not point at `omarchy plugin update`:\n%s", out)
	}
	if c := f.calls(); strings.Contains(c, "omarchy plugin enable") || strings.Contains(c, "omarchy-restart-shell") {
		t.Errorf("install enabled or reloaded a panel it did not install:\n%s", c)
	}

	f.resetCalls()
	out = f.mustMake("uninstall")
	sameTree(t, "git-managed panel after uninstall", f.panelDir(), before)
	mustNotExist(t, f.path(".local", "bin", "backstory"))
	if !strings.Contains(out, "omarchy plugin remove ridgetopai.backstory") {
		t.Errorf("uninstall does not point at `omarchy plugin remove`:\n%s", out)
	}
	if strings.Contains(f.calls(), "omarchy plugin disable ridgetopai.backstory") {
		t.Errorf("uninstall disabled a panel it does not own:\n%s", f.calls())
	}
}

// An older make install moved an `omarchy plugin` checkout to the hidden
// backup; neither a later install nor uninstall may delete it.
func TestInstallAndUninstallKeepAGitCheckoutInThePanelBackup(t *testing.T) {
	f := newInstallFixture(t)
	f.mustMake("install")
	before := gitPanel(t, f.panelBackup())

	f.flag("active", true)
	out := f.mustMake("install")
	sameTree(t, "git checkout in the panel backup after install", f.panelBackup(), before)
	assertPanelRuntimeOnly(t, f.panelDir())
	if !strings.Contains(out, "leaving "+f.panelBackup()) {
		t.Errorf("install does not name the backup it left:\n%s", out)
	}

	f.mustMake("uninstall")
	sameTree(t, "git checkout in the panel backup after uninstall", f.panelBackup(), before)
	mustNotExist(t, f.panelDir())
}

func TestInstallKeepsAUserEditedHyprlandFile(t *testing.T) {
	f := newInstallFixture(t)
	f.mustMake("install")
	hypr := f.path(".config", "hypr", "backstory.lua")
	shipped, _ := os.ReadFile("hyprland/backstory.lua")
	// The edit the file itself invites: a different BACKSTORY_BIND.
	edited := bytes.Replace(shipped, []byte(`BACKSTORY_BIND = "CTRL + SHIFT + B"`), []byte(`BACKSTORY_BIND = "SUPER + ALT + B"`), 1)
	if bytes.Equal(edited, shipped) {
		t.Fatal("hyprland/backstory.lua has no BACKSTORY_BIND line to edit")
	}
	if err := os.WriteFile(hypr, edited, 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}

	f.flag("active", true)
	out := f.mustMake("install")
	if got, _ := os.ReadFile(hypr); !bytes.Equal(got, edited) { //nolint:gosec // fixture path under t.TempDir
		t.Error("install overwrote the user's edited backstory.lua")
	}
	if got, err := os.ReadFile(hypr + ".new"); err != nil || !bytes.Equal(got, shipped) { //nolint:gosec // fixture path under t.TempDir
		t.Errorf("backstory.lua.new is not this version's file: %v", err)
	}
	if !strings.Contains(out, hypr+".new") {
		t.Errorf("install does not say where the new file went:\n%s", out)
	}

	f.mustMake("uninstall")
	mustNotExist(t, hypr+".new")
}

// A symlinked backstory.lua (dotfiles) stays a symlink, whether its target
// matches this version, differs, or is missing.
func TestInstallKeepsASymlinkedHyprlandFile(t *testing.T) {
	shipped, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		content []byte // nil: dangling link
		wantNew bool
	}{
		{"matching", shipped, false},
		{"edited", append(append([]byte{}, shipped...), "-- mine\n"...), true},
		{"dangling", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newInstallFixture(t)
			hypr := f.path(".config", "hypr", "backstory.lua")
			target := f.path("dotfiles", "backstory.lua")
			if err := os.MkdirAll(filepath.Dir(hypr), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
				t.Fatal(err)
			}
			if tc.content != nil {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
					t.Fatal(err)
				}
				if err := os.WriteFile(target, tc.content, 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, hypr); err != nil {
				t.Fatal(err)
			}
			f.mustMake("install")
			if got, err := os.Readlink(hypr); err != nil || got != target {
				t.Fatalf("backstory.lua is no longer the symlink to %s (readlink %q, %v)", target, got, err)
			}
			if tc.content != nil {
				if got, _ := os.ReadFile(target); !bytes.Equal(got, tc.content) { //nolint:gosec // fixture path under t.TempDir
					t.Error("install changed the symlink's target")
				}
			}
			_, err := os.Stat(hypr + ".new")
			if gotNew := err == nil; gotNew != tc.wantNew {
				t.Errorf("backstory.lua.new written = %v, want %v", gotNew, tc.wantNew)
			}
		})
	}
}

func TestInstallUpdatesAPreviouslyShippedHyprlandFile(t *testing.T) {
	versions := shippedHyprVersions(t)
	if len(versions) < 2 {
		t.Fatalf("git history has %d versions of ops/hyprland/backstory.lua, need an older one", len(versions))
	}
	f := newInstallFixture(t)
	hypr := f.path(".config", "hypr", "backstory.lua")
	if err := os.MkdirAll(filepath.Dir(hypr), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	if err := os.WriteFile(hypr, versions[1], 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	f.mustMake("install")
	shipped, _ := os.ReadFile("hyprland/backstory.lua")
	if got, _ := os.ReadFile(hypr); !bytes.Equal(got, shipped) { //nolint:gosec // fixture path under t.TempDir
		t.Error("an unedited older backstory.lua was not updated")
	}
	mustNotExist(t, hypr+".new")
}

// shippedHyprVersions is every committed ops/hyprland/backstory.lua, newest first.
func shippedHyprVersions(t *testing.T) [][]byte {
	t.Helper()
	out, err := exec.Command("git", "-C", "..", "log", "--format=%H", "--follow", "--", "ops/hyprland/backstory.lua").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	var versions [][]byte
	for _, c := range strings.Fields(string(out)) {
		b, err := exec.Command("git", "-C", "..", "show", c+":ops/hyprland/backstory.lua").Output() //nolint:gosec // commit ids from git log
		if err != nil {
			t.Fatalf("git show %s: %v", c, err)
		}
		versions = append(versions, b)
	}
	return versions
}

// The installer's list of shipped versions covers every committed version and
// the working tree's: a change to backstory.lua without its hash would make
// the next upgrade treat that version, unedited, as the user's.
func TestInstallerKnowsEveryShippedHyprlandFile(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^HYPR_SHIPPED_SHA256='\n((?:[0-9a-f]{64}\n)+)'$`).FindSubmatch(script)
	if m == nil {
		t.Fatal("install.sh has no HYPR_SHIPPED_SHA256 block of sha256 lines")
	}
	known := map[string]bool{}
	for _, h := range strings.Fields(string(m[1])) {
		known[h] = true
	}
	current, _ := os.ReadFile("hyprland/backstory.lua")
	for i, v := range append([][]byte{current}, shippedHyprVersions(t)...) {
		sum := sha256.Sum256(v)
		if h := hex.EncodeToString(sum[:]); !known[h] {
			t.Errorf("version %d of ops/hyprland/backstory.lua (sha256 %s) is not in install.sh's HYPR_SHIPPED_SHA256", i, h)
		}
	}
}

func TestInstallUpgradeLeavesADisabledPanelDisabled(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	f.mustMake("install")
	if n := strings.Count(f.calls(), enableCall); n != 1 {
		t.Fatalf("first install enabled %d times, want 1:\n%s", n, f.calls())
	}
	state := f.log + ".plugin-enabled"
	mustExist(t, state)
	_ = os.Remove(state) // the user ran `omarchy plugin disable ridgetopai.backstory`

	f.flag("active", true)
	f.resetCalls()
	out := f.mustMake("install")
	if strings.Contains(f.calls(), enableCall) {
		t.Errorf("upgrade re-enabled a panel the user disabled:\n%s", f.calls())
	}
	mustNotExist(t, state)
	assertPanelRuntimeOnly(t, f.panelDir())
	if strings.Contains(out, "NOT ENABLED") {
		t.Errorf("upgrade of a disabled panel printed the failure block:\n%s", out)
	}
}
