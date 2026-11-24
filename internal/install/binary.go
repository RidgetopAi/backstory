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
// A safe word that sh would read as a VAR=value assignment is quoted too.
func shellQuote(p string) string {
	if shellSafeBinaryPath.MatchString(p) && !shellAssignment.MatchString(p) {
		return p
	}
	return singleQuote(p)
}

func singleQuote(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// unquoteShell undoes shellQuote.
func unquoteShell(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], `'\''`, "'")
	}
	return s
}

// shellAssignment is a word sh runs as a variable assignment, not a command.
var shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// shellPathWord returns the path tok names when tok is one shell word in a
// form shellQuote writes: a plain word of safe characters that is not an
// assignment, or one single-quoted string (an embedded quote closed,
// escaped and reopened, as singleQuote writes it).
// Anything else (two words, an operator, an unbalanced quote) is not.
func shellPathWord(tok string) (string, bool) {
	if shellSafeBinaryPath.MatchString(tok) && !shellAssignment.MatchString(tok) {
		return tok, true
	}
	if len(tok) >= 2 && tok[0] == '\'' && tok[len(tok)-1] == '\'' {
		if p := unquoteShell(tok); singleQuote(p) == tok {
			return p, true
		}
	}
	return "", false
}

// ourHookBinary returns the binary of command when command is a hook
// Backstory wrote for sub: exactly `<binary> <sub>`, binary one path word
// (shellPathWord) naming a backstory binary (isBackstoryBinary): the bare
// name earlier releases wrote, any path to a binary named backstory, or this
// run's own binary under whatever name, so remove and upgrade find entries
// written for a binary that has since moved. A command that puts anything
// else around our binary (an environment prefix such as BACKSTORY_OFF=1,
// `||`, `;`, a redirect) is the owner's (ownerWrapsHook), never ours.
func ourHookBinary(command, sub string, opts Options) (string, bool) {
	tok, ok := strings.CutSuffix(command, " "+sub)
	if !ok {
		return "", false
	}
	bin, ok := shellPathWord(tok)
	if !ok || !isBackstoryBinary(bin, opts) {
		return "", false
	}
	return bin, true
}

// isBackstoryBinary is the one rule every harness uses to recognise an entry
// Backstory wrote under some binary path: a binary named backstory (the bare
// name earlier releases wrote, or any path to it), or this run's own binary
// under whatever name. Any other command is foreign.
func isBackstoryBinary(bin string, opts Options) bool {
	return bin != "" && (filepath.Base(bin) == fallbackBinaryName || bin == opts.binaryPath())
}

// hookSubWord matches the `hook <subcommand>` words of a hook command line,
// not followed by more of a subcommand name (post-tool-use is not
// post-tool-use-failure).
var hookSubWord = regexp.MustCompile(`(^|\s)hook\s+([a-z-]+)($|[^A-Za-z0-9_-])`)

// ownerWrapsHook reports whether command is the owner's own command around
// Backstory's hook for sub: not ours (ourHookBinary), but it names sub's
// `hook <subcommand>` words and Backstory (by name or this run's binary).
// The owner wrote it to change how our hook runs, or whether it does
// (BACKSTORY_OFF=1, `true ||`, `exit 0;`), so install keeps it as it is and
// adds no hook of its own beside it.
func ownerWrapsHook(command, sub string, opts Options) bool {
	if _, ok := ourHookBinary(command, sub, opts); ok {
		return false
	}
	name := strings.Fields(sub)[1]
	named := false
	for _, m := range hookSubWord.FindAllStringSubmatch(command, -1) {
		named = named || m[2] == name
	}
	return named && (strings.Contains(strings.ToLower(command), fallbackBinaryName) || strings.Contains(command, opts.binaryPath()))
}

// groupCommands returns e's command hooks as (index, command) pairs; none
// when e is not a group.
func groupCommands(e any) (map[string]any, []int, []string) {
	m, ok := e.(map[string]any)
	if !ok {
		return nil, nil, nil
	}
	hooks, ok := m["hooks"].([]any)
	if !ok {
		return nil, nil, nil
	}
	var idx []int
	var cmds []string
	for i, raw := range hooks {
		h, ok := raw.(map[string]any)
		if !ok || h["type"] != "command" {
			continue
		}
		cmd, _ := h["command"].(string)
		idx = append(idx, i)
		cmds = append(cmds, cmd)
	}
	return m, idx, cmds
}

// ourHookIndexes returns e as a group and the positions, inside its hooks
// array, of every command hook backstory wrote for subcommand sub, under any
// binary path; no positions when e is not a group or holds none. A hook is recognised by its
// command alone, whatever matcher, timeout or other hooks its group carries,
// so a group the owner narrowed or added to is still found.
func ourHookIndexes(e any, sub string, opts Options) (map[string]any, []int) {
	m, all, cmds := groupCommands(e)
	var idx []int
	for k, cmd := range cmds {
		if _, ok := ourHookBinary(cmd, sub, opts); ok {
			idx = append(idx, all[k])
		}
	}
	return m, idx
}

// groupWrapsHook reports whether e holds an owner's wrapper of our hook for
// sub (ownerWrapsHook).
func groupWrapsHook(e any, sub string, opts Options) bool {
	_, _, cmds := groupCommands(e)
	for _, cmd := range cmds {
		if ownerWrapsHook(cmd, sub, opts) {
			return true
		}
	}
	return false
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

// hookPos is one hook of ours: its group's index in the event's array and
// its index in that group's hooks.
type hookPos struct{ group, hook int }

// mergedHookGroups is what install makes of an event's groups arr: every
// group holding our hook for sub is reconciled in place (reconcileHookGroup:
// its first hook of ours runs command, a second one in the same group is
// dropped, the owner's matcher and keys stay), so a hook the owner put in
// several narrowed groups stays in each. A group that after that is
// identical to an earlier one of ours runs the same hook for the same events
// twice, so it goes. With no hook of ours anywhere, want is appended, unless
// a group holds the owner's wrapper of our hook (ownerWrapsHook): that slot
// is the owner's, and a default group beside it would undo it. Foreign
// groups and hooks are untouched. pos is where our hooks end up.
func mergedHookGroups(arr []any, want map[string]any, command, sub string, opts Options) (out []any, pos []hookPos) {
	out = make([]any, 0, len(arr)+1)
	wrapped := false
	var ours []map[string]any
	for _, e := range arr {
		g, idx := ourHookIndexes(e, sub, opts)
		if len(idx) == 0 {
			wrapped = wrapped || groupWrapsHook(e, sub, opts)
			out = append(out, e)
			continue
		}
		r := reconcileHookGroup(g, idx[0], idx, command, want["matcher"], sub)
		if slices.ContainsFunc(ours, func(o map[string]any) bool { return jsonDeepEqual(o, r) }) {
			continue
		}
		ours = append(ours, r)
		pos = append(pos, hookPos{len(out), idx[0]})
		out = append(out, r)
	}
	if len(ours) == 0 && !wrapped {
		pos = append(pos, hookPos{len(out), 0})
		out = append(out, want)
	}
	return out, pos
}

// mergeHookEntry makes root's hooks.<event> what mergedHookGroups makes of
// it, reporting whether that changed anything.
func mergeHookEntry(root map[string]any, event string, want map[string]any, sub string, opts Options) (bool, error) {
	hooksObj, err := objectField(root, "hooks")
	if err != nil {
		return false, err
	}
	arr, err := arrayField(hooksObj, event)
	if err != nil {
		return false, err
	}
	out, _ := mergedHookGroups(arr, want, hookCommandOf(want, 0), sub, opts)
	if jsonDeepEqual(arr, out) {
		return false, nil
	}
	hooksObj[event] = out
	root["hooks"] = hooksObj
	return true, nil
}

// hookEntryStatus finds Backstory's hook for sub under event. It is present
// exactly when install would leave the event's groups as they are
// (mergedHookGroups), the command keeping whatever backstory binary its
// first hook of ours names (an install made with --binary;
// isBackstoryBinary): the owner's matcher and keys never make it outdated,
// so --check never sends an install to widen them. With no hook of ours but
// an owner's wrapper of it the slot is foreign-conflict: install leaves it
// to the owner. bin is the path the hook invokes; pos is every hook of ours.
func hookEntryStatus(root map[string]any, event string, want map[string]any, sub string, opts Options) (st ItemStatus, bin string, pos []hookPos) {
	hooksRaw, ok := root["hooks"]
	if !ok {
		return StatusAbsent, "", nil
	}
	hooksObj, ok := hooksRaw.(map[string]any)
	if !ok {
		return StatusForeign, "", nil
	}
	arrRaw, ok := hooksObj[event]
	if !ok {
		return StatusAbsent, "", nil
	}
	arr, ok := arrRaw.([]any)
	if !ok {
		return StatusForeign, "", nil
	}
	for _, e := range arr {
		g, idx := ourHookIndexes(e, sub, opts)
		if len(idx) == 0 {
			continue
		}
		cmd := hookCommandOf(g, idx[0])
		out, pos := mergedHookGroups(arr, want, cmd, sub, opts)
		if !jsonDeepEqual(arr, out) {
			return StatusAbsent, "", nil
		}
		bin, _ := ourHookBinary(cmd, sub, opts)
		return StatusPresent, bin, pos
	}
	if slices.ContainsFunc(arr, func(e any) bool { return groupWrapsHook(e, sub, opts) }) {
		return StatusForeign, "", nil
	}
	return StatusAbsent, "", nil
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
