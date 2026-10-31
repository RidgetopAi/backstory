package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
