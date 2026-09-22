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

// daemonMethodNote and daemonMethodStatus are the only DaemonRequest.Method
// values the daemon side answers. recall/timeline/confirm never reach the
// socket at all: the shim returns their not-implemented error itself
// (DONE WHEN clause 4 — "never touch the store").
const (
	daemonMethodNote   = "note"
	daemonMethodStatus = "status"
)

// ServeDaemonConn is the daemon side of the shim<->daemon wire protocol: the
// socket.Handler cmd/backstory wires into socket.Listen. It starts a live
// store session for the connecting Identity, dispatches DaemonRequest lines
// against st until the connection closes, and ends the session on exit.
//
// id is resolved purely from SO_PEERCRED + /proc ancestry (never from
// anything on conn — AGENT-CONTRACT.md §Observed identity), so every record
// this connection writes is attributed to id.Kind's tier regardless of what
// a request line claims.
func ServeDaemonConn(id ident.Identity, conn net.Conn, st *store.Store, logger *log.Logger) {
	sessionID, err := startSession(st, id)
	if err != nil {
		logf(logger, "mcp: start session for pid=%d: %v", id.PID, err)
	} else {
		defer func() {
			if err := st.EndSession(sessionID, time.Now(), "eof"); err != nil {
				logf(logger, "mcp: end session %s: %v", sessionID, err)
			}
		}()
	}

	identity := storeIdentity(id, sessionID)

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		resp := dispatchDaemonRequest(sc.Bytes(), st, identity, sessionID, id)
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

func dispatchDaemonRequest(line []byte, st *store.Store, identity store.Identity, sessionID string, id ident.Identity) DaemonResponse {
	var req DaemonRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return errResponse("invalid-request", err.Error())
	}
	switch req.Method {
	case daemonMethodNote:
		return handleNote(st, identity, sessionID, id.ProjectKey, req.Params)
	case daemonMethodStatus:
		return handleStatus(st, id, sessionID)
	default:
		return errResponse("unknown-method", "unknown method "+req.Method)
	}
}

// startSession opens a live store session for a newly connected identity.
// The session's pid is the harness's, when the ancestry walk found one;
// otherwise it falls back to the immediate peer pid. sessions.project_key
// and records.project_key both foreign-key into projects, so a project this
// daemon has never seen before is upserted first — the resolver computes
// ProjectKey from git identity alone (AGENT-CONTRACT.md §Project = git
// repository identity), never from anything a request declares.
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
