package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// procDescendants returns every descendant of root (excluding root).
func procDescendants(root int) []procNode {
	entries, err := os.ReadDir(thisWeekProcRoot)
	if err != nil {
		return nil
	}
	children := map[int][]procNode{}
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
	}
	var out []procNode
	seen := map[int]bool{root: true}
	var walk func(pid, depth int)
	walk = func(pid, depth int) {
		for _, c := range children[pid] {
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
func folderOfWindowPID(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	desc := procDescendants(pid)
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
}

func (r hereResolver) key(dir string) string {
	return project.Key(dir, r.git, r.workspaces)
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
		h := r.here(abs, hereSourceArg)
		return &h, nil
	}
	if w, err := hyprActiveWindow(); err == nil {
		if dir, ok := folderOfWindowPID(w.PID); ok {
			h := r.here(dir, hereSourceFocused)
			return &h, nil
		}
	}
	if p, ok := firstProject(r.result); ok {
		return &hereJSON{ProjectKey: p.ProjectKey, DisplayName: p.DisplayName, CWD: p.CWD, Source: hereSourceRecent}, nil
	}
	return &hereJSON{Source: hereSourceRecent}, nil
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
func foldersOfWindowPID(pid int) []windowFolder {
	if pid <= 0 {
		return nil
	}
	var out []windowFolder
	for _, d := range procDescendants(pid) {
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
	if dir, ok := folderOfWindowPID(pid); ok {
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
		for _, f := range foldersOfWindowPID(c.PID) {
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
