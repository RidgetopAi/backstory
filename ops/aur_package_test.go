package ops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runBash runs script in bash and returns stdout; stderr is part of a failure.
func runBash(t *testing.T, script string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"-c", script, "bash"}, args...)...) //nolint:gosec // script is a test literal
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash: %v\n%s\n%s", err, out, stderr.String())
	}
	return string(out)
}

func copyFixture(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from) //nolint:gosec // repo path
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil { //nolint:gosec // temp dir
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil { //nolint:gosec // temp dir
		t.Fatal(err)
	}
}

// TestAURPackageInstallsWorkingUnits runs the real PKGBUILD package() against
// a fixture source tree (the repo's own ops files) and checks the $pkgdir.
func TestAURPackageInstallsWorkingUnits(t *testing.T) {
	root := t.TempDir()
	srcdir, pkgdir := filepath.Join(root, "src"), filepath.Join(root, "pkg")
	tree := filepath.Join(srcdir, "backstory-9.9.9")
	for _, f := range []string{
		"ops/backstory.service", "ops/backstory-sync.service", "ops/backstory-sync.timer",
		"ops/hyprland/backstory.lua", "LICENSE",
	} {
		copyFixture(t, filepath.Join("..", f), filepath.Join(tree, f))
	}
	if err := os.MkdirAll(filepath.Join(tree, "bin"), 0o755); err != nil { //nolint:gosec // temp dir
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "bin", "backstory"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // temp fixture binary
		t.Fatal(err)
	}
	runBash(t, `source "$PWD/aur/PKGBUILD"; pkgver=9.9.9; srcdir=$1; pkgdir=$2; cd "$srcdir"; package`, srcdir, pkgdir)

	units := filepath.Join(pkgdir, "usr", "lib", "systemd", "user")
	for name, want := range map[string]string{
		"backstory.service":      "ExecStart=/usr/bin/backstory daemon",
		"backstory-sync.service": "ExecStart=/usr/bin/backstory sync --quiet",
	} {
		b, err := os.ReadFile(filepath.Join(units, name)) //nolint:gosec // temp dir
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %q:\n%s", name, want, b)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "ExecStart=") && !strings.HasPrefix(line, "ExecStart=/usr/bin/backstory ") {
				t.Errorf("%s: ExecStart %q does not start with /usr/bin/backstory", name, line)
			}
		}
	}
	// No installed unit may mention the make-install path.
	entries, err := os.ReadDir(units)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(units, e.Name())) //nolint:gosec // temp dir
		if strings.Contains(string(b), "%h/.local/bin") {
			t.Errorf("%s still names %%h/.local/bin", e.Name())
		}
	}
	for _, p := range []string{
		"usr/bin/backstory",
		"usr/lib/systemd/user/backstory-sync.timer",
		"usr/share/backstory/hyprland/backstory.lua",
		"usr/share/licenses/backstory/LICENSE",
	} {
		if _, err := os.Stat(filepath.Join(pkgdir, p)); err != nil {
			t.Errorf("package lacks %s: %v", p, err)
		}
	}
	// The make install source is untouched.
	b, _ := os.ReadFile(filepath.Join(tree, "ops", "backstory.service")) //nolint:gosec // temp dir
	if !strings.Contains(string(b), "ExecStart=%h/.local/bin/backstory daemon") {
		t.Errorf("source unit was modified in place")
	}
}

// TestAURInstallScriptText sources backstory.install and captures only what
// the hooks print; nothing they mention is executed.
func TestAURInstallScriptText(t *testing.T) {
	post := runBash(t, `source aur/backstory.install; post_install`)
	prev := -1
	for _, want := range []string{
		"systemctl --user enable --now backstory",
		"backstory install",
		"backstory backfill",
		"marketplace",
		"/usr/share/backstory/hyprland/backstory.lua",
	} {
		i := strings.Index(post, want)
		if i < 0 {
			t.Fatalf("post_install lacks %q:\n%s", want, post)
		}
		if i < prev {
			t.Errorf("post_install: %q out of order:\n%s", want, post)
		}
		prev = i
	}
	if !strings.Contains(post, "backstory.this-week") {
		t.Errorf("post_install does not name the panel plugin id:\n%s", post)
	}
	up := runBash(t, `source aur/backstory.install; post_upgrade`)
	if !strings.Contains(up, "systemctl --user daemon-reload && systemctl --user restart backstory") {
		t.Errorf("post_upgrade lacks reload+restart:\n%s", up)
	}
}
