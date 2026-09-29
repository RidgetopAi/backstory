package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node not on PATH: the Pi extension tests must run, not skip: %v", err)
	}
	return node
}

// runPiExtension emits index.ts (and a fake backstory) into a temp dir, then
// runs testdata/pi_extension_test.mjs against it. It returns the combined
// output and the fake's log path.
func runPiExtension(t *testing.T, mode, bin string) (string, error) {
	t.Helper()
	node := requireNode(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.ts"), PiExtensionSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	// index.ts is ESM; Pi loads it through its own loader, node needs the hint.
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeSrc, err := os.ReadFile(filepath.Join("testdata", "fake_backstory.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "backstory")
	if err := os.WriteFile(fake, fakeSrc, 0o700); err != nil { //nolint:gosec // test fake must be executable
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "fake.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if bin == "" {
		bin = fake
	}
	runner, _ := filepath.Abs(filepath.Join("testdata", "pi_extension_test.mjs"))
	cmd := exec.Command(node, runner, filepath.Join(dir, "index.ts"), mode) //nolint:gosec // test-controlled args
	cmd.Env = append(os.Environ(), "BACKSTORY_BIN="+bin, "FAKE_LOG="+logPath, "FAKE_HOOK_FAIL=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestPiExtensionAgainstFakeBackstory(t *testing.T) {
	out, err := runPiExtension(t, "", "")
	if err != nil || !strings.Contains(out, "PI EXTENSION OK") {
		t.Fatalf("pi extension test failed: %v\n%s", err, out)
	}
}

func TestPiExtensionMissingBinaryIsSilent(t *testing.T) {
	out, err := runPiExtension(t, "missing", "/nonexistent/backstory-bin")
	if err != nil || !strings.Contains(out, "MISSING-BINARY OK") {
		t.Fatalf("pi extension (missing binary) failed: %v\n%s", err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// snapshot maps every file (and empty-dir marker) under root to its bytes.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if info.IsDir() {
			m[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // test temp dir
		m[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func equalSnap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestPiInstallRemoveRestoresBytes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(home string)
	}{
		{"empty home", func(string) {}},
		{"existing AGENTS.md and unrelated extension", func(home string) {
			writeFile(t, filepath.Join(home, ".pi", "agent", "AGENTS.md"), "# mine\nkeep this line\n")
			writeFile(t, filepath.Join(home, ".pi", "agent", "extensions", "mandrel-mcp.ts"), "export default () => {}\n")
			writeFile(t, filepath.Join(home, ".pi", "agent", "extensions", "other", "index.ts"), "// other\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			tc.setup(home)
			before := snapshot(t, home)
			a := piAdapter{}

			if err := a.Install(home, Options{}); err != nil {
				t.Fatal(err)
			}
			installed := snapshot(t, home)
			if err := a.Install(home, Options{}); err != nil {
				t.Fatal(err)
			}
			if !equalSnap(installed, snapshot(t, home)) {
				t.Fatal("second install changed bytes")
			}
			got, _ := os.ReadFile(DefaultPiPaths(home).ExtensionTS) //nolint:gosec // test temp dir
			if string(got) != string(PiExtensionSource()) {
				t.Fatal("installed extension differs from embedded source")
			}
			items, err := a.Check(home, Options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range items {
				if it.Status != StatusPresent {
					t.Fatalf("%s: %s after install", it.Name, it.Status)
				}
			}

			if err := a.Remove(home, Options{}); err != nil {
				t.Fatal(err)
			}
			if !equalSnap(before, snapshot(t, home)) {
				t.Fatalf("remove did not restore pre-install bytes\nbefore=%v\nafter=%v", before, snapshot(t, home))
			}
			if err := a.Remove(home, Options{}); err != nil {
				t.Fatalf("second remove: %v", err)
			}
			if !equalSnap(before, snapshot(t, home)) {
				t.Fatal("second remove changed bytes")
			}
		})
	}
}

func TestPiInstallRefusesForeignExtension(t *testing.T) {
	home := t.TempDir()
	ts := DefaultPiPaths(home).ExtensionTS
	writeFile(t, ts, "// someone else's\n")
	a := piAdapter{}
	if err := a.Install(home, Options{}); err == nil {
		t.Fatal("install overwrote a foreign index.ts")
	}
	items, _ := a.Check(home, Options{})
	if items[0].Status != StatusForeign {
		t.Fatalf("status = %s, want foreign", items[0].Status)
	}
	if err := a.Remove(home, Options{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ts); string(b) != "// someone else's\n" { //nolint:gosec // test temp dir
		t.Fatal("remove touched a foreign index.ts")
	}
}
