package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallBashBinaryWritesAbsolutePath is task 8dac3c1d: install bash
// accepts --binary and the snippet invokes that absolute path; remove still
// strips the block.
func TestInstallBashBinaryWritesAbsolutePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(t.TempDir(), "bs-fixture")
	var out, errb bytes.Buffer
	if code := runInstallBash([]string{"--binary", bin}, &out, &errb); code != 0 {
		t.Fatalf("install bash --binary: code %d\n%s", code, errb.String())
	}
	data, err := os.ReadFile(filepath.Join(home, ".bashrc")) //nolint:gosec // test path under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), bin+" shell init bash") {
		t.Fatalf(".bashrc does not invoke %s:\n%s", bin, data)
	}
	if code := runInstallBash([]string{"--remove"}, &out, &errb); code != 0 {
		t.Fatalf("remove: %d %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
		t.Fatalf("remove left the block behind: %v", err)
	}
	if code := runInstallBash([]string{"--binary", "relative/bs"}, &out, &errb); code != 2 {
		t.Fatalf("relative --binary: code %d, want 2", code)
	}
}

// TestInstallCodexSaysStopNudgeIsClaudeOnly is task 8dac3c1d's codex nit.
func TestInstallCodexSaysStopNudgeIsClaudeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	var out, errb bytes.Buffer
	if code := runInstall([]string{"codex", "--no-verify"}, &out, &errb); code != 0 {
		t.Fatalf("install codex: %d\n%s%s", code, out.String(), errb.String())
	}
	if n := strings.Count(out.String(), "Stop nudge is Claude-only"); n != 1 {
		t.Fatalf("want one Claude-only Stop note line, got %d:\n%s", n, out.String())
	}
}
