package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

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
	events, err := s.eventsForSessionID(sess.ID)
	if err != nil {
		return nil, fmt.Errorf("store: session labels for %s: %w", sess.ID, err)
	}

	seen := map[string]bool{}
	var labels []string
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
		label := project.LabelForPath(tu.Path, git, workspaces)
		if !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return []string{project.Label(sess.CWD, git, workspaces)}, nil
	}
	sort.Strings(labels)
	return labels, nil
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
