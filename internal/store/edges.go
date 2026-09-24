package store

import (
	"encoding/json"
	"fmt"
	"time"
)

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

// Edge is a row read back from edges.
type Edge struct {
	FromID     string
	ToID       string
	Type       EdgeType
	DeclaredBy string
}

// EdgesTouching returns every edge where id is either endpoint — both
// directions, every type — the neighborhood internal/recall's record
// anchor walks (SCHEMA.md's six edge types; PLAN.md §Phase 4: "walk its
// edges ... one or two hops, both directions").
func (s *Store) EdgesTouching(id string) ([]Edge, error) {
	rows, err := s.db.Query(`SELECT from_id, to_id, type, declared_by FROM edges
		WHERE from_id = ? OR to_id = ?`, id, id)
	if err != nil {
		return nil, fmt.Errorf("store: edges touching %s: %w", id, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Edge
	for rows.Next() {
		var e Edge
		var edgeType string
		if err := rows.Scan(&e.FromID, &e.ToID, &edgeType, &e.DeclaredBy); err != nil {
			return nil, fmt.Errorf("store: scan edge touching %s: %w", id, err)
		}
		e.Type = EdgeType(edgeType)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: edges touching %s: %w", id, err)
	}
	return out, nil
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

// Contradiction is one `contradicts` edge into a record in projectKey, with
// the contradicting record's own evidence — This Week's Attention "flagged
// contradictions" kind (task 56d8c63d).
type Contradiction struct {
	// TargetID is the contradicted record (the edge's to_id).
	TargetID string
	// SourceID is the contradicting record (the edge's from_id) — the
	// kind=confirm record store.Confirm's contradict action minted.
	SourceID string
	// Evidence is SourceID's own evidence timeline event ids, the positive
	// evidence the contradiction was raised on (AGENT-CONTRACT.md
	// §Outcomes: "contradiction only on positive evidence").
	Evidence []int64
}

// ContradictionsSince returns every `contradicts` edge whose to_id names a
// record in projectKey and whose from_id (the contradicting record) was
// itself inserted at or after since, oldest first by that record's own
// sequence. edges carries no timestamp column of its own (SCHEMA.md), so
// "this week" is judged by the contradicting record's own ts — the record
// whose insert is what brought the edge into existence in the first place.
func (s *Store) ContradictionsSince(projectKey string, since time.Time) ([]Contradiction, error) {
	rows, err := s.db.Query(`
		SELECT e.to_id, e.from_id, c.evidence FROM edges e
		JOIN records r ON r.id = e.to_id
		JOIN records c ON c.id = e.from_id
		WHERE e.type = ? AND r.project_key = ? AND c.ts >= ?
		ORDER BY c.rowid ASC`,
		string(EdgeContradicts), projectKey, tsToNanos(since))
	if err != nil {
		return nil, fmt.Errorf("store: contradictions since %s for project %s: %w", since, projectKey, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Contradiction
	for rows.Next() {
		var targetID, sourceID, evidenceJSON string
		if err := rows.Scan(&targetID, &sourceID, &evidenceJSON); err != nil {
			return nil, fmt.Errorf("store: scan contradiction: %w", err)
		}
		var evidence []int64
		if err := json.Unmarshal([]byte(evidenceJSON), &evidence); err != nil {
			return nil, fmt.Errorf("store: parse contradiction evidence for %s: %w", sourceID, err)
		}
		out = append(out, Contradiction{TargetID: targetID, SourceID: sourceID, Evidence: evidence})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: contradictions since %s for project %s: %w", since, projectKey, err)
	}
	return out, nil
}
