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
