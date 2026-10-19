// Package codesync collects a worktree's source files and sends them to a
// clued server's code store. This file holds what one worktree's sync
// remembers between hook runs (the state file) and the lock that keeps two
// runs on one worktree from interleaving.
package codesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

// DirName is the directory under the config dir that holds every
// worktree's state and lock file.
const DirName = "codesync"

// StateDir is <configDir>/codesync.
func StateDir(configDir string) string { return filepath.Join(configDir, DirName) }

// ErrBadWorktreeID is returned for a worktree id that WorktreeID could not
// have produced, so it never names a file outside the state directory.
var ErrBadWorktreeID = errors.New("codesync: invalid worktree id")

const (
	worktreePrefix = "wt_"
	worktreeHexLen = 16
)

// WorktreeID names a checkout: "wt_" and the first 16 hex characters of
// SHA-256 of host, a NUL byte and the absolute worktree root. Linked git
// worktrees and other machines get their own id.
func WorktreeID(host, root string) string {
	sum := sha256.Sum256([]byte(host + "\x00" + root))
	return worktreePrefix + hex.EncodeToString(sum[:])[:worktreeHexLen]
}

// ValidWorktreeID reports whether id has the shape WorktreeID produces.
func ValidWorktreeID(id string) bool {
	if len(id) != len(worktreePrefix)+worktreeHexLen || id[:len(worktreePrefix)] != worktreePrefix {
		return false
	}
	for _, c := range id[len(worktreePrefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// StatePath is <dir>/<id>.json.
func StatePath(dir, id string) (string, error) {
	if !ValidWorktreeID(id) {
		return "", ErrBadWorktreeID
	}
	return filepath.Join(dir, id+".json"), nil
}

// LockPath is <dir>/<id>.lock.
func LockPath(dir, id string) (string, error) {
	if !ValidWorktreeID(id) {
		return "", ErrBadWorktreeID
	}
	return filepath.Join(dir, id+".lock"), nil
}

// FileState is the stamp cache entry of one path, relative to the worktree
// root: the (size, mtime) stamp that was hashed, the content hash the server
// stores (empty for a file whose content is not sent), the git facts sent
// with it, and whether the server has it as it stands.
type FileState struct {
	Size     int64     `json:"size"`
	MTime    time.Time `json:"mtime"`
	SHA256   string    `json:"sha256"`
	GitOID   string    `json:"gitOid,omitempty"`
	Dirty    bool      `json:"dirty"`
	Unpushed bool      `json:"unpushed"`
	Synced   bool      `json:"synced"`
}

// Stamp is the entry's (size, mtime) stamp in the form Refused stores.
func (f FileState) Stamp() string { return Stamp(f.Size, f.MTime) }

// Stamp renders a (size, mtime) stamp as "<size>+<mtime in Unix nanoseconds>",
// the value Refused keeps per path.
func Stamp(size int64, mtime time.Time) string {
	return strconv.FormatInt(size, 10) + "+" + strconv.FormatInt(mtime.UnixNano(), 10)
}

// Pending is the work a run left for the next one: paths to upsert and paths
// to delete, each listed once and never in both.
type Pending struct {
	Upserts []string `json:"upserts,omitempty"`
	Deletes []string `json:"deletes,omitempty"`
}

// Empty reports whether nothing is queued.
func (p Pending) Empty() bool { return len(p.Upserts) == 0 && len(p.Deletes) == 0 }

// State is what one worktree's sync remembers between hook runs, stored as
// <dir>/<worktree id>.json. It holds paths, stamps and hashes, never file
// content or a token. A missing file is the zero state.
type State struct {
	Worktree      string               `json:"worktree"`
	Root          string               `json:"root"`
	Project       string               `json:"project"`
	DefaultBranch string               `json:"defaultBranch"`
	Ref           string               `json:"ref"`
	Head          string               `json:"head"`
	Upstream      string               `json:"upstream"`
	Ahead         int                  `json:"ahead"`
	Files         map[string]FileState `json:"files,omitempty"`
	Pending       Pending              `json:"pending"`
	// LastFull is when the last full reconcile finished.
	LastFull time.Time `json:"lastFull,omitzero"`
	// Refused maps a path the server rejected as too large to the stamp it
	// had then; it is retried only when the file's stamp changes.
	Refused map[string]string `json:"refused,omitempty"`
	// UnsupportedUntil is set when the server has no code support, the
	// account no entitlement or no quota left; no sync runs before it.
	UnsupportedUntil time.Time `json:"unsupportedUntil,omitzero"`
}

// FullAfter is how old the last full reconcile may get before a run
// enumerates the whole worktree again.
const FullAfter = 6 * time.Hour

// BackoffFor is how long a refusal (404 on the prefix, 403, 413 quota)
// keeps the sync quiet.
const BackoffFor = 24 * time.Hour

// FullDue reports whether the last full reconcile is missing or older than
// FullAfter at now.
func (s *State) FullDue(now time.Time) bool {
	return s.LastFull.IsZero() || now.Sub(s.LastFull) >= FullAfter
}

// BackedOff reports whether a back-off is still running at now.
func (s *State) BackedOff(now time.Time) bool { return now.Before(s.UnsupportedUntil) }

// BackOff starts a back-off of BackoffFor from now.
func (s *State) BackOff(now time.Time) { s.UnsupportedUntil = now.Add(BackoffFor) }

// QueueUpsert queues path for upload, replacing a queued delete of it.
func (s *State) QueueUpsert(path string) {
	s.Pending.Deletes = remove(s.Pending.Deletes, path)
	if !slices.Contains(s.Pending.Upserts, path) {
		s.Pending.Upserts = append(s.Pending.Upserts, path)
	}
}

// QueueDelete queues path for deletion, replacing a queued upsert of it.
func (s *State) QueueDelete(path string) {
	s.Pending.Upserts = remove(s.Pending.Upserts, path)
	if !slices.Contains(s.Pending.Deletes, path) {
		s.Pending.Deletes = append(s.Pending.Deletes, path)
	}
}

// Dequeue drops path from both queues, once it has been committed.
func (s *State) Dequeue(path string) {
	s.Pending.Upserts = remove(s.Pending.Upserts, path)
	s.Pending.Deletes = remove(s.Pending.Deletes, path)
}

func remove(list []string, path string) []string {
	out := slices.DeleteFunc(list, func(p string) bool { return p == path })
	if len(out) == 0 {
		return nil
	}
	return out
}

// Refuse records that the server rejected path as too large while it had
// stamp.
func (s *State) Refuse(path, stamp string) {
	if s.Refused == nil {
		s.Refused = map[string]string{}
	}
	s.Refused[path] = stamp
}

// IsRefused reports whether path was refused with this same stamp, so its
// content is not sent again. A changed stamp clears the refusal.
func (s *State) IsRefused(path, stamp string) bool {
	old, ok := s.Refused[path]
	if !ok {
		return false
	}
	if old == stamp {
		return true
	}
	delete(s.Refused, path)
	if len(s.Refused) == 0 {
		s.Refused = nil
	}
	return false
}

// LoadState reads the worktree's state from dir; a missing file is the zero
// state.
func LoadState(dir, id string) (State, error) {
	var st State
	path, err := StatePath(dir, id)
	if err != nil {
		return st, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("code sync state %s: %w", path, err)
	}
	return st, nil
}

// SaveState writes the worktree's state into dir (created with mode 0700)
// through a temporary file, mode 0600, in the same directory that is renamed
// over the old one, so a reader never sees a partial file.
func SaveState(dir, id string, st State) error {
	path, err := StatePath(dir, id)
	if err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// RemoveState drops the worktree's state file (WorktreeRemove). A missing
// file is not an error.
func RemoveState(dir, id string) error {
	path, err := StatePath(dir, id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// StaleAfter is how old a lock file must be before another run reclaims it:
// a hook relay that holds a lock for longer has died.
const StaleAfter = 60 * time.Second

// AcquireLock creates <dir>/<id>.lock exclusively (dir is created with mode
// 0700). held reports that another run holds it, and this run should skip;
// a lock older than StaleAfter is removed and taken once. The returned
// release removes the file and is safe to call when held is true.
func AcquireLock(dir, id string) (release func(), held bool, err error) {
	path, err := LockPath(dir, id)
	if err != nil {
		return func() {}, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}, false, err
	}
	for attempt := 0; ; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, false, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return func() {}, false, err
		}
		info, serr := os.Stat(path)
		if serr != nil && !errors.Is(serr, os.ErrNotExist) {
			return func() {}, false, serr
		}
		if attempt > 0 || (serr == nil && time.Since(info.ModTime()) < StaleAfter) {
			return func() {}, true, nil
		}
		// Stale (or gone between the two calls): remove and try once more.
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return func() {}, false, rerr
		}
	}
}
