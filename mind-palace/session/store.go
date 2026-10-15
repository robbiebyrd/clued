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
	// TitleRe keeps only sessions whose title matches (case-insensitive).
	TitleRe string `json:"titleRe,omitempty"`
	// ProjectSlugs keeps only sessions whose project_slug is one of these
	// (exact match); empty means no filter.
	ProjectSlugs []string `json:"projectSlugs,omitempty"`
	// LiveStates keeps only sessions whose live_state is one of these
	// (working, waiting, ended); empty means no filter.
	LiveStates []string `json:"liveStates,omitempty"`
	// LastSeenFrom and LastSeenBefore bound last_seen (inclusive from,
	// exclusive before); a zero time is no bound. The query service turns a
	// hotness (hot, warm, cool, cold) into these.
	LastSeenFrom   time.Time `json:"lastSeenFrom,omitempty"`
	LastSeenBefore time.Time `json:"lastSeenBefore,omitempty"`
	Limit          int       `json:"limit"`
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

// MemoryQuery selects MemorySaved events (native memory records, digests
// and the claude-remember records ingested before native memory), newest
// first. PatternRe is a case-insensitive regex over memory.text; empty
// Kinds, SessionIDs, Projects and IDs mean no filter. Projects keeps the
// records whose memory.project is one of them, IDs those whose memory.id
// is; Since (inclusive) and Until (exclusive) bound created_at, a zero time
// being no bound. Records with memory.private true are left out unless
// IncludePrivate is set.
type MemoryQuery struct {
	AccountID      string    `json:"-"`
	PatternRe      string    `json:"patternRe"`
	Kinds          []string  `json:"kinds"`
	SessionIDs     []string  `json:"sessionIds"`
	Projects       []string  `json:"projects,omitempty"`
	Since          time.Time `json:"since,omitzero"`
	Until          time.Time `json:"until,omitzero"`
	IDs            []string  `json:"ids,omitempty"`
	IncludePrivate bool      `json:"includePrivate,omitempty"`
	Limit          int       `json:"limit"`
}

// MemoryDelete selects the account's MemorySaved events to delete: those
// whose memory.id is one of IDs or whose memory.hash is one of Hashes. Both
// empty deletes nothing.
type MemoryDelete struct {
	AccountID string   `json:"-"`
	IDs       []string `json:"ids,omitempty"`
	Hashes    []string `json:"hashes,omitempty"`
}

// Empty reports whether the delete selects nothing.
func (d MemoryDelete) Empty() bool { return len(d.IDs) == 0 && len(d.Hashes) == 0 }

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

// MaxVectorHits caps VectorQuery.Limit.
const MaxVectorHits = 100

// VectorQuery ranks the account's transcript lines that carry an
// embedding of Model (enrichments.embedding, of len(Vector) numbers) by
// cosine similarity to Vector, best first. Empty SessionIDs means no
// session filter and a zero Since no lower bound on created_at. Limit is
// capped at MaxVectorHits; 0 or less means the cap.
type VectorQuery struct {
	AccountID  string    `json:"-"`
	Model      string    `json:"model"`
	Vector     []float32 `json:"vector"`
	SessionIDs []string  `json:"sessionIds"`
	Since      time.Time `json:"since"`
	Limit      int       `json:"limit"`
}

// ForgetQuery selects the account's sessions to delete with everything
// captured for them (hook events, transcript and subagent lines, blobs,
// OpenTelemetry events). The criteria are ANDed over the session records:
// SessionIDs, a regular expression over the project path, and Before
// (sessions last seen before it). With SessionIDs alone, documents of those
// ids are deleted even when their session record is gone. A query with no
// criterion deletes nothing.
//
// Limit, when above zero and ProjectPathRe or Before is given, bounds one
// call: it deletes at most Limit sessions, the oldest last_seen first, and
// Forgotten.More reports that more match. With SessionIDs alone it is
// ignored. Orphans, with Before and without ProjectPathRe, also deletes the
// captures whose session record is gone (of the SessionIDs when given):
// documents with a session id and the account's stamp, created before
// Before (timestamp for OpenTelemetry events), whose session id has no
// session record in the account. Documents without a session id (a daily
// digest, an account-level memory) are never orphans. Limit bounds the
// orphan session ids each collection contributes to one call. Without
// Before, Orphans is ignored and does not make a query non-empty.
type ForgetQuery struct {
	AccountID     string    `json:"-"`
	SessionIDs    []string  `json:"sessionIds"`
	ProjectPathRe string    `json:"projectPathRe"`
	Before        time.Time `json:"before"`
	Limit         int       `json:"limit,omitempty"`
	Orphans       bool      `json:"orphans,omitempty"`
}

// Empty reports whether the query has no criterion.
func (q ForgetQuery) Empty() bool {
	return len(q.SessionIDs) == 0 && q.ProjectPathRe == "" && q.Before.IsZero()
}

// Forgotten is what DeleteSessions deleted: the session ids (sorted,
// orphaned ones included) and the number of documents across every
// collection. More reports that a Limit cut the call short: calling again
// with the same query deletes the next batch.
type Forgotten struct {
	Sessions  []string `json:"sessions"`
	Documents int64    `json:"documents"`
	More      bool     `json:"more,omitempty"`
}

// LimitQuery selects LimitsSampled events (the context-window and
// usage-limit samples the status line posts), newest first. Empty
// SessionIDs mean every session; a zero Since means no lower bound on
// created_at.
type LimitQuery struct {
	AccountID  string    `json:"-"`
	SessionIDs []string  `json:"sessionIds"`
	Since      time.Time `json:"since"`
	Limit      int       `json:"limit"`
}

// ActivityQuery selects the transcript lines the activity-kind enricher has
// labelled (enrichments.activity-kind), newest first. Kinds keeps only
// those activity kinds (user, response, thinking, bash, commit, edit, read,
// task, tool, result, other); empty Kinds and SessionIDs mean no filter and
// a zero Since no lower bound on created_at.
type ActivityQuery struct {
	AccountID  string    `json:"-"`
	SessionIDs []string  `json:"sessionIds"`
	Kinds      []string  `json:"kinds"`
	Since      time.Time `json:"since"`
	Limit      int       `json:"limit"`
}

// OtelQuery selects stored OpenTelemetry events, newest first (timestamp,
// then sequence, descending). Empty SessionIDs and Names mean no filter, an
// empty PromptID every prompt, and a zero Since no lower bound on timestamp.
type OtelQuery struct {
	AccountID  string    `json:"-"`
	SessionIDs []string  `json:"sessionIds"`
	PromptID   string    `json:"promptId"`
	Names      []string  `json:"names"`
	Since      time.Time `json:"since"`
	Limit      int       `json:"limit"`
}

// LimitsEventName is the hook_event_name of a status-line sample (`mind-palace
// hook status`): the context window's fill, the subscription's usage
// windows, the model and the cost of the session at that moment, under
// "context_window", "rate_limits", "model", "version" and "cost".
const LimitsEventName = "LimitsSampled"

// ModelChangedEventName is the hook_event_name of the event recorded when
// the model serving a session changes (the first model seen included):
// "from" (the previous model, "" at first), "to", "seq" (the transcript
// line that named it, absent when a LimitsSampled sample did), "source"
// ("transcript" or "limits") and "at".
const ModelChangedEventName = "ModelChanged"

// StateChangedEventName is the hook_event_name of the event recorded when
// a session's live state changes: "from" (the previous state, "" at
// first), "to" (working, waiting or ended), "reason" (the hook event that
// set it, such as "Stop" or "PreToolUse:AskUserQuestion") and "at".
const StateChangedEventName = "SessionStateChanged"

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

// ErrNoAccount is returned by DeleteAccount, DeleteSessions and
// DeleteMemory for an empty account id.
var ErrNoAccount = errors.New("account id is required")

// ErrUnsupported is returned by a store that cannot perform an operation at
// all, as the remote adapter does for the enrichment methods (enrichment
// runs on the server). Callers must not wire such a store to enrich.Loop.
var ErrUnsupported = errors.New("operation not supported by this store")

// Lookup is the read access an enricher gets to the store.
type Lookup interface {
	HookEventIDsByToolUse(ctx context.Context, sessionID string, toolUseIDs []string) ([]string, error)
	// LastModelBefore is the model named by the newest assistant line of the
	// session (its line.message.model, synthetic lines aside) whose seq is
	// below seq (any seq when seq is negative) and whose created_at is not
	// after at (any time when at is zero); "" when there is none.
	LastModelBefore(ctx context.Context, sessionID string, seq int, at time.Time) (string, error)
}

// Store is the persistence port for sessions and everything captured with them.
type Store interface {
	Lookup
	// UpsertSession sets the present fields and sets started_at on insert only.
	UpsertSession(ctx context.Context, s Session) error
	TouchSession(ctx context.Context, accountID, sessionID string, at time.Time) error
	// SetGitInfoIfMissing writes the git info only when git_origin is absent.
	SetGitInfoIfMissing(ctx context.Context, sessionID, origin, branch string) error
	// SetModel records the model serving the session: when the stored model
	// differs, it sets model and model_changed_at (to at) and reports the
	// previous model ("" when none was stored) with changed true; when it is
	// the same nothing is written and changed is false. An empty model is
	// ignored; a session the account does not have is ErrNotFound.
	SetModel(ctx context.Context, accountID, sessionID, model string, at time.Time) (previous string, changed bool, err error)
	// GetSession returns ErrNotFound when the session does not exist.
	GetSession(ctx context.Context, accountID, sessionID string) (Doc, error)
	// SetLiveState records the session's live state (working, waiting,
	// ended) with the hook event that set it as the reason: when the
	// stored state differs, it sets live_state, live_state_at (to at) and
	// live_state_reason and reports the previous state ("" when none) with
	// changed true; the same state writes nothing. An empty state is
	// ignored; a session the account does not have is ErrNotFound.
	SetLiveState(ctx context.Context, accountID, sessionID, state, reason string, at time.Time) (previous string, changed bool, err error)
	// SetSessionMeta sets the given fields (the session metadata the
	// transcript's sidecar records carry: title, title_kind, slug,
	// permission_mode, pr_link, worktree, entrypoint, claude_version) on
	// the session; empty fields write nothing; a session the account does
	// not have is ErrNotFound.
	SetSessionMeta(ctx context.Context, accountID, sessionID string, fields Doc) error
	// FindSessions projects session_id, project_path, git_origin, cwd,
	// model, live_state, live_state_at, live_state_reason, title,
	// title_kind, slug, permission_mode, pr_link, worktree, entrypoint,
	// claude_version, started_at and last_seen, sorted by last_seen
	// descending.
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
	// DeleteMemory deletes the account's MemorySaved events MemoryDelete
	// selects and reports how many went; an empty AccountID is
	// ErrNoAccount.
	DeleteMemory(ctx context.Context, d MemoryDelete) (int, error)
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
	// SearchVectors projects session_id, seq, score (the cosine
	// similarity, higher is better), enrichments.search-text and
	// created_at of the lines VectorQuery ranks; a line with no
	// embedding, another model's or another length is never returned.
	SearchVectors(ctx context.Context, q VectorQuery) ([]Doc, error)
	// UsageTotals sums enrichments.usage of the lines UsageQuery selects, each
	// message once: one total per group and model, sorted by key and model.
	UsageTotals(ctx context.Context, q UsageQuery) ([]UsageTotal, error)
	// DeleteSessions deletes the sessions ForgetQuery selects and everything
	// captured for them; an empty AccountID is ErrNoAccount.
	DeleteSessions(ctx context.Context, q ForgetQuery) (Forgotten, error)

	// InsertOtelEvents stores the events, each once: an event whose
	// account, session, name, sequence and timestamp are stored already is
	// left as it is. It reports how many were new.
	InsertOtelEvents(ctx context.Context, events []OtelEvent) (int, error)
	// OtelEvents projects session_id, prompt_id, name, sequence, timestamp,
	// attributes, redacted and created_at of the events OtelQuery selects.
	OtelEvents(ctx context.Context, q OtelQuery) ([]Doc, error)

	// LimitSamples projects session_id, cwd, model, version, context_window,
	// rate_limits, cost, sampled_at and created_at of the LimitsSampled
	// events matching the query, newest first.
	LimitSamples(ctx context.Context, q LimitQuery) ([]Doc, error)
	// Activities projects session_id, seq, line.timestamp,
	// enrichments.activity-kind, enrichments.tool-result-detail and
	// enrichments.system-record (when present) and created_at of the
	// lines ActivityQuery selects, newest first (created_at, then seq,
	// descending).
	Activities(ctx context.Context, q ActivityQuery) ([]Doc, error)
	// StateChanges projects session_id, from, to, reason, at and
	// created_at of the SessionStateChanged events the query's SessionIDs,
	// Since and Limit select (Kinds is ignored), newest first.
	StateChanges(ctx context.Context, q ActivityQuery) ([]Doc, error)
	// RejectedToolUses projects session_id, seq, enrichments.error-flag
	// and created_at of the transcript lines the error-flag enricher marked
	// rejected (a tool call the harness refused), selected by the query's
	// SessionIDs, Since and Limit (Kinds is ignored), newest first.
	RejectedToolUses(ctx context.Context, q ActivityQuery) ([]Doc, error)
	// ActivityCounts counts the account's transcript lines the
	// activity-kind enricher labelled, per UTC hour of LineTime and kind
	// (enrichments.activity-kind.kind), for the hours of CountQuery,
	// sorted by hour then kind; [] when none.
	ActivityCounts(ctx context.Context, q CountQuery) ([]HourCount, error)
	// ErrorCounts counts the account's transcript lines the error-flag
	// enricher marked as errors, per UTC hour of LineTime and error type,
	// sorted by hour then type; [] when none.
	ErrorCounts(ctx context.Context, q CountQuery) ([]HourCount, error)
	// LimitBuckets sums the account's LimitsSampled events per UTC hour of
	// created_at (see LimitBucket), sorted by hour; [] when none.
	LimitBuckets(ctx context.Context, q CountQuery) ([]LimitBucket, error)
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
