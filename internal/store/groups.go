package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrProjectGroupRequiresHuman is returned when SetProjectGroup or
// ClearProjectGroup is called with an Identity that is not IdentityHuman.
// Grouping is a human-only power (decision bcc9fa54): project identity
// stays observed (git repository), but the grouping layer on top of it is
// set by the human only, never by an agent — the same pattern
// ErrTombstoneRequiresHuman enforces for records.
var ErrProjectGroupRequiresHuman = errors.New("store: project group write requires a human identity")

// GroupMembership is one row of project_groups: a project and the group it
// currently belongs to.
type GroupMembership struct {
	ProjectKey string
	GroupName  string
	SetAt      time.Time
}

// SetProjectGroup puts projectKey in groupName, moving it out of any group
// it was previously in (project_key is project_groups' primary key, so a
// project is in at most one group at a time; re-setting it is a move, not
// an additional membership). It refuses any identity other than
// IdentityHuman and leaves the table unchanged when it does. projectKey is
// resolved through s.canonicalizeProjectKey before the write (task
// d65ef8ff), so a group set on a folder's legacy plain key and its
// workspace-prefixed key land in the SAME row.
func (s *Store) SetProjectGroup(identity Identity, groupName, projectKey string) error {
	if identity.Kind != IdentityHuman {
		return ErrProjectGroupRequiresHuman
	}
	if groupName == "" {
		return fmt.Errorf("store: set project group: group name required")
	}
	if projectKey == "" {
		return fmt.Errorf("store: set project group: project key required")
	}
	projectKey = s.canonicalizeProjectKey(projectKey)
	_, err := s.db.Exec(`INSERT INTO project_groups (project_key, group_name, set_at)
		VALUES (?, ?, ?)
		ON CONFLICT(project_key) DO UPDATE SET
			group_name = excluded.group_name,
			set_at     = excluded.set_at`,
		projectKey, groupName, tsToNanos(time.Now()))
	if err != nil {
		return fmt.Errorf("store: set project group %s -> %s: %w", projectKey, groupName, err)
	}
	return nil
}

// ClearProjectGroup removes projectKey from whatever group it is in.
// Clearing a project that is not in any group is a no-op, not an error. It
// refuses any identity other than IdentityHuman and leaves the table
// unchanged when it does. projectKey is resolved through
// s.canonicalizeProjectKey first, the same as SetProjectGroup, so `group
// clear` on either form of a folder's key reverses whichever form `group
// set` actually stored under.
func (s *Store) ClearProjectGroup(identity Identity, projectKey string) error {
	if identity.Kind != IdentityHuman {
		return ErrProjectGroupRequiresHuman
	}
	projectKey = s.canonicalizeProjectKey(projectKey)
	if _, err := s.db.Exec(`DELETE FROM project_groups WHERE project_key = ?`, projectKey); err != nil {
		return fmt.Errorf("store: clear project group %s: %w", projectKey, err)
	}
	return nil
}

// GroupOf returns projectKey's current group and whether it is in one at
// all. projectKey is first resolved through s.canonicalizeProjectKey (task
// d65ef8ff): a caller querying by a folder's legacy plain key (This Week's
// own repoKey identity for pre-workspace session history) must find the
// group a human set on that folder's workspace-prefixed key, exactly the
// row Open's own sweep already merged the two into. It is read-only and
// takes no Identity: reading a project's group is not a human-only power,
// only setting or clearing one is.
func (s *Store) GroupOf(projectKey string) (string, bool, error) {
	projectKey = s.canonicalizeProjectKey(projectKey)
	var name string
	err := s.db.QueryRow(`SELECT group_name FROM project_groups WHERE project_key = ?`, projectKey).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: group of %s: %w", projectKey, err)
	}
	return name, true, nil
}

// ListGroups returns every project_groups row, ordered by group name then
// project key.
func (s *Store) ListGroups() ([]GroupMembership, error) {
	rows, err := s.db.Query(`SELECT project_key, group_name, set_at FROM project_groups
		ORDER BY group_name, project_key`)
	if err != nil {
		return nil, fmt.Errorf("store: list groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []GroupMembership
	for rows.Next() {
		var m GroupMembership
		var setAt int64
		if err := rows.Scan(&m.ProjectKey, &m.GroupName, &setAt); err != nil {
			return nil, fmt.Errorf("store: scan project group: %w", err)
		}
		m.SetAt = tsFromNanos(setAt)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list groups: %w", err)
	}
	return out, nil
}
