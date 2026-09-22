package store

import "time"

// Every ts-like column is stored as an INTEGER unix nanosecond value (UTC),
// never text, so range and equality comparisons in SQL are numeric — a
// RFC3339Nano TEXT column compares lexically, and drops the fractional part
// when ns == 0, so a whole-second timestamp's text can sort AFTER a
// timestamp a fraction of a second later (PLAN.md §Phase 1, critic T2 on
// 7c4dfc90). The Go API is unaffected: every exported field and parameter
// is still a time.Time.

// tsToNanos converts a time.Time to the unix-nanosecond integer stored in
// a NOT NULL ts column.
func tsToNanos(t time.Time) int64 {
	return t.UTC().UnixNano()
}

// tsFromNanos converts a stored unix-nanosecond integer back to a UTC
// time.Time.
func tsFromNanos(n int64) time.Time {
	return time.Unix(0, n).UTC()
}

// nullableTS converts a *time.Time to a nanosecond int64 for a nullable ts
// column, or nil.
func nullableTS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return tsToNanos(*t)
}
