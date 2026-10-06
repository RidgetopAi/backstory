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

// UnknownTS is what a reader renders for an event or record time that is
// zero or at/before the unix epoch — rows an old backfill wrote from Go's
// zero time.Time (task 456410f0). A time that cannot be real is shown as
// unknown, never as a date.
const UnknownTS = "unknown"

// ValidTS reports whether t is a real stored time: strictly after the unix
// epoch.
func ValidTS(t time.Time) bool {
	return t.After(time.Unix(0, 0))
}

// FormatTS renders t in layout (UTC), or UnknownTS when t is not a valid
// time.
func FormatTS(t time.Time, layout string) string {
	if !ValidTS(t) {
		return UnknownTS
	}
	return t.UTC().Format(layout)
}
