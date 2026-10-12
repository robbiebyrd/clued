package memory

import "time"

// fileTime gives distinct, ordered modification times for fixtures.
func fileTime(i int) time.Time {
	return time.Date(2026, 10, 12, 10, i, 0, 0, time.UTC)
}
