package skill_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/skill"
)

var listItemRe = regexp.MustCompile(`^(\d+)\. (.*)$`)

// parseSkillLines reads AGENT-CONTRACT.md and returns the eight logical
// lines of its "## The skill" section's numbered list (items 1-8),
// unwrapped: a continuation line (three-space indented) is joined onto its
// item with a single space, and the "N. " / "   " prefixes are stripped.
// This is independent of skill.Embedded's content on purpose — it is the
// test's own reconstruction of "the eight lines of AGENT-CONTRACT.md §The
// skill" that clause 3 requires Embedded to match byte-for-byte.
func parseSkillLines(t *testing.T, contractPath string) []string {
	t.Helper()
	f, err := os.Open(contractPath) //nolint:gosec // contractPath is a fixed test-source-relative path, not external input
	if err != nil {
		t.Fatalf("open %s: %v", contractPath, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	inSection := false
	var items []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			items = append(items, current.String())
			current.Reset()
		}
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			if inSection {
				break // left "## The skill" for the next heading
			}
			if strings.HasPrefix(line, "## The skill") {
				inSection = true
			}
			continue
		}
		if !inSection {
			continue
		}
		if m := listItemRe.FindStringSubmatch(line); m != nil {
			flush()
			current.WriteString(m[2])
			continue
		}
		if strings.HasPrefix(line, "   ") && current.Len() > 0 {
			current.WriteString(" ")
			current.WriteString(strings.TrimPrefix(line, "   "))
			continue
		}
		flush() // blank line, or prose outside the list: item is done
	}
	flush()
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", contractPath, err)
	}
	return items
}

// TestEmbeddedSkillMatchesAgentContract is the punch's DONE WHEN clause 3:
// the embedded skill is byte-for-byte the eight lines of AGENT-CONTRACT.md
// §The skill (description line + seven body lines).
//
// Mutation probe: change one word of internal/skill/SKILL.md (e.g.
// "ceremony" -> "ceremonyx") -> RED ("skill.Embedded line 4 = ...ceremonyx.,
// want ...ceremony."); restore -> GREEN.
func TestEmbeddedSkillMatchesAgentContract(t *testing.T) {
	want := parseSkillLines(t, "../../AGENT-CONTRACT.md")
	if len(want) != 8 {
		t.Fatalf("parsed %d lines from AGENT-CONTRACT.md §The skill, want 8: %#v", len(want), want)
	}

	_, rules := splitFrontmatter(t, skill.Embedded)
	got := append([]string{want[0]}, strings.Split(strings.TrimRight(rules, "\n"), "\n")...)
	if len(got) != 8 {
		t.Fatalf("skill.Embedded has %d rule lines after the frontmatter, want 7: %#v", len(got)-1, got[1:])
	}

	// Line 1 of the contract is the intents line; the file carries it as the
	// frontmatter description (checked by TestEmbeddedSkillHasFrontmatter).
	for i := 1; i < len(want); i++ {
		if got[i] != want[i] {
			t.Errorf("skill.Embedded line %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}

// TestInstallSymlinksToPackagedPathWhenPresent is clause 3's packaged-path
// requirement: when the packaged skill path exists under a temp prefix,
// install writes a symlink to it rather than the embedded copy.
func TestInstallSymlinksToPackagedPathWhenPresent(t *testing.T) {
	prefix := t.TempDir()
	packaged := filepath.Join(prefix, skill.PackagedSkillPath)
	if err := os.MkdirAll(filepath.Dir(packaged), 0o750); err != nil {
		t.Fatalf("mkdir packaged dir: %v", err)
	}
	if err := os.WriteFile(packaged, []byte("packaged copy\n"), 0o600); err != nil {
		t.Fatalf("write packaged skill: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "skills", "backstory", "SKILL.md")
	changed, err := skill.Install(dest, prefix)
	if err != nil {
		t.Fatalf("skill.Install: %v", err)
	}
	if !changed {
		t.Fatalf("skill.Install reported no change on a fresh dest")
	}

	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("lstat dest: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("dest is not a symlink, want a symlink to the packaged path")
	}
	target, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("readlink dest: %v", err)
	}
	if target != packaged {
		t.Errorf("symlink target = %q, want %q", target, packaged)
	}

	// A second install changes nothing: same symlink, no error.
	changed, err = skill.Install(dest, prefix)
	if err != nil {
		t.Fatalf("second skill.Install: %v", err)
	}
	if changed {
		t.Errorf("second skill.Install reported a change; want idempotent no-op")
	}
}

// TestInstallWritesEmbeddedWhenPackagedPathAbsent is clause 3's fallback:
// with no packaged copy under the prefix, install writes the embedded copy
// verbatim.
func TestInstallWritesEmbeddedWhenPackagedPathAbsent(t *testing.T) {
	prefix := t.TempDir() // empty: no packaged skill under it
	dest := filepath.Join(t.TempDir(), "skills", "backstory", "SKILL.md")

	changed, err := skill.Install(dest, prefix)
	if err != nil {
		t.Fatalf("skill.Install: %v", err)
	}
	if !changed {
		t.Fatalf("skill.Install reported no change on a fresh dest")
	}

	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("lstat dest: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("dest is a symlink, want a regular file (embedded copy)")
	}
	data, err := os.ReadFile(dest) //nolint:gosec // dest is a t.TempDir() path this test built, not external input
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(data, skill.Embedded) {
		t.Errorf("dest content != skill.Embedded")
	}
}

// The skill tells agents to set supersedes whenever a record replaces or
// corrects an earlier one, not only for the Resume handoff.
func TestEmbeddedSkillInstructsSupersedeOnReplacement(t *testing.T) {
	s := string(skill.Embedded)
	for _, want := range []string{"replaces or corrects", "`related_decisions`", "`supersedes`", "`confirm supersede`"} {
		if !strings.Contains(s, want) {
			t.Errorf("embedded skill missing %q", want)
		}
	}
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestPastEmbeddedSHA256CoversEveryPriorVersion(t *testing.T) {
	have := map[string]bool{}
	for _, h := range skill.PastEmbeddedSHA256 {
		have[h] = true
	}
	for _, p := range pastSkills {
		if hashHex([]byte(p.body)) != p.sha256 {
			t.Fatalf("test data for %s does not match its recorded hash", p.commit)
		}
		if !have[p.sha256] {
			t.Errorf("PastEmbeddedSHA256 lacks the %s version (%s)", p.commit, p.sha256)
		}
	}
	if have[hashHex(skill.Embedded)] {
		t.Error("PastEmbeddedSHA256 contains the current embedded hash")
	}
}

func TestOutdatedSkillIsReportedAndReplacedWithBackup(t *testing.T) {
	for _, p := range pastSkills {
		t.Run(p.commit, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "skills", "backstory", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte(p.body), 0o600); err != nil {
				t.Fatal(err)
			}
			prefix := t.TempDir()
			if got := skill.CheckStatus(dest, prefix); got != skill.StatusOutdated {
				t.Fatalf("CheckStatus = %q, want outdated", got)
			}
			changed, err := skill.Install(dest, prefix)
			if err != nil || !changed {
				t.Fatalf("Install = %v, %v; want changed", changed, err)
			}
			got, _ := os.ReadFile(dest) //nolint:gosec // test temp path
			if !bytes.Equal(got, skill.Embedded) {
				t.Error("dest is not the current embedded copy after Install")
			}
			backups, _ := filepath.Glob(dest + ".bak-*")
			if len(backups) != 1 {
				t.Fatalf("want exactly one backup beside dest, got %v", backups)
			}
			old, _ := os.ReadFile(backups[0]) //nolint:gosec // test temp path
			if string(old) != p.body {
				t.Error("backup does not hold the old bytes")
			}
			if got := skill.CheckStatus(dest, prefix); got != skill.StatusPresent {
				t.Errorf("CheckStatus after Install = %q, want present", got)
			}
		})
	}
}

func TestUnknownSkillStaysForeignAndUntouched(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "SKILL.md")
	body := []byte("someone else's skill\n")
	if err := os.WriteFile(dest, body, 0o600); err != nil {
		t.Fatal(err)
	}
	prefix := t.TempDir()
	if got := skill.CheckStatus(dest, prefix); got != skill.StatusForeign {
		t.Fatalf("CheckStatus = %q, want foreign-conflict", got)
	}
	if changed, err := skill.Install(dest, prefix); err != nil || changed {
		t.Fatalf("Install = %v, %v; want unchanged", changed, err)
	}
	got, _ := os.ReadFile(dest) //nolint:gosec // test temp path
	if !bytes.Equal(got, body) {
		t.Error("foreign file was modified")
	}
	if backups, _ := filepath.Glob(dest + ".bak-*"); len(backups) != 0 {
		t.Errorf("unexpected backup %v", backups)
	}
}

var skillNameRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// splitFrontmatter returns the lines between the leading "---" fences and
// the remainder of the file.
func splitFrontmatter(t *testing.T, data []byte) (front []string, rest string) {
	t.Helper()
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatalf("SKILL.md does not begin with a --- frontmatter block: %.40q", s)
	}
	block, rest, ok := strings.Cut(s[len("---\n"):], "\n---\n")
	if !ok {
		t.Fatal("SKILL.md frontmatter has no closing --- fence")
	}
	return strings.Split(block, "\n"), rest
}

// Agent Skills (and so Pi and Claude Code) skip a SKILL.md without a leading
// YAML block carrying a valid name and a non-empty description of <=1024 chars.
func TestEmbeddedSkillHasFrontmatter(t *testing.T) {
	front, _ := splitFrontmatter(t, skill.Embedded)
	meta := map[string]string{}
	for _, l := range front {
		k, v, ok := strings.Cut(l, ": ")
		if !ok {
			t.Fatalf("frontmatter line is not `key: value`: %q", l)
		}
		if strings.HasPrefix(v, `"`) {
			u, err := strconv.Unquote(v)
			if err != nil {
				t.Fatalf("frontmatter %s is not a valid double-quoted YAML string: %v", k, err)
			}
			v = u
		}
		meta[k] = v
	}
	if len(meta) != 2 {
		t.Errorf("frontmatter keys = %v, want exactly name and description", meta)
	}
	if meta["name"] != "backstory" || !skillNameRe.MatchString(meta["name"]) {
		t.Errorf("name = %q, want backstory matching %s", meta["name"], skillNameRe)
	}
	if n := len(meta["description"]); n == 0 || n > 1024 {
		t.Errorf("description length = %d, want 1..1024", n)
	}
}

// The rule lines that followed the old prose header survive byte-for-byte.
func TestEmbeddedSkillKeepsRuleLinesVerbatim(t *testing.T) {
	before := pastSkills[len(pastSkills)-3] // 462e93f: the version before the frontmatter
	_, wantRules, ok := strings.Cut(before.body, "\n")
	if !ok {
		t.Fatal("462e93f test data has no first line")
	}
	_, rules := splitFrontmatter(t, skill.Embedded)
	// The handoff line (6) is deliberately reworded by task 01c20ad2; every
	// other rule line is verbatim.
	gotLines, wantLines := strings.Split(rules, "\n"), strings.Split(wantRules, "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("rule line count = %d, want %d", len(gotLines), len(wantLines))
	}
	for i := range gotLines {
		if strings.HasPrefix(wantLines[i], "end with `note handoff`") {
			continue
		}
		if gotLines[i] != wantLines[i] {
			t.Errorf("rule line %d differs:\n got %q\nwant %q", i+1, gotLines[i], wantLines[i])
		}
	}
}

// Task 01c20ad2 clause 1: the handoff line does not carry this-session
// prohibitions forward, and says next is an action the next session can start.
func TestSkillHandoffLineDropsSessionOnlyInstructions(t *testing.T) {
	body := string(skill.Embedded)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var handoff string
	for _, l := range lines {
		if strings.HasPrefix(l, "end with `note handoff`") {
			handoff = l
		}
	}
	if handoff == "" {
		t.Fatalf("no handoff line in skill:\n%s", body)
	}
	for _, want := range []string{"Do not carry forward instructions that applied only to the current session", "`next` is an action the next session can start"} {
		if !strings.Contains(handoff, want) {
			t.Errorf("handoff line missing %q: %s", want, handoff)
		}
	}
	if strings.Contains(handoff, "what not to do") {
		t.Errorf("handoff line still asks for %q: %s", "what not to do", handoff)
	}
}
