package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// The Go caches must resolve outside the sandbox HOME, or every go build a
// test runs fills the sandbox with a module cache that cannot be removed.
func TestSandboxGoCachesResolveOutsideSandboxRoot(t *testing.T) {
	if sandboxRoot == "" {
		t.Fatal("sandboxRoot not set by TestMain")
	}
	for _, k := range goCacheVars {
		v := os.Getenv(k)
		if v == "" || !filepath.IsAbs(v) {
			t.Errorf("%s = %q, want an absolute path", k, v)
			continue
		}
		if within(sandboxRoot, v) {
			t.Errorf("%s = %q resolves inside sandbox root %s", k, v, sandboxRoot)
		}
	}
	if os.Getenv("HOME") != sandboxRoot {
		t.Errorf("HOME = %q, want sandbox root %q", os.Getenv("HOME"), sandboxRoot)
	}
}

// A read-only tree (like the Go module cache) is removed.
func TestRemoveSandboxRemovesReadOnlyTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sb")
	dir := filepath.Join(root, "go", "pkg", "mod", "x@v1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dir, filepath.Dir(dir)} {
		if err := os.Chmod(d, 0o500); err != nil { //nolint:gosec // test dirs need the execute bit
			t.Fatal(err)
		}
	}
	if err := removeSandbox(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("sandbox still present: %v", err)
	}
}

// A cleanup that cannot remove the dir reports the path and an error.
func TestRemoveSandboxFailureIsLoud(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "sb")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// A read-only parent stops the final unlink of root itself.
	if err := os.Chmod(parent, 0o500); err != nil { //nolint:gosec // test dirs need the execute bit
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) }) //nolint:gosec // test dirs need the execute bit
	err := removeSandbox(root)
	if err == nil {
		t.Fatal("removeSandbox returned nil although the dir could not be removed")
	}
	if !strings.Contains(err.Error(), root) {
		t.Errorf("error %q does not name the path %s", err, root)
	}
}

// Independent of TestMain's own variables: ask the go tool what it would use
// under the test's environment and actually run a build. Unpinned caches land
// under the sandbox HOME (go/pkg/mod, .cache/go-build) and fail this test.
func TestGoToolCachesStayOutsideSandboxHome(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" || !strings.Contains(filepath.Base(home), "backstory-test-home-") {
		t.Fatalf("HOME = %q is not the TestMain sandbox", home)
	}
	out, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE", "GOPATH").Output() //nolint:gosec // fixed args
	if err != nil {
		t.Fatalf("go env: %v", err)
	}
	for _, v := range strings.Fields(string(out)) {
		if within(home, v) {
			t.Errorf("go tool would use %s inside sandbox HOME %s", v, home)
		}
	}
	src := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "x"), src).CombinedOutput(); err != nil { //nolint:gosec // fixed args
		t.Fatalf("go build: %v\n%s", err, b)
	}
	for _, rel := range []string{filepath.Join("go", "pkg", "mod"), filepath.Join(".cache", "go-build")} {
		if _, err := os.Stat(filepath.Join(home, rel)); err == nil { //nolint:gosec // test-only stat of fixed relative paths under the sandbox
			t.Errorf("go build created %s under the sandbox HOME", rel)
		}
	}
}
