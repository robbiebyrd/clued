package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// staleLock is how old a lock file may get before another process takes it
// over: its holder is presumed dead (a claim holds the lock for one read and
// one write).
const staleLock = 30 * time.Second

var lockIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// Lock takes an exclusive lock on a document for agents sharing a checkout:
// a lock file created with O_EXCL under the system temp directory, in a
// folder named after the kind's root (lock files never land in the
// repository). It waits until the lock is free or ctx is done, and takes
// over a lock older than staleLock.
func (s *Store) Lock(ctx context.Context, kindName, id string) (func(), error) {
	if !lockIDRe.MatchString(id) {
		return nil, fmt.Errorf("filestore: cannot lock %q", id)
	}
	root, err := s.root(kindName)
	if err != nil {
		return nil, err
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	sum := sha256.Sum256([]byte(root))
	dir := filepath.Join(os.TempDir(), "mind-palace-locks", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, kindName+"-"+id+".lock")
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > staleLock {
			_ = os.Remove(path)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("filestore: %s %s is locked: %w", kindName, id, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
