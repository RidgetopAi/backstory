package store

import (
	"fmt"
	"time"
)

// maxLedgerScan bounds how many of a location's newest records LedgerCounts
// reads: the block's Ledger line is a hint about whether recall is worth a
// call, not an audit, so a location with more records than this counts its
// newest maxLedgerScan.
const maxLedgerScan = 5000

// LedgerCounts is a location's current decision / outcome / claim records.
type LedgerCounts struct {
	Decisions int
	Outcomes  int
	Claims    int
	// Newest is the ts of the newest counted record; zero when all counts are 0.
	Newest time.Time
}

// Total is the number of counted records.
func (c LedgerCounts) Total() int { return c.Decisions + c.Outcomes + c.Claims }

// LedgerCounts counts the CURRENT decision, outcome and claim records in
// ls: not tombstoned, not superseded by a later record, and (for anything
// with an expiry) not expired as of asOf — the same notion of "current" recall
// annotates items with.
func (s *Store) LedgerCounts(ls LocationScope, asOf time.Time) (LedgerCounts, error) {
	recs, err := s.RecordsForLocation(ls, maxLedgerScan)
	if err != nil {
		return LedgerCounts{}, fmt.Errorf("store: ledger counts: %w", err)
	}
	superseded := map[string]bool{}
	rows, err := s.db.Query(`SELECT to_id FROM edges WHERE type = ?`, string(EdgeSupersedes))
	if err != nil {
		return LedgerCounts{}, fmt.Errorf("store: ledger counts: superseded: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return LedgerCounts{}, fmt.Errorf("store: ledger counts: scan superseded: %w", err)
		}
		superseded[id] = true
	}
	if err := rows.Err(); err != nil {
		return LedgerCounts{}, fmt.Errorf("store: ledger counts: superseded: %w", err)
	}

	var c LedgerCounts
	for _, r := range recs {
		if r.TombstonedAt != nil || superseded[r.ID] {
			continue
		}
		if r.ExpiresAt != nil && !r.ExpiresAt.After(asOf) {
			continue
		}
		switch r.Kind {
		case KindDecision:
			c.Decisions++
		case KindOutcome:
			c.Outcomes++
		case KindClaim:
			c.Claims++
		default:
			continue
		}
		if r.TS.After(c.Newest) {
			c.Newest = r.TS
		}
	}
	return c, nil
}
