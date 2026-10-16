package session

import "time"

// TranscriptSpan is the earliest and latest "timestamp" of lines, zero when
// no line carries one. Backfill and import date a session by it, so a
// session loaded long after it ran starts and was last seen when it did,
// not when it was loaded.
func TranscriptSpan(lines []TranscriptLine) (first, last time.Time) {
	for _, l := range lines {
		m, _ := l.Line.(map[string]any)
		s, _ := m["timestamp"].(string)
		at, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			continue
		}
		if first.IsZero() || at.Before(first) {
			first = at
		}
		if at.After(last) {
			last = at
		}
	}
	return first, last
}
