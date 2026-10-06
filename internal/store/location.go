package store

import (
	"database/sql"
	"errors"
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

	m := locationMatcher{label: ls.Label, repoKey: ls.RepoKey}
	seen := map[string]bool{}
	consider := func(sess Session) error {
		if seen[sess.ID] {
			return nil
		}
		seen[sess.ID] = true
		in, err := s.sessionInLocation(m, sess, git, workspaces)
		if err != nil {
			return err
		}
		if in {
			ls.SessionIDs[sess.ID] = true
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
func (s *Store) recordIDsInScope(q purgeQuerier, ls LocationScope, sinceNanos, untilNanos int64, onlyLive bool, kind RecordKind) ([]string, error) {
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
	if kind != "" {
		window += " AND kind = ?"
		wargs = append(wargs, string(kind))
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
	ids, err := s.recordIDsInScope(s.db, ls, 0, 0, false, "")
	if err != nil {
		return nil, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return s.getRecords(ids)
}

// locationMatcher is the ONE membership rule for "does this session belong
// to location L": the session is filed under the location's own repo key,
// or its observed work-location labels (sessionLabels) include the
// location's label. LocationScope (every record/session reader and purge)
// and LatestHandoffAt (the block's Resume, This Week's row handoff) both
// decide through it, so no two surfaces can disagree about which handoff
// belongs to a location (task ed31b744).
type locationMatcher struct {
	label   string
	repoKey string
}

func (s *Store) sessionInLocation(m locationMatcher, sess Session, git project.Git, workspaces []string) (bool, error) {
	if m.repoKey != "" && s.canonicalizeProjectKey(sess.ProjectKey) == m.repoKey {
		return true, nil
	}
	labels, err := s.sessionLabels(sess, git, workspaces)
	if err != nil {
		return false, err
	}
	for _, l := range labels {
		if l == m.label {
			return true, nil
		}
	}
	return false, nil
}

// LocationScopeForKey is the scope of a bare project key: records filed
// under the key plus every record written by a session filed under it —
// which is where `note` re-homes a repo session's handoff (the workspace
// key, decision f3fa04c7). It is what a caller holding only a key (recall's
// `project` parameter, `records|export|purge --project`) resolves through.
func (s *Store) LocationScopeForKey(key string) (LocationScope, error) {
	return s.locationScopeForKeyQ(s.db, key)
}

func (s *Store) locationScopeForKeyQ(q purgeQuerier, key string) (LocationScope, error) {
	key = s.canonicalizeProjectKey(key)
	ls := LocationScope{RepoKey: key, SessionIDs: map[string]bool{}}
	rows, err := q.Query(`SELECT id FROM sessions WHERE project_key = ?`, key)
	if err != nil {
		return LocationScope{}, fmt.Errorf("store: location scope for key %s: %w", key, err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return LocationScope{}, fmt.Errorf("store: location scope for key %s: %w", key, err)
	}
	for _, id := range ids {
		ls.SessionIDs[id] = true
	}
	return ls, nil
}

// maxHandoffScanForLocation bounds LatestHandoffAt's newest-first scan, the
// read-side safety cap for a workspace with years of handoff history.
const maxHandoffScanForLocation = 1000

// LatestHandoffAt returns the newest non-tombstoned handoff belonging to
// dir under the same membership rule as LocationScope.HasRecord, without
// building the whole scope: candidates are the handoffs filed under dir's
// repo key and under its workspace home key, newest first.
func (s *Store) LatestHandoffAt(dir string, git project.Git, workspaces []string) (Record, bool, error) {
	dir = filepath.Clean(dir)
	m := locationMatcher{label: project.Label(dir, git, workspaces)}
	var keys []any
	if key := project.Key(dir, git, workspaces); !project.IsWorkspaceKey(key) {
		m.repoKey = s.canonicalizeProjectKey(key)
		keys = append(keys, m.repoKey)
	}
	if home, ok := project.WorkspaceHome(dir, workspaces); ok {
		keys = append(keys, s.canonicalizeProjectKey(home))
	}
	if len(keys) == 0 {
		// Neither a repo nor under a workspace: the dir's own key.
		key := s.canonicalizeProjectKey(project.Key(dir, git, workspaces))
		m.repoKey = key
		keys = append(keys, key)
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := append([]any{string(KindHandoff)}, keys...)
	args = append(args, maxHandoffScanForLocation)
	//nolint:gosec // marks is only "?" placeholders; every value is bound
	query := `SELECT id FROM records
		WHERE kind = ? AND tombstoned_at IS NULL AND project_key IN (` + marks + `)
		ORDER BY rowid DESC LIMIT ?`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return Record{}, false, fmt.Errorf("store: latest handoff at %s: %w", dir, err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return Record{}, false, fmt.Errorf("store: latest handoff at %s: %w", dir, err)
	}
	recs, err := s.getRecords(ids)
	if err != nil {
		return Record{}, false, fmt.Errorf("store: latest handoff at %s: %w", dir, err)
	}
	for _, h := range recs {
		if m.repoKey != "" && h.ProjectKey == m.repoKey {
			return h, true, nil
		}
		if h.SessionID == "" {
			continue
		}
		sess, err := s.getSession(h.SessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return Record{}, false, fmt.Errorf("store: latest handoff at %s: %w", dir, err)
		}
		in, err := s.sessionInLocation(m, sess, git, workspaces)
		if err != nil {
			return Record{}, false, fmt.Errorf("store: latest handoff at %s: %w", dir, err)
		}
		if in {
			return h, true, nil
		}
	}
	return Record{}, false, nil
}

// SearchRecordsInScope is SearchRecordsInProject over a whole scope: the
// FTS matches among records filed under ls.RepoKey or written by one of
// ls's sessions.
func (s *Store) SearchRecordsInScope(ls LocationScope, query string, limit int) ([]SearchResult, error) {
	ftsQuery := escapeFTS5Query(query)
	if ftsQuery == "" {
		return nil, nil
	}
	ids := ls.sortedSessionIDs()
	seen := map[string]bool{}
	var out []SearchResult
	for start := 0; start == 0 || start < len(ids); start += locationQueryChunk {
		chunk := ids[min(start, len(ids)):min(start+locationQueryChunk, len(ids))]
		var cond []string
		args := []any{ftsQuery}
		if start == 0 && ls.RepoKey != "" {
			cond = append(cond, "r.project_key = ?")
			args = append(args, ls.RepoKey)
		}
		if len(chunk) > 0 {
			cond = append(cond, "r.session_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+")")
			for _, id := range chunk {
				args = append(args, id)
			}
		}
		if len(cond) == 0 {
			continue
		}
		args = append(args, limit)
		//nolint:gosec // cond is assembled from fixed fragments and "?" placeholders; every value is bound
		query := `SELECT r.id, r.ts, r.kind, r.tier, r.text
			FROM records_fts
			JOIN records r ON r.rowid = records_fts.rowid
			WHERE records_fts MATCH ? AND r.tombstoned_at IS NULL AND (` + strings.Join(cond, " OR ") + `)
			ORDER BY rank, r.ts ASC
			LIMIT ?`
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("store: search records in scope: %w", err)
		}
		res, err := scanSearchResults(rows)
		if err != nil {
			return nil, fmt.Errorf("store: search records in scope: %w", err)
		}
		for _, r := range res {
			if !seen[r.ID] && len(out) < limit {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// ResolveProjectRef resolves a caller-supplied project reference — a
// project key (repo keys are absolute common-dir paths, so a known key wins
// over reading the same string as a directory) or a directory path — to the
// project key and the location scope its records are read through. Anything
// that is neither a known key nor an absolute directory is an error, never
// silently the caller's own project.
func (s *Store) ResolveProjectRef(ref string, git project.Git, workspaces []string) (string, LocationScope, error) {
	if _, ok, err := s.GetProject(ref); err != nil {
		return "", LocationScope{}, err
	} else if ok {
		ls, err := s.LocationScopeForKey(ref)
		return s.canonicalizeProjectKey(ref), ls, err
	}
	if !filepath.IsAbs(ref) {
		return "", LocationScope{}, fmt.Errorf("unknown project %q (want a project key or an absolute path)", ref)
	}
	dir := filepath.Clean(ref)
	ls, err := s.LocationScope(dir, git, workspaces)
	if err != nil {
		return "", LocationScope{}, err
	}
	return s.canonicalizeProjectKey(project.Key(dir, git, workspaces)), ls, nil
}
