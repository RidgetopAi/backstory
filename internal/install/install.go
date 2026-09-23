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
	ItemSkill            = "skill"
	ItemClaudeMDStub     = "claude-md-stub"
)

// ItemStatus is a --check item's reported state: present, absent, or
// foreign-conflict (something else already occupies the slot we'd write).
type ItemStatus string

const (
	StatusPresent ItemStatus = "present"
	StatusAbsent  ItemStatus = "absent"
	StatusForeign ItemStatus = "foreign-conflict"
)

// MCPServerName is the mcpServers key this installer owns exclusively
// (AGENT-CONTRACT.md / the punch's WHAT TO BUILD, item 1).
const MCPServerName = "backstory"

// HookCommand is the exact SessionStart hook command string the installer
// writes and verify-on-install executes through sh -c. It must match the
// `backstory hook session-start` subcommand (cmd/backstory/hook.go).
const HookCommand = "backstory hook session-start"

// HookMatcher is the single combined SessionStart matcher this installer
// writes: one entry covers startup, resume and clear rather than three
// separate entries (AGENT-CONTRACT.md's installer note picks one and tests
// it).
const HookMatcher = "startup|resume|clear"

// DefaultHookTimeoutSeconds is the SessionStart hook's timeout when Options
// does not override it.
const DefaultHookTimeoutSeconds = 10

// Stub markers and text for the one-line CLAUDE.md stub
// (AGENT-CONTRACT.md §The skill: "one line pointing at the skill and the
// tool").
const (
	StubMarkerBegin = "<!-- backstory:begin -->"
	StubMarkerEnd   = "<!-- backstory:end -->"
	StubLine        = "Backstory: call the `backstory` MCP tool's `recall`; see `~/.claude/skills/backstory/SKILL.md`."
)

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

	mcpChanged, err := mergeMCPServer(claudeRoot)
	if err != nil {
		return err
	}
	hookChanged, err := mergeSessionStartHook(settingsRoot, opts.timeoutSeconds())
	if err != nil {
		return err
	}

	if mcpChanged {
		if err := writeJSONAtomic(paths.ClaudeJSON, claudeRoot, claudeMode); err != nil {
			return fmt.Errorf("%s: %w", ItemMCPServer, err)
		}
	}
	if hookChanged {
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
		if err := verifyHook(); err != nil {
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
		{Name: ItemMCPServer, Status: mcpServerStatus(claudeRoot)},
		{Name: ItemSessionStartHook, Status: sessionStartHookStatus(settingsRoot, opts.timeoutSeconds())},
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
	hookChanged := removeSessionStartHook(settingsRoot, opts.timeoutSeconds())

	if mcpChanged {
		if err := writeJSONAtomic(paths.ClaudeJSON, claudeRoot, claudeMode); err != nil {
			return fmt.Errorf("%s: %w", ItemMCPServer, err)
		}
	}
	if hookChanged {
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

func wantMCPServerValue() map[string]any {
	return map[string]any{
		"type":    "stdio",
		"command": "backstory",
		"args":    []any{"mcp"},
	}
}

func mergeMCPServer(root map[string]any) (changed bool, err error) {
	servers, err := objectField(root, "mcpServers")
	if err != nil {
		return false, err
	}
	want := wantMCPServerValue()
	if existing, ok := servers[MCPServerName]; ok && jsonDeepEqual(existing, want) {
		return false, nil
	}
	servers[MCPServerName] = want
	root["mcpServers"] = servers
	return true, nil
}

func mcpServerStatus(root map[string]any) ItemStatus {
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
	if jsonDeepEqual(entry, wantMCPServerValue()) {
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

// --- hooks.SessionStart ---

func wantSessionStartEntry(timeoutSeconds int) map[string]any {
	return map[string]any{
		"matcher": HookMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": HookCommand,
				"timeout": float64(timeoutSeconds),
			},
		},
	}
}

func mergeSessionStartHook(root map[string]any, timeoutSeconds int) (changed bool, err error) {
	hooksObj, err := objectField(root, "hooks")
	if err != nil {
		return false, err
	}
	arr, err := arrayField(hooksObj, "SessionStart")
	if err != nil {
		return false, err
	}

	want := wantSessionStartEntry(timeoutSeconds)
	for _, e := range arr {
		if jsonDeepEqual(e, want) {
			return false, nil
		}
	}
	arr = append(arr, want)
	hooksObj["SessionStart"] = arr
	root["hooks"] = hooksObj
	return true, nil
}

func sessionStartHookStatus(root map[string]any, timeoutSeconds int) ItemStatus {
	hooksRaw, ok := root["hooks"]
	if !ok {
		return StatusAbsent
	}
	hooksObj, ok := hooksRaw.(map[string]any)
	if !ok {
		return StatusForeign
	}
	arrRaw, ok := hooksObj["SessionStart"]
	if !ok {
		return StatusAbsent
	}
	arr, ok := arrRaw.([]any)
	if !ok {
		return StatusForeign
	}
	want := wantSessionStartEntry(timeoutSeconds)
	for _, e := range arr {
		if jsonDeepEqual(e, want) {
			return StatusPresent
		}
	}
	return StatusAbsent
}

func removeSessionStartHook(root map[string]any, timeoutSeconds int) (changed bool) {
	hooksRaw, ok := root["hooks"]
	if !ok {
		return false
	}
	hooksObj, ok := hooksRaw.(map[string]any)
	if !ok {
		return false
	}
	arrRaw, ok := hooksObj["SessionStart"]
	if !ok {
		return false
	}
	arr, ok := arrRaw.([]any)
	if !ok {
		return false
	}

	want := wantSessionStartEntry(timeoutSeconds)
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
		delete(hooksObj, "SessionStart")
	} else {
		hooksObj["SessionStart"] = kept
	}
	if len(hooksObj) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooksObj
	}
	return true
}

// --- CLAUDE.md stub ---

// InstallStub appends the one-line marker-delimited stub to claudeMDPath,
// creating the file if absent. It is idempotent: if the markers are already
// present, nothing is written.
func InstallStub(claudeMDPath string) error {
	data, statErr := os.ReadFile(claudeMDPath) //nolint:gosec // claudeMDPath is the caller-chosen CLAUDE.md location
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	content := string(data)
	if strings.Contains(content, StubMarkerBegin) {
		return nil // already installed
	}

	mode := os.FileMode(0o600)
	if info, err := os.Stat(claudeMDPath); err == nil {
		mode = info.Mode().Perm()
	}

	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += stubBlock

	if err := os.MkdirAll(filepath.Dir(claudeMDPath), 0o750); err != nil {
		return err
	}
	return writeAtomic(claudeMDPath, []byte(content), mode)
}

func stubStatus(claudeMDPath string) ItemStatus {
	data, err := os.ReadFile(claudeMDPath) //nolint:gosec // claudeMDPath is the caller-chosen CLAUDE.md location
	if err != nil {
		return StatusAbsent
	}
	if strings.Contains(string(data), StubMarkerBegin) {
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
func verifyHook() error {
	cmd := exec.Command("sh", "-c", HookCommand) //nolint:gosec // HookCommand is our own named constant, not external input
	cmd.Stdin = strings.NewReader(syntheticSessionStartPayload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s: %v (stderr: %s)", ErrVerifyFailed, ItemSessionStartHook, err, stderr.String())
	}

	out := strings.TrimRight(stdout.String(), "\n")
	if !verifyStdoutOK(out, daemonReachable()) {
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
	case out != "" && strings.HasSuffix(out, block.FinalLine):
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
func daemonReachable() bool {
	path, err := verifySocketPath()
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", path, verifyDialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
