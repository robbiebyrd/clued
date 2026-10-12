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
	StartedAt   time.Time // zero when unknown
	LastSeen    time.Time // zero when unknown
	TopCommands []string  // most recent distinct Bash commands, newest first
}

// SessionSource looks up captured sessions. Current returns nil, nil when no
// session was captured for cwd.
type SessionSource interface {
	Current(ctx context.Context, cwd string) (*SessionSnapshot, error)
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
