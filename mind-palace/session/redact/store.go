package redact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Paths are the excludePaths globs (filepath.Match syntax, a leading ~
// standing for the home directory). A path is excluded when it or one of
// its parent directories matches one of them.
type Paths []string

// NewPaths expands ~ in patterns and checks their syntax; a bad pattern is
// skipped and reported in errs.
func NewPaths(patterns []string) (p Paths, errs []error) {
	home, _ := os.UserHomeDir()
	for i, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if home != "" && (pat == "~" || strings.HasPrefix(pat, "~/")) {
			pat = filepath.Join(home, pat[1:])
		}
		if _, err := filepath.Match(pat, ""); err != nil {
			errs = append(errs, fmt.Errorf("excludePaths[%d] %q: %w", i, pat, err))
			continue
		}
		p = append(p, filepath.Clean(pat))
	}
	return p, errs
}

// Excludes reports whether path, or a directory above it, matches.
func (p Paths) Excludes(path string) bool {
	if len(p) == 0 || path == "" {
		return false
	}
	for dir := filepath.Clean(path); ; {
		for _, pat := range p {
			if ok, _ := filepath.Match(pat, dir); ok {
				return true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// Store is a session.Store whose writes are redacted (when R is set) and
// whose sessions under an excluded path are never written: their session
// record, hook events, transcript and subagent lines and blobs are dropped.
// A session is known to be excluded from the cwd or project path of its
// session record, its hook events or its transcript lines; a batch of lines
// with one excluded line is dropped whole for that session. Reads pass
// through. With MetadataOnly, content is never written either: transcript
// and subagent lines and blobs are dropped (with a nil error, so a caller
// moves on rather than retrying) and hook events are stored as
// session.WithoutContent of them, and a session's title is not set.
type Store struct {
	session.Store
	R            *Redactor
	Exclude      Paths
	MetadataOnly bool

	mu       sync.Mutex
	excluded map[string]bool
}

// Wrap returns inner itself when there is nothing to redact or exclude,
// else the Store around it.
func Wrap(inner session.Store, r *Redactor, exclude Paths) session.Store {
	if r == nil && len(exclude) == 0 {
		return inner
	}
	return &Store{Store: inner, R: r, Exclude: exclude}
}

// FromConfig wraps inner as cfg says: redaction when cfg.RedactAtCapture,
// with cfg.RedactPatterns, cfg.ExcludePaths, and metadata-only capture
// when cfg.CaptureContent is false. Patterns that do not compile are
// skipped and returned as one error beside the store, which still applies
// the others.
func FromConfig(inner session.Store, cfg session.Config) (session.Store, error) {
	var errs []error
	var r *Redactor
	if cfg.RedactsAtCapture() {
		var bad []error
		r, bad = New(cfg.RedactPatterns)
		errs = append(errs, bad...)
	}
	paths, bad := NewPaths(cfg.ExcludePaths)
	errs = append(errs, bad...)
	if !cfg.CapturesContent() {
		return &Store{Store: inner, R: r, Exclude: paths, MetadataOnly: true}, errors.Join(errs...)
	}
	return Wrap(inner, r, paths), errors.Join(errs...)
}

func (s *Store) mark(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.excluded == nil {
		s.excluded = map[string]bool{}
	}
	s.excluded[sessionID] = true
}

func (s *Store) isExcluded(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.excluded[sessionID]
}

// excludes marks and reports a session whose path is excluded, or reports
// one already marked.
func (s *Store) excludes(sessionID string, paths ...string) bool {
	for _, p := range paths {
		if s.Exclude.Excludes(p) {
			s.mark(sessionID)
			return true
		}
	}
	return s.isExcluded(sessionID)
}

func lineCwd(line any) string {
	if m, ok := line.(map[string]any); ok {
		cwd, _ := m["cwd"].(string)
		return cwd
	}
	return ""
}

func (s *Store) UpsertSession(ctx context.Context, in session.Session) error {
	if s.excludes(in.SessionID, in.ProjectPath, in.Cwd) {
		return nil
	}
	return s.Store.UpsertSession(ctx, in)
}

func (s *Store) TouchSession(ctx context.Context, accountID, sessionID string, at time.Time) error {
	if s.isExcluded(sessionID) {
		return nil
	}
	return s.Store.TouchSession(ctx, accountID, sessionID, at)
}

func (s *Store) SetGitInfoIfMissing(ctx context.Context, sessionID, origin, branch string) error {
	if s.isExcluded(sessionID) {
		return nil
	}
	return s.Store.SetGitInfoIfMissing(ctx, sessionID, origin, branch)
}

// SetSessionMeta drops session.SessionContentFields (the title) under
// MetadataOnly; the other fields pass through.
func (s *Store) SetSessionMeta(ctx context.Context, accountID, sessionID string, fields session.Doc) error {
	if s.isExcluded(sessionID) {
		return nil
	}
	if s.MetadataOnly {
		kept := make(session.Doc, len(fields))
		for k, v := range fields {
			kept[k] = v
		}
		for _, f := range session.SessionContentFields {
			delete(kept, f)
		}
		if len(kept) == 0 {
			return nil
		}
		fields = kept
	}
	return s.Store.SetSessionMeta(ctx, accountID, sessionID, fields)
}

// InsertHookEvent redacts every string of the event; a redacted event
// carries the names of the rules that fired under "redacted".
func (s *Store) InsertHookEvent(ctx context.Context, ev session.Doc) error {
	sid, _ := ev.String("session_id")
	cwd, _ := ev.String("cwd")
	if s.excludes(sid, cwd) {
		return nil
	}
	if s.MetadataOnly {
		ev, _ = session.WithoutContent(ev)
	}
	if s.R != nil {
		masked, fired := s.R.Value(ev)
		if len(fired) > 0 {
			ev = masked.(session.Doc)
			ev["redacted"] = fired
		}
	}
	return s.Store.InsertHookEvent(ctx, ev)
}

func (s *Store) UpsertTranscriptLines(ctx context.Context, lines []session.TranscriptLine) error {
	for _, l := range lines {
		s.excludes(l.SessionID, lineCwd(l.Line))
	}
	if s.MetadataOnly {
		return nil
	}
	kept := make([]session.TranscriptLine, 0, len(lines))
	for _, l := range lines {
		if s.isExcluded(l.SessionID) {
			continue
		}
		if s.R != nil {
			masked, fired := s.R.Value(l.Line)
			l.Line, l.Redacted = masked, Merge(l.Redacted, fired)
		}
		kept = append(kept, l)
	}
	if len(kept) == 0 {
		return nil
	}
	return s.Store.UpsertTranscriptLines(ctx, kept)
}

func (s *Store) UpsertSubagentLine(ctx context.Context, l session.SubagentLine) error {
	if s.excludes(l.SessionID, lineCwd(l.Line)) || s.MetadataOnly {
		return nil
	}
	if s.R != nil {
		masked, fired := s.R.Value(l.Line)
		l.Line, l.Redacted = masked, Merge(l.Redacted, fired)
	}
	return s.Store.UpsertSubagentLine(ctx, l)
}

// UpsertBlob redacts text content; base64 content is stored as given.
func (s *Store) UpsertBlob(ctx context.Context, b session.Blob) error {
	if s.isExcluded(b.SessionID) || s.MetadataOnly {
		return nil
	}
	if s.R != nil && b.Encoding != "base64" {
		masked, fired := s.R.String(b.Content)
		b.Content, b.Redacted = masked, Merge(b.Redacted, fired)
	}
	return s.Store.UpsertBlob(ctx, b)
}

// InsertOtelEvents drops the events of excluded sessions and redacts every
// string of the attributes that are kept.
func (s *Store) InsertOtelEvents(ctx context.Context, events []session.OtelEvent) (int, error) {
	kept := make([]session.OtelEvent, 0, len(events))
	for _, e := range events {
		if s.isExcluded(e.SessionID) {
			continue
		}
		if s.R != nil {
			masked, fired := s.R.Value(e.Attributes)
			if len(fired) > 0 {
				e.Attributes, e.Redacted = masked.(session.Doc), Merge(e.Redacted, fired)
			}
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		return 0, nil
	}
	return s.Store.InsertOtelEvents(ctx, kept)
}
