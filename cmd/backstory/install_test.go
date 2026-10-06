package main

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seedHome writes a $HOME with other mcpServers, unrelated top-level keys
// in ~/.claude.json, and a foreign SessionStart hook plus unrelated keys in
// ~/.claude/settings.json — the punch's clause 1 fixture.
func seedHome(t *testing.T, home string) (claudeJSONPath, settingsJSONPath string) {
	t.Helper()
	claudeJSONPath = filepath.Join(home, ".claude.json")
	settingsJSONPath = filepath.Join(home, ".claude", "settings.json")

	claudeJSON := `{
  "unrelatedTopLevelKey": "keep-me",
  "mcpServers": {
    "some-other-tool": {"type": "stdio", "command": "other-tool", "args": ["serve"]}
  }
}`
	settingsJSON := `{
  "anotherUnrelatedKey": 42,
  "hooks": {
    "SessionStart": [
      {"matcher": "startup", "hooks": [{"type": "command", "command": "foreign-hook --flag", "timeout": 3}]}
    ]
  }
}`
	if err := os.WriteFile(claudeJSONPath, []byte(claudeJSON), 0o600); err != nil {
		t.Fatalf("seed claude.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsJSONPath), 0o750); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(settingsJSONPath, []byte(settingsJSON), 0o600); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}
	// A distinguishing, non-default mode: the clause 1 fixture must prove
	// install preserves the file's original mode, not just happen to match
	// whatever default install would have chosen.
	if err := os.Chmod(settingsJSONPath, 0o400); err != nil {
		t.Fatalf("chmod settings.json: %v", err)
	}
	return claudeJSONPath, settingsJSONPath
}

func runInstallSubprocess(t *testing.T, bin, home string, extraArgs ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	args := append([]string{"install", "claude", "--no-verify"}, extraArgs...)
	cmd := exec.Command(bin, args...) //nolint:gosec // bin is the binary this test just built
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	var outBuf, errBuf safeBuffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run backstory install claude: %v (stderr: %s)", err, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func fileSHA256(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // path is a t.TempDir() path this test built, not external input
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return sha256.Sum256(data)
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// TestInstallClaudePreservesForeignEntriesAndIsIdempotent is the punch's
// DONE WHEN clause 1: under a seeded $HOME with other mcpServers, unrelated
// keys, and a foreign SessionStart hook, `install claude --no-verify` exits
// 0, adds our entries while preserving every pre-existing key and the
// foreign hook (deep-equal on parsed JSON minus our additions), keeps file
// modes unchanged, and a second run changes zero bytes in every touched
// file.
func TestInstallClaudePreservesForeignEntriesAndIsIdempotent(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	claudeJSONPath, settingsJSONPath := seedHome(t, home)

	modeClaudeBefore := fileMode(t, claudeJSONPath)
	modeSettingsBefore := fileMode(t, settingsJSONPath)

	stdout, stderr, exitCode := runInstallSubprocess(t, bin, home)
	if exitCode != 0 {
		t.Fatalf("install claude --no-verify exit code = %d, want 0 (stdout: %s, stderr: %s)", exitCode, stdout, stderr)
	}

	// Every pre-existing key and the foreign hook survive, plus our own
	// additions are present.
	var claudeRoot map[string]any
	claudeData, err := os.ReadFile(claudeJSONPath) //nolint:gosec // claudeJSONPath is a t.TempDir() path this test built, not external input
	if err != nil {
		t.Fatalf("read claude.json after install: %v", err)
	}
	if err := json.Unmarshal(claudeData, &claudeRoot); err != nil {
		t.Fatalf("parse claude.json after install: %v", err)
	}
	if claudeRoot["unrelatedTopLevelKey"] != "keep-me" {
		t.Errorf("claude.json lost unrelatedTopLevelKey: %#v", claudeRoot)
	}
	servers, _ := claudeRoot["mcpServers"].(map[string]any)
	if servers == nil {
		t.Fatalf("claude.json has no mcpServers after install: %#v", claudeRoot)
	}
	if _, ok := servers["some-other-tool"]; !ok {
		t.Errorf("claude.json lost mcpServers.some-other-tool: %#v", servers)
	}
	backstoryServer, ok := servers["backstory"].(map[string]any)
	if !ok {
		t.Fatalf("claude.json has no mcpServers.backstory after install: %#v", servers)
	}
	if backstoryServer["command"] != bin {
		t.Errorf("mcpServers.backstory.command = %v, want the absolute binary path %s", backstoryServer["command"], bin)
	}

	var settingsRoot map[string]any
	settingsData, err := os.ReadFile(settingsJSONPath) //nolint:gosec // settingsJSONPath is a t.TempDir() path this test built, not external input
	if err != nil {
		t.Fatalf("read settings.json after install: %v", err)
	}
	if err := json.Unmarshal(settingsData, &settingsRoot); err != nil {
		t.Fatalf("parse settings.json after install: %v", err)
	}
	if settingsRoot["anotherUnrelatedKey"] != float64(42) {
		t.Errorf("settings.json lost anotherUnrelatedKey: %#v", settingsRoot)
	}
	hooksObj, _ := settingsRoot["hooks"].(map[string]any)
	if hooksObj == nil {
		t.Fatalf("settings.json has no hooks after install: %#v", settingsRoot)
	}
	sessionStart, _ := hooksObj["SessionStart"].([]any)
	if len(sessionStart) != 2 {
		t.Fatalf("hooks.SessionStart has %d entries, want 2 (foreign + ours): %#v", len(sessionStart), sessionStart)
	}
	foundForeign, foundOurs := false, false
	for _, e := range sessionStart {
		entry, _ := e.(map[string]any)
		hooks, _ := entry["hooks"].([]any)
		for _, h := range hooks {
			hook, _ := h.(map[string]any)
			switch hook["command"] {
			case "foreign-hook --flag":
				foundForeign = true
			case bin + " hook session-start":
				foundOurs = true
			}
		}
	}
	if !foundForeign {
		t.Errorf("hooks.SessionStart lost the foreign hook: %#v", sessionStart)
	}
	if !foundOurs {
		t.Errorf("hooks.SessionStart is missing our hook: %#v", sessionStart)
	}

	// The skill file and CLAUDE.md stub exist.
	skillPath := filepath.Join(home, ".claude", "skills", "backstory", "SKILL.md")
	if _, err := os.Lstat(skillPath); err != nil {
		t.Errorf("skill file missing after install: %v", err)
	}
	claudeMDPath := filepath.Join(home, ".claude", "CLAUDE.md")
	claudeMDData, err := os.ReadFile(claudeMDPath) //nolint:gosec // claudeMDPath is a t.TempDir() path this test built, not external input
	if err != nil {
		t.Fatalf("read CLAUDE.md after install: %v", err)
	}
	if !strings.Contains(string(claudeMDData), "backstory:begin") {
		t.Errorf("CLAUDE.md missing the backstory stub markers: %q", claudeMDData)
	}

	// File modes are unchanged.
	if got := fileMode(t, claudeJSONPath); got != modeClaudeBefore {
		t.Errorf("claude.json mode = %04o, want unchanged %04o", got, modeClaudeBefore)
	}
	if got := fileMode(t, settingsJSONPath); got != modeSettingsBefore {
		t.Errorf("settings.json mode = %04o, want unchanged %04o", got, modeSettingsBefore)
	}

	// A second run exits 0 and changes zero bytes in every touched file.
	shaClaudeAfterFirst := fileSHA256(t, claudeJSONPath)
	shaSettingsAfterFirst := fileSHA256(t, settingsJSONPath)
	shaSkillAfterFirst := fileSHA256(t, skillPath)
	shaClaudeMDAfterFirst := fileSHA256(t, claudeMDPath)

	stdout2, stderr2, exitCode2 := runInstallSubprocess(t, bin, home)
	if exitCode2 != 0 {
		t.Fatalf("second install claude --no-verify exit code = %d, want 0 (stdout: %s, stderr: %s)", exitCode2, stdout2, stderr2)
	}

	if got := fileSHA256(t, claudeJSONPath); got != shaClaudeAfterFirst {
		t.Errorf("claude.json changed on second run: sha256 %x -> %x", shaClaudeAfterFirst, got)
	}
	if got := fileSHA256(t, settingsJSONPath); got != shaSettingsAfterFirst {
		t.Errorf("settings.json changed on second run: sha256 %x -> %x", shaSettingsAfterFirst, got)
	}
	if got := fileSHA256(t, skillPath); got != shaSkillAfterFirst {
		t.Errorf("skill file changed on second run: sha256 %x -> %x", shaSkillAfterFirst, got)
	}
	if got := fileSHA256(t, claudeMDPath); got != shaClaudeMDAfterFirst {
		t.Errorf("CLAUDE.md changed on second run: sha256 %x -> %x", shaClaudeMDAfterFirst, got)
	}
}
