package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

func replaceOnce(s, old, repl string) string { return strings.Replace(s, old, repl, 1) }

const hermesFixtureYAML = `# hand-written
model:
  default: claude-sonnet
  provider: anthropic   # not the memory provider
terminal:
  backend: local
memory:
  memory_enabled: true
  provider: ''
skills:
  - a
  - b
`

func hermesFixture(t *testing.T, yaml string) HermesPaths {
	t.Helper()
	p := HermesPathsIn(t.TempDir())
	if yaml != "" {
		if err := os.MkdirAll(filepath.Dir(p.ConfigYAML), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p.ConfigYAML, yaml)
	}
	return p
}

func TestHermesInstallWritesPluginAndProvider(t *testing.T) {
	p := hermesFixture(t, hermesFixtureYAML)
	if err := InstallHermes(p, Options{}); err != nil {
		t.Fatal(err)
	}
	files, err := HermesPluginFiles()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		if got := readFile(t, filepath.Join(p.PluginDir, name)); got != want {
			t.Errorf("%s differs from embedded plugin", name)
		}
	}
	got := readFile(t, p.ConfigYAML)
	if !strings.Contains(got, "  provider: backstory  # backstory:orig=") {
		t.Fatalf("memory.provider not set:\n%s", got)
	}
	// Every other line survives byte for byte.
	for _, line := range strings.Split(hermesFixtureYAML, "\n") {
		if line == "  provider: ''" {
			continue
		}
		if !strings.Contains(got, line+"\n") && line != "" {
			t.Errorf("line lost: %q", line)
		}
	}
	if strings.Contains(got, "hooks_auto_accept") {
		t.Error("installer wrote hooks_auto_accept")
	}
	items, _ := CheckHermes(p, Options{})
	for _, it := range items {
		if it.Status != StatusPresent {
			t.Errorf("%s: %s", it.Name, it.Status)
		}
	}
}

func TestHermesIdempotentAndRemoveRestoresBytes(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"empty-provider", hermesFixtureYAML},
		{"null-provider", "memory:\n    provider:\n    other: 1\n"},
		{"memory-without-provider", "a: 1\nmemory:\n    memory_enabled: true\nb: 2\n"},
		{"memory-empty-block", "memory:\nb: 2\n"},
		{"no-memory-key", "model: x\n"},
		{"no-trailing-newline", "model: x"},
		{"crlf", "memory:\r\n  provider: ''\r\n"},
		{"absent", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := hermesFixture(t, tc.yaml)
			if err := InstallHermes(p, Options{}); err != nil {
				t.Fatal(err)
			}
			b1 := readFile(t, p.ConfigYAML)
			pl1 := readFile(t, filepath.Join(p.PluginDir, "__init__.py"))
			st1, _ := os.Stat(p.ConfigYAML)
			pst1, _ := os.Stat(filepath.Join(p.PluginDir, "__init__.py"))
			time.Sleep(20 * time.Millisecond)
			if err := InstallHermes(p, Options{}); err != nil {
				t.Fatal(err)
			}
			st2, _ := os.Stat(p.ConfigYAML)
			pst2, _ := os.Stat(filepath.Join(p.PluginDir, "__init__.py"))
			if readFile(t, p.ConfigYAML) != b1 || !st1.ModTime().Equal(st2.ModTime()) {
				t.Error("second install wrote config.yaml")
			}
			if readFile(t, filepath.Join(p.PluginDir, "__init__.py")) != pl1 || !pst1.ModTime().Equal(pst2.ModTime()) {
				t.Error("second install wrote the plugin")
			}
			if err := RemoveHermes(p, Options{}); err != nil {
				t.Fatal(err)
			}
			if tc.yaml == "" {
				if _, err := os.Stat(p.ConfigYAML); !os.IsNotExist(err) {
					t.Errorf("config.yaml should be gone: %v", err)
				}
			} else if got := readFile(t, p.ConfigYAML); got != tc.yaml {
				t.Errorf("remove did not restore bytes:\n got %q\nwant %q", got, tc.yaml)
			}
			if _, err := os.Stat(p.PluginDir); !os.IsNotExist(err) {
				t.Errorf("plugin dir should be gone: %v", err)
			}
		})
	}
}

func TestHermesForeignProviderIsConflictAndUntouched(t *testing.T) {
	for _, yaml := range []string{
		"memory:\n  provider: honcho\n  x: 1\n",
		"memory:\n  provider: \"mem0\"  # mine\n",
		"memory: {provider: honcho}\n",
	} {
		p := hermesFixture(t, yaml)
		err := InstallHermes(p, Options{})
		if !errors.Is(err, ErrForeignConflict) {
			t.Fatalf("%q: err = %v, want foreign-conflict", yaml, err)
		}
		if got := readFile(t, p.ConfigYAML); got != yaml {
			t.Errorf("config.yaml changed:\n%s", got)
		}
		items, _ := CheckHermes(p, Options{})
		if items[1].Name != ItemHermesMemoryProvider || items[1].Status != StatusForeign {
			t.Errorf("check: %+v", items)
		}
		if err := RemoveHermes(p, Options{}); err != nil {
			t.Fatal(err)
		}
		if readFile(t, p.ConfigYAML) != yaml {
			t.Error("remove touched a foreign provider")
		}
	}
}

func TestHermesForeignPluginFilesUntouched(t *testing.T) {
	p := hermesFixture(t, "")
	if err := os.MkdirAll(p.PluginDir, 0o750); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(p.PluginDir, "__init__.py")
	writeFile(t, mine, "# someone else's\n")
	err := InstallHermes(p, Options{})
	if !errors.Is(err, ErrForeignConflict) {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, mine) != "# someone else's\n" {
		t.Error("foreign plugin overwritten")
	}
	if _, err := os.Stat(filepath.Join(p.PluginDir, "plugin.yaml")); err == nil {
		t.Error("conflict still wrote plugin.yaml")
	}
	if err := RemoveHermes(p, Options{}); err != nil || readFile(t, mine) != "# someone else's\n" {
		t.Errorf("remove touched foreign plugin: %v", err)
	}
}

func TestHermesNeverWritesHookConsent(t *testing.T) {
	p := hermesFixture(t, hermesFixtureYAML)
	if err := InstallHermes(p, Options{}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(p.ConfigYAML)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.Contains(path, "shell-hooks-allowlist") {
			t.Errorf("wrote %s", path)
		}
		if b, _ := os.ReadFile(path); strings.Contains(string(b), "hooks_auto_accept") { //nolint:gosec // test walk over a temp dir
			t.Errorf("%s mentions hooks_auto_accept", path)
		}
		return nil
	})
}

func TestHermesAdapterRegistered(t *testing.T) {
	a, ok := AdapterByName(HarnessHermes)
	if !ok || a.Name() != HarnessHermes {
		t.Fatal("hermes adapter missing")
	}
	home := t.TempDir()
	t.Setenv(HermesHomeEnv, filepath.Join(home, "hh"))
	if err := a.Install(home, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "hh", "plugins", "backstory", "plugin.yaml")); err != nil {
		t.Error(err)
	}
}
