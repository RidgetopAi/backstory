// Package ops holds the systemd --user unit Backstory ships in-repo, and the
// test that pins its shape (task f2718b5b, punch acceptance clause 3).
package ops

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestBackstoryServiceHasRequiredDirectivesNoSandbox is the punch's
// acceptance clause 3 (task f2718b5b): ops/backstory.service is the unit
// measured running on Brian's desktop 2026-09-22 (context d38ca301) —
// ExecStart, Restart, RuntimeDirectory, the BACKSTORY_CLAUDE_ROOT
// environment, and NoNewPrivileges present — with none of the
// mount-namespace sandbox directives probe 2 showed break
// /proc/<harness_pid>/cwd under the `systemd --user` manager's implicit user
// namespace.
func TestBackstoryServiceHasRequiredDirectivesNoSandbox(t *testing.T) {
	data, err := os.ReadFile("backstory.service")
	if err != nil {
		t.Fatalf("read backstory.service: %v", err)
	}
	content := string(data)

	required := []string{
		"ExecStart=%h/.local/bin/backstory daemon",
		"Restart=on-failure",
		"RestartSec=2s",
		"RuntimeDirectory=backstory",
		"Environment=BACKSTORY_CLAUDE_ROOT=%h/.claude/projects",
		"NoNewPrivileges=yes",
	}
	for _, want := range required {
		if !strings.Contains(content, want) {
			t.Errorf("backstory.service missing directive %q", want)
		}
	}

	forbidden := []string{
		"ProtectSystem",
		"ProtectHome",
		"PrivateTmp",
		"PrivateNetwork",
		"ReadWritePaths",
		"PrivateUsers",
	}
	for _, bad := range forbidden {
		// Anchored to the start of a directive line (ignoring leading
		// whitespace) so the mention of these names in the file's own
		// explanatory comment block doesn't trip the assertion — only an
		// actual `Key=value` directive counts.
		re := regexp.MustCompile(`(?m)^\s*` + bad + `\s*=`)
		if re.MatchString(content) {
			t.Errorf("backstory.service sets forbidden mount-namespace directive %q "+
				"(task f2718b5b: breaks /proc/<harness_pid>/cwd under systemd --user, context d38ca301)", bad)
		}
	}
}
