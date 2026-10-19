// Package codetest is the conformance suite every code.Store must pass, so
// the memory, Mongo and remote stores behave identically. It uses the code
// package API only: no type assertion on the store.
//
// The suite plays the part of server/codeapi where the port expects stamped
// values: it sets Owner on worktrees and changes to Owner, and each upsert's
// Session to its change's SessionID. It also keeps every request inside what
// the transport accepts (a clean entry has a GitOID, an entry of a change
// with no Upstream is Unpushed, a PutWorktree carries one ref, Source is
// left empty), so a store reached over HTTP runs the same cases. Such a store
// has no read route; its test passes a Split whose Reads is the server-side
// store and whose principal's account is Owner.
//
// Times are compared by order only (a store may truncate them, Mongo to the
// millisecond), and nil and empty slices are treated alike.
package codetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
)

// Owner is the account the suite stamps on worktrees and changes. A store
// tested over the transport authenticates with a token of this account.
const Owner = "acct_codetest"

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) code.Store

// Run executes the suite; open must return a fresh, empty store each call.
func Run(t *testing.T, open Factory) {
	cases := []struct {
		name string
		run  func(*testing.T, code.Store)
	}{
		{"an empty store holds nothing", emptyStore},
		{"put blob stores content by its sha256 once and usage counts it", putBlob},
		{"put blob refuses a hash or size that does not match the content", putBlobVerifies},
		{"blob of an unknown sha256 is not found", blobNotFound},
		{"missing blobs returns the unknown hashes in the order given", missingBlobs},
		{"put worktree creates it with its owner and metadata", putWorktreeCreates},
		{"put worktree updates metadata, keeps every ref and clears removed", putWorktreeUpdates},
		{"put worktree refuses a malformed worktree id", putWorktreeInvalidID},
		{"commit to an unknown worktree is not found and changes nothing", commitUnknownWorktree},
		{"commit applies upserts and deletes and assigns increasing seqs from 1", commitUpsertsDeletes},
		{"tree is sorted by path", treeSorted},
		{"an upsert without content needs no blob", upsertWithoutContent},
		{"commit naming a missing blob fails and changes nothing", commitMissingBlob},
		{"a full commit replaces the whole tree of its ref only", commitFull},
		{"refs are isolated per worktree and worktrees from each other", refsIsolated},
		{"entries round-trip git_oid, dirty, unpushed, mode, lang and size", entryRoundTrip},
		{"each entry keeps the session of the change that last wrote it", entrySession},
		{"changes round-trip owner, head, upstream, ahead and session", changeRoundTrip},
		{"the change log is ordered and replayable with changes after seq", changeLogReplay},
		{"a retried commit re-applies its entries and appends at most a duplicate", commitRetry},
		{"commit refuses malformed paths, refs and hashes and changes nothing", commitValidation},
		{"remove worktree marks it removed and appends a tombstone", removeWorktree},
		{"touch session records first and last activity and the end", touchSession},
		{"worktrees lists every worktree with owner, default branch and sessions", listWorktrees},
		{"concurrent commits each get their own seq", concurrentCommits},
		{"stored and returned values are copies", copies},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

// Split is a Store whose writes go to one store and whose reads go to
// another. A store reached over the write-only transport is tested as
// Split{Writes: client, Reads: the server-side store}. MissingBlobs is a
// write-side call (the transport's manifest route).
type Split struct {
	Writes code.Store
	Reads  code.Store
}

var _ code.Store = Split{}

// MissingBlobs asks Writes.
func (s Split) MissingBlobs(ctx context.Context, shas []string) ([]string, error) {
	return s.Writes.MissingBlobs(ctx, shas)
}

// PutBlob writes to Writes.
func (s Split) PutBlob(ctx context.Context, b code.Blob) error { return s.Writes.PutBlob(ctx, b) }

// PutWorktree writes to Writes.
func (s Split) PutWorktree(ctx context.Context, w code.Worktree) error {
	return s.Writes.PutWorktree(ctx, w)
}

// Commit writes to Writes.
func (s Split) Commit(ctx context.Context, c code.Change) (int64, error) {
	return s.Writes.Commit(ctx, c)
}

// RemoveWorktree writes to Writes.
func (s Split) RemoveWorktree(ctx context.Context, id string) error {
	return s.Writes.RemoveWorktree(ctx, id)
}

// TouchSession writes to Writes.
func (s Split) TouchSession(ctx context.Context, worktree, sessionID string, ended bool) error {
	return s.Writes.TouchSession(ctx, worktree, sessionID, ended)
}

// Worktrees reads from Reads.
func (s Split) Worktrees(ctx context.Context) ([]code.Worktree, error) {
	return s.Reads.Worktrees(ctx)
}

// Tree reads from Reads.
func (s Split) Tree(ctx context.Context, worktree, ref string) ([]code.Entry, error) {
	return s.Reads.Tree(ctx, worktree, ref)
}

// Changes reads from Reads.
func (s Split) Changes(ctx context.Context, afterSeq int64, limit int) ([]code.Change, error) {
	return s.Reads.Changes(ctx, afterSeq, limit)
}

// Blob reads from Reads.
func (s Split) Blob(ctx context.Context, sha string) (code.Blob, error) {
	return s.Reads.Blob(ctx, sha)
}

// Usage reads from Reads.
func (s Split) Usage(ctx context.Context) (int64, int64, error) { return s.Reads.Usage(ctx) }

// Fixtures.

const (
	wt1 = "wt_0123456789abcdef"
	wt2 = "wt_fedcba9876543210"
	wt3 = "gh_123456789"

	head     = "1111111111111111111111111111111111111111"
	upstream = "2222222222222222222222222222222222222222"
)

// newBlob returns the blob of content with its hash and size.
func newBlob(content string) code.Blob {
	sum := sha256.Sum256([]byte(content))
	return code.Blob{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)), Content: []byte(content)}
}

// oid returns a 40-hex git object id derived from s.
func oid(s string) string {
	sum := sha256.Sum256([]byte("oid\x00" + s))
	return hex.EncodeToString(sum[:])[:40]
}

// worktree returns a client worktree carrying ref.
func worktree(id, ref string) code.Worktree {
	return code.Worktree{
		ID:            id,
		Owner:         Owner,
		Root:          "/home/dev/src/" + id,
		Host:          "devbox",
		Origin:        "git@github.com:org/repo.git",
		DefaultBranch: "main",
		Refs:          []string{ref},
	}
}

// entry returns a clean, pushed entry for path holding b, written by
// session.
func entry(path string, b code.Blob, session string) code.Entry {
	return code.Entry{
		Path:    path,
		SHA256:  b.SHA256,
		Size:    b.Size,
		Mode:    "100644",
		Lang:    "go",
		GitOID:  oid(path + "\x00" + b.SHA256),
		Session: session,
	}
}

// change returns a change to (wt, ref) by session, pushed at upstream, with
// its upserts' Session stamped as the transport would.
func change(wt, ref, session string, upserts []code.Entry, deletes ...string) code.Change {
	for i := range upserts {
		upserts[i].Session = session
	}
	return code.Change{
		Owner:     Owner,
		Worktree:  wt,
		Ref:       ref,
		Head:      head,
		Upstream:  upstream,
		Ahead:     0,
		SessionID: session,
		Upserts:   upserts,
		Deletes:   deletes,
	}
}

// Helpers.

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got  %#v\n want %#v", what, got, want)
	}
}

func isErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got error %v, want %v", what, err, want)
	}
}

// putBlobs stores each blob.
func putBlobs(t *testing.T, s code.Store, bs ...code.Blob) {
	t.Helper()
	for _, b := range bs {
		must(t, s.PutBlob(t.Context(), b))
	}
}

func putWorktree(t *testing.T, s code.Store, w code.Worktree) {
	t.Helper()
	must(t, s.PutWorktree(t.Context(), w))
}

func commit(t *testing.T, s code.Store, c code.Change) int64 {
	t.Helper()
	seq, err := s.Commit(t.Context(), c)
	must(t, err)
	return seq
}

// tree returns the (worktree, ref) tree with nil slices made empty.
func tree(t *testing.T, s code.Store, wt, ref string) []code.Entry {
	t.Helper()
	es, err := s.Tree(t.Context(), wt, ref)
	must(t, err)
	return normEntries(es)
}

func changes(t *testing.T, s code.Store, after int64, limit int) []code.Change {
	t.Helper()
	cs, err := s.Changes(t.Context(), after, limit)
	must(t, err)
	return cs
}

// allChanges pages through the whole log.
func allChanges(t *testing.T, s code.Store) []code.Change {
	t.Helper()
	return changes(t, s, 0, 1_000_000)
}

func worktrees(t *testing.T, s code.Store) []code.Worktree {
	t.Helper()
	ws, err := s.Worktrees(t.Context())
	must(t, err)
	return ws
}

// findWorktree returns the listed worktree with that id.
func findWorktree(t *testing.T, s code.Store, id string) code.Worktree {
	t.Helper()
	for _, w := range worktrees(t, s) {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("worktree %s is not listed", id)
	return code.Worktree{}
}

func findSession(t *testing.T, w code.Worktree, id string) code.SessionSeen {
	t.Helper()
	for _, s := range w.Sessions {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("worktree %s has no session %s: %#v", w.ID, id, w.Sessions)
	return code.SessionSeen{}
}

func normEntries(es []code.Entry) []code.Entry {
	out := make([]code.Entry, 0, len(es))
	return append(out, es...)
}

// normChange zeroes At (set by the store) and makes empty slices nil.
func normChange(c code.Change) code.Change {
	c.At = time.Time{}
	if len(c.Upserts) == 0 {
		c.Upserts = nil
	} else {
		c.Upserts = append([]code.Entry(nil), c.Upserts...)
	}
	if len(c.Deletes) == 0 {
		c.Deletes = nil
	} else {
		c.Deletes = append([]string(nil), c.Deletes...)
	}
	return c
}

func sortedRefs(refs []string) []string {
	out := append([]string{}, refs...)
	sort.Strings(out)
	return out
}

func seqs(cs []code.Change) []int64 {
	out := make([]int64, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Seq)
	}
	return out
}

// increasing fails unless the seqs strictly increase.
func increasing(t *testing.T, what string, ss []int64) {
	t.Helper()
	for i := 1; i < len(ss); i++ {
		if ss[i] <= ss[i-1] {
			t.Fatalf("%s: seqs not increasing: %v", what, ss)
		}
	}
}

// notBefore fails when got is before want or zero.
func notBefore(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if got.IsZero() {
		t.Fatalf("%s is zero", what)
	}
	if got.Before(want.Truncate(time.Millisecond)) {
		t.Fatalf("%s %v is before %v", what, got, want)
	}
}

// snapshot is a store's whole visible state, to show a refused write
// changed nothing.
type snapshot struct {
	trees   map[string][]code.Entry
	changes []int64
}

func snap(t *testing.T, s code.Store, refs ...[2]string) snapshot {
	t.Helper()
	sn := snapshot{trees: map[string][]code.Entry{}, changes: seqs(allChanges(t, s))}
	for _, r := range refs {
		sn.trees[r[0]+" "+r[1]] = tree(t, s, r[0], r[1])
	}
	return sn
}

// Cases.

func emptyStore(t *testing.T, s code.Store) {
	ctx := t.Context()
	eq(t, "worktrees", len(worktrees(t, s)), 0)
	eq(t, "changes", len(allChanges(t, s)), 0)
	eq(t, "tree", tree(t, s, wt1, "main"), []code.Entry{})
	blobs, bytes, err := s.Usage(ctx)
	must(t, err)
	eq(t, "usage", [2]int64{blobs, bytes}, [2]int64{0, 0})
	a := newBlob("a")
	missing, err := s.MissingBlobs(ctx, []string{a.SHA256})
	must(t, err)
	eq(t, "missing", missing, []string{a.SHA256})
}

func putBlob(t *testing.T, s code.Store) {
	ctx := t.Context()
	a, b := newBlob("package a\n"), newBlob("package b // longer\n")
	putBlobs(t, s, a)
	got, err := s.Blob(ctx, a.SHA256)
	must(t, err)
	eq(t, "sha256", got.SHA256, a.SHA256)
	eq(t, "size", got.Size, a.Size)
	eq(t, "content", string(got.Content), string(a.Content))

	putBlobs(t, s, a, newBlob("package a\n")) // storing a held blob again is a no-op
	blobs, bytes, err := s.Usage(ctx)
	must(t, err)
	eq(t, "usage after re-put", [2]int64{blobs, bytes}, [2]int64{1, a.Size})

	putBlobs(t, s, b)
	blobs, bytes, err = s.Usage(ctx)
	must(t, err)
	eq(t, "usage", [2]int64{blobs, bytes}, [2]int64{2, a.Size + b.Size})

	bin := newBlob("\x00\x01\xff\xfe binary \x00")
	putBlobs(t, s, bin)
	got, err = s.Blob(ctx, bin.SHA256)
	must(t, err)
	eq(t, "binary content", got.Content, bin.Content)
}

func putBlobVerifies(t *testing.T, s code.Store) {
	ctx := t.Context()
	a, b := newBlob("real content"), newBlob("other content")
	bad := []struct {
		name string
		blob code.Blob
	}{
		{"content of another hash", code.Blob{SHA256: a.SHA256, Size: b.Size, Content: b.Content}},
		{"uppercase hash", code.Blob{SHA256: strings.ToUpper(a.SHA256), Size: a.Size, Content: a.Content}},
		{"short hash", code.Blob{SHA256: a.SHA256[:40], Size: a.Size, Content: a.Content}},
		{"empty hash", code.Blob{Size: a.Size, Content: a.Content}},
		{"size larger than content", code.Blob{SHA256: a.SHA256, Size: a.Size + 1, Content: a.Content}},
		{"size smaller than content", code.Blob{SHA256: a.SHA256, Size: a.Size - 1, Content: a.Content}},
	}
	for _, c := range bad {
		isErr(t, c.name, s.PutBlob(ctx, c.blob), code.ErrInvalid)
	}
	missing, err := s.MissingBlobs(ctx, []string{a.SHA256})
	must(t, err)
	eq(t, "missing after refusals", missing, []string{a.SHA256})
	_, err = s.Blob(ctx, a.SHA256)
	isErr(t, "blob after refusals", err, code.ErrNotFound)
	blobs, bytes, err := s.Usage(ctx)
	must(t, err)
	eq(t, "usage after refusals", [2]int64{blobs, bytes}, [2]int64{0, 0})
}

func blobNotFound(t *testing.T, s code.Store) {
	putBlobs(t, s, newBlob("held"))
	_, err := s.Blob(t.Context(), newBlob("not held").SHA256)
	isErr(t, "unknown blob", err, code.ErrNotFound)
}

func missingBlobs(t *testing.T, s code.Store) {
	ctx := t.Context()
	b1, b2, b3, b4 := newBlob("one"), newBlob("two"), newBlob("three"), newBlob("four")
	putBlobs(t, s, b2, b4)
	missing, err := s.MissingBlobs(ctx, []string{b3.SHA256, b2.SHA256, b1.SHA256, b4.SHA256})
	must(t, err)
	eq(t, "missing", missing, []string{b3.SHA256, b1.SHA256})
	missing, err = s.MissingBlobs(ctx, []string{b2.SHA256, b4.SHA256})
	must(t, err)
	eq(t, "none missing", len(missing), 0)
	missing, err = s.MissingBlobs(ctx, nil)
	must(t, err)
	eq(t, "no hashes asked", len(missing), 0)
}

func putWorktreeCreates(t *testing.T, s code.Store) {
	before := time.Now()
	w := worktree(wt1, "main")
	putWorktree(t, s, w)
	ws := worktrees(t, s)
	eq(t, "worktrees", len(ws), 1)
	got := ws[0]
	eq(t, "id", got.ID, wt1)
	eq(t, "owner", got.Owner, Owner)
	eq(t, "root", got.Root, w.Root)
	eq(t, "host", got.Host, w.Host)
	eq(t, "origin", got.Origin, w.Origin)
	eq(t, "default branch", got.DefaultBranch, "main")
	eq(t, "refs", sortedRefs(got.Refs), []string{"main"})
	eq(t, "sessions", len(got.Sessions), 0)
	eq(t, "removed", got.Removed, false)
	notBefore(t, "updated at", got.UpdatedAt, before)

	putWorktree(t, s, worktree(wt3, "main"))
	eq(t, "github-style id", findWorktree(t, s, wt3).ID, wt3)
}

func putWorktreeUpdates(t *testing.T, s code.Store) {
	putWorktree(t, s, worktree(wt1, "main"))
	first := findWorktree(t, s, wt1)
	putBlobs(t, s, newBlob("x"))
	commit(t, s, change(wt1, "feature/x", "s1", []code.Entry{entry("x.go", newBlob("x"), "")}))
	must(t, s.RemoveWorktree(t.Context(), wt1))
	eq(t, "removed", findWorktree(t, s, wt1).Removed, true)

	w := worktree(wt1, "release")
	w.Origin = "https://github.com/org/renamed.git"
	w.DefaultBranch = "trunk"
	w.Root = "/srv/checkout"
	w.Host = "other"
	putWorktree(t, s, w)
	got := findWorktree(t, s, wt1)
	eq(t, "removed after put", got.Removed, false)
	eq(t, "origin", got.Origin, w.Origin)
	eq(t, "default branch", got.DefaultBranch, "trunk")
	eq(t, "root", got.Root, w.Root)
	eq(t, "host", got.Host, w.Host)
	eq(t, "owner", got.Owner, Owner)
	eq(t, "refs", sortedRefs(got.Refs), []string{"feature/x", "main", "release"})
	eq(t, "sessions kept", len(got.Sessions), 1)
	notBefore(t, "updated at", got.UpdatedAt, first.UpdatedAt)
	eq(t, "worktrees", len(worktrees(t, s)), 1)

	putWorktree(t, s, worktree(wt1, "main")) // a ref already known is not added twice
	eq(t, "refs after re-put", sortedRefs(findWorktree(t, s, wt1).Refs), []string{"feature/x", "main", "release"})
}

func putWorktreeInvalidID(t *testing.T, s code.Store) {
	for _, id := range []string{"wt_0123", "wt_0123456789ABCDEF", "gh_0123", "gh_", "../wt_0123456789abcdef", "x"} {
		isErr(t, "put worktree "+id, s.PutWorktree(t.Context(), worktree(id, "main")), code.ErrInvalid)
	}
	eq(t, "worktrees", len(worktrees(t, s)), 0)
}

func commitUnknownWorktree(t *testing.T, s code.Store) {
	ctx := t.Context()
	a := newBlob("a")
	putBlobs(t, s, a)
	putWorktree(t, s, worktree(wt1, "main"))
	_, err := s.Commit(ctx, change(wt2, "main", "s1", []code.Entry{entry("a.go", a, "")}))
	isErr(t, "commit to unknown worktree", err, code.ErrNotFound)
	eq(t, "tree", tree(t, s, wt2, "main"), []code.Entry{})
	eq(t, "changes", len(allChanges(t, s)), 0)
	isErr(t, "touch unknown worktree", s.TouchSession(ctx, wt2, "s1", false), code.ErrNotFound)
	isErr(t, "remove unknown worktree", s.RemoveWorktree(ctx, wt2), code.ErrNotFound)
	eq(t, "changes after remove", len(allChanges(t, s)), 0)
}

func commitUpsertsDeletes(t *testing.T, s code.Store) {
	a, b, b2, c := newBlob("a"), newBlob("b"), newBlob("b v2"), newBlob("c")
	putBlobs(t, s, a, b, b2, c)
	putWorktree(t, s, worktree(wt1, "main"))

	seq1 := commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("a.go", a, ""), entry("b.go", b, ""), entry("c.go", c, "")}))
	eq(t, "first seq", seq1, int64(1))
	eq(t, "tree after first", tree(t, s, wt1, "main"),
		[]code.Entry{entry("a.go", a, "s1"), entry("b.go", b, "s1"), entry("c.go", c, "s1")})

	seq2 := commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("b.go", b2, "")}, "c.go"))
	if seq2 <= seq1 {
		t.Fatalf("second seq %d is not after %d", seq2, seq1)
	}
	eq(t, "tree after second", tree(t, s, wt1, "main"),
		[]code.Entry{entry("a.go", a, "s1"), entry("b.go", b2, "s1")})

	commit(t, s, change(wt1, "main", "s1", nil, "a.go", "never-there.go"))
	eq(t, "tree after delete", tree(t, s, wt1, "main"), []code.Entry{entry("b.go", b2, "s1")})
	eq(t, "seqs", len(allChanges(t, s)), 3)
}

func treeSorted(t *testing.T, s code.Store) {
	b := newBlob("x")
	putBlobs(t, s, b)
	putWorktree(t, s, worktree(wt1, "main"))
	paths := []string{"z.go", "a/b/c.go", "a.go", "A.go", "a/b.go", "a-b.go", "ü/n.go", "a_b.go", "_x", "a/b/c/d.go"}
	var es []code.Entry
	for _, p := range paths {
		es = append(es, entry(p, b, ""))
	}
	commit(t, s, change(wt1, "main", "s1", es[:5]))
	commit(t, s, change(wt1, "main", "s1", es[5:]))
	want := append([]string{}, paths...)
	sort.Strings(want)
	var got []string
	for _, e := range tree(t, s, wt1, "main") {
		got = append(got, e.Path)
	}
	eq(t, "paths", got, want)
}

func upsertWithoutContent(t *testing.T, s code.Store) {
	putWorktree(t, s, worktree(wt1, "main"))
	e := code.Entry{Path: "assets/logo.png", Size: 52_000, Mode: "100644", GitOID: oid("logo")}
	commit(t, s, change(wt1, "main", "s1", []code.Entry{e}))
	e.Session = "s1"
	eq(t, "tree", tree(t, s, wt1, "main"), []code.Entry{e})
}

func commitMissingBlob(t *testing.T, s code.Store) {
	ctx := t.Context()
	a, b, gone := newBlob("a"), newBlob("b"), newBlob("never uploaded")
	putBlobs(t, s, a, b)
	putWorktree(t, s, worktree(wt1, "main"))
	seq1 := commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("a.go", a, "")}))
	before := snap(t, s, [2]string{wt1, "main"})

	_, err := s.Commit(ctx, change(wt1, "main", "s2", []code.Entry{entry("b.go", b, ""), entry("gone.go", gone, "")}, "a.go"))
	isErr(t, "commit with a missing blob", err, code.ErrMissingBlob)
	_, err = s.Commit(ctx, code.Change{Owner: Owner, Worktree: wt1, Ref: "main", Head: head, Upstream: upstream, SessionID: "s2", Full: true,
		Upserts: []code.Entry{entry("gone.go", gone, "s2")}})
	isErr(t, "full commit with a missing blob", err, code.ErrMissingBlob)
	eq(t, "state after refusal", snap(t, s, [2]string{wt1, "main"}), before)

	putBlobs(t, s, gone)
	seq2 := commit(t, s, change(wt1, "main", "s2", []code.Entry{entry("b.go", b, ""), entry("gone.go", gone, "")}, "a.go"))
	if seq2 <= seq1 {
		t.Fatalf("seq after retry %d is not after %d", seq2, seq1)
	}
	eq(t, "tree after upload and retry", tree(t, s, wt1, "main"),
		[]code.Entry{entry("b.go", b, "s2"), entry("gone.go", gone, "s2")})
}

func commitFull(t *testing.T, s code.Store) {
	a, b, b2, c, d := newBlob("a"), newBlob("b"), newBlob("b2"), newBlob("c"), newBlob("d")
	putBlobs(t, s, a, b, b2, c, d)
	putWorktree(t, s, worktree(wt1, "main"))
	commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("a.go", a, ""), entry("b.go", b, ""), entry("c.go", c, "")}))
	commit(t, s, change(wt1, "dev", "s1", []code.Entry{entry("a.go", a, "")}))

	full := change(wt1, "main", "s2", []code.Entry{entry("d.go", d, ""), entry("b.go", b2, "")})
	full.Full = true
	commit(t, s, full)
	eq(t, "tree after full", tree(t, s, wt1, "main"), []code.Entry{entry("b.go", b2, "s2"), entry("d.go", d, "s2")})
	eq(t, "other ref untouched", tree(t, s, wt1, "dev"), []code.Entry{entry("a.go", a, "s1")})

	empty := change(wt1, "main", "s3", nil)
	empty.Full = true
	commit(t, s, empty)
	eq(t, "tree after an empty full", tree(t, s, wt1, "main"), []code.Entry{})
	eq(t, "full flag in the log", allChanges(t, s)[2].Full, true)
}

func refsIsolated(t *testing.T, s code.Store) {
	a, b, c := newBlob("a"), newBlob("b"), newBlob("c")
	putBlobs(t, s, a, b, c)
	putWorktree(t, s, worktree(wt1, "main"))
	putWorktree(t, s, worktree(wt2, "main"))
	detached := "detached@abc1234"

	commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("a.go", a, ""), entry("b.go", b, "")}))
	commit(t, s, change(wt1, "feature/y", "s1", []code.Entry{entry("a.go", a, ""), entry("b.go", b, "")}))
	commit(t, s, change(wt1, "feature/y", "s1", []code.Entry{entry("c.go", c, "")}, "a.go"))
	commit(t, s, change(wt1, detached, "s1", []code.Entry{entry("c.go", c, "")}))
	commit(t, s, change(wt2, "main", "s9", []code.Entry{entry("b.go", c, "")}))

	eq(t, "wt1 main", tree(t, s, wt1, "main"), []code.Entry{entry("a.go", a, "s1"), entry("b.go", b, "s1")})
	eq(t, "wt1 feature", tree(t, s, wt1, "feature/y"), []code.Entry{entry("b.go", b, "s1"), entry("c.go", c, "s1")})
	eq(t, "wt1 detached", tree(t, s, wt1, detached), []code.Entry{entry("c.go", c, "s1")})
	eq(t, "wt2 main", tree(t, s, wt2, "main"), []code.Entry{entry("b.go", c, "s9")})
	eq(t, "unknown ref", tree(t, s, wt1, "nope"), []code.Entry{})
	eq(t, "unknown worktree", tree(t, s, "wt_aaaaaaaaaaaaaaaa", "main"), []code.Entry{})

	eq(t, "wt1 refs", sortedRefs(findWorktree(t, s, wt1).Refs), []string{detached, "feature/y", "main"})
	eq(t, "wt2 refs", sortedRefs(findWorktree(t, s, wt2).Refs), []string{"main"})
}

func entryRoundTrip(t *testing.T, s code.Store) {
	a, b, c, d := newBlob("clean"), newBlob("dirty"), newBlob("untracked"), newBlob("script")
	putBlobs(t, s, a, b, c, d)
	putWorktree(t, s, worktree(wt1, "main"))
	oid64 := strings.Repeat("ab", 32)
	es := []code.Entry{
		// committed and pushed
		{Path: "clean.go", SHA256: a.SHA256, Size: a.Size, Mode: "100644", Lang: "go", GitOID: oid("clean")},
		// modified in the working tree
		{Path: "dirty.ts", SHA256: b.SHA256, Size: b.Size + 7, Mode: "100644", Lang: "typescript", GitOID: oid("dirty"), Dirty: true, Unpushed: true},
		// untracked: no git_oid
		{Path: "new/untracked.py", SHA256: c.SHA256, Size: c.Size, Mode: "100644", Lang: "python", Dirty: true, Unpushed: true},
		// committed, not pushed, SHA-256 repository
		{Path: "run.sh", SHA256: d.SHA256, Size: d.Size, Mode: "100755", GitOID: oid64, Unpushed: true},
		// symlink, no content sent, unknown language
		{Path: "link", Size: 9, Mode: "120000", GitOID: oid("link")},
	}
	commit(t, s, change(wt1, "main", "s1", append([]code.Entry(nil), es...)))
	want := append([]code.Entry(nil), es...)
	for i := range want {
		want[i].Session = "s1"
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
	eq(t, "tree", tree(t, s, wt1, "main"), want)

	logged := allChanges(t, s)[0].Upserts
	sort.Slice(logged, func(i, j int) bool { return logged[i].Path < logged[j].Path })
	eq(t, "upserts in the log", normEntries(logged), want)
}

func entrySession(t *testing.T, s code.Store) {
	a, b, b2 := newBlob("a"), newBlob("b"), newBlob("b2")
	putBlobs(t, s, a, b, b2)
	putWorktree(t, s, worktree(wt1, "main"))
	commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("a.go", a, ""), entry("b.go", b, "")}))
	commit(t, s, change(wt1, "main", "s2", []code.Entry{entry("b.go", b2, "")}))
	eq(t, "tree", tree(t, s, wt1, "main"), []code.Entry{entry("a.go", a, "s1"), entry("b.go", b2, "s2")})

	commit(t, s, change(wt1, "main", "s3", nil, "b.go"))
	eq(t, "a delete leaves other sessions' entries", tree(t, s, wt1, "main"), []code.Entry{entry("a.go", a, "s1")})

	full := change(wt1, "main", "s4", []code.Entry{entry("a.go", a, "")})
	full.Full = true
	commit(t, s, full)
	eq(t, "full commit rewrites the session", tree(t, s, wt1, "main"), []code.Entry{entry("a.go", a, "s4")})
}

func changeRoundTrip(t *testing.T, s code.Store) {
	a, b := newBlob("a"), newBlob("b")
	putBlobs(t, s, a, b)
	putWorktree(t, s, worktree(wt1, "main"))
	before := time.Now()

	unpushedEntry := entry("a.go", a, "")
	unpushedEntry.Unpushed = true
	neverPushed := change(wt1, "topic", "s1", []code.Entry{unpushedEntry})
	neverPushed.Upstream = ""
	neverPushed.Ahead = 0
	neverPushed.Head = strings.Repeat("c", 64)

	pushed := change(wt1, "main", "s2", []code.Entry{entry("b.go", b, "")}, "old.go")
	pushed.Ahead = 3

	seq1 := commit(t, s, neverPushed)
	seq2 := commit(t, s, pushed)
	cs := allChanges(t, s)
	eq(t, "changes", len(cs), 2)
	for i, want := range []code.Change{neverPushed, pushed} {
		notBefore(t, fmt.Sprintf("change %d at", i), cs[i].At, before)
		want.Seq = []int64{seq1, seq2}[i]
		eq(t, fmt.Sprintf("change %d", i), normChange(cs[i]), normChange(want))
	}
	if cs[1].At.Before(cs[0].At) {
		t.Fatalf("second change at %v is before the first at %v", cs[1].At, cs[0].At)
	}
}

func changeLogReplay(t *testing.T, s code.Store) {
	a, b, c, d := newBlob("a"), newBlob("b"), newBlob("c"), newBlob("d")
	putBlobs(t, s, a, b, c, d)
	putWorktree(t, s, worktree(wt1, "main"))
	putWorktree(t, s, worktree(wt2, "main"))
	full := change(wt1, "main", "s2", []code.Entry{entry("d.go", d, ""), entry("a.go", b, "")})
	full.Full = true
	cs := []code.Change{
		change(wt1, "main", "s1", []code.Entry{entry("a.go", a, ""), entry("b.go", b, "")}),
		change(wt2, "main", "s9", []code.Entry{entry("x.go", c, "")}),
		change(wt1, "dev", "s1", []code.Entry{entry("c.go", c, "")}),
		change(wt1, "main", "s1", []code.Entry{entry("c.go", c, "")}, "b.go"),
		full,
		change(wt1, "dev", "s3", nil, "c.go"),
		change(wt1, "main", "s3", []code.Entry{entry("e/f.go", a, "")}),
	}
	var assigned []int64
	for _, c := range cs {
		assigned = append(assigned, commit(t, s, c))
	}
	eq(t, "first seq", assigned[0], int64(1))
	increasing(t, "assigned", assigned)

	all := allChanges(t, s)
	eq(t, "log seqs", seqs(all), assigned)
	for i := range cs {
		want := cs[i]
		want.Seq = assigned[i]
		eq(t, fmt.Sprintf("change %d", i), normChange(all[i]), normChange(want))
	}

	eq(t, "after the third", seqs(changes(t, s, assigned[2], 100)), assigned[3:])
	eq(t, "limited", seqs(changes(t, s, 0, 2)), assigned[:2])
	eq(t, "after the last", len(changes(t, s, assigned[len(assigned)-1], 100)), 0)

	var paged []code.Change
	for after := int64(0); ; {
		page := changes(t, s, after, 3)
		if len(page) == 0 {
			break
		}
		if len(page) > 3 {
			t.Fatalf("page of %d changes exceeds limit 3", len(page))
		}
		paged = append(paged, page...)
		after = page[len(page)-1].Seq
	}
	eq(t, "paged seqs", seqs(paged), assigned)

	// Replaying the log rebuilds every tree.
	trees := map[[2]string]map[string]code.Entry{}
	for _, c := range paged {
		k := [2]string{c.Worktree, c.Ref}
		if c.Full || trees[k] == nil {
			trees[k] = map[string]code.Entry{}
		}
		for _, p := range c.Deletes {
			delete(trees[k], p)
		}
		for _, e := range c.Upserts {
			trees[k][e.Path] = e
		}
	}
	eq(t, "replayed trees", len(trees), 3)
	for k, files := range trees {
		want := []code.Entry{}
		for _, e := range files {
			want = append(want, e)
		}
		sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
		eq(t, "replayed "+k[0]+" "+k[1], tree(t, s, k[0], k[1]), want)
	}
}

func commitRetry(t *testing.T, s code.Store) {
	a, b, b2 := newBlob("a"), newBlob("b"), newBlob("b2")
	putBlobs(t, s, a, b, b2)
	putWorktree(t, s, worktree(wt1, "main"))
	commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("b.go", b, ""), entry("old.go", a, "")}))

	c := change(wt1, "main", "s2", []code.Entry{entry("a.go", a, ""), entry("b.go", b2, "")}, "old.go")
	seq1 := commit(t, s, c)
	want := tree(t, s, wt1, "main")
	seq2 := commit(t, s, c)
	if seq2 < seq1 {
		t.Fatalf("retried seq %d is before %d", seq2, seq1)
	}
	eq(t, "tree after retry", tree(t, s, wt1, "main"), want)
	eq(t, "tree", want, []code.Entry{entry("a.go", a, "s2"), entry("b.go", b2, "s2")})

	all := allChanges(t, s)
	if n := len(all); n != 2 && n != 3 {
		t.Fatalf("log holds %d changes after a retry, want 2 or 3", n)
	}
	for _, logged := range all[1:] {
		want := c
		want.Seq = logged.Seq
		eq(t, "logged retry", normChange(logged), normChange(want))
	}
}

func commitValidation(t *testing.T, s code.Store) {
	ctx := t.Context()
	a := newBlob("a")
	putBlobs(t, s, a)
	putWorktree(t, s, worktree(wt1, "main"))
	commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("a.go", a, "")}))
	before := snap(t, s, [2]string{wt1, "main"})

	withPath := func(p string) code.Change {
		return change(wt1, "main", "s2", []code.Entry{entry(p, a, "")})
	}
	withEntry := func(edit func(*code.Entry)) code.Change {
		e := entry("ok.go", a, "")
		edit(&e)
		return change(wt1, "main", "s2", []code.Entry{e})
	}
	withChange := func(edit func(*code.Change)) code.Change {
		c := change(wt1, "main", "s2", []code.Entry{entry("ok.go", a, "")})
		edit(&c)
		return c
	}
	bad := []struct {
		name string
		c    code.Change
	}{
		{"empty path", withPath("")},
		{"parent path", withPath("../escape.go")},
		{"parent inside path", withPath("a/../../escape.go")},
		{"absolute path", withPath("/etc/passwd")},
		{"dot path", withPath(".")},
		{"dot element", withPath("a/./b.go")},
		{"doubled slash", withPath("a//b.go")},
		{"trailing slash", withPath("dir/")},
		{"backslash", withPath(`a\b.go`)},
		{"NUL", withPath("a\x00b.go")},
		{"invalid UTF-8", withPath("a\xffb.go")},
		{"too long path", withPath(strings.Repeat("a", code.MaxPathBytes+1))},
		{"malformed delete", change(wt1, "main", "s2", nil, "../escape.go")},
		{"absolute delete", change(wt1, "main", "s2", nil, "/a.go")},
		{"uppercase sha256", withEntry(func(e *code.Entry) { e.SHA256 = strings.ToUpper(e.SHA256) })},
		{"short sha256", withEntry(func(e *code.Entry) { e.SHA256 = e.SHA256[:63] })},
		{"malformed git oid", withEntry(func(e *code.Entry) { e.GitOID = "xyz" })},
		{"empty ref", withChange(func(c *code.Change) { c.Ref = "" })},
		{"ref with ..", withChange(func(c *code.Change) { c.Ref = "bad..ref" })},
		{"ref with space", withChange(func(c *code.Change) { c.Ref = "bad ref" })},
		{"detached with short sha", withChange(func(c *code.Change) { c.Ref = "detached@abc" })},
		{"malformed head", withChange(func(c *code.Change) { c.Head = "HEAD" })},
		{"malformed upstream", withChange(func(c *code.Change) { c.Upstream = upstream[:39] + "G" })},
	}
	for _, c := range bad {
		_, err := s.Commit(ctx, c.c)
		isErr(t, c.name, err, code.ErrInvalid)
	}
	eq(t, "state after refusals", snap(t, s, [2]string{wt1, "main"}), before)

	// The longest valid path is accepted.
	long := strings.Repeat("a", code.MaxPathBytes)
	commit(t, s, withPath(long))
	if es := tree(t, s, wt1, "main"); len(es) != 2 || es[1].Path != long {
		t.Fatalf("longest valid path not stored: %d entries", len(es))
	}
}

func removeWorktree(t *testing.T, s code.Store) {
	ctx := t.Context()
	a := newBlob("a")
	putBlobs(t, s, a)
	putWorktree(t, s, worktree(wt1, "main"))
	putWorktree(t, s, worktree(wt2, "main"))
	commit(t, s, change(wt1, "main", "s1", []code.Entry{entry("a.go", a, "")}))
	last := commit(t, s, change(wt1, "dev", "s1", []code.Entry{entry("a.go", a, "")}))
	before := time.Now()

	must(t, s.RemoveWorktree(ctx, wt1))
	got := findWorktree(t, s, wt1)
	eq(t, "removed", got.Removed, true)
	eq(t, "owner kept", got.Owner, Owner)
	eq(t, "other worktree", findWorktree(t, s, wt2).Removed, false)
	eq(t, "removed worktrees are listed", len(worktrees(t, s)), 2)

	all := allChanges(t, s)
	eq(t, "changes", len(all), 3)
	tomb := all[2]
	if tomb.Seq <= last {
		t.Fatalf("tombstone seq %d is not after %d", tomb.Seq, last)
	}
	notBefore(t, "tombstone at", tomb.At, before)
	eq(t, "tombstone", normChange(tomb), code.Change{Seq: tomb.Seq, Owner: Owner, Worktree: wt1, Full: true})

	isErr(t, "remove unknown", s.RemoveWorktree(ctx, "wt_aaaaaaaaaaaaaaaa"), code.ErrNotFound)
	eq(t, "changes after unknown remove", len(allChanges(t, s)), 3)
}

func touchSession(t *testing.T, s code.Store) {
	ctx := t.Context()
	isErr(t, "touch unknown", s.TouchSession(ctx, wt1, "s1", false), code.ErrNotFound)
	putWorktree(t, s, worktree(wt1, "main"))
	before := time.Now()

	must(t, s.TouchSession(ctx, wt1, "s1", false))
	w := findWorktree(t, s, wt1)
	eq(t, "sessions", len(w.Sessions), 1)
	started := findSession(t, w, "s1")
	notBefore(t, "first at", started.FirstAt, before)
	notBefore(t, "last at", started.LastAt, started.FirstAt)
	eq(t, "ended", started.Ended, false)

	must(t, s.TouchSession(ctx, wt1, "s1", false))
	must(t, s.TouchSession(ctx, wt1, "s1", true))
	ended := findSession(t, findWorktree(t, s, wt1), "s1")
	eq(t, "ended after end", ended.Ended, true)
	if !ended.FirstAt.Equal(started.FirstAt) {
		t.Fatalf("first at moved from %v to %v", started.FirstAt, ended.FirstAt)
	}
	notBefore(t, "last at after end", ended.LastAt, started.LastAt)

	must(t, s.TouchSession(ctx, wt1, "s2", false))
	a := newBlob("a")
	putBlobs(t, s, a)
	commit(t, s, change(wt1, "main", "s3", []code.Entry{entry("a.go", a, "")}))
	w = findWorktree(t, s, wt1)
	eq(t, "sessions after commit", len(w.Sessions), 3)
	eq(t, "s1 still ended", findSession(t, w, "s1").Ended, true)
	eq(t, "s2 open", findSession(t, w, "s2").Ended, false)
	committed := findSession(t, w, "s3")
	eq(t, "s3 open", committed.Ended, false)
	notBefore(t, "s3 first at", committed.FirstAt, before)
	notBefore(t, "s3 last at", committed.LastAt, committed.FirstAt)

	// A commit by a session already seen moves its LastAt and keeps FirstAt.
	commit(t, s, change(wt1, "main", "s2", []code.Entry{entry("a.go", a, "")}))
	s2 := findSession(t, findWorktree(t, s, wt1), "s2")
	if !s2.FirstAt.Equal(findSession(t, w, "s2").FirstAt) {
		t.Fatalf("s2 first at moved on commit")
	}
	notBefore(t, "s2 last at after commit", s2.LastAt, findSession(t, w, "s2").LastAt)
	eq(t, "sessions not duplicated", len(findWorktree(t, s, wt1).Sessions), 3)
}

func listWorktrees(t *testing.T, s code.Store) {
	ctx := t.Context()
	w1 := worktree(wt1, "main")
	w2 := worktree(wt2, "feature/z")
	w2.DefaultBranch = "master"
	w2.Host = "laptop"
	w3 := worktree(wt3, "main")
	w3.DefaultBranch = ""
	for _, w := range []code.Worktree{w1, w2, w3} {
		putWorktree(t, s, w)
	}
	must(t, s.TouchSession(ctx, wt1, "s1", false))
	must(t, s.TouchSession(ctx, wt2, "s2", true))
	must(t, s.RemoveWorktree(ctx, wt3))

	ws := worktrees(t, s)
	var ids []string
	for _, w := range ws {
		ids = append(ids, w.ID)
	}
	sort.Strings(ids)
	eq(t, "ids", ids, []string{wt3, wt1, wt2})

	for _, want := range []struct {
		w        code.Worktree
		sessions []string
		removed  bool
	}{{w1, []string{"s1"}, false}, {w2, []string{"s2"}, false}, {w3, nil, true}} {
		got := findWorktree(t, s, want.w.ID)
		eq(t, want.w.ID+" owner", got.Owner, Owner)
		eq(t, want.w.ID+" default branch", got.DefaultBranch, want.w.DefaultBranch)
		eq(t, want.w.ID+" host", got.Host, want.w.Host)
		eq(t, want.w.ID+" root", got.Root, want.w.Root)
		eq(t, want.w.ID+" origin", got.Origin, want.w.Origin)
		eq(t, want.w.ID+" refs", sortedRefs(got.Refs), want.w.Refs)
		eq(t, want.w.ID+" removed", got.Removed, want.removed)
		var sessions []string
		for _, ss := range got.Sessions {
			sessions = append(sessions, ss.ID)
		}
		eq(t, want.w.ID+" sessions", sessions, want.sessions)
	}
	eq(t, "s2 ended", findSession(t, findWorktree(t, s, wt2), "s2").Ended, true)
}

func concurrentCommits(t *testing.T, s code.Store) {
	const writers, each = 6, 5
	a := newBlob("a")
	putBlobs(t, s, a)
	putWorktree(t, s, worktree(wt1, "main"))

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		got  []int64
		errs []error
	)
	for w := range writers {
		wg.Go(func() {
			for i := range each {
				p := fmt.Sprintf("w%d/f%d.go", w, i)
				seq, err := s.Commit(t.Context(), change(wt1, "main", fmt.Sprintf("s%d", w), []code.Entry{entry(p, a, "")}))
				mu.Lock()
				got = append(got, seq)
				if err != nil {
					errs = append(errs, err)
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent commits failed: %v", errs)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	increasing(t, "assigned seqs", got)
	eq(t, "log seqs", seqs(allChanges(t, s)), got)
	eq(t, "tree size", len(tree(t, s, wt1, "main")), writers*each)
}

func copies(t *testing.T, s code.Store) {
	ctx := t.Context()
	a, b := newBlob("original"), newBlob("b")
	in := code.Blob{SHA256: a.SHA256, Size: a.Size, Content: append([]byte(nil), a.Content...)}
	must(t, s.PutBlob(ctx, in))
	in.Content[0] = 'X'
	got, err := s.Blob(ctx, a.SHA256)
	must(t, err)
	eq(t, "stored blob", string(got.Content), "original")
	got.Content[0] = 'Y'
	again, err := s.Blob(ctx, a.SHA256)
	must(t, err)
	eq(t, "returned blob", string(again.Content), "original")

	putBlobs(t, s, b)
	w := worktree(wt1, "main")
	putWorktree(t, s, w)
	w.Refs[0] = "mutated"
	c := change(wt1, "main", "s1", []code.Entry{entry("a.go", a, ""), entry("b.go", b, "")}, "gone.go")
	commit(t, s, c)
	c.Upserts[0].Path = "mutated.go"
	c.Deletes[0] = "mutated.go"
	want := []code.Entry{entry("a.go", a, "s1"), entry("b.go", b, "s1")}
	eq(t, "stored tree", tree(t, s, wt1, "main"), want)
	eq(t, "stored refs", sortedRefs(findWorktree(t, s, wt1).Refs), []string{"main"})
	eq(t, "stored deletes", allChanges(t, s)[0].Deletes, []string{"gone.go"})

	es, err := s.Tree(ctx, wt1, "main")
	must(t, err)
	es[0].Path = "mutated.go"
	eq(t, "returned tree", tree(t, s, wt1, "main"), want)

	cs := allChanges(t, s)
	cs[0].Upserts[0].Path = "mutated.go"
	cs[0].Deletes[0] = "mutated.go"
	eq(t, "returned change", normChange(allChanges(t, s)[0]).Upserts[0].Path, "a.go")
	eq(t, "returned deletes", allChanges(t, s)[0].Deletes, []string{"gone.go"})

	must(t, s.TouchSession(ctx, wt1, "s1", false))
	ws := worktrees(t, s)
	ws[0].Refs[0] = "mutated"
	ws[0].Sessions[0].ID = "mutated"
	got1 := findWorktree(t, s, wt1)
	eq(t, "returned refs", sortedRefs(got1.Refs), []string{"main"})
	eq(t, "returned sessions", got1.Sessions[0].ID, "s1")
}
