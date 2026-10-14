package session

import (
	"context"
	"errors"
	"time"
)

// The query structs travel over the session transport as JSON with the
// camel-case keys below. Go's decoder matches keys case-insensitively, so a
// server or client from before the keys were named, which used the Go field
// names ("ProjectPathRe", "SessionID"), still interoperates. AccountID is
// never sent: the server stamps the principal's account on every query.

// SessionQuery selects sessions. The regexes are case-insensitive; QueryRe
// matches project_path or cwd. Limit is capped at 500.
type SessionQuery struct {
	AccountID     string `json:"-"`
	ProjectPathRe string `json:"projectPathRe"`
	GitOriginRe   string `json:"gitOriginRe"`
	QueryRe       string `json:"queryRe"`
	Limit         int    `json:"limit"`
}

// LineQuery selects transcript lines. An empty AccountID means no account filter.
type LineQuery struct {
	AccountID  string `json:"-"`
	SessionID  string `json:"sessionId"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	Descending bool   `json:"descending"`
}

// CommandQuery selects Bash tool events whose tool_input.command matches
// PatternRe, newest first.
type CommandQuery struct {
	AccountID  string   `json:"-"`
	PatternRe  string   `json:"patternRe"`
	SessionIDs []string `json:"sessionIds"`
	Limit      int      `json:"limit"`
}

// MemoryQuery selects MemorySaved events (summaries ingested from a
// claude-remember store), newest first. PatternRe is a case-insensitive
// regex over memory.text; empty Kinds and SessionIDs mean no filter.
type MemoryQuery struct {
	AccountID  string   `json:"-"`
	PatternRe  string   `json:"patternRe"`
	Kinds      []string `json:"kinds"`
	SessionIDs []string `json:"sessionIds"`
	Limit      int      `json:"limit"`
}

// FileQuery selects the hook events that touched a file, newest first: the
// PreToolUse events the file-tracker enricher recorded (by
// enrichments.file-tracker.file_path) and the PostToolUse Edit and Write
// events the edit-diff-stats enricher measured (by tool_input.file_path).
// PathRe is a case-insensitive regex over the path; empty matches every
// file. Empty SessionIDs means no session filter.
type FileQuery struct {
	AccountID  string   `json:"-"`
	PathRe     string   `json:"pathRe"`
	SessionIDs []string `json:"sessionIds"`
	Limit      int      `json:"limit"`
}

// ErrorQuery selects the transcript lines the error-flag enricher marked as
// errors (enrichments.error-flag.is_error), newest first. Types keeps only
// those error types (hook_error, tool_failure, error_text); empty Types and
// SessionIDs mean no filter.
type ErrorQuery struct {
	AccountID  string   `json:"-"`
	Types      []string `json:"types"`
	SessionIDs []string `json:"sessionIds"`
	Limit      int      `json:"limit"`
}

// TextQuery searches the indexed text of transcript lines (the search-text
// enricher's enrichments.search-text) for the words of Query, best match
// first. A line matches when it holds any of the words; lines holding more
// of them, more often, rank higher. Words are matched case-insensitively;
// a word starting with "-" excludes the lines holding it and a "quoted
// phrase" must appear as written. Empty SessionIDs means no filter.
type TextQuery struct {
	AccountID  string   `json:"-"`
	Query      string   `json:"query"`
	SessionIDs []string `json:"sessionIds"`
	Limit      int      `json:"limit"`
}

// MemoryEventName is the hook_event_name of an ingested memory record. Such
// an event carries the record under "memory" with a "hash" that identifies
// its text; a store keeps one event per account and hash.
const MemoryEventName = "MemorySaved"

// ErrNotFound is returned when a session does not exist.
var ErrNotFound = errors.New("session not found")

// ErrTooLarge is returned (possibly wrapped) when a store refuses a document
// because its content exceeds the store's size limit, as a remote store does
// for a 413. Callers record such a refusal instead of retrying it.
var ErrTooLarge = errors.New("content too large")

// ErrNoAccount is returned by DeleteAccount for an empty account id.
var ErrNoAccount = errors.New("account id is required")

// ErrUnsupported is returned by a store that cannot perform an operation at
// all, as the remote adapter does for the enrichment methods (enrichment
// runs on the server). Callers must not wire such a store to enrich.Loop.
var ErrUnsupported = errors.New("operation not supported by this store")

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
	// FileEvents projects session_id, hook_event_name, tool_name,
	// tool_use_id, tool_input.file_path, enrichments.file-tracker,
	// enrichments.edit-diff-stats and created_at of the events FileQuery
	// selects.
	FileEvents(ctx context.Context, q FileQuery) ([]Doc, error)
	// ErrorLines projects session_id, seq, line, enrichments.error-flag and
	// created_at of the lines ErrorQuery selects.
	ErrorLines(ctx context.Context, q ErrorQuery) ([]Doc, error)
	// SearchTranscripts projects session_id, seq, score (higher is better),
	// enrichments.search-text and created_at of the lines TextQuery matches.
	SearchTranscripts(ctx context.Context, q TextQuery) ([]Doc, error)
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
	// RequeueSkipped clears enrichments.<enricher>_skipped on the account's
	// documents in the collection, so the next pass considers them again
	// (an account that switched the enricher on), and returns how many it
	// cleared. An empty accountID is ErrNoAccount.
	RequeueSkipped(ctx context.Context, collection, accountID, enricher string) (int64, error)
	// DeleteAccount removes every document stamped with the account
	// (sessions, hook events, transcript and subagent lines, blobs) and
	// returns how many went. An empty accountID is ErrNoAccount: documents
	// without an account are never deleted wholesale.
	DeleteAccount(ctx context.Context, accountID string) (int64, error)
	Close(ctx context.Context) error
}
