package mcp

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

// daemonMethodNote, daemonMethodStatus and daemonMethodRecall are the
// DaemonRequest.Method values the daemon side answers via the mcp shim's
// note/status/recall tools. timeline/confirm still never reach the socket
// at all: the shim returns their not-implemented error itself (DONE WHEN
// clause 4 — "never touch the store").
const (
	daemonMethodNote   = "note"
	daemonMethodStatus = "status"
	daemonMethodRecall = "recall"
)

// DaemonMethodBlock is the SessionStart block's daemon-side method
// (AGENT-CONTRACT.md §The SessionStart block). It is exported because
// cmd/backstory's `hook session-start` subcommand builds a DaemonRequest for
// it directly, bypassing the mcp shim's JSON-RPC tools/call indirection
// entirely — the block is not one of the five frozen v0 tools.
const DaemonMethodBlock = "block"

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
// for id.ProjectKey, regardless of what a request line claims either.
//
// captureOff is checked fresh on every note request (SCHEMA.md invariant 8:
// "honoured on every write path") and reported by status — the same
// capture-off flag file cmd/backstory's `hook post-tool-use` already
// refuses to record on, passed in by daemon.go rather than resolved here so
// this package never has to know how the flag file's path is derived.
func ServeDaemonConn(id ident.Identity, conn net.Conn, st *store.Store, procfs ident.ProcFS, logger *log.Logger, sessions *SessionRegistry, captureOff func() (bool, error)) {
	end := func(sessionID, reason string) {
		if err := st.EndSession(sessionID, time.Now(), reason); err != nil {
			logf(logger, "mcp: end session %s: %v", sessionID, err)
		}
	}
	sessionID, err := sessions.SessionFor(id, procfs, end, func() (string, error) { return startSession(st, id) })
	if err != nil {
		logf(logger, "mcp: start session for pid=%d: %v", id.PID, err)
	} else if id.HarnessPID == 0 {
		defer end(sessionID, "eof")
	}

	identity := storeIdentity(id, sessionID)

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		resp := dispatchDaemonRequest(sc.Bytes(), st, procfs, identity, sessionID, id, captureOff)
		b, err := json.Marshal(resp)
		if err != nil {
			logf(logger, "mcp: marshal daemon response: %v", err)
			return
		}
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

func dispatchDaemonRequest(line []byte, st *store.Store, procfs ident.ProcFS, identity store.Identity, sessionID string, id ident.Identity, captureOff func() (bool, error)) DaemonResponse {
	var req DaemonRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errResponse("invalid-request", err.Error())
	}
	switch req.Method {
	case daemonMethodNote:
		return handleNote(st, identity, sessionID, id.ProjectKey, req.Params, captureOff)
	case daemonMethodStatus:
		return handleStatus(st, id, sessionID, captureOff)
	case daemonMethodRecall:
		return handleRecall(st, id, req.Params)
	case DaemonMethodBlock:
		return handleBlock(st, procfs, id, sessionID)
	case DaemonMethodPostToolUse:
		return handlePostToolUse(st, sessionID, req.Params)
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
