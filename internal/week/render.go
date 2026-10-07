package week

import (
	"fmt"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// render is Build's text form: one heading per non-empty section, in
// Attention / Where you left off / The week order, each section omitted
// entirely — no heading, no filler line — when it has nothing to show
// (decision 9be5c1d5: "Empty -> empty array, and the text render prints
// NOTHING for the section", the same per-slot omission rule
// internal/block already applies to the SessionStart block).
func render(r Result) string {
	var sections []string
	if s := renderAttention(r.Attention); s != "" {
		sections = append(sections, s)
	}
	if s := renderWhereLeftOff(r.WhereLeftOff); s != "" {
		sections = append(sections, s)
	}
	if s := renderWeek(r.Week); s != "" {
		sections = append(sections, s)
	}
	return strings.Join(sections, "\n\n")
}

func renderAttention(items []AttentionItem) string {
	if len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, "Attention:")
	for _, it := range items {
		// A stale-handoff reason already leads with the project label.
		line := fmt.Sprintf("- [%s] %s: %s", it.Kind, it.ProjectKey, it.Reason)
		if it.HandoffID != "" {
			line = fmt.Sprintf("- [%s] %s", it.Kind, it.Reason)
		}
		if len(it.EvidenceIDs) > 0 {
			line += " (ids " + strings.Join(it.EvidenceIDs, ",") + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderWhereLeftOff(rows []WhereLeftOffRow) string {
	if len(rows) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, "Where you left off:")
	for _, row := range rows {
		if row.Group == "" {
			lines = append(lines, "- "+renderProjectSummary(row.Project))
			continue
		}
		lines = append(lines, fmt.Sprintf("- Group %s:", row.Group))
		for _, child := range row.Children {
			lines = append(lines, "  - "+renderProjectSummary(child))
		}
	}
	return strings.Join(lines, "\n")
}

func renderProjectSummary(p ProjectSummary) string {
	line := fmt.Sprintf("%s (%s) — last activity %s", p.DisplayName, p.CWD, formatTS(p.LastActivity))
	if p.HandoffID != "" {
		line += fmt.Sprintf("; handoff (id %s): %s", p.HandoffID, p.HandoffFirstLine)
		if p.HandoffStale {
			line += " ⚠ possibly stale"
		}
	}
	return line
}

func renderWeek(days []DayProjectStats) string {
	if len(days) == 0 {
		return ""
	}
	lines := make([]string, 0, len(days)+1)
	lines = append(lines, "The week:")
	for _, d := range days {
		lines = append(lines, fmt.Sprintf("%s · %s: %d session(s), %d file(s) touched, %d record(s) written",
			d.Day.Format("2006-01-02"), d.DisplayName, d.Sessions, d.FilesTouched, d.RecordsWritten))
	}
	return strings.Join(lines, "\n")
}

func formatTS(t time.Time) string {
	return store.FormatTS(t, time.RFC3339)
}
