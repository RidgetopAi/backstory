package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RidgetopAi/backstory/internal/payload"
)

// migrationDataHooks maps a migration version to Go-side data
// transformation logic that must run inside that migration's own
// transaction, after its SQL statements and before schema_version records
// it. It exists for migration 3 (0003_printable_project_key_separator.sql's
// comment explains why): modernc.org/sqlite's length/substr/replace()
// built-ins silently truncate at an embedded NUL byte even though the
// underlying stored bytes round-trip correctly through plain
// SELECT/INSERT/UPDATE, so a NUL-separated project_key cannot be rewritten
// in pure SQL. Migration 6 (0006_tool_use_payload_shape.sql's comment
// explains why) reuses the same mechanism to rewrite pre-payload-contract
// tool.use rows, routing each row's `detail` value by its own `name` field —
// logic Go expresses far more legibly than SQLite's json1 functions would.
var migrationDataHooks = map[int]func(context.Context, *sql.Tx) error{
	3:  rewriteNulProjectKeySeparators,
	6:  rewriteToolUseDetailPayloads,
	10: installRecordsNoUpdateTrigger,
	13: installRecordsNoUpdateTrigger,
}

// recordsNoUpdateTriggerSQL is the one definition of records_no_update.
// Migration 0010 installs it (via its data hook) and the workspace sweep
// recreates it after its drop-rewrite-recreate of records.project_key, so
// the two can never drift. records is append-only except for the human
// delete path, which may, in one UPDATE, set tombstoned_at from NULL and
// scrub text to ”. Every other column — including event_cursor and
// git_head, next — is immutable (a tombstone scrub may also clear next). An UPDATE that changes neither text nor
// tombstoned_at is a harmless no-op; re-tombstoning is refused.
const recordsNoUpdateTriggerSQL = `
CREATE TRIGGER records_no_update
BEFORE UPDATE ON records
WHEN NOT (
  old.id           = new.id AND
  old.ts           = new.ts AND
  old.kind         = new.kind AND
  old.tier         = new.tier AND
  old.about        = new.about AND
  old.session_id   IS new.session_id AND
  old.project_key  IS new.project_key AND
  old.evidence     = new.evidence AND
  old.outcome      IS new.outcome AND
  old.promoter     IS new.promoter AND
  old.expires_at   IS new.expires_at AND
  old.event_cursor = new.event_cursor AND
  old.git_head     IS new.git_head AND
  (
    (old.text = new.text AND old.next IS new.next AND old.tombstoned_at IS new.tombstoned_at)
    OR
    (old.tombstoned_at IS NULL AND new.tombstoned_at IS NOT NULL AND
     (new.text = old.text OR new.text = '') AND
     (new.next IS old.next OR new.next IS NULL))
  )
)
BEGIN
  SELECT RAISE(ABORT, 'records: append-only, only tombstoned_at and a tombstone scrub of text may change');
END;`

// recordsNoUpdateTriggerV1SQL is records_no_update exactly as
// 0001_init.sql defines it, frozen. Migration 3's data hook drops the
// trigger to rewrite records.project_key and must recreate the schema as
// it stood at that version (event_cursor/git_head do not exist yet);
// migration 0005 and 0010 then replace it.
const recordsNoUpdateTriggerV1SQL = `
CREATE TRIGGER records_no_update
BEFORE UPDATE ON records
WHEN NOT (
  old.id          = new.id AND
  old.ts          = new.ts AND
  old.kind        = new.kind AND
  old.tier        = new.tier AND
  old.text        = new.text AND
  old.about       = new.about AND
  old.session_id  IS new.session_id AND
  old.project_key IS new.project_key AND
  old.evidence    = new.evidence AND
  old.outcome     IS new.outcome AND
  old.promoter    IS new.promoter AND
  old.expires_at  IS new.expires_at
)
BEGIN
  SELECT RAISE(ABORT, 'records: append-only, only tombstoned_at may change');
END;`

// installRecordsNoUpdateTrigger is migration 0010's data hook: the SQL file
// drops the old trigger, this creates the current one from the shared const.
func installRecordsNoUpdateTrigger(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, recordsNoUpdateTriggerSQL); err != nil {
		return fmt.Errorf("create records_no_update: %w", err)
	}
	return nil
}

// rewriteNulProjectKeySeparators rewrites every NUL-separated project_key
// in projects, sessions and records to the printable "|" separator
// internal/project.Key now produces (task e7951178, critic T1 on
// 14704ebe). records.project_key is guarded by the records_no_update
// append-only trigger, so it is dropped for the one UPDATE that rewrites
// project_key and recreated immediately after.
func rewriteNulProjectKeySeparators(ctx context.Context, tx *sql.Tx) error {
	if err := rewriteNulSeparatedColumn(ctx, tx,
		`SELECT rowid, key FROM projects WHERE key LIKE '%' || X'00' || '%'`,
		`UPDATE projects SET key = ? WHERE rowid = ?`); err != nil {
		return err
	}
	if err := rewriteNulSeparatedColumn(ctx, tx,
		`SELECT rowid, project_key FROM sessions WHERE project_key LIKE '%' || X'00' || '%'`,
		`UPDATE sessions SET project_key = ? WHERE rowid = ?`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER records_no_update`); err != nil {
		return fmt.Errorf("drop records_no_update: %w", err)
	}
	if err := rewriteNulSeparatedColumn(ctx, tx,
		`SELECT rowid, project_key FROM records WHERE project_key LIKE '%' || X'00' || '%'`,
		`UPDATE records SET project_key = ? WHERE rowid = ?`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, recordsNoUpdateTriggerV1SQL); err != nil {
		return fmt.Errorf("recreate records_no_update: %w", err)
	}
	return nil
}

// rewriteNulSeparatedColumn selects (rowid, value) via selectSQL, rewrites
// every embedded NUL byte in value to "|" in Go, and writes it back via
// updateSQL (which must take the new value then the rowid, in that order).
// The rewrite is done in Go, not SQL, because SQL string functions cannot
// be trusted with an embedded NUL on this driver (see migrationDataHooks).
func rewriteNulSeparatedColumn(ctx context.Context, tx *sql.Tx, selectSQL, updateSQL string) error {
	rows, err := tx.QueryContext(ctx, selectSQL)
	if err != nil {
		return fmt.Errorf("select for NUL-separator rewrite: %w", err)
	}
	type pendingRow struct {
		rowid int64
		value string
	}
	var pending []pendingRow
	for rows.Next() {
		var r pendingRow
		if err := rows.Scan(&r.rowid, &r.value); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan for NUL-separator rewrite: %w", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate for NUL-separator rewrite: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close rows for NUL-separator rewrite: %w", err)
	}

	for _, r := range pending {
		newValue := strings.ReplaceAll(r.value, "\x00", "|")
		if _, err := tx.ExecContext(ctx, updateSQL, newValue, r.rowid); err != nil {
			return fmt.Errorf("update for NUL-separator rewrite: %w", err)
		}
	}
	return nil
}

// timelineEventsNoUpdateTriggerSQL recreates timeline_events_no_update
// exactly as 0001_init.sql defines it. rewriteToolUseDetailPayloads drops
// the trigger to rewrite timeline_events.payload (the trigger refuses every
// UPDATE, unconditionally — timeline_events has no analog of records'
// tombstoned_at exception) and must recreate it identically before the
// migration commits.
const timelineEventsNoUpdateTriggerSQL = `
CREATE TRIGGER timeline_events_no_update
BEFORE UPDATE ON timeline_events
BEGIN
  SELECT RAISE(ABORT, 'timeline_events: append-only, no update');
END;`

// timelineEventsNoDeleteTriggerSQL is the one definition of
// timeline_events_no_delete, exactly as 0001_init.sql defines it. The human
// purge (PurgeSessions) drops it, deletes, and recreates this text inside
// one transaction.
const timelineEventsNoDeleteTriggerSQL = `
CREATE TRIGGER timeline_events_no_delete
BEFORE DELETE ON timeline_events
BEGIN
  SELECT RAISE(ABORT, 'timeline_events: append-only, no delete');
END;`

// toolUseFileFields mirrors internal/backfill/claude/tools.go's fileTools:
// the tool.use payload's pre-8ba5487a `detail` value becomes `path` for
// these tools, `command` for Bash, under this migration's routing. store
// cannot import internal/backfill/claude (that package imports store), so
// this is a manually kept-in-sync copy of the same four names, not a shared
// import — both express the one routing rule internal/payload's doc comment
// names.
var toolUseFileFields = map[string]bool{
	"Edit":      true,
	"Write":     true,
	"Read":      true,
	"MultiEdit": true,
}

// oldToolUsePayload is the pre-8ba5487a tool.use payload shape: one shared
// `detail` field for whichever string the tool carried, file path or shell
// command alike. It exists only to read rows this migration rewrites away,
// never to write one — payload.ToolUse is what every write, migrated or
// new, produces.
type oldToolUsePayload struct {
	ToolUseID string `json:"tool_use_id,omitempty"`
	Name      string `json:"name"`
	Detail    string `json:"detail,omitempty"`
}

// rewriteToolUseDetailPayloads rewrites every timeline_events row whose
// kind is tool.use and whose payload still carries the pre-8ba5487a
// `detail` key into the current payload.ToolUse shape: `detail` becomes
// `path` for the file-editing tools, `command` for Bash, and is dropped
// (matching current write-side behavior) for any other tool the old shape
// happened to tag with one. A row whose payload has no `detail` key —
// already migrated, or a tool outside both groups, whose old and new shapes
// are identical either way — matches nothing and is left byte-for-byte
// alone; a malformed payload is left alone too, never destroyed, since a
// row this rewrite cannot safely parse is still evidence the daemon
// recorded.
func rewriteToolUseDetailPayloads(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER timeline_events_no_update`); err != nil {
		return fmt.Errorf("drop timeline_events_no_update: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, payload FROM timeline_events WHERE kind = ?`, payload.KindToolUse)
	if err != nil {
		return fmt.Errorf("select tool.use rows for detail-shape rewrite: %w", err)
	}
	type pendingEvent struct {
		id      int64
		payload string
	}
	var pending []pendingEvent
	for rows.Next() {
		var r pendingEvent
		if err := rows.Scan(&r.id, &r.payload); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan tool.use row for detail-shape rewrite: %w", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate tool.use rows for detail-shape rewrite: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close tool.use rows for detail-shape rewrite: %w", err)
	}

	for _, r := range pending {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.payload), &probe); err != nil {
			continue
		}
		if _, hasDetail := probe["detail"]; !hasDetail {
			continue
		}
		var old oldToolUsePayload
		if err := json.Unmarshal([]byte(r.payload), &old); err != nil {
			continue
		}

		rewritten := payload.ToolUse{ToolUseID: old.ToolUseID, Name: old.Name}
		switch {
		case toolUseFileFields[old.Name]:
			rewritten.Path = old.Detail
		case old.Name == "Bash":
			rewritten.Command = old.Detail
		}

		newPayload, err := json.Marshal(rewritten)
		if err != nil {
			return fmt.Errorf("marshal rewritten tool.use payload for event %d: %w", r.id, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE timeline_events SET payload = ? WHERE id = ?`, string(newPayload), r.id); err != nil {
			return fmt.Errorf("update tool.use payload for event %d: %w", r.id, err)
		}
	}

	if _, err := tx.ExecContext(ctx, timelineEventsNoUpdateTriggerSQL); err != nil {
		return fmt.Errorf("recreate timeline_events_no_update: %w", err)
	}
	return nil
}
