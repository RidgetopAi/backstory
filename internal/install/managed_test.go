package install

import (
	"crypto/sha256"
	"encoding/hex"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// committedVersions is every committed version of this package's file
// source (git log --follow), newest first.
func committedVersions(t *testing.T, source string) map[string]string {
	t.Helper()
	out, err := exec.Command("git", "log", "--format=%h", "--follow", "--", source).Output() //nolint:gosec // a source path from managedFiles
	if err != nil {
		t.Fatalf("git log %s: %v", source, err)
	}
	versions := map[string]string{}
	for _, c := range strings.Fields(string(out)) {
		b, err := exec.Command("git", "show", c+":./"+source).Output() //nolint:gosec // commit ids from git log, a source path from managedFiles
		if err != nil {
			t.Fatalf("git show %s:%s: %v", c, source, err)
		}
		versions[c] = string(b)
	}
	return versions
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The population: every file this package embeds (its non-test //go:embed
// directives) is a managedFile, and every managedFile is embedded.
func TestManagedFilesAreEveryEmbeddedFile(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("no Go files in the package directory: %v", err)
	}
	var embedded []string
	fset := token.NewFileSet()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if rest, ok := strings.CutPrefix(c.Text, "//go:embed "); ok {
					embedded = append(embedded, strings.Fields(rest)...)
				}
			}
		}
	}
	var sources []string
	for _, m := range managedFiles {
		sources = append(sources, m.source)
	}
	slices.Sort(embedded)
	slices.Sort(sources)
	if len(embedded) == 0 || !slices.Equal(embedded, sources) {
		t.Errorf("embedded files %v, managedFiles %v: each embedded file needs a managedFile with its shipped hashes", embedded, sources)
	}
}

// Every committed version of a managed file, and the working tree's, is in its
// shipped list: a change to an embedded file without its hash would make the
// next install treat that version, unedited, as the owner's edit.
func TestManagedFilesKnowEveryShippedVersion(t *testing.T) {
	for _, m := range managedFiles {
		current, err := os.ReadFile(m.source)
		if err != nil {
			t.Fatal(err)
		}
		versions := committedVersions(t, m.source)
		versions["working tree"] = string(current)
		for at, v := range versions {
			if h := sha256Hex(v); !slices.Contains(m.shipped, h) {
				t.Errorf("%s at %s (sha256 %s) is not in its managedFile's shipped list", m.source, at, h)
			}
		}
	}
}

// oldestVersion is the first committed version of source.
func oldestVersion(t *testing.T, source string) string {
	t.Helper()
	out, err := exec.Command("git", "log", "--format=%h", "--follow", "--", source).Output() //nolint:gosec // a source path from managedFiles
	if err != nil {
		t.Fatal(err)
	}
	c := strings.Fields(string(out))
	if len(c) < 2 {
		t.Fatalf("%s has %d committed versions; this test needs an older one", source, len(c))
	}
	return committedVersions(t, source)[c[len(c)-1]]
}

func TestPiExtensionOutdatedIsReplacedEditedIsKept(t *testing.T) {
	a := piAdapter{}
	home := t.TempDir()
	p := DefaultPiPaths(home)
	opts := Options{Prefix: t.TempDir()}
	if err := os.MkdirAll(p.ExtensionDir, 0o750); err != nil {
		t.Fatal(err)
	}

	writeFile(t, p.ExtensionTS, oldestVersion(t, piExtensionFile.source))
	if st, _ := piExtensionStatus(p.ExtensionTS); st != StatusOutdated {
		t.Fatalf("an older shipped extension = %s, want outdated", st)
	}
	if err := a.Install(home, opts); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p.ExtensionTS) != string(piExtensionSource) {
		t.Error("install did not replace an older shipped extension")
	}

	edited := string(piExtensionSource) + "// mine\n"
	writeFile(t, p.ExtensionTS, edited)
	if err := a.Install(home, opts); err != nil {
		t.Fatalf("install over an edited extension: %v", err)
	}
	if readFile(t, p.ExtensionTS) != edited {
		t.Error("install replaced an edited extension")
	}
	if !strings.Contains(readFile(t, p.AgentsMD), PiStubLine) {
		t.Error("the rest of the pi harness was not installed beside the edited extension")
	}
	if n := a.InstallNotice(home, opts); !strings.Contains(n, p.ExtensionTS) {
		t.Errorf("install notice %q does not name the kept file", n)
	}
	items, err := a.Check(home, opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := checkStatus(t, items)[ItemPiExtension].Status; st != StatusEdited {
		t.Errorf("--check pi-extension = %s, want edited", st)
	}
	if err := a.Remove(home, opts); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p.ExtensionTS) != edited {
		t.Error("--remove deleted an edited extension")
	}
	if n := a.RemoveNotice(home, opts); !strings.Contains(n, p.ExtensionTS) {
		t.Errorf("remove notice %q does not name the kept file", n)
	}
}

func TestHermesPluginOutdatedIsReplacedEditedIsKept(t *testing.T) {
	a := hermesAdapter{}
	home := t.TempDir()
	t.Setenv(HermesHomeEnv, "")
	p := DefaultHermesPaths(home)
	opts := Options{Prefix: t.TempDir()}
	files, err := HermesPluginFiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.PluginDir, 0o750); err != nil {
		t.Fatal(err)
	}
	py := filepath.Join(p.PluginDir, "__init__.py")

	// An older shipped __init__.py, rendered with tool schemas as install wrote it.
	old := strings.Replace(oldestVersion(t, hermesPluginPyFile.source), hermesToolsToken, `[{"name":"recall"}]`, 1)
	writeFile(t, py, old)
	if st := hermesPluginStatus(p.PluginDir); st != StatusAbsent { // plugin.yaml still missing
		t.Fatalf("plugin status = %s, want absent", st)
	}
	writeFile(t, filepath.Join(p.PluginDir, "plugin.yaml"), files["plugin.yaml"])
	if st := hermesPluginStatus(p.PluginDir); st != StatusOutdated {
		t.Fatalf("an older shipped plugin = %s, want outdated", st)
	}
	if err := a.Install(home, opts); err != nil {
		t.Fatal(err)
	}
	if readFile(t, py) != files["__init__.py"] {
		t.Error("install did not replace an older shipped __init__.py")
	}

	edited := files["__init__.py"] + "# mine\n"
	writeFile(t, py, edited)
	if err := a.Install(home, opts); err != nil {
		t.Fatalf("install over an edited plugin: %v", err)
	}
	if readFile(t, py) != edited {
		t.Error("install replaced an edited __init__.py")
	}
	if n := a.InstallNotice(home, opts); !strings.Contains(n, py) || !strings.Contains(n, HermesRestartNotice) {
		t.Errorf("install notice %q does not name the kept file and the restart", n)
	}
	items, err := a.Check(home, opts)
	if err != nil {
		t.Fatal(err)
	}
	if st := checkStatus(t, items)[ItemHermesPlugin].Status; st != StatusEdited {
		t.Errorf("--check hermes plugin = %s, want edited", st)
	}
	if err := a.Remove(home, opts); err != nil {
		t.Fatal(err)
	}
	if readFile(t, py) != edited {
		t.Error("--remove deleted an edited __init__.py")
	}
	if _, err := os.Stat(filepath.Join(p.PluginDir, "plugin.yaml")); !os.IsNotExist(err) {
		t.Error("--remove kept the unedited plugin.yaml")
	}
	if n := a.RemoveNotice(home, opts); !strings.Contains(n, py) {
		t.Errorf("remove notice %q does not name the kept file", n)
	}
}

func TestSyncUnitEditedIsKept(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	oldSM, oldExe := ServiceManager, executablePath
	ServiceManager = func(...string) error { return nil }
	executablePath = func() (string, error) { return "/opt/tmp/backstory", nil }
	t.Cleanup(func() { ServiceManager, executablePath = oldSM, oldExe })

	if _, err := InstallSyncTimer(home); err != nil {
		t.Fatal(err)
	}
	timer := filepath.Join(SyncUnitDir(home), SyncTimerName)
	svc := filepath.Join(SyncUnitDir(home), SyncServiceName)
	edited := strings.Replace(SyncTimerUnit, "[Timer]", "[Timer]\n# mine", 1)
	if edited == SyncTimerUnit {
		t.Fatal("timer unit has no [Timer] section to edit")
	}
	writeFile(t, timer, edited)
	writeFile(t, svc, SyncServiceUnitFor("/old/place/backstory")) // an older binary: outdated, replaced
	if s := SyncTimerStatus(home); s != StatusEdited {
		t.Fatalf("status = %s, want edited", s)
	}
	kept, err := InstallSyncTimer(home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kept, []string{timer}) || readFile(t, timer) != edited {
		t.Errorf("install kept %v; timer now:\n%s", kept, readFile(t, timer))
	}
	if readFile(t, svc) != SyncServiceUnitFor("/opt/tmp/backstory") {
		t.Error("install did not replace the outdated service unit")
	}
	kept, err = RemoveSyncTimer(home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kept, []string{timer}) || readFile(t, timer) != edited {
		t.Errorf("remove kept %v, want the edited timer", kept)
	}
	if _, err := os.Stat(svc); !os.IsNotExist(err) {
		t.Error("remove kept the unedited service unit")
	}
}

// The unrender functions undo exactly what install fills in.
func TestUnrenderUndoesInstallRendering(t *testing.T) {
	files, err := HermesPluginFiles()
	if err != nil {
		t.Fatal(err)
	}
	if unrenderHermesTools(files["__init__.py"]) != hermesPluginPy {
		t.Error("unrenderHermesTools does not give back the embedded __init__.py")
	}
	for _, bin := range []string{"/usr/bin/backstory", "/tmp/my 50%/back\"story"} {
		if got := unrenderSyncExecStart(SyncServiceUnitFor(bin)); got != SyncServiceUnit {
			t.Errorf("unrenderSyncExecStart(%q) =\n%s", bin, got)
		}
	}
}
