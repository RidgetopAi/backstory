package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
)

// ErrProjectLinkRequiresHuman is returned when SetProjectLink or
// ClearProjectLink is called with an Identity that is not IdentityHuman. A
// link decides which projects on different machines are "the same", so like
// grouping it is a human-only power and is never reachable over the socket.
var ErrProjectLinkRequiresHuman = errors.New("store: project link write requires a human identity")

// SyncSource says where a project's sync identity came from.
type SyncSource string

const (
	// SyncSourceNone: the project has no sync identity.
	SyncSourceNone SyncSource = ""
	// SyncSourceRemote: derived from the normalized projects.remote_url.
	SyncSourceRemote SyncSource = "remote"
	// SyncSourceLink: set by a human in project_links; overrides the remote.
	SyncSourceLink SyncSource = "link"
	// SyncSourceSynced: a placeholder project ("sync:<id>") made by an
	// import; its identity is the id in its key.
	SyncSourceSynced SyncSource = "synced"
)

// SyncIdentity is a project's machine-independent identity.
type SyncIdentity struct {
	ProjectKey string
	ID         string // "" when Source is SyncSourceNone
	Source     SyncSource
}

// SetProjectLink gives projectKey the human-chosen sync identity syncID,
// replacing any earlier link. It refuses any identity other than
// IdentityHuman and leaves the table unchanged. The project must exist.
func (s *Store) SetProjectLink(identity Identity, syncID, projectKey string) error {
	if identity.Kind != IdentityHuman {
		return ErrProjectLinkRequiresHuman
	}
	syncID = strings.TrimSpace(syncID)
	if syncID == "" {
		return fmt.Errorf("store: set project link: sync id required")
	}
	if projectKey == "" {
		return fmt.Errorf("store: set project link: project key required")
	}
	projectKey = s.canonicalizeProjectKey(projectKey)
	if _, found, err := s.GetProject(projectKey); err != nil {
		return err
	} else if !found {
		return fmt.Errorf("store: set project link: unknown project %q", projectKey)
	}
	before, err := s.SyncIdentityOf(projectKey)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO project_links (local_key, sync_id, set_at)
		VALUES (?, ?, ?)
		ON CONFLICT(local_key) DO UPDATE SET
			sync_id = excluded.sync_id,
			set_at  = excluded.set_at`,
		projectKey, syncID, tsToNanos(time.Now()))
	if err != nil {
		return fmt.Errorf("store: set project link %s -> %s: %w", projectKey, syncID, err)
	}
	return s.afterSyncIdentityChange(projectKey, before)
}

// ClearProjectLink removes projectKey's link; clearing an unlinked project is
// a no-op. It refuses any identity other than IdentityHuman.
func (s *Store) ClearProjectLink(identity Identity, projectKey string) error {
	if identity.Kind != IdentityHuman {
		return ErrProjectLinkRequiresHuman
	}
	projectKey = s.canonicalizeProjectKey(projectKey)
	if _, err := s.db.Exec(`DELETE FROM project_links WHERE local_key = ?`, projectKey); err != nil {
		return fmt.Errorf("store: clear project link %s: %w", projectKey, err)
	}
	return nil
}

// SyncIdentityOf returns projectKey's sync identity: its link if one is set,
// else its normalized remote, else the id in a "sync:<id>" placeholder key,
// else none. A project that does not exist has
// none.
func (s *Store) SyncIdentityOf(projectKey string) (SyncIdentity, error) {
	projectKey = s.canonicalizeProjectKey(projectKey)
	var link, remote sql.NullString
	err := s.db.QueryRow(`SELECT l.sync_id, p.remote_url FROM projects p
		LEFT JOIN project_links l ON l.local_key = p.key WHERE p.key = ?`, projectKey).Scan(&link, &remote)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncIdentity{ProjectKey: projectKey}, nil
	}
	if err != nil {
		return SyncIdentity{}, fmt.Errorf("store: sync identity of %s: %w", projectKey, err)
	}
	return resolveSyncIdentity(projectKey, link, remote), nil
}

// ListSyncIdentities returns every project with its sync identity, ordered
// by project key.
func (s *Store) ListSyncIdentities() ([]SyncIdentity, error) {
	rows, err := s.db.Query(`SELECT p.key, l.sync_id, p.remote_url FROM projects p
		LEFT JOIN project_links l ON l.local_key = p.key ORDER BY p.key`)
	if err != nil {
		return nil, fmt.Errorf("store: list sync identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []SyncIdentity{}
	for rows.Next() {
		var key string
		var link, remote sql.NullString
		if err := rows.Scan(&key, &link, &remote); err != nil {
			return nil, fmt.Errorf("store: scan sync identity: %w", err)
		}
		out = append(out, resolveSyncIdentity(key, link, remote))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sync identities: %w", err)
	}
	return out, nil
}

func resolveSyncIdentity(key string, link, remote sql.NullString) SyncIdentity {
	if link.Valid && link.String != "" {
		return SyncIdentity{ProjectKey: key, ID: link.String, Source: SyncSourceLink}
	}
	if remote.Valid {
		if id, ok := project.NormalizeRemote(remote.String); ok {
			return SyncIdentity{ProjectKey: key, ID: id, Source: SyncSourceRemote}
		}
	}
	if id := strings.TrimPrefix(key, SyncPlaceholderPrefix); id != key && id != "" {
		return SyncIdentity{ProjectKey: key, ID: id, Source: SyncSourceSynced}
	}
	return SyncIdentity{ProjectKey: key}
}
