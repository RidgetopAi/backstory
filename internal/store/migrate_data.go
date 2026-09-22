package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// migrationDataHooks maps a migration version to Go-side data
// transformation logic that must run inside that migration's own
// transaction, after its SQL statements and before schema_version records
// it. It exists for migration 3 (0003_printable_project_key_separator.sql's
// comment explains why): modernc.org/sqlite's length/substr/replace()
// built-ins silently truncate at an embedded NUL byte even though the
// underlying stored bytes round-trip correctly through plain
// SELECT/INSERT/UPDATE, so a NUL-separated project_key cannot be rewritten
// in pure SQL.
var migrationDataHooks = map[int]func(context.Context, *sql.Tx) error{
	3: rewriteNulProjectKeySeparators,
}

// recordsNoUpdateTriggerSQL recreates records_no_update exactly as
// 0001_init.sql defines it. rewriteNulProjectKeySeparators drops the
// trigger to rewrite records.project_key (its WHEN clause requires
// old.project_key IS new.project_key) and must recreate it identically
// before the migration commits.
const recordsNoUpdateTriggerSQL = `
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
	if _, err := tx.ExecContext(ctx, recordsNoUpdateTriggerSQL); err != nil {
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
