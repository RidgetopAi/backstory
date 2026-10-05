package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"net"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// daemonMethodNote, daemonMethodStatus, daemonMethodRecall,
// daemonMethodConfirm and daemonMethodTimeline are the DaemonRequest.Method
// values the daemon side answers via the mcp shim's
// note/status/recall/confirm/timeline tools.
const (
	daemonMethodNote     = "note"
	daemonMethodStatus   = "status"
	daemonMethodRecall   = "recall"
	daemonMethodConfirm  = "confirm"
	daemonMethodTimeline = "timeline"
)

// DaemonMethodBlock is the SessionStart block's daemon-side method
// (AGENT-CONTRACT.md §The SessionStart block). It is exported because
// cmd/backstory's `hook session-start` subcommand builds a DaemonRequest for
// it directly, bypassing the mcp shim's JSON-RPC tools/call indirection
// entirely — the block is not one of the five frozen v0 tools.
const DaemonMethodBlock = "block"

// errCaptureOff is startSession's captureOff-gated start callback's
// sentinel error (task 9c62f9dc, SCHEMA.md invariant 8): it tells
// ServeDaemonConn's caller apart from a genuine startSession failure so the
// former is never logged as one, and — just as importantly — tells
// SessionRegistry.SessionFor not to cache an empty session id against this
// harness process. A cached empty id would wrongly survive capture being
// switched back on, permanently starving that harness process of a real
// session for the rest of the daemon's lifetime.
var errCaptureOff = errors.New("mcp: capture is off, no session started")

// ServeDaemonConn is the daemon side of the shim<->daemon wire protocol: the
// socket.Handler cmd/backstory wires into socket.Listen. A store session is
// one run of an observed harness process, never one socket connection
// (task 32c6900d): sessions resolves this connection's id to the live
// session every other connection from the same harness process shares,
// minting a new one via startSession only the first time that harness
// process is observed. procfs backs the block method's coordination-slot
// liveness check (block.Params.ProcFS); it is otherwise unused.
//
// A connection from an unidentified harness (id.HarnessPID == 0) keeps
// today's per-connection behaviour exactly: sessions never registers or
// reuses it, so it gets its own fresh session that ends when this
// connection does. A connection from a known harness process never ends its
// shared session on EOF — the process it belongs to almost always outlives
// any single short-lived hook connection, so ending the session here would
// just recreate the per-connection churn this fix removes.
//
// id is resolved purely from SO_PEERCRED + /proc ancestry (never from
// anything on conn — AGENT-CONTRACT.md §Observed identity), so every record
// this connection writes is attributed to id.Kind's tier regardless of what
// a request line claims, and the SessionStart block it renders is always
// for id.ProjectKey, regardless of what a request line claims either. git
// backs the session.git_state observation every live session end records
// (liveSessionEnder, task c2573b35); RealGit in production, a fake in tests.
//
// captureOff is checked fresh on every note and post_tool_use request
// (SCHEMA.md invariant 8: "honoured on every write path"), reported by
// status, and checked once more here before a session is ever started
// (invariant 8 again: no sessions row is written while capture is off,
// task 9c62f9dc) — the same capture-off flag file cmd/backstory's `hook
// post-tool-use` already refuses to record on, passed in by daemon.go
// rather than resolved here so this package never has to know how the flag
// file's path is derived.
func ServeDaemonConn(id ident.Identity, conn net.Conn, st *store.Store, procfs ident.ProcFS, git project.Git, logger *log.Logger, sessions *SessionRegistry, captureOff func() (bool, error), workspaces []string) {
	// The first request line decides the connection's identity: a plugin's
	// harness-reported location (location.go) selects the project; every
	// other case is the observed /proc identity, exactly as before. The
	// scanner is created here so that line is consumed by the same loop.
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var first []byte
	hasFirst := sc.Scan()
	if hasFirst {
		first = append([]byte(nil), sc.Bytes()...)
		id = applyLocation(id, first, sessions, git, workspaces)
	} else {
		// A connection that closes before its first request line (a plugin's
		// is_available() liveness probe) mints no session: a probe stays
		// free instead of leaving an empty-harness-sid live session behind.
		return
	}

	if declinesSession(first) {
		identity := storeIdentity(id, "")
		resp := dispatchDaemonRequest(first, st, procfs, identity, "", id, git, captureOff, workspaces)
		if b, err := json.Marshal(resp); err == nil {
			_, _ = conn.Write(append(b, '\n'))
		}
		return
	}

	end := liveSessionEnder(st, git, logger, captureOff)
	start := func() (string, error) {
		if off, err := captureOff(); err != nil {
			return "", err
		} else if off {
			return "", errCaptureOff
		}
		return startSession(st, id)
	}
	sessionID, err := sessions.SessionFor(id, procfs, end, start)
	if err != nil {
		if !errors.Is(err, errCaptureOff) {
			logf(logger, "mcp: start session for pid=%d: %v", id.PID, err)
		}
	} else if id.HarnessPID == 0 {
		defer end(sessionID, "eof")
	}

	identity := storeIdentity(id, sessionID)

	for line, more := first, hasFirst; more; {
		resp := dispatchDaemonRequest(line, st, procfs, identity, sessionID, id, git, captureOff, workspaces)
		b, err := json.Marshal(resp)
		if err != nil {
			logf(logger, "mcp: marshal daemon response: %v", err)
			return
		}
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return
		}
		if more = sc.Scan(); more {
			line = sc.Bytes()
		}
	}
}

// noSessionMethods are the daemon methods whose request may set no_session:
// read-only ones only, so the flag can never suppress a write's provenance.
// Named config, not a literal check inside ServeDaemonConn.
var noSessionMethods = map[string]bool{
	daemonMethodStatus: true,
	DaemonMethodBlock:  true,
}

// declinesSession reports whether a connection's first request line asks for
// no session (the installer's health probe) on a method that allows it.
func declinesSession(line []byte) bool {
	var req DaemonRequest
	if json.Unmarshal(line, &req) != nil {
		return false
	}
	return req.NoSession && noSessionMethods[req.Method]
}

// liveSessionEnder returns the one callback every live-session end goes
// through: this connection's own EOF and the registry sweep that ends dead
// harnesses' sessions (task 25b74537's ReasonHarnessExited). Each end first
// records session.git_state for the ENDED session's own cwd (read back from
// its row — a sweep ends sessions this connection never owned), then ends
// it. A cwd that cannot be read back records could-not-observe; nothing is
// recorded while capture is off.
func liveSessionEnder(st *store.Store, git project.Git, logger *log.Logger, captureOff func() (bool, error)) func(sessionID, reason string) {
	return func(sessionID, reason string) {
		now := time.Now()
		cwd, err := st.SessionCWD(sessionID)
		if err != nil {
			logf(logger, "mcp: read cwd for ending session %s: %v", sessionID, err)
			cwd = ""
		}
		// Capture off (or unreadable) → no session.git_state event: it is a
		// timeline write like any other (SCHEMA.md invariant 8, task
		// 9c62f9dc). Ending the session row still happens.
		if off, err := captureOff(); err == nil && !off {
			recordSessionEndGitState(st, git, sessionID, cwd, now, logger)
		}
		if err := st.EndSession(sessionID, now, reason); err != nil {
			logf(logger, "mcp: end session %s: %v", sessionID, err)
		}
	}
}

// recordSessionEndGitState appends a session.git_state timeline event
// observing cwd's git working tree state as a live session ends — the
// evidence This Week's Attention needs to flag "ended with uncommitted
// changes" on (task c2573b35, AGENT-CONTRACT.md's "the daemon alone mints
// events"). Only ServeDaemonConn's own EOF path calls this: a backfilled
// session is imported wholesale from a transcript that already ended, with
// no live cwd left to observe, so it never gets this event. A failed
// observation (git.State's ok == false: git failed, or cwd is not a working
// tree at all) records payload.SessionGitState{CouldNotObserve: true} —
// never a zero UncommittedCount, which would be indistinguishable from a
// clean tree (SCHEMA.md invariant 7).
func recordSessionEndGitState(st *store.Store, git project.Git, sessionID, cwd string, now time.Time, logger *log.Logger) {
	p := payload.SessionGitState{CouldNotObserve: true}
	// An unknown cwd is could-not-observe: git.State("") would observe the
	// DAEMON's own working directory instead.
	if state, ok := git.State(cwd); cwd != "" && ok {
		count := state.Uncommitted
		p = payload.SessionGitState{Branch: state.Branch, UncommittedCount: &count}
	}
	b, err := json.Marshal(p)
	if err != nil {
		logf(logger, "mcp: marshal session git state for %s: %v", sessionID, err)
		return
	}
	if _, err := st.AppendEvent(store.Event{
		TS: now, Kind: payload.KindSessionGitState, SessionID: sessionID,
		Source: "daemon", Payload: string(b),
	}); err != nil {
		logf(logger, "mcp: append session git state for %s: %v", sessionID, err)
	}
}

func dispatchDaemonRequest(line []byte, st *store.Store, procfs ident.ProcFS, identity store.Identity, sessionID string, id ident.Identity, git project.Git, captureOff func() (bool, error), workspaces []string) DaemonResponse {

	var req DaemonRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errResponse("invalid-request", err.Error())
	}
	switch req.Method {
	case daemonMethodNote:
		return handleNote(st, git, identity, sessionID, id.ProjectKey, id.CWD, workspaces, req.Params, captureOff)
	case daemonMethodStatus:
		return handleStatus(st, id, sessionID, captureOff)
	case daemonMethodRecall:
		return handleRecall(st, id, req.Params, workspaces)
	case daemonMethodConfirm:
		return handleConfirm(st, identity, sessionID, id.ProjectKey, req.Params)
	case daemonMethodTimeline:
		return handleTimeline(st, id, sessionID, req.Params)
	case DaemonMethodBlock:
		return handleBlock(st, procfs, id, sessionID, git, workspaces)
	case DaemonMethodPostToolUse:
		return handlePostToolUse(st, sessionID, req.Params, captureOff)
	case DaemonMethodShellEmit:
		return handleShellEmit(st, sessionID, req.Params, captureOff)
	default:
		return errResponse("unknown-method", "unknown method "+req.Method)
	}
}

// startSession opens a live store session for a newly connected identity —
// called by SessionRegistry.SessionFor's start callback, so it only actually
// runs the first time a given harness process is observed (or every time,
// for an unidentified caller with no stable process identity to key on).
// The session's pid is the harness's, when the ancestry walk found one;
// otherwise it falls back to the immediate peer pid. sessions.project_key
// and records.project_key both foreign-key into projects, so a project this
// daemon has never seen before is upserted first — the resolver computes
// ProjectKey from git identity alone (AGENT-CONTRACT.md §Project = git
// repository identity), never from anything a request declares.
//
// task 32c6900d scoped this fix to the daemon's own live-session bookkeeping
// only, leaving a later backfill of the same run's transcript free to add a
// second (backfilled-origin) session for it. task 25b74537 closed that gap
// on the importer's side (internal/backfill/claude.Store.
// LiveSessionByHarnessSessionID): a transcript whose harness_session_id
// still matches a live session minted here attaches to it instead.
func startSession(st *store.Store, id ident.Identity) (string, error) {
	if id.ProjectKey != "" {
		if err := st.UpsertProject(store.Project{
			Key:       id.ProjectKey,
			Toplevel:  id.CWD,
			FirstSeen: time.Now(),
		}); err != nil {
			return "", err
		}
	}

	pid := id.HarnessPID
	if pid == 0 {
		pid = id.PID
	}
	agent := id.Harness
	if agent == "" {
		agent = ident.HarnessUnknown
	}
	return st.StartSession(store.StartSessionParams{
		Agent:            agent,
		HarnessSessionID: id.Declared["session"],
		PID:              &pid,
		CWD:              id.CWD,
		ProjectKey:       id.ProjectKey,
		StartedAt:        time.Now(),
		Origin:           store.OriginLive,
	})
}

// storeIdentity maps an observed ident.Identity to the store.Identity
// InsertRecord derives a tier from. This is the ONLY place that mapping
// happens on the daemon side, and it never reads anything from a request
// line (AGENT-CONTRACT.md §The never-list, item 2).
func storeIdentity(id ident.Identity, sessionID string) store.Identity {
	// ident.KindAgent (any socket peer, harness known or not) is the default:
	// the resolver never produces KindHuman from a peer-cred walk at all
	// (AGENT-CONTRACT.md §Observed identity), so this is here only to make
	// KindInference's mapping explicit, not because agent needs a branch.
	kind := store.IdentityAgent
	switch id.Kind {
	case ident.KindHuman:
		kind = store.IdentityHuman
	case ident.KindInference:
		kind = store.IdentityInference
	}
	return store.Identity{Kind: kind, Actor: sessionID}
}

func errResponse(code, message string) DaemonResponse {
	return DaemonResponse{Error: &DaemonError{Code: code, Message: message}}
}

func logf(logger *log.Logger, format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Printf(format, args...)
}
