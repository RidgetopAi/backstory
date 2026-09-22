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

// EdgeSpec is one edge to insert alongside a new record, as part of
// InsertRecordWithEdges: the new record's id supplies one endpoint, OtherID
// supplies the other. Field names the caller-facing parameter this edge
// came from (e.g. "links", "supersedes"), so an unknown OtherID can be
// attributed back to it in a caller-facing error.
type EdgeSpec struct {
	OtherID    string
	Type       EdgeType
	DeclaredBy string
	Field      string
	// Incoming makes OtherID the edge's from_id and the new record's id
	// its to_id — e.g. a linked record "informs" the new one. The default
	// (false) makes the new record's id the from_id and OtherID the
	// to_id — e.g. the new record "supersedes" OtherID.
	Incoming bool
}

// UnknownEdgeTargetError is returned by InsertRecordWithEdges when an
// EdgeSpec names a record id that does not exist. Nothing is inserted when
// this error is returned — not the record, not any edge (critic T1 on
// 14704ebe, task e7951178: a supersedes edge written after the record's
// own insert used to leave a partial write on failure).
type UnknownEdgeTargetError struct {
	Field string
	ID    string
}

func (e *UnknownEdgeTargetError) Error() string {
	return fmt.Sprintf("store: unknown %s target %q", e.Field, e.ID)
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
