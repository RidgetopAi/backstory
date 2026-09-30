package store

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RidgetopAi/backstory/internal/project"
)

// locationQueryChunk bounds how many session ids one IN (...) query binds,
// safely under SQLite's variable limit.
const locationQueryChunk = 500

// LocationScope is exactly the sessions and records This Week attributes to
// one row (task a9a784ec): the row's work-location label decides
// membership with the SAME sessionLabels rule the per-label rows use, plus
// — for a git repo row — every session and record filed under the repo's
// own project key. A label row (a non-git folder under a workspace) or the
// workspace-root row does NOT inherit the shared workspace key's other
// records or sessions: only what is attributed to that label.
type LocationScope struct {
	Dir   string
	Label string
	// RepoKey is the row's own repo project key; empty when dir's key is a
	// workspace identity (a label / workspace-root row).
	RepoKey    string
	SessionIDs map[string]bool
}

// LocationScope resolves dir to its scope. dir is a row's summary cwd.
func (s *Store) LocationScope(dir string, git project.Git, workspaces []string) (LocationScope, error) {
	dir = filepath.Clean(dir)
	ls := LocationScope{
		Dir:        dir,
		Label:      project.Label(dir, git, workspaces),
		SessionIDs: map[string]bool{},
	}
	if key := project.Key(dir, git, workspaces); !project.IsWorkspaceKey(key) {
		ls.RepoKey = s.canonicalizeProjectKey(key)
	}

	seen := map[string]bool{}
	consider := func(sess Session) error {
		if seen[sess.ID] {
			return nil
		}
		seen[sess.ID] = true
		if ls.RepoKey != "" && s.canonicalizeProjectKey(sess.ProjectKey) == ls.RepoKey {
			ls.SessionIDs[sess.ID] = true
			return nil
		}
		labels, err := s.sessionLabels(sess, git, workspaces)
		if err != nil {
			return err
		}
		for _, l := range labels {
			if l == ls.Label {
				ls.SessionIDs[sess.ID] = true
				break
			}
		}
		return nil
	}

	if ls.RepoKey != "" {
		rows, err := s.db.Query(`SELECT id FROM sessions WHERE project_key = ?`, ls.RepoKey)
		if err != nil {
			return LocationScope{}, fmt.Errorf("store: location scope %s: %w", dir, err)
		}
		ids, err := scanIDs(rows)
		if err != nil {
			return LocationScope{}, fmt.Errorf("store: location scope %s: %w", dir, err)
		}
		for _, id := range ids {
			sess, err := s.getSession(id)
			if err != nil {
				return LocationScope{}, fmt.Errorf("store: location scope %s: %w", dir, err)
			}
			if err := consider(sess); err != nil {
				return LocationScope{}, err
			}
		}
	}
	if home, ok := project.WorkspaceHome(dir, workspaces); ok {
		sessions, err := s.sessionsForHome(s.canonicalizeProjectKey(home), workspaces)
		if err != nil {
			return LocationScope{}, fmt.Errorf("store: location scope %s: %w", dir, err)
		}
		for _, sess := range sessions {
			if err := consider(sess); err != nil {
				return LocationScope{}, err
			}
		}
	}
	return ls, nil
}

// HasRecord reports whether r belongs to the scope.
func (ls LocationScope) HasRecord(r Record) bool {
	return (ls.RepoKey != "" && r.ProjectKey == ls.RepoKey) || ls.SessionIDs[r.SessionID]
}

func (ls LocationScope) sortedSessionIDs() []string {
	ids := make([]string, 0, len(ls.SessionIDs))
	for id := range ls.SessionIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// recordIDsInScope returns the ids of every record in the scope (tombstoned
// ones included) with ts in [sinceNanos, untilNanos) (a zero bound is open),
// newest first by sequence, each with its rowid.
func (s *Store) recordIDsInScope(q purgeQuerier, ls LocationScope, sinceNanos, untilNanos int64, onlyLive bool) ([]string, error) {
	window := ""
	var wargs []any
	if sinceNanos != 0 {
		window += " AND ts >= ?"
		wargs = append(wargs, sinceNanos)
	}
	if untilNanos != 0 {
		window += " AND ts < ?"
		wargs = append(wargs, untilNanos)
	}
	if onlyLive {
		window += " AND tombstoned_at IS NULL"
	}
	type row struct {
		id    string
		rowid int64
	}
	byID := map[string]row{}
	run := func(cond string, cargs []any) error {
		rows, err := q.Query(`SELECT id, rowid FROM records WHERE `+cond+window, append(cargs, wargs...)...) //nolint:gosec // cond is assembled from fixed fragments and "?" placeholders; every value is bound
		if err != nil {
			return fmt.Errorf("store: location records: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.rowid); err != nil {
				return fmt.Errorf("store: location records: %w", err)
			}
			byID[r.id] = r
		}
		return rows.Err()
	}
	if ls.RepoKey != "" {
		if err := run("project_key = ?", []any{ls.RepoKey}); err != nil {
			return nil, err
		}
	}
	ids := ls.sortedSessionIDs()
	for start := 0; start < len(ids); start += locationQueryChunk {
		chunk := ids[start:min(start+locationQueryChunk, len(ids))]
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		if err := run("session_id IN ("+marks+")", args); err != nil {
			return nil, err
		}
	}
	rowsOut := make([]row, 0, len(byID))
	for _, r := range byID {
		rowsOut = append(rowsOut, r)
	}
	sort.Slice(rowsOut, func(i, j int) bool { return rowsOut[i].rowid > rowsOut[j].rowid })
	out := make([]string, len(rowsOut))
	for i, r := range rowsOut {
		out[i] = r.id
	}
	return out, nil
}

// RecordsForLocation returns every record in the scope, tombstoned ones
// included, newest first by sequence, capped at limit.
func (s *Store) RecordsForLocation(ls LocationScope, limit int) ([]Record, error) {
	ids, err := s.recordIDsInScope(s.db, ls, 0, 0, false)
	if err != nil {
		return nil, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return s.getRecords(ids)
}
