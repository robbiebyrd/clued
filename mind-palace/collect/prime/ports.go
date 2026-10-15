// Package prime composes the probes, the session store and the plan and
// story services into one report that primes a Claude Code session.
package prime

import (
	"context"
	"time"
)

// SessionSnapshot is what the SESSION section shows about the most recent
// captured session of a working directory.
type SessionSnapshot struct {
	SessionID   string
	Model       string    // the model last seen serving it; "" when unknown
	LiveState   string    // working, waiting or ended; "" when unknown
	Hotness     string    // hot, warm, cool or cold; "" when unknown
	StartedAt   time.Time // zero when unknown
	LastSeen    time.Time // zero when unknown
	TopCommands []string  // most recent distinct Bash commands, newest first
}

// SessionSource looks up captured sessions. Current returns nil, nil when no
// session was captured for cwd.
type SessionSource interface {
	Current(ctx context.Context, cwd string) (*SessionSnapshot, error)
}

// MemoryNote is one record of the MEMORY section: a native memory record
// (session/memory) or a claude-remember record captured before native
// memory (Legacy).
type MemoryNote struct {
	ID        string
	Kind      string // memory-pinned, memory-handoff, memory-recent, session-summary, …; entry, day, … for legacy records
	Day       string // YYYY-MM-DD when the record carries one
	Time      string // HH:MM for a legacy entry
	Branch    string
	Text      string
	Legacy    bool
	CreatedAt time.Time // when it was captured; zero when unknown
}

// MemorySource reads the memory bundle of a working directory's project:
// the notes to inject, in order, within budgetTokens (estimated at four
// characters a token), the smaller bundle after a compaction when compact
// is set. It returns an empty slice when the project has no memory.
type MemorySource interface {
	Bundle(ctx context.Context, cwd string, budgetTokens int, compact bool) ([]MemoryNote, error)
}

// DocSummary is one line of the STORIES or PLANS section.
type DocSummary struct {
	ID     string
	Type   string
	Status string
	Title  string
}

// DocSource reads the plans and stories mind-palace holds for a project.
type DocSource interface {
	// ActiveStories lists stories in pending, ready or in_progress status, by id.
	ActiveStories(ctx context.Context) ([]DocSummary, error)
	// ActivePlans lists in_progress plans first, then ready ones, each by id.
	ActivePlans(ctx context.Context) ([]DocSummary, error)
	// PlanContent returns the Markdown content of one plan.
	PlanContent(ctx context.Context, id string) (string, error)
}
