// Package ident resolves the caller of a Backstory socket connection to an
// observed Identity: SO_PEERCRED gives (uid, pid); the resolver walks /proc
// ancestry from that pid to the first known harness process. Nothing a
// caller sends over the wire ever contributes to Kind, UID, PID, HarnessPID,
// Harness, CWD, or ProjectKey (AGENT-CONTRACT.md §Observed identity — never
// declared).
package ident

// Kind is the class of caller an Identity was observed for.
type Kind int

const (
	// KindHuman is reserved for the panel/CLI's direct, trusted path — not
	// reachable from this package's peer-cred + /proc walk.
	KindHuman Kind = iota
	// KindAgent is any process that reached the socket through the observed
	// peer-cred + /proc ancestry walk, harness known or not.
	KindAgent
	// KindInference is the daemon's own inference pass, never a socket peer.
	KindInference
)

func (k Kind) String() string {
	switch k {
	case KindHuman:
		return "human"
	case KindAgent:
		return "agent"
	case KindInference:
		return "inference"
	default:
		return "unknown"
	}
}

// HarnessUnknown is the Harness value when the /proc ancestry walk reaches
// pid 1, a cycle, or an unreadable process without meeting a known harness
// binary name.
const HarnessUnknown = "unknown"

// Identity is what Backstory observed about a socket peer. Declared holds
// join keys only (a harness's session_id, a subagent's actor label) —
// AGENT-CONTRACT.md is explicit that these never become identity.
type Identity struct {
	Kind       Kind
	UID        int
	PID        int
	HarnessPID int
	// HarnessStartTicks is the matched harness process's /proc start time
	// (Status.StartTicks), captured alongside HarnessPID so a session
	// registry keyed on the pair never conflates a live harness process with
	// an unrelated later process that reused its pid.
	HarnessStartTicks uint64
	Harness           string
	CWD               string
	ProjectKey        string
	Declared          map[string]string
	// Reason names why CWD/ProjectKey are empty despite a harness being
	// found, e.g. "cwd unreadable: permission denied" — the case a
	// mount-sandboxed systemd --user unit's implicit user namespace
	// produces by making /proc/<harness_pid>/cwd unreadable (task
	// f2718b5b). Empty whenever the cwd read succeeded, so callers can
	// tell that case apart from "no harness found" instead of both
	// silently looking like project="".
	Reason string
}

// PeerCreds is the (uid, pid) SO_PEERCRED reports for a socket connection.
type PeerCreds struct {
	UID int
	PID int
}
