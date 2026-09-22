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
