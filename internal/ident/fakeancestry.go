package ident

import (
	"fmt"
	"sync"
)

// FakeAncestryHop is one node of a synthetic /proc ancestry chain injected
// via AnchoredFakeProcFS. Hop 0 of a chain is always the connecting peer's
// own process; Hop 1 (when present) is its immediate matched-or-not parent,
// and so on — the same shape as fakeProcFS's hand-built trees in
// resolver_test.go, just JSON-serializable so a test process can hand it to
// a `backstory daemon` subprocess over an environment variable.
type FakeAncestryHop struct {
	Name       string `json:"name"`
	StartTicks uint64 `json:"start_ticks"`
	// Cwd is read only for the hop the resolver actually calls Cwd on: the
	// matched harness hop, or hop 0 when no harness matches.
	Cwd string `json:"cwd"`
}

// fakeAncestryPIDBase is where AnchoredFakeProcFS starts minting synthetic
// ancestor pids — far above any pid a real kernel hands out, so a synthetic
// pid can never collide with a real one.
const fakeAncestryPIDBase = 1_900_000_000

type resolvedFakeHop struct {
	ppid       int
	name       string
	startTicks uint64
	cwd        string
}

// AnchoredFakeProcFS presents Hops as a synthetic /proc ancestry chain for
// every connecting peer, without ever touching the real filesystem or a
// real pid beyond hop 0. The resolver's walk always starts at
// SO_PEERCRED's real pid — unpredictable ahead of time, since it belongs to
// whatever subprocess a test spawned — so the first Status/Cwd call for a
// pid this instance has never seen transparently becomes that walk's hop 0,
// and every hop above it (Hops[1:]) is handed a freshly minted synthetic
// pid from fakeAncestryPIDBase upward. Each never-before-seen real pid
// mints its OWN fresh set of synthetic ancestor pids, so two connections
// configured with the identical Hops never collide on a shared HarnessPID
// by construction (task fe2cff2a: before this type existed,
// cmd/backstory's hook tests spawned real `backstory daemon` and `backstory
// hook` subprocesses that resolved identity against the REAL /proc of the
// process running `go test`, so a test run from inside a Claude Code
// session — where a real "claude" ancestor sits somewhere above the test
// binary — could make two independent hook invocations resolve to the SAME
// real harness ancestor and collapse into one SessionRegistry entry,
// silently breaking any test that expected two independent session rows).
// Safe for concurrent use.
type AnchoredFakeProcFS struct {
	Hops []FakeAncestryHop

	mu    sync.Mutex
	next  int
	known map[int]resolvedFakeHop
}

func (f *AnchoredFakeProcFS) resolve(pid int) (resolvedFakeHop, error) {
	if len(f.Hops) == 0 {
		return resolvedFakeHop{}, fmt.Errorf("ident: AnchoredFakeProcFS has no hops configured")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.known == nil {
		f.known = map[int]resolvedFakeHop{}
		f.next = fakeAncestryPIDBase
	}
	if rh, ok := f.known[pid]; ok {
		return rh, nil
	}

	// pid is unseen: it becomes hop 0 of a brand new walk. Mint synthetic
	// pids for every hop above it right now and register the whole chain,
	// so the later calls the resolver makes for those synthetic pids
	// resolve too.
	pids := make([]int, len(f.Hops))
	pids[0] = pid
	for i := 1; i < len(f.Hops); i++ {
		pids[i] = f.next
		f.next++
	}
	for i, hop := range f.Hops {
		ppid := 1
		if i+1 < len(pids) {
			ppid = pids[i+1]
		}
		f.known[pids[i]] = resolvedFakeHop{ppid: ppid, name: hop.Name, startTicks: hop.StartTicks, cwd: hop.Cwd}
	}
	return f.known[pid], nil
}

// Status implements ProcFS.
func (f *AnchoredFakeProcFS) Status(pid int) (Status, error) {
	rh, err := f.resolve(pid)
	if err != nil {
		return Status{}, err
	}
	return Status{PPid: rh.ppid, Name: rh.name, StartTicks: rh.startTicks}, nil
}

// Cwd implements ProcFS.
func (f *AnchoredFakeProcFS) Cwd(pid int) (string, error) {
	rh, err := f.resolve(pid)
	if err != nil {
		return "", err
	}
	return rh.cwd, nil
}

// Cmdline implements ProcFS. No caller of AnchoredFakeProcFS needs it.
func (f *AnchoredFakeProcFS) Cmdline(int) ([]string, error) { return nil, nil }
