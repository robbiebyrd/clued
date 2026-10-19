package code

import (
	"context"
	"errors"
)

// Errors a Store returns, matched with errors.Is. A store may wrap them with
// detail; the transport maps them to its error kinds.
var (
	// ErrNotFound is an unknown worktree or blob. The transport also
	// answers a worktree held by another owner as not found.
	ErrNotFound = errors.New("code: not found")
	// ErrInvalid is a request that fails validation: a malformed path,
	// hash, worktree id or ref, or a blob whose content does not hash to
	// its SHA256 or whose Size differs from its length.
	ErrInvalid = errors.New("code: invalid")
	// ErrMissingBlob is a Commit whose upsert names a blob the store does
	// not hold (the transport's 409 conflict); the client uploads it and
	// retries.
	ErrMissingBlob = errors.New("code: missing blob")
)

// Store holds the collected code of one namespaced project: blobs shared by
// every worktree, one tree per (worktree, ref), the worktrees and the change
// log. It is opened per namespaced project (CluedOptions.Code), so no method
// takes a project. A Store persists what it is given; owner and session
// stamping happen before it (server/codeapi). Implementations pass
// code/codetest and are safe for concurrent use.
type Store interface {
	// MissingBlobs returns the hashes in shas the store does not hold, in
	// the order given.
	MissingBlobs(ctx context.Context, shas []string) ([]string, error)
	// PutBlob stores b after verifying that the SHA-256 of b.Content is
	// b.SHA256 (ErrInvalid otherwise). Storing a blob already held is a
	// no-op.
	PutBlob(ctx context.Context, b Blob) error
	// PutWorktree creates or updates the worktree's metadata (Owner,
	// Source, Root, Host, Origin, DefaultBranch), adds w.Refs to the refs
	// it knows and clears Removed; Sessions and UpdatedAt are the store's.
	PutWorktree(ctx context.Context, w Worktree) error
	// Commit applies c to the (c.Worktree, c.Ref) tree and appends it to
	// the log atomically, returning its Seq. Upserts replace the entries at
	// their paths and Deletes remove paths; Full replaces the whole tree
	// with Upserts. An upsert naming a blob the store lacks fails with
	// ErrMissingBlob and changes nothing; an unknown worktree is
	// ErrNotFound. A retried commit re-applies the same entries and appends
	// at most a duplicate log entry.
	Commit(ctx context.Context, c Change) (seq int64, err error)
	// RemoveWorktree marks the worktree removed and appends a tombstone
	// change (see Change); an unknown worktree is ErrNotFound.
	RemoveWorktree(ctx context.Context, id string) error
	// TouchSession records activity of sessionID on the worktree: it adds
	// the session with FirstAt on first sight, moves LastAt, and sets Ended
	// when ended is true. An unknown worktree is ErrNotFound.
	TouchSession(ctx context.Context, worktree, sessionID string, ended bool) error

	// The read side, for code/codetest and the code-graph indexer only:
	// no HTTP route serves it.

	// Worktrees lists every worktree of the project, removed ones
	// included.
	Worktrees(ctx context.Context) ([]Worktree, error)
	// Tree returns the entries of the (worktree, ref) tree sorted by Path,
	// empty when there is none.
	Tree(ctx context.Context, worktree, ref string) ([]Entry, error)
	// Changes returns up to limit changes with Seq greater than afterSeq,
	// in Seq order.
	Changes(ctx context.Context, afterSeq int64, limit int) ([]Change, error)
	// Blob returns the blob with that SHA-256, or ErrNotFound.
	Blob(ctx context.Context, sha string) (Blob, error)
	// Usage returns the number of blobs the project holds and their total
	// size in bytes.
	Usage(ctx context.Context) (blobs int64, bytes int64, err error)
}
