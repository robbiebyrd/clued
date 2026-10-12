package catchup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session/tail"
)

// state is what one session's catch-up remembers between hooks, stored as
// <stateDir>/<sid>.json. A missing file is the zero state.
type state struct {
	Offset    int64                     `json:"offset"`
	Seq       int                       `json:"seq"`
	Artifacts map[string]tail.FileStamp `json:"artifacts,omitempty"`
	// Refused lists artifacts the store rejected as too large; they keep
	// their stamp in Artifacts so they are retried only when the file changes.
	Refused map[string]bool `json:"refused,omitempty"`
}

func loadState(path string) (state, error) {
	var st state
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, fmt.Errorf("catch-up state %s: %w", path, err)
	}
	return st, nil
}

// saveState writes the state through a temporary file in the same directory
// and renames it over the old one, so a reader never sees a partial file.
func saveState(path string, st state) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
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

// staleAfter is how old a lock file must be before another run reclaims it:
// a hook relay that holds a lock for longer has died.
const staleAfter = 60 * time.Second

// acquireLock creates the lock file exclusively. held reports that another
// run holds it; a lock older than staleAfter is removed and taken once. The
// returned release removes the file and is safe to call when held is true.
func acquireLock(path string) (release func(), held bool, err error) {
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
		if attempt > 0 || (serr == nil && time.Since(info.ModTime()) < staleAfter) {
			return func() {}, true, nil
		}
		// Stale (or gone between the two calls): remove and try once more.
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return func() {}, false, rerr
		}
	}
}
