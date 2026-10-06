package install

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Hook subcommands the installer writes after the binary path.
const (
	subSessionStart         = "hook session-start"
	subPostToolUse          = "hook post-tool-use"
	subPostToolUseFailure   = "hook post-tool-use-failure"
	subStop                 = "hook stop"
	subSessionStartCodex    = "hook session-start --harness codex"
	subPostToolUseCodex     = "hook post-tool-use --harness codex"
	fallbackBinaryName      = "backstory"
	shellSafeBinaryPathExpr = `^[A-Za-z0-9_@%+=:,./-]+$`
)

var shellSafeBinaryPath = regexp.MustCompile(shellSafeBinaryPathExpr)

// binaryPath is the executable hook commands and MCP entries invoke: the
// caller's BinaryPath, else this process's own executable, else the bare
// name (PATH lookup) when neither can be resolved.
func (o Options) binaryPath() string {
	if o.BinaryPath != "" {
		return o.BinaryPath
	}
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			return abs
		}
	}
	return fallbackBinaryName
}

// hookCommand is the sh -c command line for one hook subcommand.
func (o Options) hookCommand(sub string) string {
	return shellQuote(o.binaryPath()) + " " + sub
}

// shellQuote leaves a path of safe characters alone and single-quotes any
// other, so a binary under a directory with spaces still runs through sh -c.
func shellQuote(p string) string {
	if shellSafeBinaryPath.MatchString(p) {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// unquoteShell undoes shellQuote.
func unquoteShell(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], `'\''`, "'")
	}
	return s
}

// isBackstoryHookCommand reports whether command is `<some backstory binary>
// <sub>`: the bare name earlier releases wrote, any path to a binary named
// backstory, or this run's own binary under whatever name, so remove and
// upgrade find entries written for a binary that has since moved.
func isBackstoryHookCommand(command, sub string, opts Options) bool {
	if command == sub {
		return false
	}
	prefix, ok := strings.CutSuffix(command, " "+sub)
	if !ok {
		return false
	}
	bin := unquoteShell(prefix)
	return filepath.Base(bin) == fallbackBinaryName || bin == opts.binaryPath()
}

// isOurHookEntry reports whether e is a hook group backstory wrote for
// subcommand sub, under any binary path, matcher or timeout.
func isOurHookEntry(e any, sub string, opts Options) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := m["hooks"].([]any)
	if !ok || len(hooks) != 1 {
		return false
	}
	h, ok := hooks[0].(map[string]any)
	if !ok || h["type"] != "command" {
		return false
	}
	cmd, _ := h["command"].(string)
	return isBackstoryHookCommand(cmd, sub, opts)
}

// mergeHookEntry makes root's hooks.<event> hold want exactly once: a group
// backstory wrote earlier (bare command, old matcher, another binary path)
// is replaced in place, duplicates are dropped, foreign groups are untouched.
func mergeHookEntry(root map[string]any, event string, want map[string]any, sub string, opts Options) (bool, error) {
	hooksObj, err := objectField(root, "hooks")
	if err != nil {
		return false, err
	}
	arr, err := arrayField(hooksObj, event)
	if err != nil {
		return false, err
	}
	out := make([]any, 0, len(arr)+1)
	placed, changed := false, false
	for _, e := range arr {
		exact := jsonDeepEqual(e, want)
		if !exact && !isOurHookEntry(e, sub, opts) {
			out = append(out, e)
			continue
		}
		if placed {
			changed = true
			continue
		}
		placed = true
		changed = changed || !exact
		out = append(out, want)
	}
	if !placed {
		out = append(out, want)
		changed = true
	}
	if !changed {
		return false, nil
	}
	hooksObj[event] = out
	root["hooks"] = hooksObj
	return true, nil
}

// hookEntryStatus finds Backstory's group for sub under event. A group
// matches when it equals want, or equals want with the command's binary
// replaced by whatever path the group names (an install made with --binary):
// the hook subcommand and the group's shape are Backstory's marker, not the
// current binary path. bin is the path the matched group invokes; idx its
// position in the event's array.
func hookEntryStatus(root map[string]any, event string, want map[string]any, sub string, opts Options) (st ItemStatus, bin string, idx int) {
	hooksRaw, ok := root["hooks"]
	if !ok {
		return StatusAbsent, "", 0
	}
	hooksObj, ok := hooksRaw.(map[string]any)
	if !ok {
		return StatusForeign, "", 0
	}
	arrRaw, ok := hooksObj[event]
	if !ok {
		return StatusAbsent, "", 0
	}
	arr, ok := arrRaw.([]any)
	if !ok {
		return StatusForeign, "", 0
	}
	for i, e := range arr {
		if jsonDeepEqual(e, want) {
			return StatusPresent, opts.binaryPath(), i
		}
		if cmd, ok := recordedHookCommand(e); ok {
			if prefix, ok := strings.CutSuffix(cmd, " "+sub); ok && jsonDeepEqual(e, withHookCommand(want, cmd)) {
				return StatusPresent, unquoteShell(prefix), i
			}
		}
	}
	return StatusAbsent, "", 0
}

// recordedHookCommand is the command of a single-hook group, if e is one.
func recordedHookCommand(e any) (string, bool) {
	m, ok := e.(map[string]any)
	if !ok {
		return "", false
	}
	hooks, ok := m["hooks"].([]any)
	if !ok || len(hooks) != 1 {
		return "", false
	}
	h, ok := hooks[0].(map[string]any)
	if !ok {
		return "", false
	}
	cmd, ok := h["command"].(string)
	return cmd, ok
}

// withHookCommand returns a copy of the single-hook group want with its
// command replaced.
func withHookCommand(want map[string]any, command string) map[string]any {
	out := map[string]any{}
	for k, v := range want {
		out[k] = v
	}
	hooks, _ := want["hooks"].([]any)
	if len(hooks) != 1 {
		return out
	}
	h, _ := hooks[0].(map[string]any)
	h2 := map[string]any{}
	for k, v := range h {
		h2[k] = v
	}
	h2["command"] = command
	out["hooks"] = []any{h2}
	return out
}

// removeHookEntry drops every group backstory wrote for sub, whatever binary
// path, matcher or timeout it carries.
func removeHookEntry(root map[string]any, event, sub string, opts Options) bool {
	hooksObj, ok := root["hooks"].(map[string]any)
	if !ok {
		return false
	}
	arr, ok := hooksObj[event].([]any)
	if !ok {
		return false
	}
	kept := make([]any, 0, len(arr))
	for _, e := range arr {
		if !isOurHookEntry(e, sub, opts) {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(arr) {
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
