package store

import (
	"fmt"
	"time"
)

// SearchResult is one row of a SearchRecords match.
type SearchResult struct {
	ID   string
	TS   time.Time
	Kind RecordKind
	Tier Tier
	Text string
}

// SearchRecords runs an FTS5 query over records.text, best match first (ties
// broken chronologically, earliest first — a numeric comparison on the
// INTEGER ts column, never the lexical comparison an RFC3339Nano TEXT ts
// column would give), and excludes tombstoned records (a delete removes a
// record from recall even though the row itself stays — AGENT-CONTRACT.md
// §User-only powers).
func (s *Store) SearchRecords(query string, limit int) ([]SearchResult, error) {
	rows, err := s.db.Query(`SELECT r.id, r.ts, r.kind, r.tier, r.text
		FROM records_fts
		JOIN records r ON r.rowid = records_fts.rowid
		WHERE records_fts MATCH ? AND r.tombstoned_at IS NULL
		ORDER BY rank, r.ts ASC
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		var ts int64
		var kind, tier string
		if err := rows.Scan(&r.ID, &ts, &kind, &tier, &r.Text); err != nil {
			return nil, fmt.Errorf("store: scan search result: %w", err)
		}
		r.TS = tsFromNanos(ts)
		r.Kind = RecordKind(kind)
		r.Tier = Tier(tier)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search records: %w", err)
	}
	return out, nil
}
