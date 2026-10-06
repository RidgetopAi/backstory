// Codex adapter (decision 3e14db82): `backstory install codex` registers the
// MCP server in ~/.codex/config.toml, the SessionStart and PostToolUse hooks
// in ~/.codex/hooks.json, a one-line stub in ~/.codex/AGENTS.md, and the Backstory skill in
// ~/.codex/skills/backstory/SKILL.md.
//
// config.toml is hand-written (comments, trust tables, other servers), so it
// is never parsed and re-serialized: the backstory table is appended as a
// marker-delimited text block and removed by cutting exactly that block.
package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/RidgetopAi/backstory/internal/skill"
)

// Codex item names, for --check output.
const (
	ItemCodexMCPServer = "mcp-server"
	ItemCodexAgentsMD  = "agents-md-stub"
	ItemCodexSkill     = "codex-skill"
)

// Codex hook commands. Payload handling is `backstory hook … --harness
// codex` (cmd/backstory/hook.go).
const (
	CodexHookCommandSessionStart = "backstory hook session-start --harness codex"
	CodexHookCommandPostToolUse  = "backstory hook post-tool-use --harness codex"
	// CodexHookMatcher is empty: match every source / every tool.
	CodexHookMatcher = ""
)

// Markers around the block appended to config.toml. The begin line carries
// codexNoEOLFlag when the original file lacked a trailing newline and the
// installer had to add one, so remove restores the original bytes exactly.
const (
	CodexTOMLMarkerBegin = "# backstory:begin"
	CodexTOMLMarkerEnd   = "# backstory:end"
	codexTOMLBeginLine   = CodexTOMLMarkerBegin + " (managed by `backstory install codex`; do not edit)"
	codexNoEOLFlag       = " [no-eol]"
	codexTOMLBody        = "[mcp_servers.backstory]\ncommand = \"backstory\"\nargs = [\"mcp\"]\n"

	// CodexStubLine is the one-line AGENTS.md stub.
	CodexStubLine = "Backstory: if no SessionStart block is present, call the `backstory` MCP tool's `recall`; see the `backstory` skill, `~/.codex/skills/backstory/SKILL.md`."
)

var codexStubBlock = StubMarkerBegin + "\n" + CodexStubLine + "\n" + StubMarkerEnd + "\n"

// Codex 0.151 only runs a hook whose hash is recorded in config.toml under
//
//	[hooks.state."<hooks.json path>:<event>:<group>:<handler>"]
//	trusted_hash = "..."
//
// Codex derives that hash itself and we cannot reproduce it reliably from
// outside, so Backstory never writes trust entries: it tells the user to
// approve the hooks in codex and --check reports StatusNotTrusted until an
// entry for each hook's key exists. The entries are the user's (written by
// codex), so --remove leaves them alone.
const (
	codexEventSessionStart = "session_start"
	codexEventPostToolUse  = "post_tool_use"

	// CodexTrustNotice is the one line printed after `install codex`.
	CodexTrustNotice = "codex: action needed: start codex and approve the Backstory hooks (SessionStart, PostToolUse); until then Codex 0.151 will not run them, and `backstory install --check` reports them as not-trusted"
)

// ErrForeignConflict marks an item whose slot is occupied by something
// backstory did not write. The item is left untouched.
var ErrForeignConflict = errors.New("foreign-conflict")

// ErrMalformedCodexHooksJSON is returned for an unparseable hooks.json.
var ErrMalformedCodexHooksJSON = errors.New("install: ~/.codex/hooks.json is not valid JSON")

// foreignBackstoryTable matches a table header or dotted key that defines
// mcp_servers.backstory (bare or quoted).
var foreignBackstoryTable = regexp.MustCompile(`(?m)^[ \t]*(?:\[\[?[ \t]*mcp_servers[ \t]*\.[ \t]*(?:"backstory"|'backstory'|backstory)[ \t]*[.\]]|mcp_servers[ \t]*\.[ \t]*(?:"backstory"|'backstory'|backstory)[ \t]*[.=])`)

// CodexPaths locates the files the Codex adapter touches.
type CodexPaths struct {
	ConfigTOML string
	HooksJSON  string
	AgentsMD   string
	SkillPath  string // ~/.codex/skills/backstory/SKILL.md
}

// DefaultCodexPaths returns Codex's user-level surfaces under home.
func DefaultCodexPaths(home string) CodexPaths {
	dir := filepath.Join(home, ".codex")
	return CodexPaths{
		ConfigTOML: filepath.Join(dir, "config.toml"),
		HooksJSON:  filepath.Join(dir, "hooks.json"),
		AgentsMD:   filepath.Join(dir, "AGENTS.md"),
		SkillPath:  filepath.Join(dir, "skills", "backstory", "SKILL.md"),
	}
}

type codexAdapter struct{}

func (codexAdapter) Name() string { return HarnessCodex }

func (codexAdapter) Install(home string, opts Options) error {
	return InstallCodex(DefaultCodexPaths(home), opts)
}

// InstallNotice is printed by the CLI after a successful install.
func (codexAdapter) InstallNotice() string { return CodexTrustNotice }

func (codexAdapter) Remove(home string, opts Options) error {
	return RemoveCodex(DefaultCodexPaths(home), opts)
}

func (codexAdapter) Check(home string, opts Options) ([]Item, error) {
	return CheckCodex(DefaultCodexPaths(home), opts)
}

func codexHookEntry(command string, timeoutSeconds int) map[string]any {
	return map[string]any{
		"matcher": CodexHookMatcher,
		"hooks": []any{
			map[string]any{"type": "command", "command": command, "timeout": float64(timeoutSeconds)},
		},
	}
}

// InstallCodex installs every Codex item. A malformed hooks.json fails
// before anything is written. A foreign mcp_servers.backstory is reported as
// an ErrForeignConflict naming that item, after every other item has still
// been installed. Verify-on-install is not run: the codex payload shape is
// owned by the hook subcommand's own punch.
func InstallCodex(paths CodexPaths, opts Options) error {
	hooksRoot, hooksMode, err := loadJSONObject(paths.HooksJSON, ErrMalformedCodexHooksJSON)
	if err != nil {
		return err
	}
	ss, err := mergeHookEntry(hooksRoot, "SessionStart", codexHookEntry(CodexHookCommandSessionStart, opts.timeoutSeconds()))
	if err != nil {
		return err
	}
	pt, err := mergeHookEntry(hooksRoot, "PostToolUse", codexHookEntry(CodexHookCommandPostToolUse, opts.timeoutSeconds()))
	if err != nil {
		return err
	}
	if ss || pt {
		if err := writeJSONAtomic(paths.HooksJSON, hooksRoot, hooksMode); err != nil {
			return fmt.Errorf("%s: %w", ItemSessionStartHook, err)
		}
	}
	if _, err := skill.Install(paths.SkillPath, opts.Prefix); err != nil {
		return fmt.Errorf("%s: %w", ItemCodexSkill, err)
	}
	if err := installStubBlock(paths.AgentsMD, codexStubBlock); err != nil {
		return fmt.Errorf("%s: %w", ItemCodexAgentsMD, err)
	}
	return installCodexTOML(paths.ConfigTOML)
}

// RemoveCodex reverses InstallCodex, leaving foreign content untouched and
// restoring pre-install bytes (or absence).
func RemoveCodex(paths CodexPaths, opts Options) error {
	hooksRoot, hooksMode, err := loadJSONObject(paths.HooksJSON, ErrMalformedCodexHooksJSON)
	if err != nil {
		return err
	}
	ss := removeHookEntry(hooksRoot, "SessionStart", codexHookEntry(CodexHookCommandSessionStart, opts.timeoutSeconds()))
	pt := removeHookEntry(hooksRoot, "PostToolUse", codexHookEntry(CodexHookCommandPostToolUse, opts.timeoutSeconds()))
	if ss || pt {
		if len(hooksRoot) == 0 {
			if err := os.Remove(paths.HooksJSON); err != nil {
				return fmt.Errorf("%s: %w", ItemSessionStartHook, err)
			}
		} else if err := writeJSONAtomic(paths.HooksJSON, hooksRoot, hooksMode); err != nil {
			return fmt.Errorf("%s: %w", ItemSessionStartHook, err)
		}
	}
	if err := skill.Remove(paths.SkillPath, opts.Prefix); err != nil {
		return fmt.Errorf("%s: %w", ItemCodexSkill, err)
	}
	// Prune the skill directories install created, stopping at the first one in use.
	skillsDir := filepath.Dir(filepath.Dir(paths.SkillPath))
	for d := filepath.Dir(paths.SkillPath); d != filepath.Dir(skillsDir); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			break
		}
	}
	if err := removeStub(paths.AgentsMD); err != nil {
		return fmt.Errorf("%s: %w", ItemCodexAgentsMD, err)
	}
	if err := removeCodexTOML(paths.ConfigTOML); err != nil {
		return fmt.Errorf("%s: %w", ItemCodexMCPServer, err)
	}
	return nil
}

// CheckCodex reports every item's status without writing.
func CheckCodex(paths CodexPaths, opts Options) ([]Item, error) {
	hooksRoot, _, err := loadJSONObject(paths.HooksJSON, ErrMalformedCodexHooksJSON)
	if err != nil {
		return nil, err
	}
	return []Item{
		{Name: ItemCodexMCPServer, Status: codexTOMLStatus(paths.ConfigTOML)},
		{Name: ItemSessionStartHook, Status: codexHookStatus(paths, hooksRoot, "SessionStart", codexEventSessionStart, codexHookEntry(CodexHookCommandSessionStart, opts.timeoutSeconds()))},
		{Name: ItemPostToolUseHook, Status: codexHookStatus(paths, hooksRoot, "PostToolUse", codexEventPostToolUse, codexHookEntry(CodexHookCommandPostToolUse, opts.timeoutSeconds()))},
		{Name: ItemCodexAgentsMD, Status: stubBlockStatus(paths.AgentsMD, codexStubBlock)},
		{Name: ItemCodexSkill, Status: ItemStatus(skill.CheckStatus(paths.SkillPath, opts.Prefix))},
	}, nil
}

// --- hooks.json helpers (generic over event and entry) ---

func mergeHookEntry(root map[string]any, event string, want map[string]any) (bool, error) {
	hooksObj, err := objectField(root, "hooks")
	if err != nil {
		return false, err
	}
	arr, err := arrayField(hooksObj, event)
	if err != nil {
		return false, err
	}
	for _, e := range arr {
		if jsonDeepEqual(e, want) {
			return false, nil
		}
	}
	hooksObj[event] = append(arr, want)
	root["hooks"] = hooksObj
	return true, nil
}

func hookEntryStatus(root map[string]any, event string, want map[string]any) ItemStatus {
	hooksRaw, ok := root["hooks"]
	if !ok {
		return StatusAbsent
	}
	hooksObj, ok := hooksRaw.(map[string]any)
	if !ok {
		return StatusForeign
	}
	arrRaw, ok := hooksObj[event]
	if !ok {
		return StatusAbsent
	}
	arr, ok := arrRaw.([]any)
	if !ok {
		return StatusForeign
	}
	for _, e := range arr {
		if jsonDeepEqual(e, want) {
			return StatusPresent
		}
	}
	return StatusAbsent
}

func removeHookEntry(root map[string]any, event string, want map[string]any) bool {
	hooksObj, ok := root["hooks"].(map[string]any)
	if !ok {
		return false
	}
	arr, ok := hooksObj[event].([]any)
	if !ok {
		return false
	}
	kept := make([]any, 0, len(arr))
	found := false
	for _, e := range arr {
		if !found && jsonDeepEqual(e, want) {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return false
	}
	if len(kept) == 0 {
		delete(hooksObj, event)
	} else {
		hooksObj[event] = kept
	}
	if len(hooksObj) == 0 {
		delete(root, "hooks")
	}
	return true
}

// --- config.toml text block ---

// tomlBlockSpan finds our marked block in content: begin is the index of the
// begin marker line, end is one past the end marker line's newline. ok is
// false when there is no block; a begin without an end is an error.
func tomlBlockSpan(path, content string) (begin, end int, ok bool, err error) {
	begin = strings.Index(content, CodexTOMLMarkerBegin)
	if begin == -1 {
		return 0, 0, false, nil
	}
	rel := strings.Index(content[begin:], "\n"+CodexTOMLMarkerEnd)
	if rel == -1 {
		return 0, 0, false, fmt.Errorf("%s: found %q without %q", path, CodexTOMLMarkerBegin, CodexTOMLMarkerEnd)
	}
	end = begin + rel + 1 + len(CodexTOMLMarkerEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return begin, end, true, nil
}

func codexTOMLBlock(noEOL bool) string {
	line := codexTOMLBeginLine
	if noEOL {
		line += codexNoEOLFlag
	}
	return line + "\n" + codexTOMLBody + CodexTOMLMarkerEnd + "\n"
}

// withoutBlock returns content with our block cut out, for foreign scans.
func withoutBlock(path, content string) (string, error) {
	b, e, ok, err := tomlBlockSpan(path, content)
	if err != nil || !ok {
		return content, err
	}
	return content[:b] + content[e:], nil
}

func fileMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0o600
}

func installCodexTOML(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // caller-chosen config location
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	rest, err := withoutBlock(path, content)
	if err != nil {
		return err
	}
	if foreignBackstoryTable.MatchString(rest) {
		return fmt.Errorf("%w: %s: %s already defines [mcp_servers.backstory]; left untouched", ErrForeignConflict, ItemCodexMCPServer, path)
	}

	if b, e, ok, _ := tomlBlockSpan(path, content); ok {
		noEOL := strings.HasPrefix(content[b:], codexTOMLBeginLine+codexNoEOLFlag)
		if content[b:e] == codexTOMLBlock(noEOL) {
			return nil
		}
		return writeAtomic(path, []byte(content[:b]+codexTOMLBlock(noEOL)+content[e:]), fileMode(path))
	}

	noEOL := content != "" && !strings.HasSuffix(content, "\n")
	if noEOL {
		content += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return writeAtomic(path, []byte(content+codexTOMLBlock(noEOL)), fileMode(path))
}

func removeCodexTOML(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // caller-chosen config location
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	content := string(data)
	b, e, ok, err := tomlBlockSpan(path, content)
	if err != nil || !ok {
		return err
	}
	noEOL := strings.HasPrefix(content[b:], codexTOMLBeginLine+codexNoEOLFlag)
	head := content[:b]
	if noEOL {
		head = strings.TrimSuffix(head, "\n")
	}
	out := head + content[e:]
	if out == "" {
		return os.Remove(path)
	}
	return writeAtomic(path, []byte(out), fileMode(path))
}

func codexTOMLStatus(path string) ItemStatus {
	data, err := os.ReadFile(path) //nolint:gosec // caller-chosen config location
	if err != nil {
		return StatusAbsent
	}
	content := string(data)
	rest, err := withoutBlock(path, content)
	if err != nil {
		return StatusForeign
	}
	if foreignBackstoryTable.MatchString(rest) {
		return StatusForeign
	}
	if _, _, ok, _ := tomlBlockSpan(path, content); ok {
		return StatusPresent
	}
	return StatusAbsent
}

// --- hook trust (config.toml hooks.state) ---

// codexHookStatus is hookEntryStatus, downgraded to StatusNotTrusted when the
// hook is installed but config.toml has no trusted_hash for its key.
func codexHookStatus(paths CodexPaths, root map[string]any, event, label string, want map[string]any) ItemStatus {
	st := hookEntryStatus(root, event, want)
	if st != StatusPresent {
		return st
	}
	group := codexGroupIndex(root, event, want)
	key := fmt.Sprintf("%s:%s:%d:0", paths.HooksJSON, label, group)
	if codexHookTrusted(paths.ConfigTOML, key) {
		return StatusPresent
	}
	return StatusNotTrusted
}

func codexGroupIndex(root map[string]any, event string, want map[string]any) int {
	hooksObj, _ := root["hooks"].(map[string]any)
	arr, _ := hooksObj[event].([]any)
	for i, e := range arr {
		if jsonDeepEqual(e, want) {
			return i
		}
	}
	return 0
}

var (
	tomlTableHeader = regexp.MustCompile(`^\s*\[`)
	tomlTrustedHash = regexp.MustCompile(`^\s*trusted_hash\s*=\s*(?:"[^"]+"|'[^']+')`)
)

// codexHookTrusted reports whether config.toml has a non-empty trusted_hash
// in the [hooks.state."<key>"] table. The hash itself is not verified.
func codexHookTrusted(configPath, key string) bool {
	data, err := os.ReadFile(configPath) //nolint:gosec // caller-chosen config location
	if err != nil {
		return false
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(key)
	headers := []string{
		`[hooks.state."` + escaped + `"]`,
		`[hooks.state.'` + key + `']`,
	}
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(line)
		if tomlTableHeader.MatchString(line) {
			in = false
			if i := strings.LastIndex(trim, "]"); i >= 0 {
				head := trim[:i+1]
				for _, h := range headers {
					if head == h {
						in = true
					}
				}
			}
			continue
		}
		if in && tomlTrustedHash.MatchString(line) {
			return true
		}
	}
	return false
}
