package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
)

// locationMethods are the daemon methods on which a request's "location"
// may select the session's project: only the ones an installed in-process
// harness plugin sends for itself (the Hermes plugin's block and
// post_tool_use). Named config, not a literal check inside the resolver.
// Every model-reachable method (note, recall, ...) is deliberately absent:
// a model-supplied location-like argument never moves a session
// (AGENT-CONTRACT.md §Observed identity — harness-reported location).
var locationMethods = map[string]bool{
	DaemonMethodBlock:       true,
	DaemonMethodPostToolUse: true,
}

// locationRequest is the one slice of a request line applyLocation reads.
type locationRequest struct {
	Session string          `json:"session"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// folderOwnedBy reports whether dir exists, is a directory, and is owned by
// uid. It is the daemon's own check of a harness-reported folder: a path the
// peer cannot be shown to own never selects a project.
func folderOwnedBy(dir string, uid int) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}

// locatedIdentity returns id re-pointed at folder: CWD becomes the folder
// and ProjectKey is computed with the same function /proc cwd uses.
func locatedIdentity(id ident.Identity, folder string, git project.Git, workspaces []string) ident.Identity {
	id.CWD = folder
	id.ProjectKey = project.Key(folder, git, workspaces)
	id.Located = true
	return id
}

// applyLocation decides the connection's identity from its first request
// line. A "location" on a plugin method selects the project when the peer
// passed the harness ancestry check (id.HarnessPID != 0) and the folder
// exists and is owned by the peer's uid; otherwise it is ignored and id —
// the /proc cwd identity — is returned unchanged. A later request from the
// same harness process and declared session with no location of its own
// (a model tool call) inherits the folder the plugin last reported, so the
// record lands in the chat's project.
func applyLocation(id ident.Identity, line []byte, sessions *SessionRegistry, git project.Git, workspaces []string) ident.Identity {
	if id.HarnessPID == 0 || len(line) == 0 {
		return id
	}
	var req locationRequest
	if json.Unmarshal(line, &req) != nil {
		return id
	}
	if locationMethods[req.Method] {
		var p struct {
			Location string `json:"location"`
		}
		if json.Unmarshal(req.Params, &p) == nil && p.Location != "" && folderOwnedBy(p.Location, id.UID) {
			folder := filepath.Clean(p.Location)
			sessions.rememberLocation(id, req.Session, folder)
			return locatedIdentity(id, folder, git, workspaces)
		}
		return id
	}
	if folder, ok := sessions.recallLocation(id, req.Session); ok {
		return locatedIdentity(id, folder, git, workspaces)
	}
	return id
}
