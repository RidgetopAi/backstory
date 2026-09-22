package store

import "fmt"

// EdgeType is edges.type.
type EdgeType string

const (
	EdgeSupersedes         EdgeType = "supersedes"
	EdgePossiblySupersedes EdgeType = "possibly_supersedes"
	EdgeInforms            EdgeType = "informs"
	EdgeCaused             EdgeType = "caused"
	EdgeProducedOutcome    EdgeType = "produced_outcome"
	EdgeContradicts        EdgeType = "contradicts"
)

// LinkEdge inserts an edge between two records. declaredBy is a session id,
// "human", or "daemon" (SCHEMA.md edges.declared_by) — who asserted the
// relationship, not the tier of either record it connects.
func (s *Store) LinkEdge(fromID, toID string, edgeType EdgeType, declaredBy string) error {
	_, err := s.db.Exec(`INSERT INTO edges (from_id, to_id, type, declared_by) VALUES (?, ?, ?, ?)`,
		fromID, toID, string(edgeType), declaredBy)
	if err != nil {
		return fmt.Errorf("store: link edge %s->%s (%s): %w", fromID, toID, edgeType, err)
	}
	return nil
}

// ContradictionCount counts `contradicts` edges whose to_id names a record
// in projectKey — the SessionStart block's attention slot's other half
// (AGENT-CONTRACT.md §The SessionStart block).
func (s *Store) ContradictionCount(projectKey string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM edges e
		JOIN records r ON r.id = e.to_id
		WHERE e.type = ? AND r.project_key = ?`, string(EdgeContradicts), projectKey).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: contradiction count for project %s: %w", projectKey, err)
	}
	return n, nil
}
