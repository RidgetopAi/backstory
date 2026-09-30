package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
)

// sessionLabels reports sess's distinct work-location labels (decision
// f3fa04c7's LABELS rule): derived from sess's OWN observed file-touching
// timeline events — the file paths their tool_use payloads carry, the same
// extraction laterEventsTouchingAbout already applies for about[] matching
// — resolved to the repo/folder each path lives in (project.LabelForPath),
// NEVER from any record's about[] or text. A session with no file-touching
// events is labelled with its own folder (project.Label(sess.CWD, ...)).
// Labels are returned sorted, for a deterministic result independent of
// event insertion order.
func (s *Store) sessionLabels(sess Session, git project.Git, workspaces []string) ([]string, error) {
	dirs, err := s.sessionLabelDirs(sess, git, workspaces)
	if err != nil {
		return nil, err
	}
	labels := make([]string, 0, len(dirs))
	for label := range dirs {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels, nil
}

// sessionLabelDirs is sessionLabels' own extraction, kept alongside each
// label's representative directory — the repo toplevel when the touched
// file's directory resolves to a git working tree, else the directory
// itself (mirroring project.Label's own resolution, which returns only the
// label string, not the directory it decided on). This is ActiveHomeLabels'
// own need: a label with no direct per-repo project_key of its own still
// needs a CWD / project.Key input (decision f3fa04c7 clause 5, task
// 482b2320). A session with no file-touching events maps its one fallback
// label (project.Label(sess.CWD, ...)) to sess.CWD itself, exactly as
// sessionLabels' old single-label fallback did.
func (s *Store) sessionLabelDirs(sess Session, git project.Git, workspaces []string) (map[string]string, error) {
	dirs, err := s.sessionEditedDirs(sess.ID, git, workspaces)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		dirs[project.Label(sess.CWD, git, workspaces)] = sess.CWD
	}
	return dirs, nil
}

// SessionEditedDirs is sessionLabelDirs' file-touching half on its own: the
// label -> directory map of the locations session sessionID's mutating file
// tool events touched, with NO cwd fallback — an empty map means the session
// edited nothing. handleNote's project inference (task fd14c6cc) needs to
// tell "edited exactly one repo" from "edited nothing", which the fallback
// would blur; it shares this code with This Week rather than reimplementing.
func (s *Store) SessionEditedDirs(sessionID string, git project.Git, workspaces []string) (map[string]string, error) {
	return s.sessionEditedDirs(sessionID, git, workspaces)
}

func (s *Store) sessionEditedDirs(sessionID string, git project.Git, workspaces []string) (map[string]string, error) {
	events, err := s.eventsForSessionID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: session label dirs for %s: %w", sessionID, err)
	}

	dirs := map[string]string{}
	for _, e := range events {
		if e.Kind != payload.KindToolUse {
			continue
		}
		var tu payload.ToolUse
		if json.Unmarshal([]byte(e.Payload), &tu) != nil {
			continue
		}
		if tu.Path == "" || !payload.IsMutatingFileTool(tu.Name) {
			continue
		}
		label, dir := labelAndDir(tu.Path, git, workspaces)
		if _, ok := dirs[label]; !ok {
			dirs[label] = dir
		}
	}
	return dirs, nil
}

// labelAndDir is project.LabelForPath applied to path, alongside the
// directory it resolved the label from (project.Label's own toplevel when
// path's directory is inside a git working tree, else the directory
// itself) — the one-git-shellout-per-path version of what LabelForPath and
// a separate toplevel lookup would otherwise do as two calls.
func labelAndDir(path string, git project.Git, workspaces []string) (label, dir string) {
	fileDir := filepath.Dir(path)
	if repo, ok := git.Repo(fileDir); ok {
		return project.WorkspaceRelativeName(repo.Toplevel, workspaces), repo.Toplevel
	}
	return project.WorkspaceRelativeName(fileDir, workspaces), fileDir
}

// ActiveWorkLocation is one active label's own aggregate identity within a
// home — the unit This Week's per-label rows (decision f3fa04c7 clause 5,
// task 482b2320) build from when a home's sessions carry no direct per-repo
// project_key of their own: a session started at the workspace root itself,
// whose file-touching events resolve to a repo/folder that never got its
// own session.
type ActiveWorkLocation struct {
	Label        string
	Dir          string
	LastActivity time.Time
	SessionIDs   []string
}

// ActiveHomeLabels returns every work-location label (sessionLabels) with
// activity in [since, asOf], contributed by any session whose folder
// resolves to home (sessionHomeKey) — decision f3fa04c7 clause 5's home-wide
// label scan: a home's active work locations are the union of every one of
// its sessions' own OBSERVED labels, independent of whether that session's
// own project_key happens to equal a per-repo key. Sorted by label.
func (s *Store) ActiveHomeLabels(home string, since, asOf time.Time, git project.Git, workspaces []string, keep func(Session) (bool, error)) ([]ActiveWorkLocation, error) {
	sessions, err := s.sessionsForHome(home, workspaces)
	if err != nil {
		return nil, fmt.Errorf("store: active home labels for %s: %w", home, err)
	}

	byLabel := map[string]*ActiveWorkLocation{}
	for _, sess := range sessions {
		if keep != nil {
			ok, err := keep(sess)
			if err != nil {
				return nil, fmt.Errorf("store: active home labels for %s: %w", home, err)
			}
			if !ok {
				continue
			}
		}
		last, ok, err := s.sessionLastActivity(sess, asOf)
		if err != nil {
			return nil, fmt.Errorf("store: active home labels for %s: %w", home, err)
		}
		if !ok || last.Before(since) {
			continue
		}
		dirs, err := s.sessionLabelDirs(sess, git, workspaces)
		if err != nil {
			return nil, fmt.Errorf("store: active home labels for %s: %w", home, err)
		}
		for label, dir := range dirs {
			loc, ok := byLabel[label]
			if !ok {
				loc = &ActiveWorkLocation{Label: label, Dir: dir}
				byLabel[label] = loc
			}
			loc.SessionIDs = append(loc.SessionIDs, sess.ID)
			if last.After(loc.LastActivity) {
				loc.LastActivity = last
			}
		}
	}

	out := make([]ActiveWorkLocation, 0, len(byLabel))
	for _, loc := range byLabel {
		sort.Strings(loc.SessionIDs)
		out = append(out, *loc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// sessionLastActivity is sess's own last-activity instant — its start, any
// of its own timeline events, or any record it wrote — bounded above by
// asOf, the session-scoped analog of LastActivity's project-scoped "as of"
// rule (This Week's per-label rows need session-level granularity: a home's
// sessions can span many project_key values, decision f3fa04c7 clause 5).
// ok is false only if sess.StartedAt is itself after asOf and it has no
// other activity at or before asOf either.
func (s *Store) sessionLastActivity(sess Session, asOf time.Time) (time.Time, bool, error) {
	var maxTS sql.NullInt64
	err := s.db.QueryRow(`
		SELECT MAX(ts) FROM (
			SELECT ? AS ts
			UNION ALL
			SELECT ts FROM timeline_events WHERE session_id = ?
			UNION ALL
			SELECT ts FROM records WHERE session_id = ?
		) WHERE ts <= ?`, tsToNanos(sess.StartedAt), sess.ID, sess.ID, tsToNanos(asOf)).Scan(&maxTS)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: session last activity for %s: %w", sess.ID, err)
	}
	if !maxTS.Valid {
		return time.Time{}, false, nil
	}
	return tsFromNanos(maxTS.Int64), true, nil
}

// sessionHomeKey is the home key sess's own folder resolves to (decision
// f3fa04c7): the enclosing workspace's key when sess.CWD is a workspace dir
// or lives inside one at any depth (project.WorkspaceHome), else sess's own
// stored project_key unchanged — the exact same rule handleNote applies
// when a handoff is written from that session, so a home-scoped read always
// agrees with what a home-scoped write filed.
func sessionHomeKey(sess Session, workspaces []string) string {
	if home, ok := project.WorkspaceHome(sess.CWD, workspaces); ok {
		return home
	}
	return sess.ProjectKey
}

// sessionsForHome returns every session whose own folder resolves
// (sessionHomeKey) to homeKey — the membership HandoffFreshness's home-
// scoped queries need (decision f3fa04c7): "every session whose folder
// resolves to that home", which can span many distinct project_key values
// (one per repo under a workspace), not just homeKey's own project_key
// aliases. Order is unspecified; callers care about membership, not
// sequence.
func (s *Store) sessionsForHome(homeKey string, workspaces []string) ([]Session, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE project_key IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("store: sessions for home %s: %w", homeKey, err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, fmt.Errorf("store: sessions for home %s: %w", homeKey, err)
	}

	var out []Session
	for _, id := range ids {
		sess, err := s.getSession(id)
		if err != nil {
			return nil, fmt.Errorf("store: sessions for home %s: %w", homeKey, err)
		}
		if sessionHomeKey(sess, workspaces) == homeKey {
			out = append(out, sess)
		}
	}
	return out, nil
}

// HandoffForLabel returns the newest non-tombstoned kind=handoff record in
// home whose own session's work-location labels (sessionLabels) include
// label — decision f3fa04c7's per-repo Resume/Where-you-left-off
// resolution: many repos share one home (the workspace they live under), so
// a home-scoped handoff lookup must filter by which repo the handoff's own
// session actually touched, the same way block's Resume slot and
// internal/week's per-project handoff both need to. Scans newest-first
// (RecordsForProject's own order), capped at maxHandoffScanForLabel. found
// is false when home has no handoff at all, or none of its handoffs'
// sessions carry label.
func (s *Store) HandoffForLabel(home, label string, git project.Git, workspaces []string) (Record, bool, error) {
	handoffs, err := s.RecordsForProject(home, KindHandoff, maxHandoffScanForLabel)
	if err != nil {
		return Record{}, false, fmt.Errorf("store: handoff for label %q in %s: %w", label, home, err)
	}
	for _, h := range handoffs {
		if h.SessionID == "" {
			continue
		}
		sess, err := s.getSession(h.SessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return Record{}, false, fmt.Errorf("store: handoff for label %q in %s: %w", label, home, err)
		}
		labels, err := s.sessionLabels(sess, git, workspaces)
		if err != nil {
			return Record{}, false, fmt.Errorf("store: handoff for label %q in %s: %w", label, home, err)
		}
		for _, l := range labels {
			if l == label {
				return h, true, nil
			}
		}
	}
	return Record{}, false, nil
}

// maxHandoffScanForLabel bounds HandoffForLabel's newest-first scan of a
// home's handoffs — the read-side safety cap RecordsForProject's own limit
// parameter already exists for, applied here so a workspace with years of
// handoff history must not load its entire ledger before the label filter
// ever gets a chance to narrow it down.
const maxHandoffScanForLabel = 1000

// HandoffLabel returns the comma-joined work-location labels (sessionLabels)
// of h's own session — decision f3fa04c7's "with its label on the Resume
// line": when the SessionStart block resumes a home's newest handoff at the
// workspace root itself, it names which repo(s) that handoff's session
// actually touched. Empty when h has no session.
func (s *Store) HandoffLabel(h Record, git project.Git, workspaces []string) (string, error) {
	if h.SessionID == "" {
		return "", nil
	}
	sess, err := s.getSession(h.SessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("store: handoff label for %s: %w", h.ID, err)
	}
	labels, err := s.sessionLabels(sess, git, workspaces)
	if err != nil {
		return "", fmt.Errorf("store: handoff label for %s: %w", h.ID, err)
	}
	return strings.Join(labels, ", "), nil
}

// LabelAndDir is labelAndDir for callers outside store (handleNote's about[]
// fallback, task fd14c6cc): the same per-path resolution This Week uses.
func LabelAndDir(path string, git project.Git, workspaces []string) (label, dir string) {
	return labelAndDir(path, git, workspaces)
}
