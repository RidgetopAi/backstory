package store

import (
	"database/sql"
	"fmt"
	"strings"
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
	ftsQuery := escapeFTS5Query(query)
	if ftsQuery == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT r.id, r.ts, r.kind, r.tier, r.text
		FROM records_fts
		JOIN records r ON r.rowid = records_fts.rowid
		WHERE records_fts MATCH ? AND r.tombstoned_at IS NULL
		ORDER BY rank, r.ts ASC
		LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search records: %w", err)
	}
	out, err := scanSearchResults(rows)
	if err != nil {
		return nil, fmt.Errorf("store: search records: %w", err)
	}
	return out, nil
}

// SearchRecordsInProject is SearchRecords scoped to a single project — the
// query internal/recall's free-text anchor needs (PLAN.md §Phase 4: "free
// text (FTS via SearchRecords, scoped to the project)"): SearchRecords
// itself carries no project_key column to filter on the FTS side, so this
// joins the same way and adds the one extra WHERE clause rather than
// filtering matches after the fact.
func (s *Store) SearchRecordsInProject(projectKey, query string, limit int) ([]SearchResult, error) {
	projectKey = s.canonicalizeProjectKey(projectKey)
	ftsQuery := escapeFTS5Query(query)
	if ftsQuery == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT r.id, r.ts, r.kind, r.tier, r.text
		FROM records_fts
		JOIN records r ON r.rowid = records_fts.rowid
		WHERE records_fts MATCH ? AND r.tombstoned_at IS NULL AND r.project_key = ?
		ORDER BY rank, r.ts ASC
		LIMIT ?`, ftsQuery, projectKey, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search records in project %s: %w", projectKey, err)
	}
	out, err := scanSearchResults(rows)
	if err != nil {
		return nil, fmt.Errorf("store: search records in project %s: %w", projectKey, err)
	}
	return out, nil
}

// escapeFTS5Query turns arbitrary user text into a safe FTS5 MATCH query:
// split on whitespace, drop empty tokens, wrap each remaining token in a
// double-quoted FTS5 string literal (doubling any `"` inside it per FTS5's
// own escaping rule), and join the literals with OR, so a record matching any
// term is a hit and bm25 ranks those matching more (or rarer) terms first. A quoted literal is plain text to FTS5's query-language parser, so a
// token like "wobble-party" or "a:b" is searched for as text rather than
// parsed as the hyphen/colon/quote/asterisk/paren/caret/AND/OR/NOT/NEAR
// operators those characters would otherwise trigger (the bug this fixes:
// recall erroring with "no such column: party" on a project named
// wobble-party). An all-whitespace or empty query has no tokens and
// produces "" — SearchRecords/SearchRecordsInProject treat that as an
// empty result rather than passing "" to MATCH, which FTS5 itself rejects
// as a syntax error.
func escapeFTS5Query(query string) string {
	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		terms = append(terms, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " OR ")
}

// scanSearchResults drains rows into a []SearchResult, closing rows itself
// (success or failure) so SearchRecords and SearchRecordsInProject share one
// copy of the scan/close bookkeeping instead of each repeating it.
func scanSearchResults(rows *sql.Rows) ([]SearchResult, error) {
	defer func() { _ = rows.Close() }()

	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		var ts int64
		var kind, tier string
		if err := rows.Scan(&r.ID, &ts, &kind, &tier, &r.Text); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		r.TS = tsFromNanos(ts)
		r.Kind = RecordKind(kind)
		r.Tier = Tier(tier)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
