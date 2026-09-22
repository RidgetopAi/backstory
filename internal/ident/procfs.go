package ident

// Status is the subset of /proc/<pid>/status the resolver's ancestry walk
// needs.
type Status struct {
	PPid int
	Name string
}

// ProcFS abstracts /proc so the resolver is tested against a fake tree
// (AGENT-CONTRACT.md §Observed identity) instead of the real filesystem.
// RealProcFS (procfs_linux.go) is the production implementation.
type ProcFS interface {
	Status(pid int) (Status, error)
	Cwd(pid int) (string, error)
	Cmdline(pid int) ([]string, error)
}
