package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sync change kinds, as written to sync_changes and carried on the wire.
const (
	SyncKindRecord    = "record"
	SyncKindTombstone = "tombstone"
	SyncKindEdge      = "edge"
	SyncKindSession   = "session"
)

// SyncPlaceholderPrefix prefixes the project key of a placeholder project: a
// record imported for a project this store has no local project for lands
// under "sync:<sync_id>" until a local project gains that identity.
const SyncPlaceholderPrefix = "sync:"

// ErrSyncImportRequiresHuman is returned by ImportSyncChanges for any Identity
// other than IdentityHuman. Importing a peer's records writes other people's
// words into the ledger, so it is a human-run, CLI-only power: the socket API
// has no import.
var ErrSyncImportRequiresHuman = errors.New("store: sync import requires a human identity")

// SyncRecord is a record as it crosses the wire. It carries no evidence (the
// ids name this device's raw timeline, which is never served) and, when
// TombstonedAt is set, no text, no next step and no about, ever.
type SyncRecord struct {
	ID            string   `json:"id"`
	TS            int64    `json:"ts_ns"`
	Kind          string   `json:"kind"`
	Tier          string   `json:"tier"`
	Text          string   `json:"text"`
	About         []string `json:"about"`
	SessionID     string   `json:"session_id,omitempty"`
	ProjectSyncID string   `json:"project_sync_id"`
	Outcome       string   `json:"outcome,omitempty"`
	Promoter      string   `json:"promoter,omitempty"`
	ExpiresAt     *int64   `json:"expires_at_ns,omitempty"`
	TombstonedAt  *int64   `json:"tombstoned_at_ns,omitempty"`
	GitHead       string   `json:"git_head,omitempty"`
	Next          string   `json:"next,omitempty"`
	OriginDevice  string   `json:"origin_device"`
}

// SyncEdge is an edge as it crosses the wire.
type SyncEdge struct {
	FromID     string `json:"from_id"`
	ToID       string `json:"to_id"`
	Type       string `json:"type"`
	DeclaredBy string `json:"declared_by"`
}

// SyncSession is a session stub: no cwd, no pid, no harness id.
type SyncSession struct {
	ID           string `json:"id"`
	Agent        string `json:"agent"`
	OriginDevice string `json:"origin_device"`
	StartedAt    int64  `json:"started_at_ns"`
	EndedAt      *int64 `json:"ended_at_ns,omitempty"`
	// PurgedAt is set once the session was purged on its holder; it only ever
	// goes from unset to set, on every device.
	PurgedAt *int64 `json:"purged_at_ns,omitempty"`
}

// SyncChange is one change on the wire; exactly one of Record, Edge, Session
// is set, matching Kind (a tombstone carries its Record).
type SyncChange struct {
	Kind    string       `json:"kind"`
	Record  *SyncRecord  `json:"record,omitempty"`
	Edge    *SyncEdge    `json:"edge,omitempty"`
	Session *SyncSession `json:"session,omitempty"`
}

// SyncPeer names the device a batch of changes came from.
type SyncPeer struct {
	DeviceID string
}

// SyncImportResult counts what an import wrote. Paused means capture is off
// on this store and NOTHING was written.
type SyncImportResult struct {
	Paused         bool `json:"paused"`
	Records        int  `json:"records"`
	Tombstones     int  `json:"tombstones"`
	Edges          int  `json:"edges"`
	Sessions       int  `json:"sessions"`
	EdgesDeferred  int  `json:"edges_deferred"`
	SkippedNoMatch int  `json:"skipped_no_identity"`
}

// Imported is the number of rows the import wrote or changed.
func (r SyncImportResult) Imported() int { return r.Records + r.Tombstones + r.Edges + r.Sessions }

// SyncMaxSeq is the highest sequence in the change feed (0 when empty).
func (s *Store) SyncMaxSeq() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM sync_changes`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: sync max seq: %w", err)
	}
	return n, nil
}

type feedEntry struct {
	seq       int64
	kind, ref string
}

// ChangesSince returns the changes after cursor since, at most limit feed
// entries' worth. next is the cursor to pull from afterwards: it advances past
// entries that were not served (a record whose project has no sync identity),
// so a cursor is a position in the feed, never a timestamp. more reports that
// the feed holds entries beyond next. What is served is read from the rows as
// they are NOW, so a record tombstoned after its insert crosses once, as a
// tombstone. Tombstoned records carry no text; raw timeline events are never
// served.
func (s *Store) ChangesSince(since int64, limit int) (changes []SyncChange, next int64, more bool, err error) {
	if limit <= 0 {
		return nil, since, false, fmt.Errorf("store: changes since: limit must be positive")
	}
	rows, err := s.db.Query(`SELECT seq, kind, ref FROM sync_changes WHERE seq > ? ORDER BY seq LIMIT ?`, since, limit)
	if err != nil {
		return nil, since, false, fmt.Errorf("store: changes since %d: %w", since, err)
	}
	var entries []feedEntry
	for rows.Next() {
		var e feedEntry
		if err := rows.Scan(&e.seq, &e.kind, &e.ref); err != nil {
			_ = rows.Close()
			return nil, since, false, fmt.Errorf("store: scan change: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Close(); err != nil {
		return nil, since, false, err
	}
	if err := rows.Err(); err != nil {
		return nil, since, false, err
	}
	next = since
	if len(entries) > 0 {
		next = entries[len(entries)-1].seq
	}
	self, err := s.SelfDevice()
	if err != nil {
		return nil, since, false, err
	}

	b := &feedBuilder{s: s, self: self.ID, records: map[string]*SyncRecord{}, emitted: map[string]bool{}, sessions: map[string]bool{},
		edges: map[string]bool{}}
	for _, e := range entries {
		if err := b.add(e); err != nil {
			return nil, since, false, err
		}
	}
	changes = append(changes, b.sessionOut...)
	changes = append(changes, b.recordOut...)
	changes = append(changes, b.edgeOut...)

	max, err := s.SyncMaxSeq()
	if err != nil {
		return nil, since, false, err
	}
	return changes, next, max > next, nil
}

// feedBuilder assembles one pull's changes, deduplicating by row.
type feedBuilder struct {
	s       *Store
	self    string
	records map[string]*SyncRecord // id -> served record (nil: known unservable)
	emitted map[string]bool        // record ids already in recordOut
	// sessions/edges: already emitted this pull.
	sessions   map[string]bool
	edges      map[string]bool
	sessionOut []SyncChange
	recordOut  []SyncChange
	edgeOut    []SyncChange
}

func (b *feedBuilder) add(e feedEntry) error {
	switch e.kind {
	case SyncKindRecord, SyncKindTombstone:
		return b.addRecord(e.ref)
	case SyncKindSession:
		return b.addSession(e.ref)
	case SyncKindEdge:
		from, to, typ, ok := splitEdgeRef(e.ref)
		if !ok {
			return fmt.Errorf("store: malformed edge ref %q", e.ref)
		}
		return b.addEdge(from, to, typ)
	default:
		return fmt.Errorf("store: unknown change kind %q", e.kind)
	}
}

func splitEdgeRef(ref string) (from, to, typ string, ok bool) {
	parts := strings.Split(ref, "|")
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// load reads record id for serving; nil means not servable (absent, or its
// project has no sync identity: never guess one).
func (b *feedBuilder) load(id string) (*SyncRecord, error) {
	if r, ok := b.records[id]; ok {
		return r, nil
	}
	r, err := b.s.loadSyncRecord(id, b.self)
	if err != nil {
		return nil, err
	}
	b.records[id] = r
	return r, nil
}

func (b *feedBuilder) addRecord(id string) error {
	if b.emitted[id] {
		return nil
	}
	r, err := b.load(id)
	if err != nil || r == nil {
		return err
	}
	if r.SessionID != "" {
		if err := b.addSession(r.SessionID); err != nil {
			return err
		}
	}
	b.emitted[id] = true
	kind := SyncKindRecord
	if r.TombstonedAt != nil {
		kind = SyncKindTombstone
	}
	b.recordOut = append(b.recordOut, SyncChange{Kind: kind, Record: r})
	// A record carries the edges touching it whose other end is servable, so
	// an edge whose far end had not arrived yet is re-sent with that end.
	edges, err := b.s.EdgesTouching(id)
	if err != nil {
		return err
	}
	for _, e := range edges {
		if err := b.addEdge(e.FromID, e.ToID, string(e.Type)); err != nil {
			return err
		}
	}
	return nil
}

func (b *feedBuilder) addEdge(from, to, typ string) error {
	key := from + "|" + to + "|" + typ
	if b.edges[key] {
		return nil
	}
	fr, err := b.load(from)
	if err != nil || fr == nil {
		return err
	}
	tr, err := b.load(to)
	if err != nil || tr == nil {
		return err
	}
	var declaredBy string
	err = b.s.db.QueryRow(`SELECT declared_by FROM edges WHERE from_id = ? AND to_id = ? AND type = ?`, from, to, typ).Scan(&declaredBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: load edge %s: %w", key, err)
	}
	b.edges[key] = true
	b.edgeOut = append(b.edgeOut, SyncChange{Kind: SyncKindEdge, Edge: &SyncEdge{FromID: from, ToID: to, Type: typ, DeclaredBy: declaredBy}})
	return nil
}

func (b *feedBuilder) addSession(id string) error {
	if b.sessions[id] {
		return nil
	}
	var (
		agent  string
		origin sql.NullString
		start  int64
		ended  sql.NullInt64
		purged sql.NullInt64
	)
	err := b.s.db.QueryRow(`SELECT agent, origin_device, started_at, ended_at, purged_at FROM sessions WHERE id = ?`, id).
		Scan(&agent, &origin, &start, &ended, &purged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: load session %s: %w", id, err)
	}
	b.sessions[id] = true
	ss := &SyncSession{ID: id, Agent: agent, OriginDevice: b.self, StartedAt: start}
	if origin.Valid && origin.String != "" {
		ss.OriginDevice = origin.String
	}
	if ended.Valid {
		v := ended.Int64
		ss.EndedAt = &v
	}
	if purged.Valid {
		v := purged.Int64
		ss.PurgedAt = &v
	}
	b.sessionOut = append(b.sessionOut, SyncChange{Kind: SyncKindSession, Session: ss})
	return nil
}

// loadSyncRecord reads record id as a SyncRecord, or nil when it must not be
// served. A tombstoned record's text, next step and about are dropped HERE,
// whatever the row holds: a tombstone that predates the scrub (migration 0010)
// may still carry its text, and it must not cross.
func (s *Store) loadSyncRecord(id, selfDevice string) (*SyncRecord, error) {
	var (
		r                                   SyncRecord
		about                               string
		session, project, outcome, promoter sql.NullString
		gitHead, next, origin               sql.NullString
		expires, tombstoned                 sql.NullInt64
	)
	err := s.db.QueryRow(`SELECT id, ts, kind, tier, text, about, session_id, project_key, outcome, promoter,
		expires_at, tombstoned_at, git_head, next, origin_device FROM records WHERE id = ?`, id).
		Scan(&r.ID, &r.TS, &r.Kind, &r.Tier, &r.Text, &about, &session, &project, &outcome, &promoter,
			&expires, &tombstoned, &gitHead, &next, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: load record %s: %w", id, err)
	}
	if !project.Valid || project.String == "" {
		return nil, nil
	}
	ident, err := s.SyncIdentityOf(project.String)
	if err != nil {
		return nil, err
	}
	if ident.ID == "" {
		return nil, nil
	}
	r.ProjectSyncID = ident.ID
	r.SessionID = session.String
	r.Outcome = outcome.String
	r.Promoter = promoter.String
	r.GitHead = gitHead.String
	r.Next = next.String
	r.OriginDevice = selfDevice
	if origin.Valid && origin.String != "" {
		r.OriginDevice = origin.String
	}
	if expires.Valid {
		v := expires.Int64
		r.ExpiresAt = &v
	}
	if err := json.Unmarshal([]byte(about), &r.About); err != nil {
		return nil, fmt.Errorf("store: parse record %s about: %w", id, err)
	}
	if tombstoned.Valid {
		v := tombstoned.Int64
		r.TombstonedAt = &v
		r.Text, r.Next, r.About = "", "", []string{}
		r.Promoter = ""
	}
	if r.About == nil {
		r.About = []string{}
	}
	return &r, nil
}

// requeueProjectChanges re-queues every record of projectKey in the feed. A
// record of a project with no sync identity is skipped, never guessed; once
// the project gains one (a remote, or a human link) its records are queued
// again so they are served. Peers dedupe by id.
func (s *Store) requeueProjectChanges(projectKey string) error {
	_, err := s.db.Exec(`INSERT INTO sync_changes (kind, ref)
		SELECT CASE WHEN tombstoned_at IS NULL THEN 'record' ELSE 'tombstone' END, id
		FROM records WHERE project_key = ? ORDER BY rowid`, projectKey)
	if err != nil {
		return fmt.Errorf("store: requeue sync changes for %s: %w", projectKey, err)
	}
	return nil
}

// afterSyncIdentityChange runs after a write that may have given projectKey a
// new sync identity: it requeues the project's records and rehomes any
// placeholder project that carries the identity.
func (s *Store) afterSyncIdentityChange(projectKey string, before SyncIdentity) error {
	after, err := s.SyncIdentityOf(projectKey)
	if err != nil {
		return err
	}
	if after.ID != "" && after.ID != before.ID {
		if err := s.requeueProjectChanges(projectKey); err != nil {
			return err
		}
	}
	return s.rehomeSyncPlaceholders()
}

// rehomeSyncPlaceholders moves every placeholder project ("sync:<id>") whose
// identity now belongs to a local project onto that project, through the same
// merge and project_key_aliases record the workspace sweep uses.
func (s *Store) rehomeSyncPlaceholders() error {
	var placeholders []string
	rows, err := s.db.Query(`SELECT key FROM projects WHERE substr(key, 1, ?) = ?`,
		len(SyncPlaceholderPrefix), SyncPlaceholderPrefix)
	if err != nil {
		return fmt.Errorf("store: list sync placeholders: %w", err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			_ = rows.Close()
			return err
		}
		placeholders = append(placeholders, k)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(placeholders) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: rehome sync placeholders: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	idx, err := syncIdentityIndex(tx)
	if err != nil {
		return err
	}
	moved := 0
	for _, ph := range placeholders {
		local, ok := idx[strings.TrimPrefix(ph, SyncPlaceholderPrefix)]
		if !ok {
			continue
		}
		if err := sweepOneTarget(tx, ph, local, false); err != nil {
			return fmt.Errorf("store: rehome %s -> %s: %w", ph, local, err)
		}
		moved++
	}
	if moved == 0 {
		return nil
	}
	return tx.Commit()
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// syncIdentityIndex maps each sync identity to the local (non-placeholder)
// project that holds it; when two projects share one, the lowest key wins.
func syncIdentityIndex(q queryer) (map[string]string, error) {
	rows, err := q.Query(`SELECT p.key, l.sync_id, p.remote_url FROM projects p
		LEFT JOIN project_links l ON l.local_key = p.key ORDER BY p.key`)
	if err != nil {
		return nil, fmt.Errorf("store: sync identity index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	idx := map[string]string{}
	for rows.Next() {
		var key string
		var link, remote sql.NullString
		if err := rows.Scan(&key, &link, &remote); err != nil {
			return nil, err
		}
		if strings.HasPrefix(key, SyncPlaceholderPrefix) {
			continue
		}
		if id := resolveSyncIdentity(key, link, remote).ID; id != "" {
			if _, dup := idx[id]; !dup {
				idx[id] = key
			}
		}
	}
	return idx, rows.Err()
}

// ImportSyncChanges applies a peer's changes, idempotently by id, in one
// transaction. Only a human identity may import. With capture off nothing is
// written and Paused is reported. Tier and origin_device come from the change,
// never from the importing process; a tombstoned record is stored with no text
// whatever the change carries, and a tombstone for a record already present
// applies TombstoneRecord's own scrub.
func (s *Store) ImportSyncChanges(identity Identity, peer SyncPeer, changes []SyncChange) (SyncImportResult, error) {
	var res SyncImportResult
	if identity.Kind != IdentityHuman {
		return res, ErrSyncImportRequiresHuman
	}
	if peer.DeviceID == "" {
		return res, fmt.Errorf("store: sync import: peer device id required")
	}
	paused, err := s.CapturePausedAt(time.Now())
	if err != nil {
		return res, err
	}
	if paused {
		res.Paused = true
		return res, nil
	}
	self, err := s.SelfDevice()
	if err != nil {
		return res, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return res, fmt.Errorf("store: sync import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	im := &syncImporter{tx: tx, self: self.ID, peer: peer.DeviceID, res: &res}
	if im.idx, err = syncIdentityIndex(tx); err != nil {
		return res, err
	}
	for _, pass := range []string{SyncKindSession, SyncKindRecord, SyncKindEdge} {
		for i, c := range changes {
			if syncPass(c.Kind) != pass {
				continue
			}
			if err := im.apply(c); err != nil {
				return SyncImportResult{}, fmt.Errorf("store: sync import change %d (%s): %w", i, c.Kind, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return SyncImportResult{}, fmt.Errorf("store: sync import: commit: %w", err)
	}
	if res.Tombstones > 0 {
		if err := s.checkpointTruncate(); err != nil {
			return res, err
		}
	}
	return res, nil
}

func syncPass(kind string) string {
	if kind == SyncKindTombstone {
		return SyncKindRecord
	}
	return kind
}

type syncImporter struct {
	tx   *sql.Tx
	self string
	peer string
	idx  map[string]string
	res  *SyncImportResult
}

func (im *syncImporter) origin(o string) any {
	if o == "" {
		o = im.peer
	}
	if o == im.self {
		return nil
	}
	return o
}

func (im *syncImporter) apply(c SyncChange) error {
	switch c.Kind {
	case SyncKindSession:
		if c.Session == nil {
			return errors.New("session change without a session")
		}
		return im.session(c.Session)
	case SyncKindRecord, SyncKindTombstone:
		if c.Record == nil {
			return errors.New("record change without a record")
		}
		return im.record(c.Record, c.Kind == SyncKindTombstone)
	case SyncKindEdge:
		if c.Edge == nil {
			return errors.New("edge change without an edge")
		}
		return im.edge(c.Edge)
	default:
		return fmt.Errorf("unknown change kind %q", c.Kind)
	}
}

func (im *syncImporter) session(ss *SyncSession) error {
	if ss.ID == "" {
		return errors.New("session id required")
	}
	var ended, purged sql.NullInt64
	var origin string
	err := im.tx.QueryRow(`SELECT ended_at, origin, purged_at FROM sessions WHERE id = ?`, ss.ID).Scan(&ended, &origin, &purged)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		var e, p any
		if ss.EndedAt != nil {
			e = *ss.EndedAt
		}
		if ss.PurgedAt != nil {
			p = *ss.PurgedAt
		}
		if _, err := im.tx.Exec(`INSERT INTO sessions (id, agent, cwd, started_at, ended_at, origin, origin_device, purged_at)
			VALUES (?, ?, '', ?, ?, ?, ?, ?)`, ss.ID, ss.Agent, ss.StartedAt, e, "synced", im.origin(ss.OriginDevice), p); err != nil {
			return err
		}
		im.res.Sessions++
		return nil
	case err != nil:
		return err
	}
	changed := false
	if origin == "synced" && !ended.Valid && ss.EndedAt != nil {
		if _, err := im.tx.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, *ss.EndedAt, ss.ID); err != nil {
			return err
		}
		changed = true
	}
	// Forgetting wins: a purge stamps this copy whatever its origin, and a
	// stamp already present is never moved or cleared.
	if ss.PurgedAt != nil && !purged.Valid {
		if _, err := im.tx.Exec(`UPDATE sessions SET purged_at = ? WHERE id = ?`, *ss.PurgedAt, ss.ID); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		im.res.Sessions++
	}
	return nil
}

// projectFor returns the local project key for a sync identity, creating the
// placeholder "sync:<id>" project when this store has none.
func (im *syncImporter) projectFor(syncID string) (string, error) {
	if k, ok := im.idx[syncID]; ok {
		return k, nil
	}
	ph := SyncPlaceholderPrefix + syncID
	if _, err := im.tx.Exec(`INSERT OR IGNORE INTO projects (key, toplevel, first_seen) VALUES (?, '', ?)`,
		ph, tsToNanos(time.Now())); err != nil {
		return "", err
	}
	im.idx[syncID] = ph
	return ph, nil
}

func (im *syncImporter) record(r *SyncRecord, tombstone bool) error {
	if r.ID == "" {
		return errors.New("record id required")
	}
	tombstone = tombstone || r.TombstonedAt != nil
	var exists int
	var tombstoned sql.NullInt64
	err := im.tx.QueryRow(`SELECT 1, tombstoned_at FROM records WHERE id = ?`, r.ID).Scan(&exists, &tombstoned)
	if err == nil {
		if tombstone && !tombstoned.Valid {
			if err := tombstoneRecordTx(im.tx, r.ID); err != nil {
				return err
			}
			im.res.Tombstones++
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if r.ProjectSyncID == "" {
		im.res.SkippedNoMatch++
		return nil
	}
	if len(r.Text) > MaxRecordTextBytes {
		return &CapError{Cap: CapMaxTextBytes, Limit: MaxRecordTextBytes}
	}
	project, err := im.projectFor(r.ProjectSyncID)
	if err != nil {
		return err
	}
	var session any
	if r.SessionID != "" {
		var n int
		if err := im.tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, r.SessionID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			session = r.SessionID
		}
	}
	text, next, aboutList := r.Text, nullable(r.Next), r.About
	var tombAt any
	if tombstone {
		text, next, aboutList = "", nil, nil
		ts := time.Now().UnixNano()
		if r.TombstonedAt != nil {
			ts = *r.TombstonedAt
		}
		tombAt = ts
	}
	about, err := marshalStrings(aboutList)
	if err != nil {
		return err
	}
	var outcome, promoter, expires any
	if r.Outcome != "" {
		outcome = r.Outcome
	}
	if !tombstone && r.Promoter != "" {
		promoter = r.Promoter
	}
	if r.ExpiresAt != nil {
		expires = *r.ExpiresAt
	}
	// tier and origin_device are the change's own (SCHEMA.md invariant 2 is
	// kept by the origin store; the importer never re-derives a tier).
	if _, err := im.tx.Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at,
		 tombstoned_at, event_cursor, git_head, next, origin_device)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, ?, ?, ?, 0, ?, ?, ?)`,
		r.ID, r.TS, r.Kind, r.Tier, text, about, session, project, outcome, promoter, expires,
		tombAt, nullable(r.GitHead), next, im.origin(r.OriginDevice)); err != nil {
		return fmt.Errorf("insert record %s: %w", r.ID, err)
	}
	if tombstone {
		im.res.Tombstones++
	} else {
		im.res.Records++
	}
	return nil
}

func (im *syncImporter) edge(e *SyncEdge) error {
	var n int
	if err := im.tx.QueryRow(`SELECT COUNT(*) FROM records WHERE id IN (?, ?)`, e.FromID, e.ToID).Scan(&n); err != nil {
		return err
	}
	want := 2
	if e.FromID == e.ToID {
		want = 1
	}
	if n < want {
		im.res.EdgesDeferred++
		return nil
	}
	r, err := im.tx.Exec(`INSERT OR IGNORE INTO edges (from_id, to_id, type, declared_by) VALUES (?, ?, ?, ?)`,
		e.FromID, e.ToID, e.Type, e.DeclaredBy)
	if err != nil {
		return err
	}
	if c, _ := r.RowsAffected(); c > 0 {
		im.res.Edges++
	}
	return nil
}

// PendingForgets counts the tombstones in this store's feed that the peer with
// deviceID has not yet acknowledged (feed entries past its push cursor). A
// tombstone imported from another device counts too: it is also queued for
// every peer, and the next sync clears it.
func (s *Store) PendingForgets(deviceID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT c.ref) FROM sync_changes c
		WHERE c.kind = 'tombstone' AND c.seq > (SELECT push_cursor FROM sync_peers WHERE device_id = ?)`, deviceID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: pending forgets: %w", err)
	}
	return n, nil
}
