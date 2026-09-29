package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requirePython3 FAILS (never skips) when python3 is missing: `make check`
// is this provider's gate, so a machine without it must not appear to pass.
func requirePython3(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 not on PATH: %v (required to run the Hermes provider's suite)", err)
	}
	return py
}

// hermesSuiteCmd runs real pytest when installed, else the stdlib-only
// minipytest runner over the same test file, so the suite always executes.
func hermesSuiteCmd(py string) *exec.Cmd {
	suite := filepath.Join("testdata", "hermes", "test_provider.py")
	if exec.Command(py, "-m", "pytest", "--version").Run() == nil { //nolint:gosec // py is exec.LookPath's own result
		return exec.Command(py, "-m", "pytest", "-q", "-p", "no:cacheprovider", suite) //nolint:gosec // constants
	}
	return exec.Command(py, filepath.Join("testdata", "hermes", "minipytest.py"), suite) //nolint:gosec // constants
}

// runHermesPytest installs the real plugin into a temp HERMES_HOME (or, when
// mutate is non-nil, a mutated copy of its __init__.py) and runs
// testdata/hermes/test_provider.py against it. It returns pytest's output.
func runHermesPytest(t *testing.T, mutate func(string) string) (string, error) {
	t.Helper()
	py := requirePython3(t)
	paths := HermesPathsIn(t.TempDir())
	if err := InstallHermes(paths, Options{}); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		p := filepath.Join(paths.PluginDir, "__init__.py")
		writeFile(t, p, mutate(readFile(t, p)))
	}
	cmd := hermesSuiteCmd(py)
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
