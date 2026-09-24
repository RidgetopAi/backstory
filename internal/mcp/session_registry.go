package mcp

import (
	"sync"

	"github.com/RidgetopAi/backstory/internal/ident"
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
}

// NewSessionRegistry returns an empty registry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{sessions: map[harnessKey]string{}}
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
func (r *SessionRegistry) SessionFor(id ident.Identity, start func() (string, error)) (sessionID string, err error) {
	if id.HarnessPID == 0 {
		return start()
	}

	key := harnessKey{Harness: id.Harness, HarnessPID: id.HarnessPID, StartTicks: id.HarnessStartTicks}

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
