package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// installIn runs runInstall in-process against a fixture HOME. HERMES_HOME is
// set to hermesHome when non-empty and explicitly unset otherwise.
func installIn(t *testing.T, home, hermesHome string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv(install.HermesHomeEnv, hermesHome)
	if hermesHome == "" {
		if err := os.Unsetenv(install.HermesHomeEnv); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb bytes.Buffer
	code = runInstall(append(args[1:], "--no-verify"), &out, &errb)
	return out.String(), errb.String(), code
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o750); err != nil {
			t.Fatal(err)
		}
	}
}

func allPresent(t *testing.T, name, home string) bool {
	t.Helper()
	a, _ := install.AdapterByName(name)
	items, err := a.Check(home, install.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		// not-trusted: installed, awaiting the user's approval in the harness.
		if it.Status != install.StatusPresent && it.Status != install.StatusNotTrusted {
			return false
		}
	}
	return len(items) > 0
}

func TestBareInstallDetectsOnlyPresentHarnesses(t *testing.T) {
	for _, hermesEnv := range []string{"", "set"} {
		t.Run("HERMES_HOME="+hermesEnv, func(t *testing.T) {
			home := t.TempDir()
			mkdirs(t, filepath.Join(home, ".claude"), filepath.Join(home, ".codex"))
			hh := ""
			if hermesEnv != "" {
				hh = filepath.Join(t.TempDir(), "nohermes") // does not exist
			}
			stdout, stderr, code := installIn(t, home, hh, "install")
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			for _, n := range []string{"claude", "codex"} {
				if !allPresent(t, n, home) {
					t.Errorf("%s not installed", n)
				}
			}
			wantHermes := filepath.Join(home, ".hermes")
			if hh != "" {
				wantHermes = hh
			}
			for n, path := range map[string]string{"hermes": wantHermes, "pi": filepath.Join(home, ".pi", "agent")} {
				if _, err := os.Stat(path); err == nil {
					t.Errorf("%s: %s was created", n, path)
				}
				want := n + ": skipped — " + path + " not found"
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout)
				}
			}
			if strings.Contains(stdout, "agents") {
				t.Errorf("agents mentioned in bare output:\n%s", stdout)
			}
			if !strings.Contains(stdout, "backstory install bash") {
				t.Errorf("no shell capture hint:\n%s", stdout)
			}
		})
	}
}

func TestBareInstallDetectsHermesHomeOutsideHome(t *testing.T) {
	home := t.TempDir()
	hh := filepath.Join(t.TempDir(), "elsewhere")
	mkdirs(t, hh)
	_, stderr, code := installIn(t, home, hh, "install")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(hh, "config.yaml")); err != nil {
		t.Errorf("hermes not installed under HERMES_HOME: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".hermes")); err == nil {
		t.Error("~/.hermes was created despite HERMES_HOME")
	}
}

func TestBareInstallNothingDetectedWritesNothingAndFails(t *testing.T) {
	home := t.TempDir()
	stdout, stderr, code := installIn(t, home, "", "install")
	if code == 0 {
		t.Fatalf("exit 0, want non-zero (stdout %q)", stdout)
	}
	for _, n := range install.HarnessNames() {
		if !strings.Contains(stderr, n) {
			t.Errorf("stderr missing valid name %q: %s", n, stderr)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Errorf("home not empty: %v %v", entries, err)
	}
}

func TestExplicitNamesBypassDetectionAndBareCheckRemoveUseDetectedSet(t *testing.T) {
	home := t.TempDir()
	if _, stderr, code := installIn(t, home, "", "install", "claude"); code != 0 {
		t.Fatalf("explicit claude: exit %d %s", code, stderr)
	}
	if !allPresent(t, "claude", home) {
		t.Fatal("explicit claude not installed on a home with no .claude")
	}

	// Now .claude exists, so bare acts on claude only; agents never selected.
	stdout, _, code := installIn(t, home, "", "install", "--check")
	if code != 0 {
		t.Errorf("bare --check exit %d:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "claude: ") || strings.Contains(stdout, "agents: ") || strings.Contains(stdout, "codex: present") {
		t.Errorf("bare --check output unexpected:\n%s", stdout)
	}
	if !strings.Contains(stdout, "codex: skipped") {
		t.Errorf("bare --check should report skipped codex:\n%s", stdout)
	}

	if _, _, code := installIn(t, home, "", "install", "--remove"); code != 0 {
		t.Fatalf("bare --remove exit %d", code)
	}
	if allPresent(t, "claude", home) {
		t.Error("claude still present after bare --remove")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		t.Error("bare --remove touched codex")
	}

	// agents only by name.
	mkdirs(t, filepath.Join(home, ".claude"))
	installIn(t, home, "", "install")
	if allPresent(t, "agents", home) {
		t.Error("bare install selected agents")
	}
}
