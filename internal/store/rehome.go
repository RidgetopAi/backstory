package store

import (
	"fmt"
	"log"
	"time"
)

// rehomeHandoffWhere selects the handoffs the home migration moves (decision
// dc0a4387): a live (not tombstoned) kind=handoff record filed under a
// workspace key whose writing session is filed under a real repo key — not a
// workspace key and not a sync: placeholder. Tombstoned handoffs are left
// alone: their text is gone and they never reached a peer, so there is
// nothing to sync and nothing to count.
const rehomeHandoffWhere = `r.kind = 'handoff' AND r.tombstoned_at IS NULL
	AND r.project_key LIKE 'workspace:%'
	AND s.project_key IS NOT NULL AND s.project_key <> ''
	AND s.project_key NOT LIKE 'workspace:%' AND s.project_key NOT LIKE 'sync:%'`

// rehomeHandoffs moves every workspace-homed handoff written by a repo-keyed
// session onto that repo's key, so it syncs under the repo's identity and
// counts on the repo's row. It backs the store up first (VACUUM INTO, the
// sweep pattern), then in one transaction drops records_no_update, rewrites
// project_key, recreates the trigger from its one definition, and queues a
// sync_changes row per moved record. Idempotent: once moved, a handoff no
// longer matches, so a later Open writes neither a backup nor a row.
func (s *Store) rehomeHandoffs() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM records r JOIN sessions s ON s.id = r.session_id WHERE `+rehomeHandoffWhere).Scan(&n); err != nil {
		return fmt.Errorf("store: rehome handoffs: count: %w", err)
	}
	if n == 0 {
		return nil
	}

	backupPath := s.path + ".pre-rehome-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if _, err := s.db.Exec(`VACUUM INTO ?`, backupPath); err != nil {
		return fmt.Errorf("store: rehome handoffs: backup to %s: %w", backupPath, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: rehome handoffs: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`CREATE TEMP TABLE rehome_ids AS
		SELECT r.id AS id, s.project_key AS new_key FROM records r JOIN sessions s ON s.id = r.session_id WHERE ` + rehomeHandoffWhere); err != nil {
		return fmt.Errorf("store: rehome handoffs: select: %w", err)
	}

	// records.project_key foreign-keys into projects(key); the session's own
	// row normally exists already, so this only guards a hand-edited store.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO projects (key, toplevel, first_seen)
		SELECT DISTINCT p.new_key, p.new_key, ? FROM rehome_ids p`, tsToNanos(time.Now())); err != nil {
		return fmt.Errorf("store: rehome handoffs: ensure projects: %w", err)
	}
	if _, err := tx.Exec(`DROP TRIGGER records_no_update`); err != nil {
		return fmt.Errorf("store: rehome handoffs: drop records_no_update: %w", err)
	}
	if _, err := tx.Exec(`UPDATE records SET project_key = (SELECT new_key FROM rehome_ids WHERE id = records.id)
		WHERE id IN (SELECT id FROM rehome_ids)`); err != nil {
		return fmt.Errorf("store: rehome handoffs: update: %w", err)
	}
	if _, err := tx.Exec(recordsNoUpdateTriggerSQL); err != nil {
		return fmt.Errorf("store: rehome handoffs: recreate records_no_update: %w", err)
	}
	// Queue each moved record for paired machines. A ref keeps one entry: the
	// older copies go after the insert (the feed only lets an entry be deleted
	// once a newer one exists).
	if _, err := tx.Exec(`INSERT INTO sync_changes (kind, ref) SELECT 'record', id FROM rehome_ids ORDER BY id`); err != nil {
		return fmt.Errorf("store: rehome handoffs: queue sync changes: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM sync_changes WHERE kind IN ('record','tombstone','session')
		AND ref IN (SELECT id FROM rehome_ids)
		AND seq < (SELECT MAX(n.seq) FROM sync_changes n WHERE n.ref = sync_changes.ref AND n.kind IN ('record','tombstone','session'))`); err != nil {
		return fmt.Errorf("store: rehome handoffs: supersede sync changes: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE temp.rehome_ids`); err != nil {
		return fmt.Errorf("store: rehome handoffs: drop scratch table: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: rehome handoffs: commit: %w", err)
	}
	log.Printf("store: rehome handoffs: moved %d workspace-homed handoff(s) onto their repo key (backup %s, run by %s)", n, backupPath, processName())
	return nil
}
