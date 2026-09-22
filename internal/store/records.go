package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
}

// InsertRecord appends a ledger record. Text is redacted before storage
// (redact.go); the record's tier comes from p.Identity, never from a
// parameter; a write exceeding a named cap (limits.go) inserts nothing and
// returns a *CapError.
func (s *Store) InsertRecord(p InsertRecordParams) (string, error) {
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
	_, err = s.db.Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, tsToNanos(time.Now()), string(p.Kind), string(tier), redact(p.Text), about,
		nullable(p.SessionID), nullable(p.ProjectKey), evidence, outcome, nullable(p.Promoter), nullableTS(p.ExpiresAt))
	if err != nil {
		return "", fmt.Errorf("store: insert record: %w", err)
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
		evidence, outcome, promoter, expires_at, tombstoned_at FROM records WHERE id = ?`, id).
		Scan(&r.ID, &ts, &kind, &tier, &text, &about, &sessionID, &projectKey,
			&evid, &outcome, &promoter, &expiresAt, &tombstonedAt)
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
