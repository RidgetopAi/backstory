package install

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	return isBackstoryBinary(unquoteShell(prefix), opts)
}

// isBackstoryBinary is the one rule every harness uses to recognise an entry
// Backstory wrote under some binary path: a binary named backstory (the bare
// name earlier releases wrote, or any path to it), or this run's own binary
// under whatever name. Any other command is foreign.
func isBackstoryBinary(bin string, opts Options) bool {
	return bin != "" && (filepath.Base(bin) == fallbackBinaryName || bin == opts.binaryPath())
}

// ourHookIndexes returns e as a group and the positions, inside its hooks
// array, of every command hook backstory wrote for subcommand sub, under any
// binary path; no positions when e is not a group or holds none. A hook is recognised by its
// command alone, whatever matcher, timeout or other hooks its group carries,
// so a group the owner narrowed or added to is still found.
func ourHookIndexes(e any, sub string, opts Options) (map[string]any, []int) {
	m, ok := e.(map[string]any)
	if !ok {
		return nil, nil
	}
	hooks, ok := m["hooks"].([]any)
	if !ok {
		return nil, nil
	}
	var idx []int
	for i, raw := range hooks {
		h, ok := raw.(map[string]any)
		if !ok || h["type"] != "command" {
			continue
		}
		if cmd, _ := h["command"].(string); isBackstoryHookCommand(cmd, sub, opts) {
			idx = append(idx, i)
		}
	}
	return m, idx
}

// legacyHookMatchers are the matchers earlier releases wrote as their
// default, per hook subcommand. A group still carrying one holds Backstory's
// old default rather than an owner's choice, so install moves it to the
// current default; any other matcher is the owner's.
var legacyHookMatchers = map[string][]string{
	subSessionStart: {"startup|resume|clear"},
}

// reconcileHookGroup returns a copy of group in which the hook at index keep
// runs command and every other index in drop is taken out (keep -1 drops
// them all; nil when no hook is left). The command is the only thing
// Backstory owns in a group it once wrote: the matcher, the timeout and
// every other key of the group and of the hook are the owner's and are
// kept, so install never widens which events reach the hook. The one
// exception is a matcher equal to a default an earlier release wrote
// (legacyHookMatchers), which becomes matcher.
func reconcileHookGroup(group map[string]any, keep int, drop []int, command string, matcher any, sub string) map[string]any {
	out := make(map[string]any, len(group))
	for k, v := range group {
		out[k] = v
	}
	if m, ok := group["matcher"].(string); ok && slices.Contains(legacyHookMatchers[sub], m) {
		out["matcher"] = matcher
	}
	hooks, _ := group["hooks"].([]any)
	kept := make([]any, 0, len(hooks))
	for i, raw := range hooks {
		switch {
		case i == keep:
			h, _ := raw.(map[string]any)
			h2 := make(map[string]any, len(h))
			for k, v := range h {
				h2[k] = v
			}
			h2["command"] = command
			kept = append(kept, h2)
		case !slices.Contains(drop, i):
			kept = append(kept, raw)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	out["hooks"] = kept
	return out
}

// hookCommandOf is the command of hook index i in group g (a group
// ourHookIndexes returned i for, or a want entry).
func hookCommandOf(g map[string]any, i int) string {
	hooks, _ := g["hooks"].([]any)
	if i >= len(hooks) {
		return ""
	}
	h, _ := hooks[i].(map[string]any)
	cmd, _ := h["command"].(string)
	return cmd
}

// mergeHookEntry makes root's hooks.<event> hold Backstory's hook for sub
// exactly once. With none of ours there, want is appended. Otherwise the
// first group holding our hook is reconciled in place (reconcileHookGroup:
// the command is updated, the owner's matcher and keys stay), every later
// hook of ours is dropped (a group left with no hook goes with it), and
// foreign groups and hooks are untouched.
func mergeHookEntry(root map[string]any, event string, want map[string]any, sub string, opts Options) (bool, error) {
	hooksObj, err := objectField(root, "hooks")
	if err != nil {
		return false, err
	}
	arr, err := arrayField(hooksObj, event)
	if err != nil {
		return false, err
	}
	command := hookCommandOf(want, 0)
	out := make([]any, 0, len(arr)+1)
	placed, changed := false, false
	for _, e := range arr {
		g, idx := ourHookIndexes(e, sub, opts)
		if len(idx) == 0 {
			out = append(out, e)
			continue
		}
		keep := -1
		if !placed {
			keep, placed = idx[0], true
		}
		r := reconcileHookGroup(g, keep, idx, command, want["matcher"], sub)
		if r == nil || !jsonDeepEqual(e, r) {
			changed = true
		}
		if r != nil {
			out = append(out, r)
		}
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

// hookEntryStatus finds Backstory's hook for sub under event. It is present
// when its group already is what mergeHookEntry would make of it, the
// command keeping whatever backstory binary it names (an install made with
// --binary; isBackstoryBinary): the owner's matcher and keys never make it
// outdated, so --check never sends an install to widen them. bin is the path
// the hook invokes; group and hook its position (the group in the event's
// array, the hook in that group).
func hookEntryStatus(root map[string]any, event string, want map[string]any, sub string, opts Options) (st ItemStatus, bin string, group, hook int) {
	hooksRaw, ok := root["hooks"]
	if !ok {
		return StatusAbsent, "", 0, 0
	}
	hooksObj, ok := hooksRaw.(map[string]any)
	if !ok {
		return StatusForeign, "", 0, 0
	}
	arrRaw, ok := hooksObj[event]
	if !ok {
		return StatusAbsent, "", 0, 0
	}
	arr, ok := arrRaw.([]any)
	if !ok {
		return StatusForeign, "", 0, 0
	}
	for i, e := range arr {
		g, idx := ourHookIndexes(e, sub, opts)
		if len(idx) == 0 {
			continue
		}
		cmd := hookCommandOf(g, idx[0])
		if !jsonDeepEqual(e, reconcileHookGroup(g, idx[0], idx, cmd, want["matcher"], sub)) {
			return StatusAbsent, "", 0, 0
		}
		prefix, _ := strings.CutSuffix(cmd, " "+sub)
		return StatusPresent, unquoteShell(prefix), i, idx[0]
	}
	return StatusAbsent, "", 0, 0
}

// removeHookEntry drops every hook backstory wrote for sub, whatever binary
// path, matcher or timeout its group carries; a group left with no hook goes
// with it, and the owner's other hooks in a shared group stay.
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
	changed := false
	for _, e := range arr {
		g, idx := ourHookIndexes(e, sub, opts)
		if len(idx) == 0 {
			kept = append(kept, e)
			continue
		}
		changed = true
		if r := reconcileHookGroup(g, -1, idx, "", nil, ""); r != nil {
			kept = append(kept, r)
		}
	}
	if !changed {
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
