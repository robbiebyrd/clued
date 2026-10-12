// Package catchup pushes what the daemon's tailers would have pushed for one
// session, once: the transcript lines written since the previous run and the
// subagent lines and blobs whose files changed. It is what a short-lived hook
// relay runs in place of a resident tailer, with its progress kept in a state
// file and concurrent runs for the same session serialized by a lock file.
package catchup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/tail"
)

// Paths locates one session's files. Transcript must be an absolute path
// under ProjectsDir; the session's artifact directory is
// <dir of Transcript>/<SessionID> and its file history
// <FileHistoryDir>/<SessionID>, as the daemon derives them. Host, when set,
// is stamped on every line as the daemon stamps it.
type Paths struct {
	SessionID      string
	AccountID      string
	Transcript     string
	ProjectsDir    string
	FileHistoryDir string
	Host           *session.HostInfo
}

// Report says what one run did.
type Report struct {
	Skipped   bool // another run held the lock; nothing was pushed
	Lines     int  // transcript lines upserted
	Subagents int  // subagent lines upserted
	Blobs     int  // blobs upserted
	Refused   int  // blobs the store refused as too large, now recorded in the state
	Reset     bool // the transcript shrank below the saved offset, so it was re-read from the start
}

// ErrOutsideProjects is returned when the transcript path is not an absolute
// path under ProjectsDir.
var ErrOutsideProjects = errors.New("transcript path is outside the projects directory")

// ErrBadSessionID is returned when the session id is not a single path
// element; it names the state and lock files.
var ErrBadSessionID = errors.New("session id is not a single path element")

// batchSize is how many transcript lines go in one upsert.
const batchSize = 500

// Run pushes the session's delta to the store and records how far it got in
// <stateDir>/<SessionID>.json. A run that finds <SessionID>.lock held by
// another run (younger than a minute) returns Report{Skipped: true} and nil;
// the next hook catches up. A transcript that does not exist yet contributes
// nothing. When a store call fails, the state saved covers what succeeded, so
// the next run resumes from there, and the error is returned with the
// report. Blobs refused with session.ErrTooLarge are not an error: they are
// recorded in the state and retried only when their file changes.
func Run(ctx context.Context, store session.Store, p Paths, stateDir string) (Report, error) {
	var rep Report
	sid := p.SessionID
	if !filepath.IsLocal(sid) || filepath.Base(sid) != sid {
		return rep, fmt.Errorf("%w: %q", ErrBadSessionID, sid)
	}
	// Clean resolves any ".." segments, so a prefix check against the
	// projects directory is enough to keep the path read inside it. The
	// prefix carries a trailing separator so a sibling "<root>-evil" is out.
	transcript := filepath.Clean(p.Transcript)
	root := filepath.Clean(p.ProjectsDir)
	if p.ProjectsDir == "" || !filepath.IsAbs(transcript) || !strings.HasPrefix(transcript, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator)) {
		return rep, fmt.Errorf("%w: %q", ErrOutsideProjects, p.Transcript)
	}
	sessionDir := filepath.Join(filepath.Dir(transcript), sid)
	fileHistory := ""
	if p.FileHistoryDir != "" {
		fileHistory = filepath.Join(p.FileHistoryDir, sid)
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return rep, err
	}
	release, held, err := acquireLock(filepath.Join(stateDir, sid+".lock"))
	if err != nil {
		return rep, err
	}
	defer release()
	if held {
		rep.Skipped = true
		return rep, nil
	}

	statePath := filepath.Join(stateDir, sid+".json")
	st, err := loadState(statePath)
	if err != nil {
		return rep, err
	}
	if st.Artifacts == nil {
		st.Artifacts = map[string]tail.FileStamp{}
	}
	if st.Refused == nil {
		st.Refused = map[string]bool{}
	}

	if err := pushTranscript(ctx, store, p, transcript, &st, &rep); err != nil {
		if serr := saveState(statePath, st); serr != nil {
			err = errors.Join(err, serr)
		}
		return rep, err
	}
	err = pushArtifacts(ctx, store, p, sessionDir, fileHistory, &st, &rep)
	if serr := saveState(statePath, st); serr != nil {
		err = errors.Join(err, serr)
	}
	return rep, err
}

// pushTranscript upserts the complete lines after the saved offset in
// batches, advancing the offset and seq after each batch so a failure keeps
// what was stored.
func pushTranscript(ctx context.Context, store session.Store, p Paths, transcript string, st *state, rep *Report) error {
	info, err := os.Stat(transcript)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < st.Offset {
		st.Offset, st.Seq = 0, 0
		rep.Reset = true
	}
	lines, end, err := tail.ReadLines(transcript, st.Offset)
	if err != nil {
		return err
	}
	for i := 0; i < len(lines); i += batchSize {
		j := min(i+batchSize, len(lines))
		batch := make([]session.TranscriptLine, 0, j-i)
		for k, l := range lines[i:j] {
			batch = append(batch, session.TranscriptLine{
				SessionID: p.SessionID, Seq: st.Seq + k, Line: tail.DecodeLine(l.Text), AccountID: p.AccountID, Host: p.Host,
			})
		}
		if err := store.UpsertTranscriptLines(ctx, batch); err != nil {
			return err
		}
		st.Seq += len(batch)
		st.Offset = lines[j-1].End
		rep.Lines += len(batch)
	}
	if end > st.Offset {
		st.Offset = end // trailing blank lines
	}
	return nil
}

// pushArtifacts mirrors the changed artifact files. A blob the store refuses
// as too large is stamped and listed in Refused; any other failure leaves the
// file unstamped for the next run and is returned.
func pushArtifacts(ctx context.Context, store session.Store, p Paths, sessionDir, fileHistory string, st *state, rep *Report) error {
	before := st.Artifacts
	seen, counts, merr := tail.MirrorArtifacts(ctx, store, p.AccountID, p.SessionID, sessionDir, fileHistory, p.Host, before)
	rep.Subagents += counts.Subagents
	rep.Blobs += counts.Blobs
	var errs []error
	failed := map[string]bool{}
	for _, fe := range tail.FileErrors(merr) {
		failed[fe.Key] = true
		if errors.Is(fe.Err, session.ErrTooLarge) {
			seen[fe.Key] = fe.Stamp
			if !st.Refused[fe.Key] {
				rep.Refused++
			}
			st.Refused[fe.Key] = true
			continue
		}
		errs = append(errs, fe)
	}
	if merr != nil && len(tail.FileErrors(merr)) == 0 {
		errs = append(errs, merr) // not a file failure: the context ended
	}
	for key := range st.Refused {
		cur, present := seen[key]
		if !present || (cur != before[key] && !failed[key]) {
			delete(st.Refused, key) // gone, or changed and accepted this time
		}
	}
	st.Artifacts = seen
	return errors.Join(errs...)
}
