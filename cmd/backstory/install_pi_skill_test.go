package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `install pi` writes the Pi skill; `install --check pi` reports it present,
// and a foreign file there reports foreign-conflict and stays byte-identical.
func TestInstallPiSkillCheckLines(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	dest := filepath.Join(home, ".pi", "agent", "skills", "backstory", "SKILL.md")

	if _, stderr, code := runBackstory(t, bin, home, "install", "pi"); code != 0 {
		t.Fatalf("install pi: exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("pi skill not written: %v", err)
	}
	stdout, stderr, code := runBackstory(t, bin, home, "install", "--check", "pi")
	if code != 0 || !strings.Contains(stdout, "pi: pi-skill: present") {
		t.Fatalf("install --check pi: exit %d, stdout %q, stderr %q; want a `pi: pi-skill: present` line", code, stdout, stderr)
	}

	foreignHome := t.TempDir()
	foreign := filepath.Join(foreignHome, ".pi", "agent", "skills", "backstory", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o750); err != nil {
		t.Fatal(err)
	}
	const body = "someone else's skill\n"
	if err := os.WriteFile(foreign, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	runBackstory(t, bin, foreignHome, "install", "pi")
	stdout, _, _ = runBackstory(t, bin, foreignHome, "install", "--check", "pi")
	if !strings.Contains(stdout, "pi: pi-skill: foreign-conflict") {
		t.Errorf("stdout = %q, want a `pi: pi-skill: foreign-conflict` line", stdout)
	}
	if b, _ := os.ReadFile(foreign); string(b) != body { //nolint:gosec // test temp dir
		t.Errorf("foreign skill modified: %q", b)
	}
}
