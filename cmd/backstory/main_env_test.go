package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sandboxRoot is the per-run temp HOME TestMain created; tests read it to
// assert nothing (Go caches included) resolves inside it.
var sandboxRoot string

// goCacheVars are the Go toolchain locations pinned to the developer's real
// ones before HOME is replaced, so a `go build` run by a test never writes the
// module or build cache under the sandbox.
var goCacheVars = []string{"GOPATH", "GOMODCACHE", "GOCACHE"}

// resolveGoEnv returns the real GOPATH, GOMODCACHE and GOCACHE as the go tool
// reports them under the current (still real) environment.
func resolveGoEnv() (map[string]string, error) {
	out, err := exec.Command("go", append([]string{"env"}, goCacheVars...)...).Output() //nolint:gosec // fixed args: the go tool and package-level var names
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(goCacheVars) {
		return nil, fmt.Errorf("go env: expected %d lines, got %d", len(goCacheVars), len(lines))
	}
	env := map[string]string{}
	for i, k := range goCacheVars {
		if lines[i] == "" {
			return nil, fmt.Errorf("go env %s is empty", k)
		}
		env[k] = lines[i]
	}
	return env, nil
}

// removeSandbox deletes the sandbox dir. Anything read-only (the Go module
// cache is) is made writable first. A directory that still cannot be removed
// is an error naming the path, never a silent leak.
func removeSandbox(root string) error {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700) //nolint:gosec // test dirs need the execute bit
		}
		return nil
	})
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove sandbox %s: %w", root, err)
	}
	return nil
}

// realHome is the developer's HOME as the test process was started, captured
// before TestMain replaces it. Only the real-HOME guard test reads it.
var realHome = os.Getenv("HOME")

// sandboxEnvVars are the base-dir variables TestMain points at a per-run temp
// dir so storePath, the socket path and the config path can never resolve
// under the developer's real home.
var sandboxEnvVars = []string{"HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR"}

// TestMain clears BACKSTORY_PROJECT_ROOTS from the invoking environment so no
// test (in-process or a subprocess inheriting os.Environ) is scoped by the
// developer's own setting; tests that want roots set them explicitly. It also
// points HOME and every XDG_* base dir at a per-run temp dir, so no test in
// this package can open the developer's real store (GitHub issue #2). Tests
// that set their own dirs keep working: they override these.
func TestMain(m *testing.M) {
	_ = os.Unsetenv("BACKSTORY_PROJECT_ROOTS")
	goEnv, err := resolveGoEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: resolve real go caches:", err)
		os.Exit(1)
	}
	root, err := os.MkdirTemp("", "backstory-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: create sandbox home:", err)
		os.Exit(1)
	}
	for _, k := range sandboxEnvVars {
		dir := root
		if k != "HOME" {
			dir = filepath.Join(root, k)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				fmt.Fprintln(os.Stderr, "TestMain: create", dir, err)
				os.Exit(1)
			}
		}
		if err := os.Setenv(k, dir); err != nil {
			fmt.Fprintln(os.Stderr, "TestMain: set", k, err)
			os.Exit(1)
		}
	}
	sandboxRoot = root
	for k, v := range goEnv {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "TestMain: set", k, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := removeSandbox(root); err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: LEAKED sandbox:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
