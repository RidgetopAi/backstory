package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration is one embedded, numbered SQL file applied in order.
type migration struct {
	version int
	name    string
	sql     string
}

// SchemaVersion is the version of the latest embedded migration. Open
// applies every migration up to this version and records each one in
// schema_version; a store that already has them all is left untouched.
var SchemaVersion = latestMigrationVersion()

func latestMigrationVersion() int {
	ms, err := loadMigrations()
	if err != nil || len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

// loadMigrations reads every migrations/NNNN_*.sql file and returns them
// ordered by version. The version is the numeric prefix before the first
// underscore, so the filesystem's lexical sort already matches version
// order for any zero-padded width.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	out := make([]migration, 0, len(names))
	for _, name := range names {
		version, err := parseMigrationVersion(name)
		if err != nil {
			return nil, err
		}
		b, err := migrationFS.ReadFile(path.Join("migrations", name))
		if err != nil {
			return nil, fmt.Errorf("store: read migration %s: %w", name, err)
		}
		out = append(out, migration{version: version, name: name, sql: string(b)})
	}
	return out, nil
}

func parseMigrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("store: migration %q has no version prefix", name)
	}
	v, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("store: migration %q has a non-numeric version prefix: %w", name, err)
	}
	return v, nil
}

// migrate applies every embedded migration not yet recorded in
// schema_version, each in its own transaction, oldest first. It is safe to
// call on every Open: a fully-migrated store applies nothing.
func (s *Store) migrate() error {
	// applied_at is INTEGER (unix nanoseconds) here, matching the schema
	// migration 0002 converts every other database to: on a brand-new
	// database this bootstrap runs before any migration, so migration 0001's
	// own schema_version row must already land in the final column type.
	// IF NOT EXISTS makes this a no-op against a pre-0002 database still
	// carrying the original TEXT column; migration 0002 converts that one.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create schema_version: %w", err)
	}

	applied, err := s.appliedMigrations()
	if err != nil {
		return err
	}

	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appliedMigrations() (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT version FROM schema_version`)
	if err != nil {
		return nil, fmt.Errorf("store: read schema_version: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan schema_version: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: scan schema_version: %w", err)
	}
	return applied, nil
}

// applyMigration runs one migration's statements in a transaction, on a
// single dedicated connection with foreign-key enforcement suspended for
// the duration (SQLite's recommended procedure for schema-restructuring
// migrations — a migration may recreate tables out of FK dependency order,
// e.g. 0002_ts_integer.sql). PRAGMA foreign_key_check runs inside the
// transaction, before commit, so a migration that leaves a dangling
// reference fails loudly instead of silently corrupting the schema.
func (s *Store) applyMigration(m migration) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire connection for migration %s: %w", m.name, err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("store: disable foreign_keys for migration %s: %w", m.name, err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`) }()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration %s: %w", m.name, err)
	}
	for i, stmt := range splitStatements(m.sql) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: apply migration %s (statement %d): %w", m.name, i+1, err)
		}
	}
	if hook, ok := migrationDataHooks[m.version]; ok {
		if err := hook(ctx, tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: apply migration %s data hook: %w", m.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
		m.version, tsToNanos(time.Now())); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: record migration %s: %w", m.name, err)
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %s: %w", m.name, err)
	}
	return nil
}

// checkForeignKeys runs PRAGMA foreign_key_check and turns any reported row
// into an error. It must run inside the migration's own transaction, before
// foreign_keys is turned back on, so it sees the migration's final state.
func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var violations []string
	for rows.Next() {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("foreign_key_check: scan violation: %w", err)
		}
		violations = append(violations, fmt.Sprintf("%s(rowid=%v) -> %s", table, rowid, parent))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	if len(violations) > 0 {
		return fmt.Errorf("foreign_key_check found %d dangling reference(s): %s", len(violations), strings.Join(violations, "; "))
	}
	return nil
}

// splitStatements splits a migration file into individual SQL statements on
// top-level semicolons, treating a TRIGGER's BEGIN...END body as opaque so
// the semicolons inside it are not mistaken for statement boundaries. `--`
// line comments and '...' string literals are skipped while scanning so a
// semicolon or BEGIN/END inside either (e.g. in a RAISE() message) is never
// mistaken for real SQL structure.
func splitStatements(sqlText string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	upper := strings.ToUpper(sqlText)

	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]

		if c == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-' {
			end := strings.IndexByte(sqlText[i:], '\n')
			if end < 0 {
				cur.WriteString(sqlText[i:])
				break
			}
			cur.WriteString(sqlText[i : i+end])
			i += end - 1
			continue
		}
		if c == '\'' {
			cur.WriteByte(c)
			j := i + 1
			for j < len(sqlText) {
				cur.WriteByte(sqlText[j])
				if sqlText[j] == '\'' {
					// A doubled '' is an escaped quote inside the literal.
					if j+1 < len(sqlText) && sqlText[j+1] == '\'' {
						cur.WriteByte(sqlText[j+1])
						j += 2
						continue
					}
					break
				}
				j++
			}
			i = j
			continue
		}

		cur.WriteByte(c)
		switch {
		case c == ';' && depth == 0:
			out = append(out, cur.String())
			cur.Reset()
		case isWordBoundary(sqlText, i) && strings.HasPrefix(upper[i:], "BEGIN") && isWordEnd(upper, i+len("BEGIN")):
			depth++
		case isWordBoundary(sqlText, i) && strings.HasPrefix(upper[i:], "END") && isWordEnd(upper, i+len("END")):
			depth--
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

func isWordBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	prev := s[i-1]
	return prev != '_' && (prev < 'a' || prev > 'z') && (prev < 'A' || prev > 'Z') && (prev < '0' || prev > '9')
}

func isWordEnd(upper string, i int) bool {
	if i >= len(upper) {
		return true
	}
	c := upper[i]
	return c != '_' && (c < 'A' || c > 'Z') && (c < '0' || c > '9')
}
