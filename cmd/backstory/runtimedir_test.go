package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestRuntimePathsWithoutXDGRuntimeDir covers socketPath and captureOffPath
// under a Codex-style scrubbed environment (task e111f038).
func TestRuntimePathsWithoutXDGRuntimeDir(t *testing.T) {
	cases := []struct {
		name string
		call func() (string, error)
		leaf string
	}{
		{"socketPath", socketPath, "sock"},
		{"captureOffPath", captureOffPath, "capture-off"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			root := t.TempDir()
			t.Setenv("HOME", home)
			old := runtimeRoot
			runtimeRoot = root
			t.Cleanup(func() { runtimeRoot = old })

			check := func(want string) {
				t.Helper()
				got, err := c.call()
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			}

			t.Run("uid dir present", func(t *testing.T) {
				t.Setenv("XDG_RUNTIME_DIR", "")
				uidDir := filepath.Join(root, strconv.Itoa(os.Getuid()))
				if err := os.Mkdir(uidDir, 0o700); err != nil {
					t.Fatal(err)
				}
				defer os.Remove(uidDir)
				check(filepath.Join(uidDir, "backstory", c.leaf))
			})
			t.Run("uid dir absent", func(t *testing.T) {
				t.Setenv("XDG_RUNTIME_DIR", "")
				check(filepath.Join(home, ".local", "state", "backstory", c.leaf))
			})
			t.Run("XDG_RUNTIME_DIR set", func(t *testing.T) {
				xdg := t.TempDir()
				t.Setenv("XDG_RUNTIME_DIR", xdg)
				// uid dir exists too; the env var must still win.
				uidDir := filepath.Join(root, strconv.Itoa(os.Getuid()))
				if err := os.Mkdir(uidDir, 0o700); err != nil {
					t.Fatal(err)
				}
				defer os.Remove(uidDir)
				check(filepath.Join(xdg, "backstory", c.leaf))
			})
		})
	}
}
