package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallCodexForeignConflictPrefixOnce: the CLI adds "backstory install:"
// itself, so the sentinel must not carry its own "install:" — users once saw
// "backstory install: install: foreign-conflict: …".
func TestInstallCodexForeignConflictPrefixOnce(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	foreign := "[mcp_servers.backstory]\ncommand = \"other\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "install", "codex", "--no-verify") //nolint:gosec // bin is the binary this test just built
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	var errBuf safeBuffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err == nil {
		t.Fatalf("install codex succeeded against a foreign entry; stderr: %s", errBuf.String())
	}
	stderr := errBuf.String()
	if n := strings.Count(stderr, "foreign-conflict"); n != 1 {
		t.Errorf("stderr has foreign-conflict %d times, want 1: %q", n, stderr)
	}
	if strings.Contains(stderr, "install: install:") {
		t.Errorf("stderr repeats the install prefix: %q", stderr)
	}
}
