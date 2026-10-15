package session

import (
	"sort"
	"time"
)

// CountQuery selects the account's documents of a time range for the
// hourly aggregates: Since inclusive, Until exclusive, a zero time no
// bound.
type CountQuery struct {
	AccountID string    `json:"-"`
	Since     time.Time `json:"since"`
	Until     time.Time `json:"until"`
}

// HourCount is how many documents of one kind fell in one UTC hour (Hour
// is the hour's start).
type HourCount struct {
	Hour  time.Time `json:"hour"`
	Kind  string    `json:"kind"`
	Count int64     `json:"count"`
}

// LimitBucket sums the LimitsSampled events of one UTC hour: the largest
// five-hour, seven-day and context-window used percentages and the
// largest session cost (cost.total_cost_usd) sampled in it, each -1 when
// no sample of the hour carried it, and the number of samples.
type LimitBucket struct {
	Hour        time.Time `json:"hour"`
	FiveHourMax float64   `json:"five_hour_max"`
	SevenDayMax float64   `json:"seven_day_max"`
	ContextMax  float64   `json:"context_max"`
	CostUSDMax  float64   `json:"cost_usd_max"`
	Samples     int64     `json:"samples"`
}

// LineTime is when a transcript line happened, for the hourly aggregates:
// its line.timestamp (RFC 3339, as Claude Code writes it) when that
// parses, else its created_at, so a transcript backfilled long after the
// session counts in the hours it was written.
func LineTime(d Doc) time.Time {
	if s, ok := d.Line().String("timestamp"); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC()
		}
	}
	t, _ := d["created_at"].(time.Time)
	return t.UTC()
}

// InRange reports whether t is in [since, until), a zero bound being none.
func (q CountQuery) InRange(t time.Time) bool {
	return (q.Since.IsZero() || !t.Before(q.Since)) && (q.Until.IsZero() || t.Before(q.Until))
}

// SortHourCounts orders counts by hour, then kind.
func SortHourCounts(counts []HourCount) {
	sort.Slice(counts, func(i, j int) bool {
		if !counts[i].Hour.Equal(counts[j].Hour) {
			return counts[i].Hour.Before(counts[j].Hour)
		}
		return counts[i].Kind < counts[j].Kind
	})
}

// LineTimeSlack is how much earlier than its created_at a line's own
// timestamp may be for a store to prefilter a range by created_at (a
// line is stored after it is written; the slack covers clock skew). A
// store looks only at lines created at or after Since minus the slack.
const LineTimeSlack = 24 * time.Hour
