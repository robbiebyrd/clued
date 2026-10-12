package session

import (
	"context"
	"errors"
	"time"
)

// SessionQuery selects sessions. The regexes are case-insensitive; QueryRe
// matches project_path or cwd. Limit is capped at 500.
type SessionQuery struct {
	AccountID     string
	ProjectPathRe string
	GitOriginRe   string
	QueryRe       string
	Limit         int
}

// LineQuery selects transcript lines. An empty AccountID means no account filter.
type LineQuery struct {
	AccountID  string
	SessionID  string
	Offset     int
	Limit      int
	Descending bool
}

// CommandQuery selects Bash tool events whose tool_input.command matches
// PatternRe, newest first.
type CommandQuery struct {
	AccountID  string
	PatternRe  string
	SessionIDs []string
	Limit      int
}

// MemoryQuery selects MemorySaved events (summaries ingested from a
// claude-remember store), newest first. PatternRe is a case-insensitive
// regex over memory.text; empty Kinds and SessionIDs mean no filter.
type MemoryQuery struct {
	AccountID  string
	PatternRe  string
	Kinds      []string
	SessionIDs []string
	Limit      int
}

// MemoryEventName is the hook_event_name of an ingested memory record. Such
// an event carries the record under "memory" with a "hash" that identifies
// its text; a store keeps one event per account and hash.
const MemoryEventName = "MemorySaved"

// ErrNotFound is returned when a session does not exist.
var ErrNotFound = errors.New("session not found")

// Lookup is the read access an enricher gets to the store.
type Lookup interface {
	HookEventIDsByToolUse(ctx context.Context, sessionID string, toolUseIDs []string) ([]string, error)
}

// Store is the persistence port for sessions and everything captured with them.
type Store interface {
	Lookup
	// UpsertSession sets the present fields and sets started_at on insert only.
	UpsertSession(ctx context.Context, s Session) error
	TouchSession(ctx context.Context, accountID, sessionID string, at time.Time) error
	// SetGitInfoIfMissing writes the git info only when git_origin is absent.
	SetGitInfoIfMissing(ctx context.Context, sessionID, origin, branch string) error
	// GetSession returns ErrNotFound when the session does not exist.
	GetSession(ctx context.Context, accountID, sessionID string) (Doc, error)
	// FindSessions projects session_id, project_path, git_origin, cwd,
	// started_at and last_seen, sorted by last_seen descending.
	FindSessions(ctx context.Context, q SessionQuery) ([]Doc, error)
	// InsertHookEvent stores one event as given. A MemorySaved event whose
	// memory.hash the account already holds is dropped without error.
	InsertHookEvent(ctx context.Context, ev Doc) error
	// UpsertTranscriptLines is an unordered bulk upsert on {session_id, seq};
	// created_at is set on insert only.
	UpsertTranscriptLines(ctx context.Context, lines []TranscriptLine) error
	CountTranscriptLines(ctx context.Context, accountID, sessionID string) (int, error)
	// TranscriptLines returns the lines without _id, ordered by seq.
	TranscriptLines(ctx context.Context, q LineQuery) ([]Doc, error)
	UpsertSubagentLine(ctx context.Context, l SubagentLine) error
	SubagentIDs(ctx context.Context, accountID, sessionID string) ([]string, error)
	SubagentLines(ctx context.Context, accountID, sessionID, subagentID string) ([]Doc, error)
	// UpsertBlob updates the content; created_at is set on insert only.
	UpsertBlob(ctx context.Context, b Blob) error
	Blobs(ctx context.Context, accountID, sessionID string) ([]Doc, error)
	// BashEvents projects session_id, tool_input and created_at.
	BashEvents(ctx context.Context, q CommandQuery) ([]Doc, error)
	// MemoryEvents projects session_id, cwd, memory and created_at of the
	// MemorySaved events matching the query, newest first.
	MemoryEvents(ctx context.Context, q MemoryQuery) ([]Doc, error)
	// FullSession is the session_full join; ErrNotFound when the session does not exist.
	FullSession(ctx context.Context, accountID, sessionID string) (Doc, error)
	// Unenriched returns documents (including _id) that have none of
	// enrichments.<enricher>, enrichments.<enricher>_failed and
	// enrichments.<enricher>_skipped.
	Unenriched(ctx context.Context, collection, enricher string, limit int) ([]Doc, error)
	SetEnrichment(ctx context.Context, collection string, id any, enricher string, result any) error
	SetEnrichmentFailure(ctx context.Context, collection string, id any, enricher, message string, at time.Time) error
	// SetEnrichmentSkipped marks a document the enricher does not apply to, so
	// it leaves the queue.
	SetEnrichmentSkipped(ctx context.Context, collection string, id any, enricher string) error
	Close(ctx context.Context) error
}
