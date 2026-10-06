package mcp

import (
	"log"
	"sync"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// harnessKey identifies one observed harness process instance: the daemon's
// unit of "one session" (this punch's WHAT TO BUILD — a store session is one
// run of a harness, never one socket connection). StartTicks distinguishes
// a live harness process from a later, unrelated process that reused its
// pid (AGENT-CONTRACT.md §Observed identity; ident.Identity.HarnessStartTicks).
type harnessKey struct {
	Harness    string
	HarnessPID int
	StartTicks uint64
	// Project is empty for the harness's /proc-cwd session, and the located
	// project key for a session selected by a harness-reported folder: one
	// harness process serving many chats holds one session per project.
	Project string
	// Dir is the observed directory, set only for a shell session whose
	// project is a workspace key: sibling non-git folders share that key but
	// are distinct locations, so each gets its own session.
	Dir string
}

// locationKey identifies one chat of one harness process: the harness
// process plus the session id its plugin declares.
type locationKey struct {
	Harness    harnessKey
	HarnessSID string
}

// ReasonHarnessExited is the store.EndSession exit_kind SessionRegistry's
// sweep records when it evicts a harness process's session because /proc no
// longer backs it (SCHEMA.md sessions.exit_kind): the process exited, or a
// later, unrelated process reused its pid — /proc's start-time field
// (ident.Identity.HarnessStartTicks) is what tells the two apart, but from
// the session's point of view both mean the harness process it was opened
// for is gone (task 25b74537).
const ReasonHarnessExited = "harness-exited"

// ReasonDaemonRestarted is the exit_kind EndOrphanedSessions records for a
// session row a previous daemon run left live: the registry that owned it
// died with that run, so no connection can still be writing to it (task
// 6d68ac6f).
const ReasonDaemonRestarted = "daemon-restarted"

// EndOrphanedSessions ends every live session row still open in st with
// ReasonDaemonRestarted. It must run at daemon start before the socket
// listens, so a row "re-bound by a live connection" cannot exist yet: a
// harness that is still running re-opens a fresh session on its next
// connection. Each row ends exactly as a live end does (liveSessionEnder):
// a non-shell session first records session.git_state for its own cwd as
// observed now (could-not-observe on failure, nothing while capture is
// off), a shell session records none (task 78ca0350). It returns how many
// rows it ended.
func EndOrphanedSessions(st *store.Store, git project.Git, logger *log.Logger, captureOff func() (bool, error)) (int, error) {
	ids, err := st.LiveSessionIDsLeftOpen()
	if err != nil {
		return 0, err
	}
	end := liveSessionEnder(st, git, logger, captureOff)
	for _, id := range ids {
		end(id, ReasonDaemonRestarted)
	}
	return len(ids), nil
}

// SessionRegistry maps each observed harness process to the one live store
// session every connection from that process shares, for the lifetime of
// the daemon process that owns it (one registry per `backstory daemon`
// run, or per test daemon). It never keys on anything a connection
// declares — only on ident.Identity fields the daemon itself observed via
// SO_PEERCRED + /proc ancestry.
type SessionRegistry struct {
	mu       sync.Mutex
	sessions map[harnessKey]string
	// locations remembers the folder a plugin last reported per chat, so
	// that chat's later location-less calls (model tool calls) file under
	// the same project.
	locations map[locationKey]string
}

// NewSessionRegistry returns an empty registry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{sessions: map[harnessKey]string{}, locations: map[locationKey]string{}}
}

// SessionFor returns the live store session id for id's harness process,
// calling start to mint one the first time this harness process is
// observed and reusing it for every later connection from the same process
// (same Harness + HarnessPID + HarnessStartTicks) — regardless of what any
// connection's own request line declares as its "session" join key, and
// regardless of how many different declared values different connections
// from that same process carry (observed identity wins, never merges or
// splits on a declared id).
//
// A connection whose harness could not be identified (id.HarnessPID == 0)
// is never registered or reused: it always gets a fresh session via start,
// preserving today's per-connection behaviour for an unknown caller — the
// registry has no stable process identity to key it on in the first place.
//
// Before resolving id's own session, SessionFor sweeps every OTHER
// registered harness process's entry against procfs (the daemon notices
// "on the next connection", per this punch's WHAT TO BUILD): an entry whose
// pid no longer has a /proc status, or whose current /proc status reports a
// different start time (the kernel recycled the pid for a later, unrelated
// process), means that harness process is gone. A stale entry is ended via
// end (an honest exit reason — ReasonHarnessExited) and evicted from the
// registry, so a status call from any other live harness stops listing it
// in other_live_sessions and the registry does not grow for the daemon's
// whole lifetime (DONE WHEN clauses 1 and 2). procfs == nil skips the sweep
// entirely — used by callers with no process table to check against.
func (r *SessionRegistry) SessionFor(id ident.Identity, procfs ident.ProcFS, end func(sessionID, reason string), start func() (string, error)) (sessionID string, err error) {
	if procfs != nil {
		r.sweep(procfs, end)
	}

	if id.HarnessPID == 0 {
		return start()
	}

	key := baseKey(id)
	if id.Located {
		key.Project = id.ProjectKey
		if id.Harness == ident.HarnessShell && project.IsWorkspaceKey(id.ProjectKey) {
			key.Dir = id.CWD
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if sid, ok := r.sessions[key]; ok {
		return sid, nil
	}
	sid, err := start()
	if err != nil {
		return "", err
	}
	r.sessions[key] = sid
	return sid, nil
}

// sweep evicts every registered harness process whose recorded (pid,
// StartTicks) no longer matches what procfs currently reports for that pid
// — either the pid has no /proc entry at all (the process exited) or it now
// belongs to a different process (a different StartTicks) — and calls end
// for each one evicted this way. The DB call end makes happens outside r's
// lock, so a slow store write never blocks an unrelated connection's own
// SessionFor call.
func (r *SessionRegistry) sweep(procfs ident.ProcFS, end func(sessionID, reason string)) {
	r.mu.Lock()
	var stale []string
	for key, sid := range r.sessions {
		st, err := procfs.Status(key.HarnessPID)
		if err != nil || st.StartTicks != key.StartTicks {
			stale = append(stale, sid)
			delete(r.sessions, key)
		}
	}
	for key := range r.locations {
		st, err := procfs.Status(key.Harness.HarnessPID)
		if err != nil || st.StartTicks != key.Harness.StartTicks {
			delete(r.locations, key)
		}
	}
	r.mu.Unlock()

	if end == nil {
		return
	}
	for _, sid := range stale {
		end(sid, ReasonHarnessExited)
	}
}

func baseKey(id ident.Identity) harnessKey {
	return harnessKey{Harness: id.Harness, HarnessPID: id.HarnessPID, StartTicks: id.HarnessStartTicks}
}

// rememberLocation records folder as the chat's current working folder. A
// request with no declared session id has no chat to remember it for.
func (r *SessionRegistry) rememberLocation(id ident.Identity, declaredSession, folder string) {
	if declaredSession == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.locations[locationKey{Harness: baseKey(id), HarnessSID: declaredSession}] = folder
}

func (r *SessionRegistry) recallLocation(id ident.Identity, declaredSession string) (string, bool) {
	if declaredSession == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	folder, ok := r.locations[locationKey{Harness: baseKey(id), HarnessSID: declaredSession}]
	return folder, ok
}
