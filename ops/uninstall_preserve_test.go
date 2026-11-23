package ops

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// What make uninstall must leave alone: a sync unit or a backstory.lua the
// user edited or symlinked. It deletes only files that hold a version
// Backstory shipped.

// scriptHashes is install.sh's single-quoted block of sha256 lines named name.
func scriptHashes(t *testing.T, script []byte, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + name + `='\n((?:[0-9a-f]{64}\n)+)'$`).FindSubmatch(script)
	if m == nil {
		t.Fatalf("install.sh has no %s block of sha256 lines", name)
	}
	return strings.Fields(string(m[1]))
}

// scriptUnrender is install.sh's SYNC_EXECSTART_UNRENDER sed program (a
// single-quoted sh string, which holds no quote of its own).
func scriptUnrender(t *testing.T, script []byte) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^SYNC_EXECSTART_UNRENDER='([^']*)'$`).FindSubmatch(script)
	if m == nil {
		t.Fatal("install.sh has no SYNC_EXECSTART_UNRENDER")
	}
	return string(m[1])
}

// The shell's sync-unit lists are internal/install's: `make uninstall` and
// `backstory install sync --remove` agree on what Backstory shipped. (The Go
// lists are held to git history by internal/install's managed_test.go.)
func TestInstallerSyncUnitHashesAreManagedGos(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"SYNC_SERVICE_SHIPPED_SHA256": "units/" + install.SyncServiceName,
		"SYNC_TIMER_SHIPPED_SHA256":   "units/" + install.SyncTimerName,
	} {
		got, want := scriptHashes(t, script, name), install.ShippedSHA256(source)
		slices.Sort(got)
		slices.Sort(want)
		if len(want) == 0 || !slices.Equal(got, want) {
			t.Errorf("install.sh %s = %v, internal/install %s = %v", name, got, source, want)
		}
	}
}

// The shell's sed program undoes the ExecStart binary exactly as
// `backstory install sync` writes it, for any binary path.
func TestInstallerUnrendersTheSyncServiceAsInstallSyncWritesIt(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	prog := scriptUnrender(t, script)
	for _, bin := range []string{"/usr/bin/backstory", "/home/u/.local/bin/backstory", "/tmp/my 50%/back\"story", `/a\b/backstory`} {
		cmd := exec.Command("sed", "-E", prog) //nolint:gosec // the program is install.sh's own
		cmd.Stdin = strings.NewReader(install.SyncServiceUnitFor(bin))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("sed: %v", err)
		}
		if string(out) != install.SyncServiceUnit {
			t.Errorf("binary %q: unrendered unit\n%s\nwant\n%s", bin, out, install.SyncServiceUnit)
		}
	}
}

func writeFixtureFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
}

func TestUninstallKeepsEditedSyncUnitsAndRemovesShippedOnes(t *testing.T) {
	f := newInstallFixture(t)
	f.mustMake("install")
	units := f.path(".config", "systemd", "user")
	timer, svc := filepath.Join(units, install.SyncTimerName), filepath.Join(units, install.SyncServiceName)
	edited := strings.Replace(install.SyncTimerUnit, "[Timer]", "[Timer]\n# mine", 1)
	if edited == install.SyncTimerUnit {
		t.Fatal("the timer unit has no [Timer] section to edit")
	}
	writeFixtureFile(t, timer, []byte(edited))
	writeFixtureFile(t, svc, []byte(install.SyncServiceUnitFor("/opt/my dir/backstory")))

	out := f.mustMake("uninstall")
	if got, _ := os.ReadFile(timer); string(got) != edited { //nolint:gosec // fixture path under t.TempDir
		t.Error("uninstall deleted the edited sync timer")
	}
	if !strings.Contains(out, "kept your edited "+timer) {
		t.Errorf("uninstall does not name the kept timer:\n%s", out)
	}
	mustNotExist(t, svc)
}

func TestUninstallKeepsAnEditedOrSymlinkedHyprlandFile(t *testing.T) {
	shipped, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatal(err)
	}
	edited := append(append([]byte{}, shipped...), "-- mine\n"...)

	t.Run("edited", func(t *testing.T) {
		f := newInstallFixture(t)
		f.mustMake("install")
		hypr := f.path(".config", "hypr", "backstory.lua")
		writeFixtureFile(t, hypr, edited)
		out := f.mustMake("uninstall")
		if got, _ := os.ReadFile(hypr); !bytes.Equal(got, edited) { //nolint:gosec // fixture path under t.TempDir
			t.Error("uninstall deleted the edited backstory.lua")
		}
		if !strings.Contains(out, "kept your edited "+hypr) {
			t.Errorf("uninstall does not name the kept file:\n%s", out)
		}
	})
	t.Run("symlinked", func(t *testing.T) {
		f := newInstallFixture(t)
		hypr := f.path(".config", "hypr", "backstory.lua")
		target := f.path("dotfiles", "backstory.lua")
		writeFixtureFile(t, target, shipped)
		if err := os.MkdirAll(filepath.Dir(hypr), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
			t.Fatal(err)
		}
		if err := os.Symlink(target, hypr); err != nil {
			t.Fatal(err)
		}
		f.mustMake("install")
		out := f.mustMake("uninstall")
		if got, err := os.Readlink(hypr); err != nil || got != target {
			t.Errorf("uninstall removed the symlinked backstory.lua (readlink %q, %v)", got, err)
		}
		if !strings.Contains(out, "kept your symlinked "+hypr) {
			t.Errorf("uninstall does not name the kept symlink:\n%s", out)
		}
	})
	t.Run("shipped", func(t *testing.T) {
		f := newInstallFixture(t)
		f.mustMake("install")
		hypr := f.path(".config", "hypr", "backstory.lua")
		mustExist(t, hypr)
		f.mustMake("uninstall")
		mustNotExist(t, hypr)
	})
}
