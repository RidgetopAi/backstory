package store

import (
	"fmt"
	"strings"
	"unicode"
)

// Tunables for RelatedDecisions' full-text side. Named here, never inlined.
const (
	// RelatedMinTermRunes is the shortest term used for the full-text match:
	// shorter words ("to", "the", "use") are noise that would make every
	// decision a candidate.
	RelatedMinTermRunes = 4
	// RelatedMaxTerms caps how many distinct terms of the new text feed the
	// full-text query.
	RelatedMaxTerms = 12
	// relatedFTSFetch is how many full-text hits are read before the
	// current/other filters are applied in Go-visible SQL; the SQL itself
	// filters, so this only bounds the scan.
	relatedFTSFetch = 50
)

// RelatedDecisions returns up to limit CURRENT decisions in projectKey that
// are likely about the same thing as a decision being written: those sharing
// an about[] path (most shared paths first, then newest), then those matching
// the text's distinctive terms by full text (best bm25 first). Current means
// not tombstoned and not the to_id of any supersedes edge; excludeIDs (the new
// record itself, a record it already supersedes) are skipped. It only reads:
// no edge is ever written. Any text is safe — terms are reduced to letters
// and digits and quoted as FTS5 literals.
func (s *Store) RelatedDecisions(projectKey string, excludeIDs, about []string, text string, limit int) ([]Record, error) {
	if limit <= 0 {
		return nil, nil
	}
	projectKey = s.canonicalizeProjectKey(projectKey)
	exclude := make(map[string]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		exclude[id] = true
	}

	const current = `r.kind = 'decision' AND r.tombstoned_at IS NULL AND r.project_key = ?
		AND NOT EXISTS (SELECT 1 FROM edges e WHERE e.to_id = r.id AND e.type = 'supersedes')`

	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if !exclude[id] && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	if len(about) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(about)), ",")
		args := []any{projectKey}
		for _, a := range about {
			args = append(args, a)
		}
		args = append(args, args[1:]...)
		rows, err := s.db.Query(`SELECT r.id FROM records r
			WHERE `+current+` AND r.about IS NOT NULL
			AND EXISTS (SELECT 1 FROM json_each(r.about) j WHERE j.value IN (`+marks+`))
			ORDER BY (SELECT COUNT(DISTINCT j.value) FROM json_each(r.about) j WHERE j.value IN (`+marks+`)) DESC, r.ts DESC`,
			args...)
		if err != nil {
			return nil, fmt.Errorf("store: related decisions by about: %w", err)
		}
		got, err := scanIDs(rows)
		if err != nil {
			return nil, fmt.Errorf("store: related decisions by about: %w", err)
		}
		for _, id := range got {
			add(id)
		}
	}

	if len(ids) < limit {
		if q := relatedFTSQuery(text); q != "" {
			rows, err := s.db.Query(`SELECT r.id FROM records_fts
				JOIN records r ON r.rowid = records_fts.rowid
				WHERE records_fts MATCH ? AND `+current+`
				ORDER BY rank, r.ts DESC LIMIT ?`, q, projectKey, relatedFTSFetch)
			if err != nil {
				return nil, fmt.Errorf("store: related decisions by text: %w", err)
			}
			got, err := scanIDs(rows)
			if err != nil {
				return nil, fmt.Errorf("store: related decisions by text: %w", err)
			}
			for _, id := range got {
				add(id)
			}
		}
	}

	if len(ids) > limit {
		ids = ids[:limit]
	}
	return s.getRecords(ids)
}

// relatedFTSQuery reduces arbitrary text to an OR of quoted distinctive terms
// ("" when none): letters/digits runs of at least RelatedMinTermRunes, lower-
// cased, deduplicated, at most RelatedMaxTerms. Because only letters and
// digits survive, no FTS5 operator or quote can reach the parser.
func relatedFTSQuery(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var terms []string
	seen := map[string]bool{}
	for _, w := range words {
		if len([]rune(w)) < RelatedMinTermRunes || seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"`)
		if len(terms) == RelatedMaxTerms {
			break
		}
	}
	return strings.Join(terms, " OR ")
}
