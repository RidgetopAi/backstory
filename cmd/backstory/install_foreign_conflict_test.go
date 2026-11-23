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

// TestInstallClaudeForeignMCPServerReportedAndUntouched: a foreign
// mcpServers.backstory in ~/.claude.json takes the same reporting path as
// Codex's: `install claude` exits 1 naming foreign-conflict once, a bare
// install reports claude skipped, and the file keeps its bytes throughout.
func TestInstallClaudeForeignMCPServerReportedAndUntouched(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	claudeJSON := filepath.Join(home, ".claude.json")
	foreign := `{"mcpServers":{"backstory":{"type":"http","url":"https://example.invalid/mcp"}}}`
	if err := os.WriteFile(claudeJSON, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string, error) {
		cmd := exec.Command(bin, args...) //nolint:gosec // bin is the binary this test just built
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		var outBuf, errBuf safeBuffer
		cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
		err := cmd.Run()
		return outBuf.String(), errBuf.String(), err
	}
	unchanged := func(step string) {
		t.Helper()
		got, err := os.ReadFile(claudeJSON) //nolint:gosec // t.TempDir() path
		if err != nil || string(got) != foreign {
			t.Errorf("after %s ~/.claude.json = %q (%v), want it untouched", step, got, err)
		}
	}

	_, stderr, err := run("install", "claude", "--no-verify")
	if err == nil {
		t.Fatalf("install claude succeeded against a foreign entry; stderr: %s", stderr)
	}
	if n := strings.Count(stderr, "foreign-conflict"); n != 1 || !strings.Contains(stderr, "mcp-server") {
		t.Errorf("stderr = %q, want foreign-conflict once naming mcp-server", stderr)
	}
	unchanged("install claude")

	stdout, _, _ := run("install", "--check")
	if !strings.Contains(stdout, "claude: mcp-server: foreign-conflict") {
		t.Errorf("--check stdout = %q, want claude: mcp-server: foreign-conflict", stdout)
	}
	if _, stderr, err := run("install", "claude", "--remove"); err != nil {
		t.Fatalf("install claude --remove: %v; stderr: %s", err, stderr)
	}
	unchanged("install claude --remove")
}
