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

import (
	"fmt"
	"time"
)

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
	// and the ref of every Commit, and drops one only on RemoveRef (a
	// branch deleted on GitHub).
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
// tree of that worktree is gone. A ref tombstone, appended on RemoveRef, is
// the same with the removed Ref set (and no Head): that one tree is gone and
// the ref is no longer among the worktree's Refs. Replaying either as a Full
// change with no upserts gives the same (empty) tree.
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

// Activity event kinds (Event.Kind) the GitHub sync records. The store
// accepts any non-empty kind; these name the allow-listed ones of plan 0011.
const (
	// EventPush is a push to a branch: Ref, Before, Commit (the new tip),
	// Commits, Forced, Actor.
	EventPush = "push"
	// EventBranchCreated and EventBranchDeleted are a branch created or
	// deleted on GitHub: Ref, Commit (created only), Actor.
	EventBranchCreated = "branch_created"
	EventBranchDeleted = "branch_deleted"
	// EventPRPrefix starts the kinds of pull request events ("pr_opened",
	// "pr_synchronize", "pr_closed", "pr_reopened", "pr_ready_for_review"):
	// PR, Ref and Commit (head), BaseRef and BaseCommit, Merged,
	// MergeCommit, Actor, URL.
	EventPRPrefix = "pr_"
	// EventCI is a completed check suite, check run or workflow run, or a
	// commit status: Commit, Name, Status, Conclusion, URL.
	EventCI = "ci"
	// EventRepoPrefix starts the kinds of repository events ("repo_renamed",
	// "repo_transferred", "repo_archived", "repo_deleted"): Repo, Actor.
	EventRepoPrefix = "repo_"
	// EventPlanSync, EventPlanSyncConflict and EventPlanSyncProblem are the
	// outcomes of the plan and story sync from the default branch (see
	// ghsync's DocsEvent): Commit, DocKind, DocID, Path, Action and, for a
	// problem, Problems.
	EventPlanSync         = "plan_sync"
	EventPlanSyncConflict = "plan_sync_conflict"
	EventPlanSyncProblem  = "plan_sync_problem"
)

// MaxEventKindBytes is the longest Event.Kind, in bytes.
const MaxEventKindBytes = 64

// Event is one record of a project's write-only activity log
// (Store.AppendEvents): an allow-listed subset of a GitHub webhook payload
// or of a sync outcome, never the payload itself. Actor logins are kept,
// e-mail addresses never. Every field but Seq is set by the writer (the
// GitHub sync); fields that do not apply to a Kind stay empty.
type Event struct {
	// Seq is the event's position in the project's activity log, assigned
	// by the store on AppendEvents (increasing, starting at 1, its own
	// sequence apart from the change log's); the writer sends 0.
	Seq int64
	// Kind is what happened: one of the Event* kinds above (non-empty, at
	// most MaxEventKindBytes).
	Kind string
	// Delivery is the GitHub webhook delivery id the event came from, empty
	// for an import or a reconcile.
	Delivery string
	// Worktree is the GitHub-backed worktree the event concerns (gh_<id>,
	// see ValidWorktreeID), empty when none applies.
	Worktree string
	// Repo is the repository as owner/repo.
	Repo string
	// Ref is the branch (a push, a branch event, a pull request's head),
	// see ValidRef; empty when none applies.
	Ref string
	// Commit is the commit the event is about (40 or 64 lowercase hex, see
	// ValidGitOID): a push's new tip, a pull request's head, a CI run's
	// commit, the commit a plan sync read.
	Commit string
	// Before is a push's previous tip (all zeros for a new branch).
	Before string
	// Commits is the number of commits a push carried.
	Commits int
	// Forced is true for a force push.
	Forced bool
	// Actor is the GitHub login that caused the event (pusher, sender).
	Actor string
	// PR is the pull request number, 0 when none applies.
	PR int
	// BaseRef and BaseCommit are a pull request's base branch and commit.
	BaseRef    string
	BaseCommit string
	// Merged is true for a pull request closed by a merge, and MergeCommit
	// its merge commit.
	Merged      bool
	MergeCommit string
	// Name is a CI check's, check suite's app or workflow's name, or a
	// commit status's context.
	Name string
	// Status and Conclusion are a CI run's status ("completed") and
	// conclusion ("success", "failure", ...), or a commit status's state in
	// Status.
	Status     string
	Conclusion string
	// URL is the event's page on GitHub (the pull request, the run).
	URL string
	// DocKind is a plan sync event's document kind (plan, story), empty for
	// a problem with the config file.
	DocKind string
	// DocID is the plan or story id, empty when the file names none.
	DocID string
	// Path is a plan sync event's repository path.
	Path string
	// Action is what a plan_sync did: created, updated or archived.
	Action string
	// Problems are a plan_sync_problem's validation messages.
	Problems []string
	// At is when the event happened (or the sync ran), set by the writer;
	// the store sets the time of the append when it is zero.
	At time.Time
}

// Validate checks the fields of e a store relies on, wrapping ErrInvalid:
// Kind is non-empty, at most MaxEventKindBytes of lowercase letters, digits
// and "_"; Worktree, Ref, BaseRef, the commits and Path are empty or well
// formed (ValidWorktreeID, ValidRef, ValidGitOID, ValidPath); Commits and
// PR are not negative.
func (e Event) Validate() error {
	if e.Kind == "" || len(e.Kind) > MaxEventKindBytes {
		return fmt.Errorf("%w: event kind %q", ErrInvalid, e.Kind)
	}
	for i := 0; i < len(e.Kind); i++ {
		if c := e.Kind[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return fmt.Errorf("%w: event kind %q", ErrInvalid, e.Kind)
		}
	}
	if e.Worktree != "" && !ValidWorktreeID(e.Worktree) {
		return fmt.Errorf("%w: event worktree %q", ErrInvalid, e.Worktree)
	}
	for _, r := range []string{e.Ref, e.BaseRef} {
		if r != "" && !ValidRef(r) {
			return fmt.Errorf("%w: event ref %q", ErrInvalid, r)
		}
	}
	for _, c := range []string{e.Commit, e.Before, e.BaseCommit, e.MergeCommit} {
		if c != "" && !ValidGitOID(c) {
			return fmt.Errorf("%w: event commit %q", ErrInvalid, c)
		}
	}
	if e.Path != "" && !ValidPath(e.Path) {
		return fmt.Errorf("%w: event path %q", ErrInvalid, e.Path)
	}
	if e.Commits < 0 || e.PR < 0 {
		return fmt.Errorf("%w: event commits %d, pull request %d", ErrInvalid, e.Commits, e.PR)
	}
	return nil
}
