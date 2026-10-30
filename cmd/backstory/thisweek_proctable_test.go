package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeFakeProc adds <root>/<pid>/status with the Name and PPid lines
// procPPidAndComm reads.
func writeFakeProc(t *testing.T, root string, pid, ppid int, name string) {
	t.Helper()
	d := filepath.Join(root, fmt.Sprint(pid))
	if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // test dir
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "status"), []byte(fmt.Sprintf("Name:\t%s\nPPid:\t%d\n", name, ppid)), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

func descendantPIDs(nodes []procNode) map[int]bool {
	out := map[int]bool{}
	for _, n := range nodes {
		out[n.pid] = true
	}
	return out
}

// TestProcTableReadsTheProcessTableOnce: windowsByProject asks for the
// descendants of every open window, and each used to re-read every
// /proc/<pid> on the machine (about 55 ms of a 310 ms this-week run on a
// desktop with a dozen windows). A procTable reads the table on first use
// and answers every later question from that snapshot, the browser check
// (a window process's name) included.
func TestProcTableReadsTheProcessTableOnce(t *testing.T) {
	proc := t.TempDir()
	orig := thisWeekProcRoot
	thisWeekProcRoot = proc
	t.Cleanup(func() { thisWeekProcRoot = orig })

	writeFakeProc(t, proc, 100, 1, "foot")
	writeFakeProc(t, proc, 101, 100, "zsh")
	writeFakeProc(t, proc, 102, 101, "claude")
	writeFakeProc(t, proc, 103, 1, "chromium")

	procs := &procTable{}
	if got := descendantPIDs(procs.descendants(100)); !got[101] || !got[102] || len(got) != 2 {
		t.Fatalf("descendants(100) = %v; want 101 and 102", got)
	}

	for _, pid := range []string{"102", "103"} {
		if err := os.RemoveAll(filepath.Join(proc, pid)); err != nil {
			t.Fatal(err)
		}
	}
	if got := descendantPIDs(procs.descendants(100)); !got[102] {
		t.Errorf("after 102 left /proc, the same table's descendants(100) = %v; want its first snapshot, still with 102", got)
	}
	if got := descendantPIDs((&procTable{}).descendants(100)); got[102] {
		t.Errorf("a new table's descendants(100) = %v; want a fresh read without 102", got)
	}
	if got := procs.comm(102); got != "claude" {
		t.Errorf("after 102 left /proc, the same table's comm(102) = %q; want its first snapshot, claude", got)
	}
	if !isBrowserPID(procs, 103) {
		t.Errorf("after chromium (103) left /proc, the same table no longer sees a browser; want its first snapshot")
	}
	if isBrowserPID(&procTable{}, 103) {
		t.Errorf("a new table sees chromium (103) after it left /proc; want a fresh read")
	}
}

// TestNilProcTableReadsFresh: a zero-value hereResolver (a future test, say)
// carries a nil *procTable. Asking it must read the process table, not
// panic; with nowhere to keep a snapshot, each call reads afresh.
func TestNilProcTableReadsFresh(t *testing.T) {
	proc := t.TempDir()
	orig := thisWeekProcRoot
	thisWeekProcRoot = proc
	t.Cleanup(func() { thisWeekProcRoot = orig })

	writeFakeProc(t, proc, 100, 1, "foot")
	writeFakeProc(t, proc, 101, 100, "zsh")
	writeFakeProc(t, proc, 103, 1, "chromium")

	var procs *procTable
	if got := descendantPIDs(procs.descendants(100)); !got[101] || len(got) != 1 {
		t.Errorf("nil table's descendants(100) = %v; want 101", got)
	}
	if got := procs.comm(101); got != "zsh" {
		t.Errorf("nil table's comm(101) = %q; want zsh", got)
	}
	if !isBrowserPID(procs, 103) {
		t.Errorf("nil table does not see chromium (103) as a browser")
	}

	writeFakeProc(t, proc, 101, 100, "fish")
	if got := procs.comm(101); got != "fish" {
		t.Errorf("after 101 became fish, nil table's comm(101) = %q; want a fresh read, fish", got)
	}
}

// TestThisWeekRunSharesOneProcTable: one `this-week --here auto` run reads the
// process table once and shares it between here and the windows. The fake
// hyprctl re-parents bar's shell to pid 1 on its second `clients` call, which
// comes after here's first read and before the windows are matched: a shared
// snapshot still has the shell under bar's terminal and finds bar's window, a
// fresh read per window would not. (Folders are read live; the snapshot holds
// the parent links and names.)
func TestThisWeekRunSharesOneProcTable(t *testing.T) {
	e := newHereEnv(t)
	clients := `[{"address":"0xf","class":"ghostty","pid":401,"focusHistoryID":0},` +
		`{"address":"0xb","class":"ghostty","pid":402,"focusHistoryID":1}]`
	count := filepath.Join(t.TempDir(), "clients-calls")
	writeScript(t, filepath.Join(e.fakeBin, "hyprctl"), `case "$1" in
activewindow) echo '{"address":"0xf","class":"ghostty","pid":401}';;
clients) n=$(( $(cat '`+count+`' 2>/dev/null || echo 0) + 1 )); echo $n > '`+count+`'
  [ $n = 2 ] && printf 'Name:\tbash\nPPid:\t1\n' > '`+filepath.Join(e.proc, "412", "status")+`'
  echo '`+clients+`';;
esac`)
	e.addProc(t, 401, 1, "ghostty", e.home, "")
	e.addProc(t, 411, 401, "bash", e.foo, "/dev/pts/1")
	e.addProc(t, 402, 1, "ghostty", e.home, "")
	e.addProc(t, 412, 402, "bash", e.bar, "/dev/pts/2")

	m := mustRun(t, e, "--here", "auto")
	if h := hereOf(t, m); h["project_key"] != e.fooKey || h["source"] != "focused" {
		t.Fatalf("here = %v; want foo, the focused terminal", h)
	}
	if w := windowsByKey(t, m)[e.barKey]; w != "0xb" {
		t.Errorf("bar's window = %q; want 0xb from the run's one snapshot of /proc (its shell was re-parented mid-run)", w)
	}
}
