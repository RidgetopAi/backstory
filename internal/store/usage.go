package store

import (
	"fmt"
	"sort"
	"time"
)

// UsageStats is the aggregate the Omarchy usage record is built from. Days
// are calendar days in the Location the caller passed to UsageStats.
type UsageStats struct {
	// TodaySessions and TotalSessions count sessions by started_at.
	TodaySessions int
	TotalSessions int
	// ActiveDates are the distinct days (YYYY-MM-DD, ascending) on which a
	// session started or a live record was written.
	ActiveDates []string
	// RecentDays has one entry per day inside the recent window that has at
	// least one record, ascending; MessageCount is records written that day.
	RecentDays []DayCount
}

// DayCount is one day's record count.
type DayCount struct {
	Date         string
	MessageCount int
}

const usageDateLayout = "2006-01-02"

// UsageStats aggregates sessions and (non-tombstoned) records as of now.
// recentDays is the width of the RecentDays window, today included.
func (s *Store) UsageStats(now time.Time, loc *time.Location, recentDays int) (UsageStats, error) {
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	today := now.Format(usageDateLayout)
	windowStart := time.Date(now.Year(), now.Month(), now.Day()-(recentDays-1), 0, 0, 0, 0, loc).Format(usageDateLayout)

	var out UsageStats
	active := map[string]bool{}
	perDay := map[string]int{}

	sessRows, err := s.db.Query(`SELECT started_at FROM sessions`)
	if err != nil {
		return UsageStats{}, fmt.Errorf("store: usage sessions: %w", err)
	}
	for sessRows.Next() {
		var ns int64
		if err := sessRows.Scan(&ns); err != nil {
			_ = sessRows.Close()
			return UsageStats{}, fmt.Errorf("store: usage sessions scan: %w", err)
		}
		day := tsFromNanos(ns).In(loc).Format(usageDateLayout)
		out.TotalSessions++
		if day == today {
			out.TodaySessions++
		}
		active[day] = true
	}
	if err := sessRows.Err(); err != nil {
		_ = sessRows.Close()
		return UsageStats{}, fmt.Errorf("store: usage sessions: %w", err)
	}
	_ = sessRows.Close()

	recRows, err := s.db.Query(`SELECT ts FROM records WHERE tombstoned_at IS NULL`)
	if err != nil {
		return UsageStats{}, fmt.Errorf("store: usage records: %w", err)
	}
	defer func() { _ = recRows.Close() }()
	for recRows.Next() {
		var ns int64
		if err := recRows.Scan(&ns); err != nil {
			return UsageStats{}, fmt.Errorf("store: usage records scan: %w", err)
		}
		day := tsFromNanos(ns).In(loc).Format(usageDateLayout)
		active[day] = true
		if day >= windowStart && day <= today {
			perDay[day]++
		}
	}
	if err := recRows.Err(); err != nil {
		return UsageStats{}, fmt.Errorf("store: usage records: %w", err)
	}

	for d := range active {
		out.ActiveDates = append(out.ActiveDates, d)
	}
	sort.Strings(out.ActiveDates)
	for d, n := range perDay {
		out.RecentDays = append(out.RecentDays, DayCount{Date: d, MessageCount: n})
	}
	sort.Slice(out.RecentDays, func(i, j int) bool { return out.RecentDays[i].Date < out.RecentDays[j].Date })
	return out, nil
}
