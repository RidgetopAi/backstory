package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// NoteParams is note's argument shape (AGENT-CONTRACT.md §`note` — one
// write, two required fields). It deliberately has no Tier or Session
// field: whatever a caller sends under those keys is dropped by
// json.Unmarshal, never read (AGENT-CONTRACT.md §The never-list, item 2).
type NoteParams struct {
	Kind       string   `json:"kind"`
	Text       string   `json:"text"`
	About      []string `json:"about,omitempty"`
	Supersedes string   `json:"supersedes,omitempty"`
	Evidence   []int64  `json:"evidence,omitempty"`
	// Links is accepted per AGENT-CONTRACT.md's note field list. Each id
	// becomes an `informs` edge (store.EdgeInforms) from the named record
	// to the new one — chosen over a records.links column because it
	// reuses the edges table's existing atomic-insert path and its
	// foreign-key-shaped identity, rather than adding a second, parallel
	// way to say "these records relate". An id that names no existing
	// record is a JSON-RPC error naming "links"; nothing is inserted.
	Links   []string `json:"links,omitempty"`
	Expires string   `json:"expires,omitempty"` // RFC 3339
}

// NoteResult is note's return value.
//
// Edges is an additive result field (task b172e778, real use 2026-09-28):
// verified on the desk that a `supersedes` edge WAS stored even though the
// tool's result gave the agent no sign the link took. It echoes every edge
// this note actually created — the same {OtherID, Type} pairs
// handleNote built from `supersedes` and `links` — never a fresh read of
// what wound up in the edges table, since the insert itself already
// succeeded atomically (store.InsertRecordWithEdges) or the daemon would
// have returned an error instead of a NoteResult at all. The frozen v0
// INPUT schema (testdata/tools-v0.json) is untouched: this only adds an
// output field.
type NoteResult struct {
	ID   string `json:"id"`
	Tier string `json:"tier"`
	// ProjectKey is the key the record actually landed under, read back from
	// the stored record (task fd14c6cc): a note from a workspace-cwd session
	// that edited one repo is filed under that repo, not the workspace.
	ProjectKey string     `json:"project_key,omitempty"`
	Edges      []NoteEdge `json:"edges,omitempty"`
}

// NoteEdge is one edge NoteResult.Edges echoes back: the edge's type and
// the id of the OTHER record it connects to (never this note's own new id,
// which the caller already has as NoteResult.ID).
type NoteEdge struct {
	Type    string `json:"type"`
	OtherID string `json:"other_id"`
}

// noteKinds is the set of record kinds the note tool may write. It excludes
// punch/stage/confirm (SCHEMA.md §Reserved for the loop, and confirm has its
// own tool) even though store.RecordKind allows them.
var noteKinds = map[string]store.RecordKind{
	string(store.KindDecision): store.KindDecision,
	string(store.KindOutcome):  store.KindOutcome,
	string(store.KindHandoff):  store.KindHandoff,
	string(store.KindNote):     store.KindNote,
	string(store.KindClaim):    store.KindClaim,
}

// handleNote validates a note request and, if valid, calls
// store.InsertRecord. The tier in the returned NoteResult comes back from a
// GetRecord read-after-write — the daemon never trusts its own belief about
// what tier InsertRecord assigned; it reads what actually landed.
//
// captureOff is checked before anything else: SCHEMA.md invariant 8 ("no
// events and no records are inserted" while the capture-off flag file is
// present) applies to every write path, not just the client-side check
// `backstory hook post-tool-use` already does before it ever dials the
// daemon — note is reachable from any socket peer, so the daemon itself
// must refuse it too.
func handleNote(st *store.Store, git project.Git, identity store.Identity, sessionID, projectKey, cwd string, workspaces []string, raw json.RawMessage, captureOff func() (bool, error)) DaemonResponse {
	if off, err := captureOff(); err != nil {
		return errResponse("internal", err.Error())
	} else if off {
		return errResponse("capture-off", "capture is paused; no records are written")
	}

	var p NoteParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResponse("invalid-params", "invalid note params: "+err.Error())
	}
	if p.Kind == "" {
		return errResponse("invalid-params", `missing required field "kind"`)
	}
	kind, ok := noteKinds[p.Kind]
	if !ok {
		return errResponse("invalid-params", fmt.Sprintf("unknown kind %q", p.Kind))
	}
	if p.Text == "" {
		return errResponse("invalid-params", `missing required field "text"`)
	}

	var expiresAt *time.Time
	if p.Expires != "" {
		t, err := time.Parse(time.RFC3339, p.Expires)
		if err != nil {
			return errResponse("invalid-params", `invalid "expires": `+err.Error())
		}
		expiresAt = &t
	}

	if kind == store.KindHandoff {
		if home, ok := handoffHomeKey(cwd, workspaces); ok {
			if err := ensureHomeProject(st, home); err != nil {
				return errResponse("internal", err.Error())
			}
			projectKey = home
		}
	} else if project.IsWorkspaceKey(projectKey) {
		repoKey, ok, err := inferRepoKey(st, git, sessionID, p.About, workspaces)
		if err != nil {
			return errResponse("internal", err.Error())
		}
		if ok {
			projectKey = repoKey
		}
	}

	edges := make([]store.EdgeSpec, 0, len(p.Links)+1)
	if p.Supersedes != "" {
		edges = append(edges, store.EdgeSpec{
			OtherID:    p.Supersedes,
			Type:       store.EdgeSupersedes,
			DeclaredBy: sessionID,
			Field:      "supersedes",
		})
	}
	for _, linkID := range p.Links {
		edges = append(edges, store.EdgeSpec{
			OtherID:    linkID,
			Type:       store.EdgeInforms,
			DeclaredBy: sessionID,
			Field:      "links",
			Incoming:   true,
		})
	}

	// Deterministic staleness stamp (task 5615ddae): HEAD of the writing
	// session's repo; "" (NULL) for a non-git dir, never an error.
	gitHead, _ := project.HeadSHA(cwd)

	id, err := st.InsertRecordWithEdges(store.InsertRecordParams{
		Identity:   identity,
		Kind:       kind,
		Text:       p.Text,
		About:      p.About,
		SessionID:  sessionID,
		ProjectKey: projectKey,
		Evidence:   p.Evidence,
		ExpiresAt:  expiresAt,
		GitHead:    gitHead,
	}, edges)
	if err != nil {
		var capErr *store.CapError
		if errors.As(err, &capErr) {
			return errResponse("rate-limited", capErr.Error())
		}
		var edgeErr *store.UnknownEdgeTargetError
		if errors.As(err, &edgeErr) {
			return errResponse("invalid-params", edgeErr.Error())
		}
		return errResponse("internal", err.Error())
	}

	rec, err := st.GetRecord(id)
	if err != nil {
		return errResponse("internal", err.Error())
	}

	noteEdges := make([]NoteEdge, len(edges))
	for i, e := range edges {
		noteEdges[i] = NoteEdge{Type: string(e.Type), OtherID: e.OtherID}
	}

	result, err := json.Marshal(NoteResult{ID: id, Tier: string(rec.Tier), ProjectKey: rec.ProjectKey, Edges: noteEdges})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}

// handoffHomeKey resolves a kind=handoff record's HOME (decision f3fa04c7):
// the enclosing workspace's key when the writing session's own folder (cwd)
// is a configured workspace dir or lives inside one, at any depth
// (project.WorkspaceHome) — reusing internal/project's own workspace rule
// rather than a second copy of it. workspaces is the caller's
// already-resolved workspace directory list (task 482b2320, decision
// f3fa04c7's clause 7): mcp never resolves workspace dirs itself (no
// project.DefaultWorkspaceDirs call, no env read). ok is false for a
// session outside every configured workspace (a repo with no workspace at
// all, or cwd empty), in which case the caller keeps the session's own
// project_key: only handoffs are re-homed; every other record kind, and the
// session's own observed identity, are untouched.
func handoffHomeKey(cwd string, workspaces []string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	return project.WorkspaceHome(cwd, workspaces)
}

// ensureHomeProject upserts a projects row for a handoff's home key before
// the record insert that references it: records.project_key is a foreign
// key into projects(key), and a workspace's own project row is otherwise
// only ever created by a session started AT the workspace root itself
// (startSession's own UpsertProject, keyed on THAT session's observed
// project_key) — a session living inside one of the workspace's repos never
// creates it. Idempotent: UpsertProject preserves first_seen on update, so
// calling this on every rehomed handoff is harmless.
func ensureHomeProject(st *store.Store, home string) error {
	dir, ok := project.WorkspaceDirOf(home)
	if !ok {
		return nil // home is always workspace-prefixed here; defensive only
	}
	return st.UpsertProject(store.Project{Key: home, Toplevel: dir, FirstSeen: time.Now()})
}

// inferRepoKey is the daemon's project inference for a non-handoff note
// written by a session whose own key is a workspace key (task fd14c6cc): when
// the session's edited-file locations (store.SessionEditedDirs — the same
// extraction This Week uses) resolve to exactly one location and that
// location is a git repo, the record is filed under that repo's key, so the
// repo's own recall finds it. A session that edited nothing applies the same
// rule to the note's declared about[] paths. Zero or several locations, or a
// non-git folder, report ok=false: the workspace key stays. The repo's
// projects row is upserted first (records.project_key is a foreign key).
func inferRepoKey(st *store.Store, git project.Git, sessionID string, about, workspaces []string) (string, bool, error) {
	dirs, err := st.SessionEditedDirs(sessionID, git, workspaces)
	if err != nil {
		return "", false, err
	}
	if len(dirs) == 0 {
		for _, a := range about {
			if a == "" || !filepath.IsAbs(a) {
				continue
			}
			label, dir := store.LabelAndDir(a, git, workspaces)
			dirs[label] = dir
		}
	}
	if len(dirs) != 1 {
		return "", false, nil
	}
	for _, dir := range dirs {
		key := project.Key(dir, git, workspaces)
		if project.IsWorkspaceKey(key) {
			return "", false, nil
		}
		if _, isRepo := git.Repo(dir); !isRepo {
			return "", false, nil
		}
		if err := st.UpsertProject(store.Project{Key: key, Toplevel: dir, FirstSeen: time.Now()}); err != nil {
			return "", false, err
		}
		return key, true, nil
	}
	return "", false, nil
}
