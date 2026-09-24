package ident

import "fmt"

// Resolver turns a socket peer's credentials into an observed Identity. It
// never reads anything the peer sent over the connection.
type Resolver struct {
	// ProcFS is the /proc abstraction the ancestry walk uses.
	ProcFS ProcFS
	// ProjectKey computes a project key from a cwd (typically
	// project.Key(cwd, someGit) wired in by the caller). Nil skips
	// ProjectKey resolution, leaving it empty.
	ProjectKey func(cwd string) string
}

// Resolve walks /proc ancestry from peer.PID upward until it meets a known
// harness process (ident/harnesses.go), pid 1, or a cycle — whichever comes
// first — and returns the Identity that was observed. It never panics: an
// unreadable pid, a cycle, or a walk that reaches pid 1 all just stop the
// walk with Harness left at HarnessUnknown.
func (r *Resolver) Resolve(peer PeerCreds) Identity {
	id := Identity{
		Kind:    KindAgent,
		UID:     peer.UID,
		PID:     peer.PID,
		Harness: HarnessUnknown,
	}

	visited := make(map[int]bool)
	for pid := peer.PID; pid > 1 && !visited[pid]; {
		visited[pid] = true
		st, err := r.ProcFS.Status(pid)
		if err != nil {
			break
		}
		if name, ok := matchHarness(st.Name); ok {
			id.Harness = name
			id.HarnessPID = pid
			id.HarnessStartTicks = st.StartTicks
			break
		}
		if st.PPid == pid {
			break // self-referencing entry; visited map alone would still catch it next loop
		}
		pid = st.PPid
	}

	cwdPID := id.HarnessPID
	if cwdPID == 0 {
		cwdPID = peer.PID
	}
	cwd, err := r.ProcFS.Cwd(cwdPID)
	if err != nil {
		id.Reason = fmt.Sprintf("cwd unreadable: %v", err)
		return id
	}
	id.CWD = cwd
	if r.ProjectKey != nil {
		id.ProjectKey = r.ProjectKey(cwd)
	}
	return id
}
