// Package code is the domain of collected code: the model of what a Claude
// Code client mirrors to the clued server (content-addressed blobs, one tree
// per worktree and ref, an append-only change log) and the Store port that
// holds it, one Store per namespaced project.
//
// The package holds the model, the port and validation only. The stores
// (code/memcode, code/mongocode) persist what they are given and pass
// code/codetest; server/codeapi is the write-only transport that validates
// requests and stamps the server-set fields; session/codesync is the client
// that fills the rest. The read side of the port exists for the conformance
// suite and the code-graph indexer, which replays Changes and fetches blobs.
//
// Each field's doc comment says who sets it: the client (the codesync
// collector or the GitHub sync), the server (codeapi stamping from the
// token's principal and the change being committed) or the store itself.
package code

import "time"

// Worktree sources: who writes a worktree. An empty Source means
// SourceClient.
const (
	// SourceClient is a Claude Code client's checkout, written by
	// session/codesync through server/codeapi.
	SourceClient = "client"
	// SourceGitHub is the GitHub-backed worktree of a project (id
	// gh_<repository id>), written by the GitHub App sync; its entries are
	// always clean and pushed.
	SourceGitHub = "github"
)

// Blob is one file's content, addressed by the SHA-256 of its bytes. Blobs
// are shared by every worktree and ref of a namespaced project and stored
// once.
type Blob struct {
	// SHA256 is the lowercase hex SHA-256 of Content, computed by the
	// client after redaction; the store verifies it on PutBlob.
	SHA256 string
	// Size is len(Content), set by the client and checked by the store.
	Size int64
	// Content is the file's bytes after the client's redaction; the
	// original secret never leaves the machine.
	Content []byte
}

// Entry is one file in a (worktree, ref) tree, as an upsert in a Change and
// as a row of Tree.
type Entry struct {
	// Path is relative to the worktree root: clean, slash-separated and
	// local (see ValidPath). Set by the client.
	Path string
	// SHA256 names the Blob holding the file's redacted content, set by
	// the client. It is empty when the content was not sent (binary, over
	// the size cap, or refused by the server); the entry still records the
	// file.
	SHA256 string
	// Size is the file's size in bytes on disk, set by the client.
	Size int64
	// Mode is git's file mode ("100644", "100755", "120000"), set by the
	// client.
	Mode string
	// Lang is the language the client detected from the path ("go",
	// "typescript"), empty when unknown; the indexer picks its parser by
	// it.
	Lang string
	// GitOID is git's blob id of this path in HEAD (40 or 64 lowercase
	// hex, see ValidGitOID), empty for an untracked file. Set by the
	// client. With Dirty false it proves the entry is the committed
	// content, which the SHA-256 of redacted bytes cannot.
	GitOID string
	// Dirty is true when the working file differs from HEAD (modified,
	// added or untracked), set by the client. A clean entry needs a
	// GitOID.
	Dirty bool
	// Unpushed is true when the path differs between the upstream commit
	// and HEAD, or the branch has no upstream; set by the client. Dirty
	// and Unpushed both false is exactly the branch's pushed content.
	Unpushed bool
	// Session is the Claude Code session that last wrote the entry. The
	// server sets it from the writing Change's SessionID; a client never
	// sends it.
	Session string
}

// Worktree is one checkout on one machine (or, with SourceGitHub, a
// repository on GitHub), the unit a tree belongs to.
type Worktree struct {
	// ID is "wt_" plus the first 16 hex characters of SHA-256 of host,
	// a NUL and the absolute worktree root (computed by the client), or
	// "gh_" plus the GitHub repository id; see ValidWorktreeID.
	ID string
	// Owner is the account that created the worktree, stamped by the
	// server from the token's principal; a value from the client is
	// ignored. Another account's write to it is answered as an unknown
	// worktree.
	Owner string
	// Source says who writes the worktree: SourceClient (or empty) or
	// SourceGitHub. Set by the writer (client: empty; GitHub sync:
	// SourceGitHub).
	Source string
	// Root is the absolute worktree root on the client's machine
	// (git rev-parse --show-toplevel), metadata only. Set by the client.
	Root string
	// Host is the client machine's host name, metadata only. Set by the
	// client.
	Host string
	// Origin is the origin remote's URL, set by the client.
	Origin string
	// DefaultBranch is the branch the base graph follows (origin's HEAD,
	// falling back to main or master), empty when unknown. Set by the
	// client.
	DefaultBranch string
	// Refs lists the refs seen on the worktree, kept by the store: it adds
	// the refs a PutWorktree carries (the transport sends the current ref)
	// and the ref of every Commit, and never drops one.
	Refs []string
	// Sessions are the Claude Code sessions seen on the worktree, kept by
	// the store from TouchSession and Commit.
	Sessions []SessionSeen
	// Removed is true once RemoveWorktree ran; set by the store.
	Removed bool
	// UpdatedAt is the last write to the worktree, set by the store.
	UpdatedAt time.Time
}

// SessionSeen is a Claude Code session's activity on one worktree, kept by
// the store from TouchSession (SessionStart, SessionEnd) and the changes the
// session commits.
type SessionSeen struct {
	// ID is the Claude Code session id.
	ID string
	// FirstAt is the session's first activity on the worktree.
	FirstAt time.Time
	// LastAt is its latest activity.
	LastAt time.Time
	// Ended is true once TouchSession reported the session's end.
	Ended bool
}

// Change is one commit of a change set to a (worktree, ref) tree and its
// record in the project's append-only change log, which the indexer replays.
//
// A tombstone, appended by the store on RemoveWorktree, has the worktree's
// ID and Owner, an empty Ref, Full true and no Upserts or Deletes: every
// tree of that worktree is gone.
type Change struct {
	// Seq is the change's position in the project's log, assigned by the
	// store on Commit (increasing, starting at 1); the client sends 0.
	Seq int64
	// Owner is the account that wrote the change, stamped by the server
	// from the token's principal; a value from the client is ignored.
	Owner string
	// Worktree is the ID of the worktree the change applies to, set by
	// the client (the route's worktree id).
	Worktree string
	// Ref is the branch, or "detached@<short sha>", whose tree the change
	// applies to (see ValidRef). Set by the client.
	Ref string
	// Head is the HEAD commit (40 or 64 lowercase hex) when the change was
	// made, set by the client.
	Head string
	// Upstream is the commit the branch's upstream points at, empty when
	// the branch has never been pushed. Set by the client.
	Upstream string
	// Ahead is the number of local commits not on Upstream, set by the
	// client.
	Ahead int
	// SessionID is the Claude Code session that produced the change, set
	// by the client; the server copies it into each upsert's Session.
	SessionID string
	// Upserts are the entries written to the tree, each replacing the
	// entry at its path. Set by the client.
	Upserts []Entry
	// Deletes are the paths removed from the tree, set by the client.
	Deletes []string
	// Full is true on a full reconcile: the tree becomes exactly Upserts,
	// and paths the client no longer sees are dropped. Set by the client.
	Full bool
	// At is when the change was committed, set by the store.
	At time.Time
}
