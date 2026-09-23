package install_test

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sha256OrFatal(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // path is a t.TempDir() path this test built, not external input
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return sha256.Sum256(data)
}

func statusMap(t *testing.T, items []install.Item) map[string]install.ItemStatus {
	t.Helper()
	m := make(map[string]install.ItemStatus, len(items))
	for _, it := range items {
		m[it.Name] = it.Status
	}
	return m
}

// TestCheckReportsAbsentOnFreshHomeAndPresentAfterInstall is clause 2's
// --check requirement: absent for every item on a fresh $HOME (and a
// non-zero-worthy report), present for every item after install.
func TestCheckReportsAbsentOnFreshHomeAndPresentAfterInstall(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir()}

	items, err := install.Check(paths, opts)
	if err != nil {
		t.Fatalf("Check on fresh home: %v", err)
	}
	statuses := statusMap(t, items)
	for name, status := range statuses {
		if status != install.StatusAbsent {
			t.Errorf("fresh home: item %s status = %s, want absent", name, status)
		}
	}
	if len(statuses) != 5 {
		t.Fatalf("Check returned %d items, want 5: %#v", len(statuses), items)
	}

	if err := install.Install(paths, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}

	items, err = install.Check(paths, opts)
	if err != nil {
		t.Fatalf("Check after install: %v", err)
	}
	statuses = statusMap(t, items)
	for name, status := range statuses {
		if status != install.StatusPresent {
			t.Errorf("after install: item %s status = %s, want present", name, status)
		}
	}
}

// TestRemoveRestoresSeededStateExactly is clause 2's --remove requirement:
// after install, --remove leaves every file deep-equal to the seeded state
// (foreign hook, other servers, prior CLAUDE.md text intact) and removes
// the skill file.
func TestRemoveRestoresSeededStateExactly(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir()}

	seededClaudeJSON := `{"foo":"bar","mcpServers":{"other-tool":{"type":"stdio","command":"other","args":[]}}}`
	seededSettingsJSON := `{"unrelated":true,"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"foreign-hook","timeout":5}]}]}}`
	seededClaudeMD := "# prior notes\nsome existing content\n"

	mustWriteFile(t, paths.ClaudeJSON, seededClaudeJSON)
	mustWriteFile(t, paths.SettingsJSON, seededSettingsJSON)
	mustWriteFile(t, paths.ClaudeMD, seededClaudeMD)

	if err := install.Install(paths, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := install.Remove(paths, opts); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	var gotClaude, wantClaude map[string]any
	gotClaudeData, err := os.ReadFile(paths.ClaudeJSON)
	if err != nil {
		t.Fatalf("read claude.json after remove: %v", err)
	}
	if err := json.Unmarshal(gotClaudeData, &gotClaude); err != nil {
		t.Fatalf("parse claude.json after remove: %v", err)
	}
	if err := json.Unmarshal([]byte(seededClaudeJSON), &wantClaude); err != nil {
		t.Fatalf("parse seeded claude.json: %v", err)
	}
	assertDeepEqualJSON(t, "claude.json", wantClaude, gotClaude)

	var gotSettings, wantSettings map[string]any
	gotSettingsData, err := os.ReadFile(paths.SettingsJSON)
	if err != nil {
		t.Fatalf("read settings.json after remove: %v", err)
	}
	if err := json.Unmarshal(gotSettingsData, &gotSettings); err != nil {
		t.Fatalf("parse settings.json after remove: %v", err)
	}
	if err := json.Unmarshal([]byte(seededSettingsJSON), &wantSettings); err != nil {
		t.Fatalf("parse seeded settings.json: %v", err)
	}
	assertDeepEqualJSON(t, "settings.json", wantSettings, gotSettings)

	gotClaudeMD, err := os.ReadFile(paths.ClaudeMD)
	if err != nil {
		t.Fatalf("read CLAUDE.md after remove: %v", err)
	}
	if string(gotClaudeMD) != seededClaudeMD {
		t.Errorf("CLAUDE.md after remove = %q, want %q (seeded state)", gotClaudeMD, seededClaudeMD)
	}

	if _, err := os.Lstat(paths.SkillPath); !os.IsNotExist(err) {
		t.Errorf("skill file still exists after remove (err=%v), want removed", err)
	}
}

func assertDeepEqualJSON(t *testing.T, label string, want, got map[string]any) {
	t.Helper()
	wantBytes, _ := json.Marshal(want)
	gotBytes, _ := json.Marshal(got)
	var wantCanon, gotCanon any
	_ = json.Unmarshal(wantBytes, &wantCanon)
	_ = json.Unmarshal(gotBytes, &gotCanon)
	wantCanonBytes, _ := json.Marshal(wantCanon)
	gotCanonBytes, _ := json.Marshal(gotCanon)
	if string(wantCanonBytes) != string(gotCanonBytes) {
		t.Errorf("%s not deep-equal to seeded state:\n got:  %s\n want: %s", label, gotCanonBytes, wantCanonBytes)
	}
}

// TestMalformedClaudeJSONRefusesAndLeavesFilesUntouched is clause 2's
// never-clobber requirement: a malformed ~/.claude.json makes Install
// return a named error and both files untouched (sha256 unchanged).
func TestMalformedClaudeJSONRefusesAndLeavesFilesUntouched(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir()}

	mustWriteFile(t, paths.ClaudeJSON, `{not valid json`)
	mustWriteFile(t, paths.SettingsJSON, `{"hooks":{}}`)

	beforeClaude := sha256OrFatal(t, paths.ClaudeJSON)
	beforeSettings := sha256OrFatal(t, paths.SettingsJSON)

	err := install.Install(paths, opts)
	if err == nil {
		t.Fatalf("Install with malformed claude.json returned nil error, want a named error")
	}
	if _, ok := errorsIs(err, install.ErrMalformedClaudeJSON); !ok {
		t.Errorf("Install error = %v, want it to wrap ErrMalformedClaudeJSON", err)
	}

	afterClaude := sha256OrFatal(t, paths.ClaudeJSON)
	afterSettings := sha256OrFatal(t, paths.SettingsJSON)
	if beforeClaude != afterClaude {
		t.Errorf("claude.json sha256 changed: before %x, after %x", beforeClaude, afterClaude)
	}
	if beforeSettings != afterSettings {
		t.Errorf("settings.json sha256 changed: before %x, after %x", beforeSettings, afterSettings)
	}

	if _, err := os.Lstat(paths.SkillPath); !os.IsNotExist(err) {
		t.Errorf("skill file was written despite malformed claude.json (err=%v)", err)
	}
	if _, err := os.Lstat(paths.ClaudeMD); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md was written despite malformed claude.json (err=%v)", err)
	}
}

// errorsIs is errors.Is spelled out so a wrapped-error mismatch prints a
// useful message above rather than just true/false.
func errorsIs(err, target error) (error, bool) {
	for e := err; e != nil; {
		if e == target {
			return e, true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return nil, false
}

// TestClaudeMDStubNotDuplicatedOnReinstall is clause 3's stub requirement:
// the AGENTS.md/CLAUDE.md stub is one line between marker comments and is
// not duplicated when install runs twice.
func TestClaudeMDStubNotDuplicatedOnReinstall(t *testing.T) {
	home := t.TempDir()
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")
	mustWriteFile(t, claudeMD, "# my existing notes\n")

	paths := install.DefaultPaths(home)
	for i := 0; i < 2; i++ {
		if err := install.InstallStub(paths.ClaudeMD); err != nil {
			t.Fatalf("round %d: InstallStub: %v", i, err)
		}
	}

	data, err := os.ReadFile(claudeMD) //nolint:gosec // claudeMD is a t.TempDir() path this test built, not external input
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(data)
	if n := strings.Count(content, install.StubMarkerBegin); n != 1 {
		t.Errorf("stub begin marker appears %d times, want 1:\n%s", n, content)
	}
	if !strings.Contains(content, "# my existing notes") {
		t.Errorf("prior CLAUDE.md text was lost:\n%s", content)
	}
}

// TestClaudeMDStubUpgradesOldTextInPlaceOnReinstall is DONE WHEN clause 5
// (task d6ddfce3): running install twice over a file holding the OLD stub
// (the unconditional "call recall" text, pre-dating this punch) leaves
// exactly one stub between the markers, with the NEW text, and all foreign
// content — before, between, and after the stub — unchanged.
func TestClaudeMDStubUpgradesOldTextInPlaceOnReinstall(t *testing.T) {
	home := t.TempDir()
	claudeMD := filepath.Join(home, ".claude", "CLAUDE.md")

	const oldStubLine = "Backstory: call the `backstory` MCP tool's `recall`; see `~/.claude/skills/backstory/SKILL.md`."
	seeded := "# before\nsome prior notes\n" +
		install.StubMarkerBegin + "\n" + oldStubLine + "\n" + install.StubMarkerEnd + "\n" +
		"# after\nmore foreign content\n"
	mustWriteFile(t, claudeMD, seeded)

	paths := install.DefaultPaths(home)
	for i := 0; i < 2; i++ {
		if err := install.InstallStub(paths.ClaudeMD); err != nil {
			t.Fatalf("round %d: InstallStub: %v", i, err)
		}
	}

	data, err := os.ReadFile(claudeMD) //nolint:gosec // claudeMD is a t.TempDir() path this test built, not external input
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(data)

	if n := strings.Count(content, install.StubMarkerBegin); n != 1 {
		t.Errorf("stub begin marker appears %d times, want 1:\n%s", n, content)
	}
	if n := strings.Count(content, install.StubMarkerEnd); n != 1 {
		t.Errorf("stub end marker appears %d times, want 1:\n%s", n, content)
	}
	if strings.Contains(content, oldStubLine) {
		t.Errorf("old stub text is still present, want it replaced:\n%s", content)
	}
	if !strings.Contains(content, install.StubLine) {
		t.Errorf("new stub text is missing:\n%s", content)
	}
	if !strings.Contains(content, "# before\nsome prior notes") {
		t.Errorf("foreign content before the stub was lost:\n%s", content)
	}
	if !strings.Contains(content, "# after\nmore foreign content") {
		t.Errorf("foreign content after the stub was lost:\n%s", content)
	}
}

// TestMalformedSettingsJSONRefusesAndLeavesFilesUntouched mirrors the
// previous test for the other JSON file: a malformed settings.json refuses
// with a named error and leaves both files untouched, even though
// claude.json alone is perfectly valid.
func TestMalformedSettingsJSONRefusesAndLeavesFilesUntouched(t *testing.T) {
	home := t.TempDir()
	paths := install.DefaultPaths(home)
	opts := install.Options{Prefix: t.TempDir()}

	mustWriteFile(t, paths.ClaudeJSON, `{"mcpServers":{}}`)
	mustWriteFile(t, paths.SettingsJSON, `not json at all`)

	beforeClaude := sha256OrFatal(t, paths.ClaudeJSON)
	beforeSettings := sha256OrFatal(t, paths.SettingsJSON)

	err := install.Install(paths, opts)
	if err == nil {
		t.Fatalf("Install with malformed settings.json returned nil error, want a named error")
	}
	if _, ok := errorsIs(err, install.ErrMalformedSettingsJSON); !ok {
		t.Errorf("Install error = %v, want it to wrap ErrMalformedSettingsJSON", err)
	}

	afterClaude := sha256OrFatal(t, paths.ClaudeJSON)
	afterSettings := sha256OrFatal(t, paths.SettingsJSON)
	if beforeClaude != afterClaude {
		t.Errorf("claude.json sha256 changed: before %x, after %x", beforeClaude, afterClaude)
	}
	if beforeSettings != afterSettings {
		t.Errorf("settings.json sha256 changed: before %x, after %x", beforeSettings, afterSettings)
	}
}
