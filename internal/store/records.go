package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RecordKind is the ledger record kind (SCHEMA.md records.kind). punch,
// stage and claim are reserved for PLAN.md §Phase 6; nothing writes them
// yet.
type RecordKind string

const (
	KindDecision RecordKind = "decision"
	KindOutcome  RecordKind = "outcome"
	KindHandoff  RecordKind = "handoff"
	KindNote     RecordKind = "note"
	KindClaim    RecordKind = "claim"
	KindPunch    RecordKind = "punch"
	KindStage    RecordKind = "stage"
	KindConfirm  RecordKind = "confirm"
)

// Tier is records.tier. It is never a caller-supplied parameter: InsertRecord
// derives it from the caller's Identity (see identity.go).
type Tier string

const (
	TierHumanDeclared Tier = "human-declared"
	TierAgentDeclared Tier = "agent-declared"
	TierInferred      Tier = "inferred"
)

// Outcome is the three-state value of a kind=outcome record
// (AGENT-CONTRACT.md §Outcomes are three-state). CouldNotObserve is its own
// value, never folded into False.
type Outcome string

const (
	OutcomeTrue            Outcome = "true"
	OutcomeFalse           Outcome = "false"
	OutcomeCouldNotObserve Outcome = "could-not-observe"
)

// ErrTombstoneRequiresHuman is returned when TombstoneRecord is called with
// an Identity that is not IdentityHuman. Delete is a human-only power
// (AGENT-CONTRACT.md §User-only powers).
var ErrTombstoneRequiresHuman = errors.New("store: tombstone requires a human identity")

// InsertRecordParams is the input to InsertRecord. There is deliberately no
// Tier field: tier is derived from Identity.Kind, never accepted directly.
type InsertRecordParams struct {
	Identity   Identity
	Kind       RecordKind
	Text       string
	About      []string
	SessionID  string
	ProjectKey string
	Evidence   []int64
	Outcome    *Outcome
	Promoter   string
	ExpiresAt  *time.Time
}

// Record is a row read back from records.
type Record struct {
	ID           string
	TS           time.Time
	Kind         RecordKind
	Tier         Tier
	Text         string
	About        []string
	SessionID    string
	ProjectKey   string
	Evidence     []int64
	Outcome      *Outcome
	Promoter     string
	ExpiresAt    *time.Time
	TombstonedAt *time.Time
	// EventCursor is the record's position in the timeline: MAX(timeline_
	// events.id) at insert time, or 0 when the timeline was empty. It is
	// never a caller-supplied parameter (there is deliberately no field for
	// it on InsertRecordParams) — InsertRecordWithEdges computes it in the
	// same transaction as the insert, the way tier is derived rather than
	// accepted (SCHEMA.md invariant 2). The SessionStart delta's boundary is
	// EventsSinceID(handoff.EventCursor), never a ts comparison (SCHEMA.md
	// invariant 10: backfilled sessions carry file clocks that lie).
	EventCursor int64
}

// InsertRecord appends a ledger record with no edges. It is a convenience
// wrapper: InsertRecordWithEdges(p, nil).
func (s *Store) InsertRecord(p InsertRecordParams) (string, error) {
	return s.InsertRecordWithEdges(p, nil)
}

// InsertRecordWithEdges appends a ledger record and every edge in edges,
// all in ONE transaction: if any edge names a target record id that does
// not exist, nothing is inserted — not the record, not any edge (critic T1
// on 14704ebe, task e7951178: note.go used to insert the record, then link
// a supersedes edge as a separate write, leaving the record persisted on
// an edge failure). Text is redacted before storage (redact.go); the
// record's tier comes from p.Identity, never from a parameter; a write
// exceeding a named cap (limits.go) inserts nothing and returns a
// *CapError.
func (s *Store) InsertRecordWithEdges(p InsertRecordParams, edges []EdgeSpec) (string, error) {
	tier, err := p.Identity.Kind.tier()
	if err != nil {
		return "", err
	}
	if p.Outcome != nil && p.Kind != KindOutcome {
		return "", fmt.Errorf("store: outcome is only valid on kind=%s records", KindOutcome)
	}
	if len(p.Text) > MaxRecordTextBytes {
		return "", &CapError{Cap: CapMaxTextBytes, Limit: MaxRecordTextBytes}
	}
	if p.SessionID != "" {
		n, err := s.recentRecordCount(p.SessionID, time.Now().Add(-time.Minute))
		if err != nil {
			return "", err
		}
		if n >= MaxRecordsPerSessionPerMinute {
			return "", &CapError{Cap: CapRecordsPerMinute, Limit: MaxRecordsPerSessionPerMinute}
		}
	}

	about, err := marshalStrings(p.About)
	if err != nil {
		return "", err
	}
	evidence, err := marshalInt64s(p.Evidence)
	if err != nil {
		return "", err
	}

	var outcome any
	if p.Outcome != nil {
		outcome = string(*p.Outcome)
	}

	id := uuid.NewString()

	tx, err := s.db.Begin()
	if err != nil {
		return "", fmt.Errorf("store: begin insert record: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has run

	if _, err := tx.Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at, event_cursor)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(id), 0) FROM timeline_events))`,
		id, tsToNanos(time.Now()), string(p.Kind), string(tier), redact(p.Text), about,
		nullable(p.SessionID), nullable(p.ProjectKey), evidence, outcome, nullable(p.Promoter), nullableTS(p.ExpiresAt)); err != nil {
		return "", fmt.Errorf("store: insert record: %w", err)
	}

	for _, e := range edges {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM records WHERE id = ?`, e.OtherID).Scan(&exists); err != nil {
			return "", fmt.Errorf("store: check edge target %s: %w", e.OtherID, err)
		}
		if exists == 0 {
			return "", &UnknownEdgeTargetError{Field: e.Field, ID: e.OtherID}
		}
		fromID, toID := id, e.OtherID
		if e.Incoming {
			fromID, toID = e.OtherID, id
		}
		if _, err := tx.Exec(`INSERT INTO edges (from_id, to_id, type, declared_by) VALUES (?, ?, ?, ?)`,
			fromID, toID, string(e.Type), e.DeclaredBy); err != nil {
			return "", fmt.Errorf("store: link edge %s->%s (%s): %w", fromID, toID, e.Type, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: commit insert record: %w", err)
	}
	return id, nil
}

func (s *Store) recentRecordCount(sessionID string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM records WHERE session_id = ? AND ts >= ?`,
		sessionID, tsToNanos(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count recent records: %w", err)
	}
	return n, nil
}

// GetRecord reads back a single record by id, including a tombstoned one
// (recall omits its text elsewhere; the store itself still has the row).
func (s *Store) GetRecord(id string) (Record, error) {
	var (
		r                               Record
		ts                              int64
		kind, tier, text, about, evid   string
		sessionID, projectKey, promoter sql.NullString
		outcome                         sql.NullString
		expiresAt, tombstonedAt         sql.NullInt64
	)
	err := s.db.QueryRow(`SELECT id, ts, kind, tier, text, about, session_id, project_key,
		evidence, outcome, promoter, expires_at, tombstoned_at, event_cursor FROM records WHERE id = ?`, id).
		Scan(&r.ID, &ts, &kind, &tier, &text, &about, &sessionID, &projectKey,
			&evid, &outcome, &promoter, &expiresAt, &tombstonedAt, &r.EventCursor)
	if err != nil {
		return Record{}, fmt.Errorf("store: get record %s: %w", id, err)
	}

	r.TS = tsFromNanos(ts)
	r.Kind = RecordKind(kind)
	r.Tier = Tier(tier)
	r.Text = text
	if err := json.Unmarshal([]byte(about), &r.About); err != nil {
		return Record{}, fmt.Errorf("store: parse record %s about: %w", id, err)
	}
	r.SessionID = sessionID.String
	r.ProjectKey = projectKey.String
	if err := json.Unmarshal([]byte(evid), &r.Evidence); err != nil {
		return Record{}, fmt.Errorf("store: parse record %s evidence: %w", id, err)
	}
	if outcome.Valid {
		o := Outcome(outcome.String)
		r.Outcome = &o
	}
	r.Promoter = promoter.String
	if expiresAt.Valid {
		t := tsFromNanos(expiresAt.Int64)
		r.ExpiresAt = &t
	}
	if tombstonedAt.Valid {
		t := tsFromNanos(tombstonedAt.Int64)
		r.TombstonedAt = &t
	}
	return r, nil
}

// LatestRecord returns the most recently inserted non-tombstoned record of
// kind in projectKey, ordered by rowid (SCHEMA.md invariant 10's "order by
// sequence, never ts" applies here too: ts is wall-clock and informational,
// insertion order is not). found is false when no such record exists.
func (s *Store) LatestRecord(projectKey string, kind RecordKind) (Record, bool, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM records
		WHERE project_key = ? AND kind = ? AND tombstoned_at IS NULL
		ORDER BY rowid DESC LIMIT 1`, projectKey, string(kind)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("store: latest %s record for project %s: %w", kind, projectKey, err)
	}
	rec, err := s.GetRecord(id)
	if err != nil {
		return Record{}, false, err
	}
	return rec, true, nil
}

// RecordsForProject returns every non-tombstoned record of kind in
// projectKey, newest first (rowid DESC — insertion order, never ts;
// SCHEMA.md invariant 10 applies to record ordering the same way it applies
// to timeline_events), capped at limit rows. It is recall's decisions
// query: unlike LatestRecord (one row), a caller wants every declared
// decision for the project, most recent first.
func (s *Store) RecordsForProject(projectKey string, kind RecordKind, limit int) ([]Record, error) {
	rows, err := s.db.Query(`SELECT id FROM records
		WHERE project_key = ? AND kind = ? AND tombstoned_at IS NULL
		ORDER BY rowid DESC LIMIT ?`, projectKey, string(kind), limit)
	if err != nil {
		return nil, fmt.Errorf("store: records for project %s kind %s: %w", projectKey, kind, err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, fmt.Errorf("store: records for project %s kind %s: %w", projectKey, kind, err)
	}
	return s.getRecords(ids)
}

// UnconfirmedDraftCount counts inferred-tier records in projectKey with no
// promoter and no expiry that has already passed — the SessionStart block's
// attention slot (AGENT-CONTRACT.md §The SessionStart block).
func (s *Store) UnconfirmedDraftCount(projectKey string, now time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM records
		WHERE project_key = ? AND tier = ? AND promoter IS NULL
		AND (expires_at IS NULL OR expires_at > ?)
		AND tombstoned_at IS NULL`,
		projectKey, string(TierInferred), tsToNanos(now)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: unconfirmed draft count for project %s: %w", projectKey, err)
	}
	return n, nil
}

// TombstoneRecord sets records.tombstoned_at and nothing else — the sole
// mutable column, and the sole human-only power (AGENT-CONTRACT.md
// §User-only powers). It refuses any identity other than IdentityHuman.
func (s *Store) TombstoneRecord(id string, identity Identity) error {
	if identity.Kind != IdentityHuman {
		return ErrTombstoneRequiresHuman
	}
	res, err := s.db.Exec(`UPDATE records SET tombstoned_at = ? WHERE id = ?`,
		tsToNanos(time.Now()), id)
	if err != nil {
		return fmt.Errorf("store: tombstone record %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: tombstone record %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: tombstone record %s: not found", id)
	}
	return nil
}

// RecordsForProjectAll returns EVERY record for projectKey — every kind,
// and including tombstoned ones (unlike RecordsForProject, which is
// kind-scoped and excludes them) — newest first by sequence (rowid DESC;
// SCHEMA.md invariant 10: order by sequence, never ts), capped at limit
// rows. It is internal/recall's project-anchor query: a project anchor's
// narrative is the project's whole ledger, and a tombstoned record must
// still surface (recall omits its text, keeps its edges) rather than
// vanish the way it does for RecordsForProject's kind-scoped callers.
func (s *Store) RecordsForProjectAll(projectKey string, limit int) ([]Record, error) {
	rows, err := s.db.Query(`SELECT id FROM records
		WHERE project_key = ?
		ORDER BY rowid DESC LIMIT ?`, projectKey, limit)
	if err != nil {
		return nil, fmt.Errorf("store: all records for project %s: %w", projectKey, err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, fmt.Errorf("store: all records for project %s: %w", projectKey, err)
	}
	return s.getRecords(ids)
}

// RecordsByIDs returns the records named by ids, newest first by sequence
// (rowid DESC; SCHEMA.md invariant 10), including tombstoned ones. An id
// with no matching record is silently skipped rather than erroring: a
// caller resolving an edge walk over a set of ids expects to get back
// whichever of them still exist, not a failure over one stale reference.
func (s *Store) RecordsByIDs(ids []string) ([]Record, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `SELECT id FROM records WHERE id IN (` + strings.Join(placeholders, ",") + `) ORDER BY rowid DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: records by ids: %w", err)
	}
	resolved, err := scanIDs(rows)
	if err != nil {
		return nil, fmt.Errorf("store: records by ids: %w", err)
	}
	return s.getRecords(resolved)
}

// FindRecordByIDPrefix resolves idOrPrefix to a record: an exact id match
// wins outright, even if idOrPrefix also happens to prefix other ids.
// Otherwise idOrPrefix must prefix EXACTLY one record's id to resolve —
// zero matches and multiple (ambiguous) matches both report found=false,
// the same honest non-answer, so an ambiguous short prefix never silently
// guesses which record was meant.
func (s *Store) FindRecordByIDPrefix(idOrPrefix string) (Record, bool, error) {
	if idOrPrefix == "" {
		return Record{}, false, nil
	}
	if rec, err := s.GetRecord(idOrPrefix); err == nil {
		return rec, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, fmt.Errorf("store: find record by prefix %s: %w", idOrPrefix, err)
	}

	rows, err := s.db.Query(`SELECT id FROM records WHERE substr(id, 1, ?) = ?`, len(idOrPrefix), idOrPrefix)
	if err != nil {
		return Record{}, false, fmt.Errorf("store: find record by prefix %s: %w", idOrPrefix, err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return Record{}, false, fmt.Errorf("store: find record by prefix %s: %w", idOrPrefix, err)
	}
	if len(ids) != 1 {
		return Record{}, false, nil
	}
	rec, err := s.GetRecord(ids[0])
	if err != nil {
		return Record{}, false, fmt.Errorf("store: find record by prefix %s: %w", idOrPrefix, err)
	}
	return rec, true, nil
}

// getRecords loads each id in order via GetRecord, preserving ids' order.
func (s *Store) getRecords(ids []string) ([]Record, error) {
	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		rec, err := s.GetRecord(id)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// scanIDs drains a `SELECT id FROM ...` rows into a slice, closing rows
// itself (success or failure) so every caller does not repeat the same
// scan/close/err bookkeeping.
func scanIDs(rows *sql.Rows) ([]string, error) {
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("scan ids: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("scan ids: %w", err)
	}
	return ids, nil
}

func marshalStrings(v []string) (string, error) {
	if v == nil {
		v = []string{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("store: marshal about: %w", err)
	}
	return string(b), nil
}

func marshalInt64s(v []int64) (string, error) {
	if v == nil {
		v = []int64{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("store: marshal evidence: %w", err)
	}
	return string(b), nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
