// Package install implements `backstory install claude` (PLAN.md §Phase 2,
// AGENT-CONTRACT.md §The skill): it registers the MCP server, the
// SessionStart hook, the skill file and the CLAUDE.md stub under a user's
// $HOME, idempotently, without disturbing anything it did not add.
package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/skill"
)

// Item names, for --check output and per-item verify-failure reporting.
const (
	ItemMCPServer        = "mcp-server"
	ItemSessionStartHook = "session-start-hook"
	ItemPostToolUseHook  = "post-tool-use-hook"
	// ItemPostToolUseFailureHook is Claude's PostToolUseFailure hook: Claude
	// Code reports a failed tool call under that event, never PostToolUse.
	ItemPostToolUseFailureHook = "post-tool-use-failure-hook"
	ItemSkill                  = "skill"
	ItemClaudeMDStub           = "claude-md-stub"
)

// ItemStatus is a --check item's reported state: present, absent, outdated
// (Backstory's own older file, which install replaces), or foreign-conflict
// (something else already occupies the slot we'd write).
type ItemStatus string

const (
	StatusPresent ItemStatus = "present"
	StatusAbsent  ItemStatus = "absent"
	StatusForeign ItemStatus = "foreign-conflict"
	// StatusOutdated is the skill file matching a past embedded version.
	StatusOutdated ItemStatus = "outdated"
	// StatusNotTrusted is a hook installed but not approved in the harness
	// (Codex's hooks.state trusted_hash), so the harness will not run it.
	StatusNotTrusted ItemStatus = "not-trusted"
)

// MCPServerName is the mcpServers key this installer owns exclusively
// (AGENT-CONTRACT.md / the punch's WHAT TO BUILD, item 1).
const MCPServerName = "backstory"

// HookCommand is the exact SessionStart hook command string the installer
// writes and verify-on-install executes through sh -c. It must match the
// `backstory hook session-start` subcommand (cmd/backstory/hook.go).
//
// HookCommand is the bare-PATH form earlier releases wrote; the installer now
// writes the absolute path of the binary (see Options.BinaryPath) and keeps
// this form only to recognise and replace those older entries.
const HookCommand = "backstory hook session-start"

// HookMatcher is the single combined SessionStart matcher this installer
// writes: one entry covers startup, resume, clear and compact (the block is
// gone after a compaction unless it is re-injected) rather than four
// separate entries (AGENT-CONTRACT.md's installer note picks one and tests
// it).
const HookMatcher = "startup|resume|clear|compact"

// HookCommandPostToolUse is the exact PostToolUse hook command string the
// installer writes. It must match the `backstory hook post-tool-use`
// subcommand (cmd/backstory/hook.go, task 04b1cb40).
const HookCommandPostToolUse = "backstory hook post-tool-use"

// HookMatcherPostToolUse is the PostToolUse matcher this installer writes:
// an empty string, matching every tool. Claude Code's documented "match
// everything" matcher, "*", has a known bug where it silently matches
// nothing; "" is the working form.
const HookMatcherPostToolUse = ""

// DefaultHookTimeoutSeconds is the SessionStart hook's timeout when Options
// does not override it.
const DefaultHookTimeoutSeconds = 10

// Stub markers and text for the one-line CLAUDE.md stub
// (AGENT-CONTRACT.md §The skill: "one line pointing at the skill and the
// tool"). StubLine's "only when no SessionStart block is present" clause
// mirrors the skill's own rule (skill/SKILL.md line 2) — before this punch
// (task d6ddfce3) the stub told the agent to call `recall` unconditionally,
// contradicting the skill it points at.
const (
	StubMarkerBegin = "<!-- backstory:begin -->"
	StubMarkerEnd   = "<!-- backstory:end -->"
	StubLine        = "Backstory: if no SessionStart block is present, call the `backstory` MCP tool's `recall`; see `~/.claude/skills/backstory/SKILL.md`." + HandoffClause
)

// HandoffClause is appended to every harness's stub line: the handoff rule
// lives in the skill, which a harness may not load, so the stub states it.
const HandoffClause = " End the session with `note handoff` carrying `next` (the one action the next session starts, as an imperative) and `supersedes` set to the Resume id; conditions, don'ts that still hold, and the exact command to verify the work belong in the handoff text, not in `next`."

var stubBlock = StubMarkerBegin + "\n" + StubLine + "\n" + StubMarkerEnd + "\n"

// Named errors. install refuses and leaves every file untouched whenever
// one of these applies.
var (
	ErrMalformedClaudeJSON   = errors.New("install: ~/.claude.json is not valid JSON")
	ErrMalformedSettingsJSON = errors.New("install: ~/.claude/settings.json is not valid JSON")
	ErrUnexpectedShape       = errors.New("install: existing config has an unexpected shape")
	ErrVerifyFailed          = errors.New("install: hook verification failed")
)

// Paths locates every file this installer touches under one $HOME.
type Paths struct {
	ClaudeJSON   string // ~/.claude.json (mcpServers)
	SettingsJSON string // ~/.claude/settings.json (hooks.SessionStart)
	SkillPath    string // ~/.claude/skills/backstory/SKILL.md
	ClaudeMD     string // ~/.claude/CLAUDE.md
}

// DefaultPaths returns Claude Code's user-level surfaces under home.
func DefaultPaths(home string) Paths {
	return Paths{
		ClaudeJSON:   filepath.Join(home, ".claude.json"),
		SettingsJSON: filepath.Join(home, ".claude", "settings.json"),
		SkillPath:    filepath.Join(home, ".claude", "skills", "backstory", "SKILL.md"),
		ClaudeMD:     filepath.Join(home, ".claude", "CLAUDE.md"),
	}
}

// Options tunes the installer. Every field has a working zero value for
// production use.
type Options struct {
	// Prefix resolves skill.PackagedSkillPath against it; "" (production)
	// means the real absolute path. Tests point it at a temp dir.
	Prefix string
	// HookTimeoutSeconds overrides DefaultHookTimeoutSeconds when non-zero.
	HookTimeoutSeconds int
	// BinaryPath is the backstory binary hook commands and MCP entries
	// invoke; "" means this process's own executable (os.Executable), so a
	// harness launched without ~/.local/bin on PATH still finds it.
	BinaryPath string
	// SocketPath is the daemon socket verification and --check dial; "" falls
	// back to $XDG_RUNTIME_DIR/backstory/sock, then ~/.local/state/backstory/sock.
	// The CLI passes the path the daemon itself resolves (which also knows the
	// /run/user/<uid> case).
	SocketPath string
	// Verify runs the written hook command through sh -c after installing
	// and fails the install if it exits non-zero (--no-verify sets this
	// false).
	Verify bool
}

func (o Options) timeoutSeconds() int {
	if o.HookTimeoutSeconds != 0 {
		return o.HookTimeoutSeconds
	}
	return DefaultHookTimeoutSeconds
}

// Install registers the MCP server, the SessionStart hook, the skill and
// the CLAUDE.md stub. It is idempotent: a second call changes zero bytes in
// every file. A malformed ~/.claude.json or settings.json leaves both files
// untouched and returns a named error.
func Install(paths Paths, opts Options) error {
	claudeRoot, claudeMode, err := loadJSONObject(paths.ClaudeJSON, ErrMalformedClaudeJSON)
	if err != nil {
		return err
	}
	settingsRoot, settingsMode, err := loadJSONObject(paths.SettingsJSON, ErrMalformedSettingsJSON)
	if err != nil {
		return err
	}

	mcpChanged, err := mergeMCPServer(claudeRoot, opts)
	if err != nil {
		return err
	}
	hookChanged, err := mergeHookEntry(settingsRoot, "SessionStart", wantSessionStartEntry(opts), subSessionStart, opts)
	if err != nil {
		return err
	}
	postToolUseChanged, err := mergeHookEntry(settingsRoot, "PostToolUse", wantPostToolUseEntry(opts), subPostToolUse, opts)
	if err != nil {
		return err
	}

	failureChanged, err := mergeHookEntry(settingsRoot, "PostToolUseFailure", wantPostToolUseFailureEntry(opts), subPostToolUseFailure, opts)
	if err != nil {
		return err
	}

	if mcpChanged {
		if err := writeJSONAtomic(paths.ClaudeJSON, claudeRoot, claudeMode); err != nil {
			return fmt.Errorf("%s: %w", ItemMCPServer, err)
		}
	}
	if hookChanged || postToolUseChanged || failureChanged {
		if err := writeJSONAtomic(paths.SettingsJSON, settingsRoot, settingsMode); err != nil {
			return fmt.Errorf("%s: %w", ItemSessionStartHook, err)
		}
	}

	if _, err := skill.Install(paths.SkillPath, opts.Prefix); err != nil {
		return fmt.Errorf("%s: %w", ItemSkill, err)
	}
	if err := InstallStub(paths.ClaudeMD); err != nil {
		return fmt.Errorf("%s: %w", ItemClaudeMDStub, err)
	}

	if opts.Verify {
		if err := verifyHook(opts); err != nil {
			return err
		}
	}
	return nil
}

// Item is one --check row.
type Item struct {
	Name   string
	Status ItemStatus
}

// Check reports every item's status. It never writes.
func Check(paths Paths, opts Options) ([]Item, error) {
	claudeRoot, _, err := loadJSONObject(paths.ClaudeJSON, ErrMalformedClaudeJSON)
	if err != nil {
		return nil, err
	}
	settingsRoot, _, err := loadJSONObject(paths.SettingsJSON, ErrMalformedSettingsJSON)
	if err != nil {
		return nil, err
	}

	items := []Item{
		{Name: ItemMCPServer, Status: mcpServerStatus(claudeRoot, opts)},
		{Name: ItemSessionStartHook, Status: hookEntryStatus(settingsRoot, "SessionStart", wantSessionStartEntry(opts))},
		{Name: ItemPostToolUseHook, Status: hookEntryStatus(settingsRoot, "PostToolUse", wantPostToolUseEntry(opts))},
		{Name: ItemPostToolUseFailureHook, Status: hookEntryStatus(settingsRoot, "PostToolUseFailure", wantPostToolUseFailureEntry(opts))},
		{Name: ItemSkill, Status: install2checkStatus(skill.CheckStatus(paths.SkillPath, opts.Prefix))},
		{Name: ItemClaudeMDStub, Status: stubStatus(paths.ClaudeMD)},
	}
	return items, nil
}

func install2checkStatus(s skill.Status) ItemStatus { return ItemStatus(s) }

// Remove reverses Install exactly: every foreign entry, other mcpServers
// key, and prior CLAUDE.md text stays intact.
func Remove(paths Paths, opts Options) error {
	claudeRoot, claudeMode, err := loadJSONObject(paths.ClaudeJSON, ErrMalformedClaudeJSON)
	if err != nil {
		return err
	}
	settingsRoot, settingsMode, err := loadJSONObject(paths.SettingsJSON, ErrMalformedSettingsJSON)
	if err != nil {
		return err
	}

	mcpChanged := removeMCPServer(claudeRoot)
	hookChanged := removeHookEntry(settingsRoot, "SessionStart", subSessionStart, opts)
	postToolUseChanged := removeHookEntry(settingsRoot, "PostToolUse", subPostToolUse, opts)
	failureChanged := removeHookEntry(settingsRoot, "PostToolUseFailure", subPostToolUseFailure, opts)

	if mcpChanged {
		if err := writeJSONAtomic(paths.ClaudeJSON, claudeRoot, claudeMode); err != nil {
			return fmt.Errorf("%s: %w", ItemMCPServer, err)
		}
	}
	if hookChanged || postToolUseChanged || failureChanged {
		if err := writeJSONAtomic(paths.SettingsJSON, settingsRoot, settingsMode); err != nil {
			return fmt.Errorf("%s: %w", ItemSessionStartHook, err)
		}
	}

	if err := skill.Remove(paths.SkillPath, opts.Prefix); err != nil {
		return fmt.Errorf("%s: %w", ItemSkill, err)
	}
	if err := removeStub(paths.ClaudeMD); err != nil {
		return fmt.Errorf("%s: %w", ItemClaudeMDStub, err)
	}
	return nil
}

// --- mcpServers.backstory ---

func wantMCPServerValue(opts Options) map[string]any {
	return map[string]any{
		"type":    "stdio",
		"command": opts.binaryPath(),
		"args":    []any{"mcp"},
	}
}

func mergeMCPServer(root map[string]any, opts Options) (changed bool, err error) {
	servers, err := objectField(root, "mcpServers")
	if err != nil {
		return false, err
	}
	want := wantMCPServerValue(opts)
	if existing, ok := servers[MCPServerName]; ok && jsonDeepEqual(existing, want) {
		return false, nil
	}
	servers[MCPServerName] = want
	root["mcpServers"] = servers
	return true, nil
}

func mcpServerStatus(root map[string]any, opts Options) ItemStatus {
	serversRaw, ok := root["mcpServers"]
	if !ok {
		return StatusAbsent
	}
	servers, ok := serversRaw.(map[string]any)
	if !ok {
		return StatusForeign
	}
	entry, ok := servers[MCPServerName]
	if !ok {
		return StatusAbsent
	}
	if jsonDeepEqual(entry, wantMCPServerValue(opts)) {
		return StatusPresent
	}
	return StatusForeign
}

func removeMCPServer(root map[string]any) (changed bool) {
	serversRaw, ok := root["mcpServers"]
	if !ok {
		return false
	}
	servers, ok := serversRaw.(map[string]any)
	if !ok {
		return false
	}
	if _, ok := servers[MCPServerName]; !ok {
		return false
	}
	delete(servers, MCPServerName)
	if len(servers) == 0 {
		delete(root, "mcpServers")
	}
	return true
}

// --- hooks.SessionStart / hooks.PostToolUse ---

func wantSessionStartEntry(opts Options) map[string]any {
	return hookEntry(HookMatcher, opts.hookCommand(subSessionStart), opts.timeoutSeconds())
}

func wantPostToolUseEntry(opts Options) map[string]any {
	return hookEntry(HookMatcherPostToolUse, opts.hookCommand(subPostToolUse), opts.timeoutSeconds())
}

func wantPostToolUseFailureEntry(opts Options) map[string]any {
	return hookEntry(HookMatcherPostToolUse, opts.hookCommand(subPostToolUseFailure), opts.timeoutSeconds())
}

func hookEntry(matcher, command string, timeoutSeconds int) map[string]any {
	return map[string]any{
		"matcher": matcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
				"timeout": float64(timeoutSeconds),
			},
		},
	}
}

// --- CLAUDE.md stub ---

// InstallStub writes the marker-delimited stub to claudeMDPath, creating the
// file if absent. It is idempotent (a second call with the current stub
// text changes zero bytes) and self-upgrading: a file already holding a
// stub block from an older release (different text between the same
// markers) has that block replaced in place with the current stubBlock,
// leaving foreign content on either side of it untouched and never
// producing a second marker pair.
func InstallStub(claudeMDPath string) error {
	return installStubBlock(claudeMDPath, stubBlock)
}

// installStubBlock is InstallStub for an arbitrary marker-delimited block
// (the Codex AGENTS.md stub shares the markers but not the text).
func installStubBlock(claudeMDPath, stubBlock string) error {
	data, statErr := os.ReadFile(claudeMDPath) //nolint:gosec // claudeMDPath is the caller-chosen CLAUDE.md location
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	content := string(data)

	mode := os.FileMode(0o600)
	if info, err := os.Stat(claudeMDPath); err == nil {
		mode = info.Mode().Perm()
	}

	beginIdx := strings.Index(content, StubMarkerBegin)
	if beginIdx == -1 {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += stubBlock
		if err := os.MkdirAll(filepath.Dir(claudeMDPath), 0o750); err != nil {
			return err
		}
		return writeAtomic(claudeMDPath, []byte(content), mode)
	}

	endIdx := strings.Index(content, StubMarkerEnd)
	if endIdx == -1 {
		return fmt.Errorf("%s: found %q without %q", claudeMDPath, StubMarkerBegin, StubMarkerEnd)
	}
	endIdx += len(StubMarkerEnd)
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}

	if content[beginIdx:endIdx] == stubBlock {
		return nil // already installed with the current text
	}

	newContent := content[:beginIdx] + stubBlock + content[endIdx:]
	return writeAtomic(claudeMDPath, []byte(newContent), mode)
}

func stubStatus(claudeMDPath string) ItemStatus {
	return stubBlockStatus(claudeMDPath, stubBlock)
}

// stubBlockStatus is present only when the markers are there AND the block
// between them is exactly want; a stale block reads as absent (install
// rewrites it in place).
func stubBlockStatus(claudeMDPath, want string) ItemStatus {
	data, err := os.ReadFile(claudeMDPath) //nolint:gosec // claudeMDPath is the caller-chosen CLAUDE.md location
	if err != nil {
		return StatusAbsent
	}
	if strings.Contains(string(data), want) {
		return StatusPresent
	}
	return StatusAbsent
}

// removeStub strips exactly the marker block InstallStub inserted,
// including the single trailing newline it always writes after
// StubMarkerEnd, restoring the file to what it was before install. If
// nothing but the stub remains, the file itself is removed (it did not
// exist before install).
func removeStub(claudeMDPath string) error {
	data, err := os.ReadFile(claudeMDPath) //nolint:gosec // claudeMDPath is the caller-chosen CLAUDE.md location
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	content := string(data)

	beginIdx := strings.Index(content, StubMarkerBegin)
	if beginIdx == -1 {
		return nil // nothing to remove
	}
	endIdx := strings.Index(content, StubMarkerEnd)
	if endIdx == -1 {
		return fmt.Errorf("%s: found %q without %q", claudeMDPath, StubMarkerBegin, StubMarkerEnd)
	}
	endIdx += len(StubMarkerEnd)
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}

	newContent := content[:beginIdx] + content[endIdx:]
	if newContent == "" {
		return os.Remove(claudeMDPath)
	}

	mode := os.FileMode(0o600)
	if info, err := os.Stat(claudeMDPath); err == nil {
		mode = info.Mode().Perm()
	}
	return writeAtomic(claudeMDPath, []byte(newContent), mode)
}

// --- JSON helpers ---

// objectField returns root[key] as a map, creating an empty one if the key
// is absent. A present key holding a non-object value is ErrUnexpectedShape
// (never clobbered, never silently ignored).
func objectField(root map[string]any, key string) (map[string]any, error) {
	raw, ok := root[key]
	if !ok {
		return map[string]any{}, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not an object", ErrUnexpectedShape, key)
	}
	return obj, nil
}

// arrayField returns obj[key] as a slice, creating an empty one if the key
// is absent. A present key holding a non-array value is ErrUnexpectedShape.
func arrayField(obj map[string]any, key string) ([]any, error) {
	raw, ok := obj[key]
	if !ok {
		return []any{}, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not an array", ErrUnexpectedShape, key)
	}
	return arr, nil
}

func jsonDeepEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	var av, bv any
	if err := json.Unmarshal(ab, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(bb, &bv); err != nil {
		return false
	}
	return jsonEqual(av, bv)
}

func jsonEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			bvv, ok := bv[k]
			if !ok || !jsonEqual(v, bvv) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// loadJSONObject reads path as a JSON object, returning an empty map (mode
// 0644) if it does not exist. Invalid JSON or a non-object root is
// malformedErr, wrapped; the caller must not have written anything yet when
// this returns an error.
func loadJSONObject(path string, malformedErr error) (m map[string]any, mode os.FileMode, err error) {
	data, readErr := os.ReadFile(path) //nolint:gosec // path is the caller-chosen config location
	if os.IsNotExist(readErr) {
		return map[string]any{}, 0o600, nil
	}
	if readErr != nil {
		return nil, 0, readErr
	}

	if err := json.Unmarshal(data, &m); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", malformedErr, err)
	}

	info, statErr := os.Stat(path)
	mode = 0o600
	if statErr == nil {
		mode = info.Mode().Perm()
	}
	return m, mode, nil
}

func writeJSONAtomic(path string, m map[string]any, mode os.FileMode) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return writeAtomic(path, data, mode)
}

// writeAtomic writes data to path via a temp file in the same directory,
// chmods it to mode, then renames it into place — a crash never leaves a
// half-written config.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".backstory-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpPath, path, err)
	}
	removeTmp = false
	return nil
}

// syntheticSessionStartPayload is fed to the hook command's stdin during
// verify-on-install; its fields mirror cmd/backstory/hook.go's
// sessionStartPayload.
const syntheticSessionStartPayload = `{"session_id":"backstory-install-verify","cwd":"/","transcript_path":"","source":"startup","hook_event_name":"SessionStart"}` + "\n"

// verifyDialTimeout bounds how long daemonReachable waits to connect to the
// daemon socket before concluding no daemon is listening.
const verifyDialTimeout = 2 * time.Second

// verifyHook runs the exact hook command Install wrote through sh -c with a
// synthetic SessionStart payload on stdin, inheriting the current process's
// environment (so a test's fake PATH and XDG dirs reach it). It fails if the
// command exits non-zero, and — since a hook must never break a harness
// boot, so exit 0 alone proves nothing — also fails unless its stdout is one
// of the three documented shapes: a rendered block, the exact empty-project
// line, or (only when independently confirmed here that no daemon is
// actually reachable, never taken on the hook's own say-so) empty.
func verifyHook(opts Options) error {
	cmd := exec.Command("sh", "-c", opts.hookCommand(subSessionStart)) //nolint:gosec // built from our own subcommand constant and the binary path, not external input
	// The probe must not mint a session (or a project for the cwd) in the
	// user's memory; the daemon honours this on the block method.
	cmd.Env = append(os.Environ(), "BACKSTORY_NO_SESSION=1")
	cmd.Stdin = strings.NewReader(syntheticSessionStartPayload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s: %v (stderr: %s)", ErrVerifyFailed, ItemSessionStartHook, err, stderr.String())
	}

	out := strings.TrimRight(stdout.String(), "\n")
	if !verifyStdoutOK(out, daemonReachable(opts)) {
		return fmt.Errorf("%w: %s: unexpected stdout %q", ErrVerifyFailed, ItemSessionStartHook, stdout.String())
	}
	return nil
}

// verifyStdoutOK reports whether out — the hook's stdout after a zero exit,
// trailing newline trimmed — matches a documented success shape: a rendered
// block ending in block.EmptyProjectLine (the empty-state body, itself
// preceded by block.HeaderLine) or in block.FinalLine (untouched by its own
// budget-cut truncation, except at a budget too small to fit anything —
// out of reach at the default budget this synthetic verify run uses), or,
// when out is completely empty, an independently confirmed absence of a
// reachable daemon. daemonUp must come from daemonReachable, not from the
// hook subprocess's own exit code — a hook that exits 0 printing nothing is
// otherwise indistinguishable from the documented no-daemon success path.
func verifyStdoutOK(out string, daemonUp bool) bool {
	switch {
	case strings.HasSuffix(out, block.EmptyProjectLine):
		return true
	case out != "" && block.EndsWithFinalLine(out):
		return true
	case out == "" && !daemonUp:
		return true
	default:
		return false
	}
}

// daemonReachable reports whether a backstory daemon is listening on this
// environment's socket, resolved the same way cmd/backstory's daemon and
// hook resolve it: $XDG_RUNTIME_DIR/backstory/sock, falling back to
// ~/.local/state/backstory/sock.
func daemonReachable(opts Options) bool {
	_, ok := DaemonAnswering(opts)
	return ok
}

// DaemonAnswering dials the daemon socket and reports its path and whether
// something answered the connection. A stale socket file with no listener
// reads as not answering.
func DaemonAnswering(opts Options) (path string, answering bool) {
	path = opts.SocketPath
	if path == "" {
		var err error
		if path, err = verifySocketPath(); err != nil {
			return "", false
		}
	}
	conn, err := net.DialTimeout("unix", path, verifyDialTimeout)
	if err != nil {
		return path, false
	}
	_ = conn.Close()
	return path, true
}

func verifySocketPath() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "backstory", "sock"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "backstory", "sock"), nil
}
