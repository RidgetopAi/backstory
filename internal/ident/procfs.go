package ident

// Status is the subset of /proc/<pid>/status (plus /proc/<pid>/stat's
// starttime field) the resolver's ancestry walk needs.
type Status struct {
	PPid int
	Name string
	// StartTicks is /proc/<pid>/stat field 22 (starttime): the process's
	// start time in clock ticks since boot. Combined with a pid it
	// disambiguates a genuinely new process from the kernel recycling an old
	// pid — the same technique ps/systemd use — so a session keyed on
	// (harness, pid, StartTicks) never conflates two different processes
	// that happened to share a pid.
	StartTicks uint64
	// SID is /proc/<pid>/stat field 6 (session): the process's kernel session
	// id, which a backgrounded or double-forked child inherits from the
	// interactive shell (that shell is its session's leader, so SID is the
	// shell's own pid) even after every intermediate parent has exited.
	SID int
}

// ProcFS abstracts /proc so the resolver is tested against a fake tree
// (AGENT-CONTRACT.md §Observed identity) instead of the real filesystem.
// RealProcFS (procfs_linux.go) is the production implementation.
type ProcFS interface {
	Status(pid int) (Status, error)
	Cwd(pid int) (string, error)
	Cmdline(pid int) ([]string, error)
}
