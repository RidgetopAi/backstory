package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/week"
)

// hereAuto is the --here value that asks for the focused terminal's folder.
const hereAuto = "auto"

// Sources of hereJSON.Source (PANEL-CONTRACT.md §here).
const (
	hereSourceArg     = "arg"
	hereSourceFocused = "focused"
	hereSourceRecent  = "recent"
)

// External-command tunables. hyprctl and tmux are looked up on PATH.
const (
	hyprctlBin = "hyprctl"
	tmuxBin    = "tmux"
	// hereCommandTimeout bounds each hyprctl/tmux call so a wedged compositor
	// can never hang the panel's data fetch.
	hereCommandTimeout = 3 * time.Second
	// tmuxClientCommPrefix/Suffix identify a tmux client process by its comm
	// ("tmux: client").
	tmuxClientCommPrefix = "tmux:"
	tmuxClientCommSuffix = "client"
	// tmuxValueFlags are the tmux global options that take a value (-c shell,
	// -f file, -L name, -S path, -T features).
	tmuxValueFlags = "cfLST"
)

// hereIgnoredRoots are kernel pseudo-filesystems: a process whose cwd lies
// under one (a browser's sandboxed renderer sits in /proc/<pid>/fdinfo) is not
// in any project folder.
var hereIgnoredRoots = []string{"/proc", "/sys", "/dev"}

// thisWeekProcRoot is the /proc this command walks. A package variable, the
// test seam for a fake /proc tree (the daemon's equivalent is
// procFSForDaemon); it is never read from the environment.
var thisWeekProcRoot = "/proc"

// hereJSON is the top-level `here` object (PANEL-CONTRACT.md §here).
type hereJSON struct {
	ProjectKey  string `json:"project_key"`
	DisplayName string `json:"display_name"`
	CWD         string `json:"cwd"`
	Source      string `json:"source"`
}

// runCommand runs a PATH-resolved command with a timeout and returns stdout.
func runCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hereCommandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed binary names, args built from pids/ttys
}

type hyprWindow struct {
	Address        string `json:"address"`
	Class          string `json:"class"`
	Title          string `json:"title"`
	PID            int    `json:"pid"`
	FocusHistoryID int    `json:"focusHistoryID"`
}

func hyprActiveWindow() (hyprWindow, error) {
	out, err := runCommand(hyprctlBin, "activewindow", "-j")
	if err != nil {
		return hyprWindow{}, fmt.Errorf("hyprctl activewindow: %w", err)
	}
	var w hyprWindow
	if err := json.Unmarshal(out, &w); err != nil {
		return hyprWindow{}, fmt.Errorf("hyprctl activewindow: %w", err)
	}
	return w, nil
}

func hyprClients() ([]hyprWindow, error) {
	out, err := runCommand(hyprctlBin, "clients", "-j")
	if err != nil {
		return nil, fmt.Errorf("hyprctl clients: %w", err)
	}
	var ws []hyprWindow
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, fmt.Errorf("hyprctl clients: %w", err)
	}
	return ws, nil
}

type procNode struct {
	pid        int
	depth      int
	comm       string
	startTicks uint64
}

func procPath(pid int, rest string) string {
	return filepath.Join(thisWeekProcRoot, strconv.Itoa(pid), rest)
}

// procPPidAndComm reads PPid and Name from <proc>/<pid>/status.
func procPPidAndComm(pid int) (ppid int, comm string, ok bool) {
	b, err := os.ReadFile(procPath(pid, "status")) //nolint:gosec // path built from an int pid
	if err != nil {
		return 0, "", false
	}
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "PPid:"):
			v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
			if err != nil {
				return 0, "", false
			}
			ppid = v
		case strings.HasPrefix(line, "Name:"):
			comm = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		}
	}
	return ppid, comm, true
}

// procStartTicks reads stat field 22; 0 when unavailable (ordering then
// falls back to pid).
func procStartTicks(pid int) uint64 {
	b, err := os.ReadFile(procPath(pid, "stat")) //nolint:gosec // path built from an int pid
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i == -1 || i+2 > len(s) {
		return 0
	}
	fields := strings.Fields(s[i+2:])
	const startTimeField = 19
	if len(fields) <= startTimeField {
		return 0
	}
	v, _ := strconv.ParseUint(fields[startTimeField], 10, 64)
	return v
}

// procTable is one run's snapshot of the process tree. It reads every
// <proc>/<pid> on first use and answers each later descendants call from that
// snapshot: windowsByProject asks about every open window, and re-reading the
// whole table each time cost about 55 ms per run on a desktop. A nil table
// has nowhere to keep a snapshot, so it reads afresh on every call.
type procTable struct {
	children map[int][]procNode
	comms    map[int]string
}

// load reads the snapshot on first use.
func (t *procTable) load() {
	if t.children == nil {
		t.children, t.comms = readProcChildren()
	}
}

// comm is pid's process name in the snapshot, "" when it was not running.
func (t *procTable) comm(pid int) string {
	if t == nil {
		t = &procTable{}
	}
	t.load()
	return t.comms[pid]
}

// descendants returns every descendant of root (excluding root).
func (t *procTable) descendants(root int) []procNode {
	if t == nil {
		t = &procTable{}
	}
	t.load()
	var out []procNode
	seen := map[int]bool{root: true}
	var walk func(pid, depth int)
	walk = func(pid, depth int) {
		for _, c := range t.children[pid] {
			if seen[c.pid] {
				continue
			}
			seen[c.pid] = true
			c.depth = depth
			out = append(out, c)
			walk(c.pid, depth+1)
		}
	}
	walk(root, 1)
	return out
}

// readProcChildren reads every <proc>/<pid> once into a ppid -> children map
// and a pid -> name map. Neither is nil, so an unreadable <proc> is not
// re-read on every call.
func readProcChildren() (map[int][]procNode, map[int]string) {
	children := map[int][]procNode{}
	comms := map[int]string{}
	entries, err := os.ReadDir(thisWeekProcRoot)
	if err != nil {
		return children, comms
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		ppid, comm, ok := procPPidAndComm(pid)
		if !ok {
			continue
		}
		children[ppid] = append(children[ppid], procNode{pid: pid, comm: comm, startTicks: procStartTicks(pid)})
		comms[pid] = comm
	}
	return children, comms
}

// usableProjectDir reports whether dir can be a folder the here card or a
// window match pins: an existing directory, not under a pseudo-filesystem, and
// not the user's home directory itself (home is never a project).
func usableProjectDir(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	dir = filepath.Clean(dir)
	for _, root := range hereIgnoredRoots {
		if dir == root || strings.HasPrefix(dir, root+"/") {
			return false
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && dir == filepath.Clean(home) {
		return false
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// tmuxServerArgs reads the tmux client's own argv from <proc>/<pid>/cmdline
// and returns the flag that selects its server: "-S <path>" or "-L <name>"
// (tmux lets -S override -L, so only one is returned). nil means the default
// server: no flag, or an unreadable cmdline. Parsing stops at the tmux command
// word; flags may be clustered ("-2L name") or attached ("-Lname").
func tmuxServerArgs(pid int) []string {
	b, err := os.ReadFile(procPath(pid, "cmdline")) //nolint:gosec // path built from an int pid
	if err != nil {
		return nil
	}
	argv := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	var name, sock string
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if len(a) < 2 || a[0] != '-' {
			break
		}
	flags:
		for j := 1; j < len(a); j++ {
			if !strings.ContainsRune(tmuxValueFlags, rune(a[j])) {
				continue
			}
			val := a[j+1:]
			if val == "" && i+1 < len(argv) {
				i++
				val = argv[i]
			}
			switch a[j] {
			case 'L':
				name = val
			case 'S':
				sock = val
			}
			break flags
		}
	}
	switch {
	case sock != "":
		return []string{"-S", sock}
	case name != "":
		return []string{"-L", name}
	}
	return nil
}

// tmuxCommand runs a tmux subcommand against the server a client uses.
func tmuxCommand(server []string, args ...string) ([]byte, error) {
	return runCommand(tmuxBin, append(append([]string{}, server...), args...)...)
}

func isTmuxClient(comm string) bool {
	return strings.HasPrefix(comm, tmuxClientCommPrefix) && strings.HasSuffix(comm, tmuxClientCommSuffix)
}

// folderOfWindowPID resolves the folder a window's terminal is in: the pane
// path of a tmux client descendant (asked of tmux by the client's tty), else
// the cwd of the deepest descendant (ties → newest). ok is false when the
// window has no descendant or no cwd is readable.
// browserNames are the process names (comm) of the browsers Omarchy tags
// (default/hypr/apps/browser.lua), with the -bin names some packages use.
var browserNames = map[string]bool{
	"chromium": true, "chrome": true, "brave": true, "vivaldi-bin": true, "msedge": true, "helium": true,
	"firefox": true, "firefox-bin": true, "zen": true, "zen-bin": true, "librewolf": true,
}

// isBrowserPID reports whether the window process pid is a browser. Its
// descendants are tabs, web apps and extension helpers (native messaging
// hosts running in their own install folder), never the folder the user is
// working in, so a browser window is not looked into.
func isBrowserPID(procs *procTable, pid int) bool {
	return browserNames[procs.comm(pid)]
}

func folderOfWindowPID(procs *procTable, pid int) (string, bool) {
	if pid <= 0 || isBrowserPID(procs, pid) {
		return "", false
	}
	desc := procs.descendants(pid)
	if len(desc) == 0 {
		return "", false
	}
	for _, d := range desc {
		if !isTmuxClient(d.comm) {
			continue
		}
		tty, err := os.Readlink(procPath(d.pid, "fd/0")) //nolint:gosec // path built from an int pid
		if err != nil {
			continue
		}
		out, err := tmuxCommand(tmuxServerArgs(d.pid), "display-message", "-c", tty, "-p", "#{pane_current_path}")
		if p := strings.TrimSpace(string(out)); err == nil && usableProjectDir(p) {
			return p, true
		}
	}
	// Deepest descendant whose cwd is readable (ties → newest).
	for len(desc) > 0 {
		best := newestDeepest(desc)
		cwd, err := os.Readlink(procPath(desc[best].pid, "cwd")) //nolint:gosec // path built from an int pid
		if err == nil && usableProjectDir(cwd) {
			return cwd, true
		}
		desc = append(desc[:best], desc[best+1:]...)
	}
	return "", false
}

func newestDeepest(nodes []procNode) int {
	best := 0
	for i, d := range nodes {
		b := nodes[best]
		if d.depth != b.depth {
			if d.depth > b.depth {
				best = i
			}
			continue
		}
		if d.startTicks != b.startTicks {
			if d.startTicks > b.startTicks {
				best = i
			}
			continue
		}
		if d.pid > b.pid {
			best = i
		}
	}
	return best
}

// hereResolver turns folders into project identities with the daemon's own
// project-key function.
type hereResolver struct {
	git         project.Git
	workspaces  []string
	result      week.Result
	displayName func(key, dir string) string
	// inScope reports whether the project (key) of folder dir is in scope for
	// BACKSTORY_PROJECT_ROOTS; nil means everything is.
	inScope func(key, dir string) bool
	// procs is this run's process-tree snapshot, shared by here and windows.
	procs *procTable
}

func (r hereResolver) key(dir string) string {
	return project.Key(dir, r.git, r.workspaces)
}

// scoped reports whether dir's project is in scope (always, with no roots).
func (r hereResolver) scoped(dir string) bool {
	return r.inScope == nil || r.inScope(r.key(dir), dir)
}

func (r hereResolver) here(dir, source string) hereJSON {
	key := r.key(dir)
	return hereJSON{ProjectKey: key, DisplayName: r.displayName(key, dir), CWD: dir, Source: source}
}

// firstProject is the most recently active where_left_off project.
func firstProject(result week.Result) (week.ProjectSummary, bool) {
	for _, row := range result.WhereLeftOff {
		if row.Group == "" {
			return row.Project, true
		}
		if len(row.Children) > 0 {
			return row.Children[0], true
		}
	}
	return week.ProjectSummary{}, false
}

// resolveHere implements `--here DIR|auto`. Only a DIR that cannot be made
// absolute is an error; every `auto` failure degrades to source "recent".
func (r hereResolver) resolveHere(arg string) (*hereJSON, error) {
	if arg != hereAuto {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return nil, fmt.Errorf("resolve --here %q: %w", arg, err)
		}
		if r.scoped(abs) {
			h := r.here(abs, hereSourceArg)
			return &h, nil
		}
		return r.recent(), nil
	}
	for _, w := range focusOrder() {
		if isBackstoryPanel(w) {
			continue
		}
		if dir, ok := folderOfWindowPID(r.procs, w.PID); ok && r.scoped(dir) {
			h := r.here(dir, hereSourceFocused)
			return &h, nil
		}
	}
	return r.recent(), nil
}

// focusOrder is the active window, then every window by Hyprland's focus
// history (focusHistoryID 0 = most recent), each once. `here` is the first
// of them whose folder is an in-scope project: Backstory's own panel, a
// browser or an out-of-scope window in front is passed over, not taken as
// "no project here".
func focusOrder() []hyprWindow {
	var out []hyprWindow
	if w, err := hyprActiveWindow(); err == nil {
		out = append(out, w)
	}
	if ws, err := hyprClients(); err == nil {
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].FocusHistoryID < ws[j].FocusHistoryID })
		for _, w := range ws {
			if len(out) == 0 || w.Address != out[0].Address {
				out = append(out, w)
			}
		}
	}
	return out
}

// Backstory's own panel window, as ops/hyprland/backstory.lua matches it
// (Panel.qml's windowTitle). It belongs to the Quickshell process, whose
// descendants are the whole shell's helpers, never the user's folder.
const (
	backstoryPanelClass = "org.quickshell"
	backstoryPanelTitle = "backstory"
)

func isBackstoryPanel(w hyprWindow) bool {
	return w.Class == backstoryPanelClass && w.Title == backstoryPanelTitle
}

// recent is the most recently active where-you-left-off project, which Build
// has already limited to in-scope projects: an out-of-scope folder, focused
// or named, never becomes the current project.
func (r hereResolver) recent() *hereJSON {
	if p, ok := firstProject(r.result); ok {
		return &hereJSON{ProjectKey: p.ProjectKey, DisplayName: p.DisplayName, CWD: p.CWD, Source: hereSourceRecent}
	}
	return &hereJSON{Source: hereSourceRecent}
}

// tmuxPaneSep separates the fields of the tmux list-panes format below; a
// tab cannot appear in a session name or index, and a path with a tab is
// rejected by field-count.
const tmuxPaneSep = "\t"

// tmuxPaneFormat is what `tmux list-panes -s` is asked for: session, window
// index, pane index, whether the window / pane is the active one, and path.
const tmuxPaneFormat = "#{session_name}\t#{window_index}\t#{pane_index}\t#{window_active}\t#{pane_active}\t#{pane_current_path}"

// windowRef is where a project is open: a Hyprland window address plus, when
// the project lives in a tmux pane, the tmux target (session:window.pane).
type windowRef struct {
	Address string
	Tmux    string
}

// windowFolder is one folder found inside a Hyprland window.
type windowFolder struct {
	Dir  string
	Tmux string // "" unless the folder is a tmux pane
	// Active marks the active pane of the active window; it wins a tie
	// between several panes of one project.
	Active bool
}

// tmuxTargetRe is the shape of a tmux target this command emits and the panel
// accepts: session:window.pane with numeric indexes. Session names are
// restricted to characters that are inert in an argv element and in tmux's
// own target grammar (tmux itself rewrites '.' and ':' in session names).
var tmuxTargetRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*:[0-9]+\.[0-9]+$`)

// tmuxSessionPanes lists every pane of the session the client on tty is
// attached to (`list-panes -s`), active pane of the active window first.
func tmuxSessionPanes(tty string, server []string) []windowFolder {
	out, err := tmuxCommand(server, "display-message", "-c", tty, "-p", "#{session_name}")
	session := strings.TrimSpace(string(out))
	if err != nil || session == "" {
		return nil
	}
	out, err = tmuxCommand(server, "list-panes", "-s", "-t", "="+session, "-F", tmuxPaneFormat)
	if err != nil {
		return nil
	}
	var panes []windowFolder
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), tmuxPaneSep)
		if len(f) != 6 || !usableProjectDir(f[5]) {
			continue
		}
		wf := windowFolder{Dir: f[5], Active: f[3] == "1" && f[4] == "1"}
		if target := f[0] + ":" + f[1] + "." + f[2]; tmuxTargetRe.MatchString(target) {
			wf.Tmux = target
		}
		panes = append(panes, wf)
	}
	return panes
}

// foldersOfWindowPID lists every folder a window's terminal holds: all panes
// of each attached tmux client's session, else (no tmux) the folder
// folderOfWindowPID resolves.
func foldersOfWindowPID(procs *procTable, pid int) []windowFolder {
	if pid <= 0 {
		return nil
	}
	var out []windowFolder
	for _, d := range procs.descendants(pid) {
		if !isTmuxClient(d.comm) {
			continue
		}
		tty, err := os.Readlink(procPath(d.pid, "fd/0")) //nolint:gosec // path built from an int pid
		if err != nil {
			continue
		}
		out = append(out, tmuxSessionPanes(tty, tmuxServerArgs(d.pid))...)
	}
	if len(out) > 0 {
		return out
	}
	if dir, ok := folderOfWindowPID(procs, pid); ok {
		return []windowFolder{{Dir: dir, Active: true}}
	}
	return nil
}

// windowsByProject maps project key → its most recently focused open window
// (lowest focusHistoryID) and, inside that window, the tmux pane holding the
// project (the active one when several); empty when hyprctl is unavailable.
func (r hereResolver) windowsByProject() map[string]windowRef {
	clients, err := hyprClients()
	if err != nil {
		return nil
	}
	out := map[string]windowRef{}
	best := map[string]int{}
	for _, c := range clients {
		if c.Address == "" {
			continue
		}
		// Within this window: key → chosen folder (active pane wins, else first).
		inWindow := map[string]windowFolder{}
		for _, f := range foldersOfWindowPID(r.procs, c.PID) {
			key := r.key(f.Dir)
			if prev, seen := inWindow[key]; seen && (prev.Active || !f.Active) {
				continue
			}
			inWindow[key] = f
		}
		for key, f := range inWindow {
			if prev, seen := best[key]; seen && prev <= c.FocusHistoryID {
				continue
			}
			best[key] = c.FocusHistoryID
			out[key] = windowRef{Address: c.Address, Tmux: f.Tmux}
		}
	}
	return out
}
