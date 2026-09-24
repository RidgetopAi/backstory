// Package export renders a per-project markdown mirror of Backstory's
// ledger: the latest handoff (with its stale flag if any), open Attention
// items, and recent decisions and notes, at a chosen altitude — for another
// tool's memory file (Claude auto-memory, Hermes MEMORY.md, OpenClaw
// imports) to mirror. Build only reads the store and returns text; it never
// writes anywhere and never guesses another tool's memory path — the user
// points it there via --out (decision 262cf929). WriteAtomic is the sole
// disk-writing half: temp file in the destination's directory, then
// rename, so a failure partway through never leaves a partial file at the
// destination path.
package export

import (
	"fmt"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/recall"
	"github.com/RidgetopAi/backstory/internal/store"
)

// Altitude is the subset of recall.Altitude export renders at: headline and
// summary. Unlike recall's own CLI, export has no --altitude full — a
// generated mirror another tool reads on every boot stays bounded, never a
// full-text dump (ORIGINAL DESCRIPTION: "backstory export ... --altitude
// headline|summary").
type Altitude = recall.Altitude

const (
	AltitudeHeadline = recall.AltitudeHeadline
	AltitudeSummary  = recall.AltitudeSummary
)

// DefaultAltitude is export's altitude when --altitude is not given
// (ORIGINAL DESCRIPTION: "at the chosen altitude (default summary)").
const DefaultAltitude = AltitudeSummary

// recallBudgetTokens bounds the recall.Build call Build makes to fetch a
// project's whole trust-annotated ledger. It is generous enough that
// export's own per-section limit (maxSectionItems), not recall's token
// budget, is what actually shapes the mirror: a markdown file written to
// disk is not a chat context window, so it has no business being cut off by
// one.
const recallBudgetTokens = 1 << 20

// maxSectionItems bounds how many decisions/notes the "Decisions & notes"
// section lists, newest first — "recent decisions and notes" (ORIGINAL
// DESCRIPTION), not the project's whole history, so the mirror stays a
// quick read for another tool's memory file.
const maxSectionItems = 50

// summaryBodyChars bounds a decision/note's rendered body at
// AltitudeSummary — the same width internal/recall's own summary altitude
// uses (recall.go's summaryBodyChars), so export's "short body" reads the
// same width recall's CLI already trained a reader to expect.
const summaryBodyChars = 240

// Params is Build's input.
type Params struct {
	Store      *store.Store
	ProjectKey string
	// Altitude is AltitudeHeadline or AltitudeSummary. Build does not
	// validate it — cmd/backstory/export.go rejects anything else at the
	// flag, the same division of responsibility recall's own CLI uses.
	Altitude Altitude
	// Now is the export's generation time, stamped into the header. Zero
	// means time.Now().
	Now time.Time
}

// Build renders projectKey's markdown mirror: a header naming Backstory,
// the project and the generation time, and that the file is a generated
// mirror (edits are overwritten); a Resume section (the latest
// non-tombstoned handoff, stale-flagged per recall's own possibly-stale
// status); an Attention section (unconfirmed drafts, contradictions, and
// the same stale flag once more as an attention item); and a Decisions &
// notes section built from recall.Build's own trust-annotated project
// ledger — so a tombstoned record's text never reaches the mirror
// (recall.Item.Text is already "" for a tombstoned status; SCHEMA.md
// invariant 1) and every item carries its stored tier verbatim, never
// computed.
func Build(p Params) (string, error) {
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}

	result, err := recall.Build(p.Store, recall.ProjectAnchor(p.ProjectKey), p.Altitude, recallBudgetTokens)
	if err != nil {
		return "", fmt.Errorf("export: %w", err)
	}

	draftCount, err := p.Store.UnconfirmedDraftCount(p.ProjectKey, now)
	if err != nil {
		return "", fmt.Errorf("export: unconfirmed draft count: %w", err)
	}
	contradictionCount, err := p.Store.ContradictionCount(p.ProjectKey)
	if err != nil {
		return "", fmt.Errorf("export: contradiction count: %w", err)
	}

	handoff, hasHandoff := latestHandoff(result.Items)
	staleHandoff := hasHandoff && handoff.Status == recall.StatusPossiblyStale

	var b strings.Builder
	writeHeader(&b, p.ProjectKey, now)
	writeResumeSection(&b, handoff, hasHandoff, staleHandoff)
	writeAttentionSection(&b, draftCount, contradictionCount, staleHandoff)
	writeDecisionsAndNotesSection(&b, result.Items, p.Altitude)
	return b.String(), nil
}

// latestHandoff returns the newest non-tombstoned handoff in items — items
// is already newest-first by sequence (recall's project-anchor ordering,
// itself store.RecordsForProjectAll's rowid DESC) — the same "most recent,
// tombstone excluded" rule store.LatestRecord applies for the SessionStart
// block's own Resume slot.
func latestHandoff(items []recall.Item) (recall.Item, bool) {
	for _, item := range items {
		if item.Kind == store.KindHandoff && item.Status != recall.StatusTombstoned {
			return item, true
		}
	}
	return recall.Item{}, false
}

// mirrorHeaderFormat names Backstory, the project, the generation time and
// the generated-mirror disclaimer in one HTML comment line invisible when
// the file is rendered as markdown, plus a heading repeating the project
// for a reader viewing the raw text — decision 262cf929: "Header names
// Backstory, the project, the generation time and that the file is a
// generated mirror (edits are overwritten)."
func writeHeader(b *strings.Builder, projectKey string, now time.Time) {
	fmt.Fprintf(b, "<!-- Generated by Backstory for project %s at %s -- this file is a generated mirror; edits are overwritten. -->\n",
		projectKey, now.UTC().Format(time.RFC3339))
	fmt.Fprintf(b, "# Backstory export: %s\n\n", projectKey)
}

func writeResumeSection(b *strings.Builder, handoff recall.Item, hasHandoff, staleHandoff bool) {
	b.WriteString("## Resume\n")
	if !hasHandoff {
		b.WriteString("no handoff yet.\n\n")
		return
	}
	fmt.Fprintf(b, "(id %s) %s\n", handoff.ID, handoff.Text)
	if staleHandoff {
		b.WriteString("⚠ possibly stale\n")
	}
	b.WriteString("\n")
}

// writeAttentionSection mirrors internal/block's SessionStart Attention
// slot in substance (unconfirmed drafts, contradictions, and whether the
// resume handoff is possibly stale — AGENT-CONTRACT.md §The SessionStart
// block) but always renders, even at zero, rather than being omitted when
// empty: a written mirror file is not a budget-constrained chat block, and
// a golden-testable document should not change shape between "nothing to
// report" and "not rendered at all".
func writeAttentionSection(b *strings.Builder, draftCount, contradictionCount int, staleHandoff bool) {
	b.WriteString("## Attention\n")
	fmt.Fprintf(b, "- %d unconfirmed draft(s)\n", draftCount)
	fmt.Fprintf(b, "- %d contradiction(s)\n", contradictionCount)
	if staleHandoff {
		b.WriteString("- resume handoff is possibly stale\n")
	}
	b.WriteString("\n")
}

// writeDecisionsAndNotesSection lists decision and note items from items —
// which already carries every project record, tombstoned ones included,
// annotated by recall.Build — newest first, capped at maxSectionItems.
// Handoffs are excluded here: they already have their own Resume section
// above, so nothing about a project's ledger is rendered twice.
func writeDecisionsAndNotesSection(b *strings.Builder, items []recall.Item, altitude Altitude) {
	b.WriteString("## Decisions & notes\n")
	rendered := 0
	for _, item := range items {
		if item.Kind != store.KindDecision && item.Kind != store.KindNote {
			continue
		}
		if rendered >= maxSectionItems {
			break
		}
		writeLedgerItem(b, item, altitude)
		rendered++
	}
	if rendered == 0 {
		b.WriteString("none.\n")
	}
}

// writeLedgerItem renders one decision/note item: its full id (never
// truncated — a generated mirror on disk benefits from the same verbatim
// id AGENT-CONTRACT.md's Resume slot always carries, not the CLI's
// abbreviated headline form), kind, stored tier and status, then its body
// at altitude — headline shows the first line only, summary truncates to
// summaryBodyChars — or nothing at all when Text is empty, which is always
// true for a tombstoned item (recall.Item's own invariant): a tombstoned
// record's text can never reach this mirror.
func writeLedgerItem(b *strings.Builder, item recall.Item, altitude Altitude) {
	fmt.Fprintf(b, "- %s · %s · %s · %s\n", item.ID, item.Kind, item.Tier, item.Status)
	if item.Text == "" {
		return
	}
	text := item.Text
	if altitude == AltitudeHeadline {
		text = firstLine(text)
	} else {
		text = truncate(text, summaryBodyChars)
	}
	fmt.Fprintf(b, "  %s\n", text)
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

func truncate(text string, maxChars int) string {
	r := []rune(text)
	if len(r) <= maxChars {
		return text
	}
	return string(r[:maxChars]) + "…"
}
