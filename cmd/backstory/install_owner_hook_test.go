package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallNamesHookLeftAsOwners: when a hook event already runs
// Backstory's hook through a command the owner wrote, `backstory install`
// leaves the file as it is and prints a line naming the harness, the event
// and the file, for Claude and Codex. A plain install prints no such line.
func TestInstallNamesHookLeftAsOwners(t *testing.T) {
	bin := buildBackstory(t)
	cases := []struct {
		harness, rel, event, command string
	}{
		{"claude", ".claude/settings.json", "PostToolUse", "BACKSTORY_OFF=1 " + bin + " hook post-tool-use"},
		{"codex", ".codex/hooks.json", "PostToolUse", "true || " + bin + " hook post-tool-use --harness codex"},
		{"codex", ".codex/hooks.json", "SessionStart", "exit 0; backstory hook session-start --harness codex"},
	}
	for _, c := range cases {
		t.Run(c.harness+"/"+c.event, func(t *testing.T) {
			for _, wrapped := range []bool{true, false} {
				home := t.TempDir()
				path := filepath.Join(home, c.rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				var before string
				if wrapped {
					b, err := json.Marshal(map[string]any{"hooks": map[string]any{c.event: []any{map[string]any{
						"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": c.command}},
					}}}})
					if err != nil {
						t.Fatal(err)
					}
					before = string(b)
					if err := os.WriteFile(path, b, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				cmd := exec.Command(bin, "install", c.harness, "--no-verify") //nolint:gosec // bin is the binary this test just built
				cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
				var outBuf, errBuf safeBuffer
				cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
				if err := cmd.Run(); err != nil {
					t.Fatalf("install %s: %v; stderr: %s", c.harness, err, errBuf.String())
				}
				stdout := outBuf.String()
				want := c.harness + ": kept your " + c.event + " hook in " + path + ": it runs Backstory through a command you wrote"
				if !wrapped {
					if strings.Contains(stdout, ": kept your ") {
						t.Errorf("plain install printed a kept-hook line:\n%s", stdout)
					}
					continue
				}
				if n := strings.Count(stdout, want); n != 1 {
					t.Errorf("stdout has the kept-hook line %d times, want 1 (%q):\n%s", n, want, stdout)
				}
				if strings.Count(stdout, ": kept your ") != 1 {
					t.Errorf("stdout names other hooks as kept:\n%s", stdout)
				}
				var after, orig map[string]map[string]any
				got, err := os.ReadFile(path) //nolint:gosec // t.TempDir() path
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(got, &after); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(before), &orig); err != nil {
					t.Fatal(err)
				}
				a, _ := json.Marshal(after["hooks"][c.event])
				o, _ := json.Marshal(orig["hooks"][c.event])
				if string(a) != string(o) {
					t.Errorf("%s after install = %s, want the owner's group unchanged: %s", c.event, a, o)
				}
			}
		})
	}
}
