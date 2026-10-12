package tail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// WatchDir calls onChange(name, fullPath) for every regular file in path that
// is new or whose mtime changed since the previous poll. Subdirectories are
// ignored and a missing directory is waited for. When onChange returns an
// error the file is reported again on the next poll, so a transient failure
// (a file caught mid-write) does not lose the change.
func WatchDir(path string, onChange func(name, fullPath string) error, opts Options) (stop func()) {
	opts = opts.withDefaults()
	mtimes := map[string]int64{}
	return poll(opts, func() bool {
		entries, err := os.ReadDir(path)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			m := info.ModTime().UnixNano()
			if seen, ok := mtimes[e.Name()]; ok && seen == m {
				continue
			}
			if err := onChange(e.Name(), filepath.Join(path, e.Name())); err != nil {
				opts.Logger.Warn("watch dir change failed, will retry", "path", filepath.Join(path, e.Name()), "err", err)
				continue
			}
			mtimes[e.Name()] = m
		}
		return true
	})
}

// FileStamp records how much of an artifact file has been mirrored: its size
// and mtime when it was last read and, for a subagent transcript, the byte
// offset and line count already stored. A file whose size and mtime both
// match its stamp is skipped by MirrorArtifacts.
type FileStamp struct {
	Size   int64 `json:"size"`
	MTime  int64 `json:"mtime"` // UnixNano
	Offset int64 `json:"offset,omitempty"`
	Seq    int   `json:"seq,omitempty"`
}

// Counts reports what one MirrorArtifacts pass wrote.
type Counts struct {
	Subagents int // subagent transcript lines upserted
	Blobs     int // subagent meta, tool result and file-history blobs upserted
	Dirs      int // how many of the three artifact directories exist
}

// Operations named by FileError.Op.
const (
	OpRead  = "read"
	OpStore = "store"
)

// FileError is one artifact MirrorArtifacts could not mirror. The file is not
// stamped in the returned map, so the next pass retries it. Stamp is the file
// as it was found, with the offset and seq the pass started from, so a caller
// that chooses not to retry can record it. Err is the underlying error (for a
// store refusal, one for which errors.Is(err, session.ErrTooLarge) may hold).
type FileError struct {
	Key   string // "<dir>/<name>", the key MirrorArtifacts uses in the stamp map
	Op    string // OpRead or OpStore
	Stamp FileStamp
	Err   error
}

func (e *FileError) Error() string { return fmt.Sprintf("%s %s: %v", e.Op, e.Key, e.Err) }
func (e *FileError) Unwrap() error { return e.Err }

// FileErrors unpacks the error MirrorArtifacts returns into its FileErrors.
func FileErrors(err error) []*FileError {
	var out []*FileError
	var walk func(error)
	walk = func(err error) {
		if err == nil {
			return
		}
		if parts := joined(err); len(parts) > 0 {
			for _, e := range parts {
				walk(e)
			}
			return
		}
		var fe *FileError
		if errors.As(err, &fe) {
			out = append(out, fe)
		}
	}
	walk(err)
	return out
}

func joined(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return nil
}

// The artifact directories and the key prefixes their files get in the stamp map.
const (
	subagentsDir   = "subagents"
	toolResultsDir = "tool-results"
	fileHistoryKey = "file-history"
)

// MirrorArtifacts mirrors a session's side artifacts into the store once:
// <sessionDir>/subagents/*.jsonl as subagent lines (numbered per subagent from
// the stamped seq, complete lines only), *.meta.json there and every file in
// <sessionDir>/tool-results as utf8 blobs, and every file in fileHistoryDir
// as a base64 blob. Only files that are new or whose size or mtime differ from
// their entry in seen are read; the returned map holds the stamps of every
// file mirrored so far (seen is not modified) and is what the next pass
// receives. A subagent transcript that shrank is read again from the start
// with seq 0, as a resumed session rewrites it. Missing or empty directory
// paths are skipped. Files that could not be read or stored are reported
// together as FileErrors (see FileErrors) and left unstamped; the others are
// still processed. A cancelled ctx ends the pass with its error.
func MirrorArtifacts(ctx context.Context, store session.Store, accountID, sessionID, sessionDir, fileHistoryDir string, host *session.HostInfo, seen map[string]FileStamp) (map[string]FileStamp, Counts, error) {
	m := &mirror{ctx: ctx, store: store, accountID: accountID, sessionID: sessionID, host: host, prev: seen, next: map[string]FileStamp{}}
	if seen == nil {
		m.prev = map[string]FileStamp{}
	}
	dirs := []struct {
		key, path string
		handle    func(key, path string, st FileStamp) error
	}{
		{subagentsDir, join(sessionDir, subagentsDir), m.subagentFile},
		{toolResultsDir, join(sessionDir, toolResultsDir), m.blob("tool-result", "utf8", utf8)},
		{fileHistoryKey, fileHistoryDir, m.blob("file-history", "base64", base64.StdEncoding.EncodeToString)},
	}
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			m.keep = append(m.keep, d.key+"/")
			continue
		}
		m.scan(d.key, d.path, d.handle)
	}
	errs := m.errs
	if err := ctx.Err(); err != nil {
		errs = append(errs, err)
	}
	return m.result(), m.counts, errors.Join(errs...)
}

func utf8(b []byte) string { return string(b) }

func join(dir, name string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

type mirror struct {
	ctx        context.Context
	store      session.Store
	accountID  string
	sessionID  string
	host       *session.HostInfo
	prev, next map[string]FileStamp
	counts     Counts
	errs       []error
	keep       []string // "<dir>/" prefixes whose previous stamps are carried over as they were
}

// result is next plus the previous stamps of directories that were not
// scanned to the end, so a transient listing failure or a cancelled context
// does not forget what was mirrored.
func (m *mirror) result() map[string]FileStamp {
	for k, v := range m.prev {
		for _, p := range m.keep {
			if strings.HasPrefix(k, p) {
				if _, done := m.next[k]; !done {
					m.next[k] = v
				}
			}
		}
	}
	return maps.Clone(m.next)
}

// scan visits the regular files of one directory; handle receives files whose
// stamp changed (with the previous stamp, zero when new, and the current size
// and mtime) and stamps the file in next on success. Unchanged files keep
// their stamp; files no longer present drop out of the map.
func (m *mirror) scan(dirKey, dir string, handle func(key, path string, st FileStamp) error) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.keep = append(m.keep, dirKey+"/")
		}
		return
	}
	m.counts.Dirs++
	for _, e := range entries {
		if m.ctx.Err() != nil {
			m.keep = append(m.keep, dirKey+"/")
			return
		}
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		key := dirKey + "/" + e.Name()
		st, known := m.prev[key]
		if known && st.Size == info.Size() && st.MTime == info.ModTime().UnixNano() {
			m.next[key] = st
			continue
		}
		st.Size, st.MTime = info.Size(), info.ModTime().UnixNano()
		if err := handle(key, filepath.Join(dir, e.Name()), st); err != nil {
			var fe *FileError
			if !errors.As(err, &fe) {
				fe = &FileError{Op: OpRead, Err: err}
			}
			fe.Key, fe.Stamp = key, st
			m.errs = append(m.errs, fe)
		}
	}
}

// blob mirrors one file as a blob of the given type and stamps it.
func (m *mirror) blob(blobType, encoding string, encode func([]byte) string) func(key, path string, st FileStamp) error {
	return func(key, path string, st FileStamp) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b := session.Blob{SessionID: m.sessionID, BlobType: blobType, Name: filepath.Base(path), Content: encode(data), Encoding: encoding, AccountID: m.accountID}
		if err := m.store.UpsertBlob(m.ctx, b); err != nil {
			return &FileError{Op: OpStore, Err: err}
		}
		m.counts.Blobs++
		m.next[key] = FileStamp{Size: st.Size, MTime: st.MTime}
		return nil
	}
}

// subagentFile mirrors one entry of the subagents directory: a transcript's
// new complete lines as subagent lines, a meta file as a utf8 blob. Other
// files are ignored and not stamped.
func (m *mirror) subagentFile(key, path string, st FileStamp) error {
	name := filepath.Base(path)
	switch {
	case strings.HasSuffix(name, ".jsonl"):
		return m.subagentLines(strings.TrimSuffix(name, ".jsonl"), key, path, st)
	case strings.HasSuffix(name, ".meta.json"):
		return m.blob("subagent-meta", "utf8", utf8)(key, path, st)
	}
	return nil
}

// subagentLines stores the complete lines written since the stamp's offset,
// numbered from its seq, and stamps the file at the new offset. A store error
// stops at the failing line and leaves the stamp as it was, so the next pass
// starts from the same place (upserts are idempotent by seq).
func (m *mirror) subagentLines(id, key, path string, st FileStamp) error {
	if st.Size < st.Offset {
		st.Offset, st.Seq = 0, 0
	}
	lines, end, err := ReadLines(path, st.Offset)
	if err != nil {
		return err
	}
	for i, l := range lines {
		line := session.SubagentLine{SessionID: m.sessionID, SubagentID: id, Seq: st.Seq + i, Line: DecodeLine(l.Text), AccountID: m.accountID, Host: m.host}
		if err := m.store.UpsertSubagentLine(m.ctx, line); err != nil {
			return &FileError{Op: OpStore, Err: err}
		}
		m.counts.Subagents++
	}
	st.Seq += len(lines)
	st.Offset = end
	m.next[key] = st
	return nil
}

// WatchArtifacts mirrors a session's side artifacts into the store as they
// appear and change, by running MirrorArtifacts on every poll: subagent JSONL
// files are tailed (each with its own seq counter), subagent meta files and
// tool results are stored as utf8 blobs, and file-history files as base64
// blobs. A file that could not be read is retried on the next poll; store
// errors are logged, never fatal, and the file is not retried until it
// changes again. The poll runs at the wait interval until all three
// directories exist.
func WatchArtifacts(sessionID, sessionDir, fileHistoryPath, accountID string, host *session.HostInfo, store session.Store, opts Options) (stop func()) {
	opts = opts.withDefaults()
	ctx := context.Background()
	seen := map[string]FileStamp{}
	return poll(opts, func() bool {
		var counts Counts
		var err error
		seen, counts, err = MirrorArtifacts(ctx, store, accountID, sessionID, sessionDir, fileHistoryPath, host, seen)
		for _, fe := range FileErrors(err) {
			if fe.Op == OpStore {
				opts.Logger.Error("store artifact failed", "session_id", sessionID, "artifact", fe.Key, "err", fe.Err)
				seen[fe.Key] = fe.Stamp
				continue
			}
			opts.Logger.Warn("artifact read failed, will retry", "session_id", sessionID, "artifact", fe.Key, "err", fe.Err)
		}
		return counts.Dirs == 3
	})
}
