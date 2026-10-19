// Package memcode is the in-memory code.Store: blobs, trees, worktrees and
// the change log of one namespaced project held in maps behind a mutex. It
// backs tests (transport, client, indexer) and `server run --store memory`;
// nothing survives the process.
//
// Like every code.Store it persists what it is given: owner and session
// stamping happen before it (server/codeapi). It validates its input itself
// (paths, refs, hashes, worktree ids, blob size and hash) and copies every
// slice on the way in and out, so callers never share its state.
package memcode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
)

// Store is an in-memory code.Store, safe for concurrent use. The zero value
// is not usable; call New.
type Store struct {
	mu        sync.Mutex
	blobs     map[string][]byte
	blobBytes int64
	worktrees map[string]*code.Worktree
	trees     map[treeKey]map[string]code.Entry
	log       []code.Change
	now       func() time.Time
}

// treeKey names one (worktree, ref) tree.
type treeKey struct {
	worktree string
	ref      string
}

var _ code.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store {
	return &Store{
		blobs:     map[string][]byte{},
		worktrees: map[string]*code.Worktree{},
		trees:     map[treeKey]map[string]code.Entry{},
		now:       time.Now,
	}
}

// MissingBlobs returns the hashes in shas the store does not hold, in the
// order given.
func (s *Store) MissingBlobs(ctx context.Context, shas []string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var missing []string
	for _, sha := range shas {
		if _, ok := s.blobs[sha]; !ok {
			missing = append(missing, sha)
		}
	}
	return missing, nil
}

// PutBlob stores b after checking its hash and size; a blob already held is
// a no-op.
func (s *Store) PutBlob(ctx context.Context, b code.Blob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !code.ValidSHA256(b.SHA256) {
		return fmt.Errorf("%w: blob sha256 %q", code.ErrInvalid, b.SHA256)
	}
	if b.Size != int64(len(b.Content)) {
		return fmt.Errorf("%w: blob size %d is not its length %d", code.ErrInvalid, b.Size, len(b.Content))
	}
	sum := sha256.Sum256(b.Content)
	if hex.EncodeToString(sum[:]) != b.SHA256 {
		return fmt.Errorf("%w: blob content does not hash to %s", code.ErrInvalid, b.SHA256)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.blobs[b.SHA256]; ok {
		return nil
	}
	s.blobs[b.SHA256] = bytes.Clone(b.Content)
	s.blobBytes += b.Size
	return nil
}

// PutWorktree creates or updates the worktree's metadata, adds w.Refs to the
// refs it knows and clears Removed.
func (s *Store) PutWorktree(ctx context.Context, w code.Worktree) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !code.ValidWorktreeID(w.ID) {
		return fmt.Errorf("%w: worktree id %q", code.ErrInvalid, w.ID)
	}
	for _, ref := range w.Refs {
		if !code.ValidRef(ref) {
			return fmt.Errorf("%w: ref %q", code.ErrInvalid, ref)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.worktrees[w.ID]
	if !ok {
		cur = &code.Worktree{ID: w.ID}
		s.worktrees[w.ID] = cur
	}
	cur.Owner = w.Owner
	cur.Source = w.Source
	cur.Root = w.Root
	cur.Host = w.Host
	cur.Origin = w.Origin
	cur.DefaultBranch = w.DefaultBranch
	for _, ref := range w.Refs {
		addRef(cur, ref)
	}
	cur.Removed = false
	cur.UpdatedAt = s.now()
	return nil
}

// Commit validates c, applies it to the (c.Worktree, c.Ref) tree and appends
// it to the log, returning its Seq.
func (s *Store) Commit(ctx context.Context, c code.Change) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validChange(c); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.worktrees[c.Worktree]
	if !ok {
		return 0, fmt.Errorf("%w: worktree %s", code.ErrNotFound, c.Worktree)
	}
	for _, e := range c.Upserts {
		if e.SHA256 == "" {
			continue
		}
		if _, held := s.blobs[e.SHA256]; !held {
			return 0, fmt.Errorf("%w: %s (%s)", code.ErrMissingBlob, e.SHA256, e.Path)
		}
	}

	now := s.now()
	k := treeKey{c.Worktree, c.Ref}
	t := s.trees[k]
	if c.Full || t == nil {
		t = map[string]code.Entry{}
		s.trees[k] = t
	}
	if !c.Full {
		for _, p := range c.Deletes {
			delete(t, p)
		}
	}
	for _, e := range c.Upserts {
		t[e.Path] = e
	}
	if len(t) == 0 {
		delete(s.trees, k)
	}

	addRef(w, c.Ref)
	if c.SessionID != "" {
		touch(w, c.SessionID, false, now)
	}
	w.UpdatedAt = now

	logged := copyChange(c)
	logged.Seq = int64(len(s.log)) + 1
	logged.At = now
	s.log = append(s.log, logged)
	return logged.Seq, nil
}

// RemoveWorktree marks the worktree removed, drops its trees and appends a
// tombstone change.
func (s *Store) RemoveWorktree(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !code.ValidWorktreeID(id) {
		return fmt.Errorf("%w: worktree id %q", code.ErrInvalid, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.worktrees[id]
	if !ok {
		return fmt.Errorf("%w: worktree %s", code.ErrNotFound, id)
	}
	now := s.now()
	for k := range s.trees {
		if k.worktree == id {
			delete(s.trees, k)
		}
	}
	w.Removed = true
	w.UpdatedAt = now
	s.log = append(s.log, code.Change{
		Seq:      int64(len(s.log)) + 1,
		Owner:    w.Owner,
		Worktree: id,
		Full:     true,
		At:       now,
	})
	return nil
}

// TouchSession records activity of sessionID on the worktree.
func (s *Store) TouchSession(ctx context.Context, worktree, sessionID string, ended bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !code.ValidWorktreeID(worktree) {
		return fmt.Errorf("%w: worktree id %q", code.ErrInvalid, worktree)
	}
	if sessionID == "" {
		return fmt.Errorf("%w: empty session id", code.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.worktrees[worktree]
	if !ok {
		return fmt.Errorf("%w: worktree %s", code.ErrNotFound, worktree)
	}
	now := s.now()
	touch(w, sessionID, ended, now)
	w.UpdatedAt = now
	return nil
}

// Worktrees lists every worktree, removed ones included, sorted by ID.
func (s *Store) Worktrees(ctx context.Context) ([]code.Worktree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]code.Worktree, 0, len(s.worktrees))
	for _, w := range s.worktrees {
		c := *w
		c.Refs = slices.Clone(w.Refs)
		c.Sessions = slices.Clone(w.Sessions)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Tree returns the entries of the (worktree, ref) tree sorted by Path.
func (s *Store) Tree(ctx context.Context, worktree, ref string) ([]code.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.trees[treeKey{worktree, ref}]
	out := make([]code.Entry, 0, len(t))
	for _, e := range t {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Changes returns up to limit changes with Seq greater than afterSeq, in Seq
// order; a limit of zero or less returns none.
func (s *Store) Changes(ctx context.Context, afterSeq int64, limit int) ([]code.Change, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Seqs are dense from 1, so the change after afterSeq is at index afterSeq.
	start := max(afterSeq, 0)
	if limit <= 0 || start >= int64(len(s.log)) {
		return nil, nil
	}
	end := min(start+int64(limit), int64(len(s.log)))
	out := make([]code.Change, 0, end-start)
	for _, c := range s.log[start:end] {
		out = append(out, copyChange(c))
	}
	return out, nil
}

// Blob returns the blob with that SHA-256, or code.ErrNotFound.
func (s *Store) Blob(ctx context.Context, sha string) (code.Blob, error) {
	if err := ctx.Err(); err != nil {
		return code.Blob{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	content, ok := s.blobs[sha]
	if !ok {
		return code.Blob{}, fmt.Errorf("%w: blob %s", code.ErrNotFound, sha)
	}
	return code.Blob{SHA256: sha, Size: int64(len(content)), Content: bytes.Clone(content)}, nil
}

// Usage returns the number of blobs held and their total size in bytes.
func (s *Store) Usage(ctx context.Context) (int64, int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.blobs)), s.blobBytes, nil
}

// validChange checks every client-set field of c the store relies on.
func validChange(c code.Change) error {
	if !code.ValidWorktreeID(c.Worktree) {
		return fmt.Errorf("%w: worktree id %q", code.ErrInvalid, c.Worktree)
	}
	if !code.ValidRef(c.Ref) {
		return fmt.Errorf("%w: ref %q", code.ErrInvalid, c.Ref)
	}
	if c.Head != "" && !code.ValidGitOID(c.Head) {
		return fmt.Errorf("%w: head %q", code.ErrInvalid, c.Head)
	}
	if c.Upstream != "" && !code.ValidGitOID(c.Upstream) {
		return fmt.Errorf("%w: upstream %q", code.ErrInvalid, c.Upstream)
	}
	if c.Ahead < 0 {
		return fmt.Errorf("%w: ahead %d", code.ErrInvalid, c.Ahead)
	}
	for _, e := range c.Upserts {
		if !code.ValidPath(e.Path) {
			return fmt.Errorf("%w: path %q", code.ErrInvalid, e.Path)
		}
		if e.SHA256 != "" && !code.ValidSHA256(e.SHA256) {
			return fmt.Errorf("%w: sha256 %q of %s", code.ErrInvalid, e.SHA256, e.Path)
		}
		if e.GitOID != "" && !code.ValidGitOID(e.GitOID) {
			return fmt.Errorf("%w: git oid %q of %s", code.ErrInvalid, e.GitOID, e.Path)
		}
		if e.Size < 0 {
			return fmt.Errorf("%w: size %d of %s", code.ErrInvalid, e.Size, e.Path)
		}
	}
	for _, p := range c.Deletes {
		if !code.ValidPath(p) {
			return fmt.Errorf("%w: deleted path %q", code.ErrInvalid, p)
		}
	}
	return nil
}

// copyChange returns c with its slices copied.
func copyChange(c code.Change) code.Change {
	c.Upserts = slices.Clone(c.Upserts)
	c.Deletes = slices.Clone(c.Deletes)
	return c
}

// addRef adds ref to the worktree's refs unless it is already known.
func addRef(w *code.Worktree, ref string) {
	if !slices.Contains(w.Refs, ref) {
		w.Refs = append(w.Refs, ref)
	}
}

// touch records activity of the session at now: FirstAt on first sight,
// LastAt always, Ended once ended is reported.
func touch(w *code.Worktree, id string, ended bool, now time.Time) {
	i := slices.IndexFunc(w.Sessions, func(ss code.SessionSeen) bool { return ss.ID == id })
	if i < 0 {
		w.Sessions = append(w.Sessions, code.SessionSeen{ID: id, FirstAt: now})
		i = len(w.Sessions) - 1
	}
	w.Sessions[i].LastAt = now
	if ended {
		w.Sessions[i].Ended = true
	}
}
