// Codex adapter (decision 3e14db82): `backstory install codex` registers the
// MCP server in ~/.codex/config.toml, the SessionStart and PostToolUse hooks
// in ~/.codex/hooks.json, a one-line stub in ~/.codex/AGENTS.md, and the Backstory skill in
// ~/.codex/skills/backstory/SKILL.md.
//
// config.toml is hand-written (comments, trust tables, other servers), so it
// is never re-serialized: the backstory table is appended as a
// marker-delimited text block and removed by cutting exactly the lines
// Backstory wrote. Codex edits the file too (it appends hooks.state and
// projects trust tables, which can land between our markers); any such line
// is foreign, is kept verbatim (moved after the end marker), and is never
// deleted. --check reads the TOML tables themselves, not the marker region.
package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	//
	// These are the bare-PATH forms earlier releases wrote; the installer
	// now writes the binary's absolute path (Options.BinaryPath) and keeps
	// these only to recognise older entries.
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

	// CodexStubLine is the one-line AGENTS.md stub.
	CodexStubLine = "Backstory: if no SessionStart block is present, call the `backstory` MCP tool's `recall`; see the `backstory` skill, `~/.codex/skills/backstory/SKILL.md`." + HandoffClause
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

// InstallNotice is printed by the CLI after a successful install: the
// approval step, or nothing when the hooks are already trusted.
func (codexAdapter) InstallNotice(home string, opts Options) string {
	if CodexHooksTrusted(home, opts) {
		return ""
	}
	return CodexTrustNotice
}

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

// InstallCodex installs every Codex item. A malformed hooks.json or a foreign
// mcp_servers.backstory (reported as an ErrForeignConflict naming that item)
// fails before anything is written. Verify-on-install is not run: the codex payload shape is
// owned by the hook subcommand's own punch.
func InstallCodex(paths CodexPaths, opts Options) error {
	hooksRoot, hooksMode, err := loadJSONObject(paths.HooksJSON, ErrMalformedCodexHooksJSON)
	if err != nil {
		return err
	}
	// A foreign mcp_servers.backstory is found before anything is written, so
	// a conflict leaves the whole harness exactly as it was.
	if err := checkCodexTOMLConflict(paths.ConfigTOML, opts); err != nil {
		return err
	}
	ss, err := mergeHookEntry(hooksRoot, "SessionStart", codexHookEntry(opts.hookCommand(subSessionStartCodex), opts.timeoutSeconds()), subSessionStartCodex, opts)
	if err != nil {
		return err
	}
	pt, err := mergeHookEntry(hooksRoot, "PostToolUse", codexHookEntry(opts.hookCommand(subPostToolUseCodex), opts.timeoutSeconds()), subPostToolUseCodex, opts)
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
	return installCodexTOML(paths.ConfigTOML, opts)
}

// RemoveCodex reverses InstallCodex, leaving foreign content untouched and
// restoring pre-install bytes (or absence).
func RemoveCodex(paths CodexPaths, opts Options) error {
	hooksRoot, hooksMode, err := loadJSONObject(paths.HooksJSON, ErrMalformedCodexHooksJSON)
	if err != nil {
		return err
	}
	ss := removeHookEntry(hooksRoot, "SessionStart", subSessionStartCodex, opts)
	pt := removeHookEntry(hooksRoot, "PostToolUse", subPostToolUseCodex, opts)
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
	if err := removeCodexTOML(paths.ConfigTOML, opts); err != nil {
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
	mcpSt, mcpBin := codexTOMLStatus(paths.ConfigTOML, opts)
	startSt, startBin := codexHookStatus(paths, hooksRoot, "SessionStart", codexEventSessionStart, subSessionStartCodex, opts)
	postSt, postBin := codexHookStatus(paths, hooksRoot, "PostToolUse", codexEventPostToolUse, subPostToolUseCodex, opts)
	return []Item{
		{Name: ItemCodexMCPServer, Status: mcpSt, Binary: mcpBin},
		{Name: ItemSessionStartHook, Status: startSt, Binary: startBin},
		{Name: ItemPostToolUseHook, Status: postSt, Binary: postBin},
		{Name: ItemCodexAgentsMD, Status: stubBlockStatus(paths.AgentsMD, codexStubBlock)},
		{Name: ItemCodexSkill, Status: ItemStatus(skill.CheckStatus(paths.SkillPath, opts.Prefix))},
	}, nil
}

// --- hooks.json helpers (generic over event and entry) ---

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

// codexTOMLBody is the [mcp_servers.backstory] table, invoking the binary by
// its absolute path (a TOML basic string).
func codexTOMLBody(opts Options) string {
	return "[mcp_servers.backstory]\ncommand = " + strconv.Quote(opts.binaryPath()) + "\nargs = [\"mcp\"]\n"
}

func codexTOMLBlock(noEOL bool, opts Options) string {
	line := codexTOMLBeginLine
	if noEOL {
		line += codexNoEOLFlag
	}
	return line + "\n" + codexTOMLBody(opts) + CodexTOMLMarkerEnd + "\n"
}

// codexBlock is a located managed block. Lines between the markers that
// Backstory did not write (Codex's own appended tables) are Foreign.
type codexBlock struct {
	Begin, End int    // span in content, End one past the end marker's newline
	OwnEnd     int    // one past the last line Backstory wrote
	NoEOL      bool   // begin line carries codexNoEOLFlag
	Foreign    string // interior text that is not Backstory's, verbatim
}

var codexOwnCommandLine = regexp.MustCompile(`^command = "(?:[^"\\\n]|\\.)*"\n$`)

// codexOwnBody returns how many bytes of interior (the text after the begin
// line) are Backstory's table: the current body exactly, or an older one
// with another binary path (same three-line shape).
func codexOwnBody(interior string, opts Options) int {
	if cur := codexTOMLBody(opts); strings.HasPrefix(interior, cur) {
		return len(cur)
	}
	const head = "[mcp_servers.backstory]\n"
	const args = "args = [\"mcp\"]\n"
	if !strings.HasPrefix(interior, head) {
		return 0
	}
	rest := interior[len(head):]
	i := strings.Index(rest, "\n")
	if i < 0 || !codexOwnCommandLine.MatchString(rest[:i+1]) || !strings.HasPrefix(rest[i+1:], args) {
		return 0
	}
	return len(head) + i + 1 + len(args)
}

// findCodexBlock locates our marked block; ok is false when there is none.
func findCodexBlock(path, content string, opts Options) (blk codexBlock, ok bool, err error) {
	begin, end, ok, err := tomlBlockSpan(path, content)
	if err != nil || !ok {
		return codexBlock{}, false, err
	}
	blk = codexBlock{Begin: begin, End: end}
	blk.NoEOL = strings.HasPrefix(content[begin:], codexTOMLBeginLine+codexNoEOLFlag)
	line := codexTOMLBeginLine
	if blk.NoEOL {
		line += codexNoEOLFlag
	}
	if !strings.HasPrefix(content[begin:end], line+"\n") {
		// Begin line altered: treat the whole interior as foreign.
		line = content[begin : begin+strings.Index(content[begin:], "\n")]
	}
	interiorStart := begin + len(line) + 1
	endMarker := strings.LastIndex(content[:end], "\n"+CodexTOMLMarkerEnd) + 1
	if interiorStart > endMarker {
		return codexBlock{}, false, fmt.Errorf("%s: malformed backstory block", path)
	}
	own := codexOwnBody(content[interiorStart:endMarker], opts)
	blk.OwnEnd = interiorStart + own
	blk.Foreign = content[blk.OwnEnd:endMarker]
	return blk, true, nil
}

// withoutBlock returns content with the lines Backstory wrote cut out
// (foreign lines inside the block stay), for foreign scans.
func withoutBlock(path, content string, opts Options) (string, error) {
	blk, ok, err := findCodexBlock(path, content, opts)
	if err != nil || !ok {
		return content, err
	}
	return content[:blk.Begin] + blk.Foreign + content[blk.End:], nil
}

func fileMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0o600
}

// checkCodexTOMLConflict returns the ErrForeignConflict installCodexTOML
// would, without writing.
func checkCodexTOMLConflict(path string, opts Options) error {
	data, err := os.ReadFile(path) //nolint:gosec // caller-chosen config location
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	rest, err := withoutBlock(path, string(data), opts)
	if err != nil {
		return err
	}
	if foreignBackstoryTable.MatchString(rest) {
		return fmt.Errorf("%w: %s: %s already defines [mcp_servers.backstory]; left untouched", ErrForeignConflict, ItemCodexMCPServer, path)
	}
	return nil
}

func installCodexTOML(path string, opts Options) error {
	data, err := os.ReadFile(path) //nolint:gosec // caller-chosen config location
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	if err := checkCodexTOMLConflict(path, opts); err != nil {
		return err
	}

	if blk, ok, _ := findCodexBlock(path, content, opts); ok {
		want := codexTOMLBlock(blk.NoEOL, opts)
		if content[blk.Begin:blk.End] == want {
			return nil
		}
		// Foreign lines found inside the block move out, after the end marker.
		out := content[:blk.Begin] + want + blk.Foreign + content[blk.End:]
		return writeAtomic(path, []byte(out), fileMode(path))
	}

	noEOL := content != "" && !strings.HasSuffix(content, "\n")
	if noEOL {
		content += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return writeAtomic(path, []byte(content+codexTOMLBlock(noEOL, opts)), fileMode(path))
}

func removeCodexTOML(path string, opts Options) error {
	data, err := os.ReadFile(path) //nolint:gosec // caller-chosen config location
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	content := string(data)
	blk, ok, err := findCodexBlock(path, content, opts)
	if err != nil || !ok {
		return err
	}
	head := content[:blk.Begin]
	if blk.NoEOL && blk.Foreign == "" {
		head = strings.TrimSuffix(head, "\n")
	}
	out := head + blk.Foreign + content[blk.End:]
	if out == "" {
		return os.Remove(path)
	}
	return writeAtomic(path, []byte(out), fileMode(path))
}

// codexTOMLStatus decides from the TOML tables, not the marker region: the
// backstory table must exist with Backstory's command and args wherever it
// sits in the file, and nothing foreign may define it.
func codexTOMLStatus(path string, opts Options) (ItemStatus, string) {
	data, err := os.ReadFile(path) //nolint:gosec // caller-chosen config location
	if err != nil {
		return StatusAbsent, ""
	}
	content := string(data)
	// Backstory's table is recognised by its shape, whatever binary it names
	// (an install made with --binary); compare against that recorded path.
	if bin, ok := codexRecordedBinary(content); ok {
		opts.BinaryPath = bin
	}
	rest, err := withoutBlock(path, content, opts)
	if err != nil {
		return StatusForeign, ""
	}
	if foreignBackstoryTable.MatchString(rest) {
		return StatusForeign, ""
	}
	if codexBackstoryTableMatches(content, opts) {
		return StatusPresent, opts.binaryPath()
	}
	// Absent, or Backstory's own older block (another binary path): install rewrites it.
	return StatusAbsent, ""
}

var tomlCommandLine = regexp.MustCompile(`^command\s*=\s*("(?:[^"\\]|\\.)*")\s*$`)

// codexRecordedBinary is the binary named by the command line of the
// [mcp_servers.backstory] table, if there is one.
func codexRecordedBinary(content string) (string, bool) {
	in := false
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			in = trim == "[mcp_servers.backstory]"
			continue
		}
		if !in {
			continue
		}
		if m := tomlCommandLine.FindStringSubmatch(trim); m != nil {
			if bin, err := strconv.Unquote(m[1]); err == nil && bin != "" {
				return bin, true
			}
		}
	}
	return "", false
}

// codexBackstoryTableMatches reports whether content has a
// [mcp_servers.backstory] table whose only entries are Backstory's command
// and args.
func codexBackstoryTableMatches(content string, opts Options) bool {
	want := map[string]bool{
		"command = " + strconv.Quote(opts.binaryPath()): true,
		`args = ["mcp"]`: true,
	}
	in, found := false, false
	seen := 0
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "[") {
			in = trim == "[mcp_servers.backstory]"
			found = found || in
			continue
		}
		if !in {
			continue
		}
		if !want[trim] {
			return false
		}
		seen++
	}
	return found && seen == len(want)
}

// --- hook trust (config.toml hooks.state) ---

// codexHookStatus is hookEntryStatus, downgraded to StatusNotTrusted when the
// hook is installed but config.toml has no trusted_hash for its key.
func codexHookStatus(paths CodexPaths, root map[string]any, event, label, sub string, opts Options) (ItemStatus, string) {
	want := codexHookEntry(opts.hookCommand(sub), opts.timeoutSeconds())
	st, bin, group := hookEntryStatus(root, event, want, sub, opts)
	if st != StatusPresent {
		return st, ""
	}
	key := fmt.Sprintf("%s:%s:%d:0", paths.HooksJSON, label, group)
	if codexHookTrusted(paths.ConfigTOML, key) {
		return StatusPresent, bin
	}
	return StatusNotTrusted, bin
}

// CodexHooksTrusted reports whether every Backstory hook installed in
// Codex's hooks.json already has a trusted_hash in config.toml.
func CodexHooksTrusted(home string, opts Options) bool {
	items, err := CheckCodex(DefaultCodexPaths(home), opts)
	if err != nil {
		return false
	}
	for _, it := range items {
		if (it.Name == ItemSessionStartHook || it.Name == ItemPostToolUseHook) && it.Status != StatusPresent {
			return false
		}
	}
	return true
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
