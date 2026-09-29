package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requirePython3Pytest FAILS (never skips) when python3 with pytest is
// missing: `make check` is this provider's gate, so a machine without it
// must not appear to pass, the same discipline panel's requireNode holds.
func requirePython3Pytest(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 not on PATH: %v (required to run the Hermes provider's pytest suite)", err)
	}
	if out, err := exec.Command(py, "-m", "pytest", "--version").CombinedOutput(); err != nil { //nolint:gosec // py is exec.LookPath's own result
		t.Fatalf("python3 -m pytest unavailable: %v\n%s (install pytest, e.g. `apt install python3-pytest`)", err, out)
	}
	return py
}

// runHermesPytest installs the real plugin into a temp HERMES_HOME (or, when
// mutate is non-nil, a mutated copy of its __init__.py) and runs
// testdata/hermes/test_provider.py against it. It returns pytest's output.
func runHermesPytest(t *testing.T, mutate func(string) string) (string, error) {
	t.Helper()
	py := requirePython3Pytest(t)
	paths := HermesPathsIn(t.TempDir())
	if err := InstallHermes(paths, Options{}); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		p := filepath.Join(paths.PluginDir, "__init__.py")
		writeFile(t, p, mutate(readFile(t, p)))
	}
	cmd := exec.Command(py, "-m", "pytest", "-q", "-p", "no:cacheprovider", filepath.Join("testdata", "hermes", "test_provider.py")) //nolint:gosec // py is exec.LookPath's own result; args are constants
	cmd.Env = append(os.Environ(), "BACKSTORY_PLUGIN_DIR="+paths.PluginDir, "PYTHONDONTWRITEBYTECODE=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHermesProviderPytest(t *testing.T) {
	out, err := runHermesPytest(t, nil)
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("pytest failed: %v", err)
	}
}

// The suite must catch a provider whose is_available() lies.
func TestHermesPytestCatchesAlwaysAvailable(t *testing.T) {
	out, err := runHermesPytest(t, func(src string) string {
		const sig = "    def is_available(self):\n"
		if !containsStr(src, sig) {
			t.Fatal("is_available not found to mutate")
		}
		return replaceOnce(src, sig, sig+"        return True\n")
	})
	if err == nil {
		t.Fatalf("mutated is_available()==True passed pytest:\n%s", out)
	}
}
