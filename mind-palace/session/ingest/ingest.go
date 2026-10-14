// Package ingest is the hook-event bookkeeping shared by the session daemon
// and the daemon-less hook relay: it tracks the sessions it sees, stamps each
// event with the account, host and time, stores it, and buffers it in the
// write-ahead log when the store is down.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/backfill"
	"github.com/robbiebyrd/clued/mind-palace/session/livestate"
	"github.com/robbiebyrd/clued/mind-palace/session/modeltrack"
	"github.com/robbiebyrd/clued/mind-palace/session/wal"
)

// DefaultTimeout bounds each store call when Service.Timeout is zero.
const DefaultTimeout = 10 * time.Second

// ErrBuffered is returned (wrapped) by Handle when the store rejected the
// event and it was appended to the WAL instead: the event is safe and will be
// replayed by FlushWAL, so callers may treat it as handled.
var ErrBuffered = errors.New("hook event buffered in WAL")

// Service stores hook events and keeps the session metadata they carry up to
// date. The zero value is not usable: at least Store must be set. A Service is
// safe for concurrent use.
type Service struct {
	Store     session.Store
	AccountID string
	Host      session.HostInfo
	Git       backfill.GitInfo // nil skips git lookups
	Logger    *slog.Logger     // nil selects slog.Default
	WALPath   string           // where rejected events are buffered
	Timeout   time.Duration    // per store call; zero selects DefaultTimeout

	// Detach, when set, runs the session bookkeeping that need not finish
	// before the event is stored (the first-sight upsert, a late git fill and
	// the last-seen touch) off the calling goroutine, with a context of the
	// caller's choosing. The daemon uses it so a slow store never stalls a
	// hook; nil runs that work inline under the context given to Handle,
	// which is what a short-lived relay wants.
	Detach func(run func(ctx context.Context))

	mu   sync.Mutex
	seen map[string]*sessionState

	walMu sync.Mutex // serialises WAL Append and Flush
}

// sessionState is what the service remembers about a session it has seen.
type sessionState struct {
	gitOriginFound bool
	gitLookup      bool   // a late lookup is in flight
	liveState      string // the live state last recorded, "" when unknown
}

// Tracked reports what Handle learned about the event's session.
type Tracked struct {
	SessionID      string
	TranscriptPath string
	Cwd            string
	// FirstSeen is true when this event is the first the service has seen
	// for the session, so the caller can start capturing its files.
	FirstSeen bool
}

// Handle records one hook event. On the first sight of a session it upserts
// the session with its cwd, git origin and branch, host, account and
// last-seen time; on later events it fills in git info once a cwd is known.
// Every event touches the session's last-seen time, is stamped with
// account_id, host and created_at, and is inserted. When the insert fails
// the stamped event is appended to the WAL and the error is returned wrapped
// in ErrBuffered. Failures of the session bookkeeping are logged, not
// returned: the event itself is what must not be lost.
func (s *Service) Handle(ctx context.Context, event session.Doc) (Tracked, error) {
	sessionID, _ := event.String("session_id")
	transcriptPath, _ := event.String("transcript_path")
	cwd, _ := event.String("cwd")
	tr := Tracked{SessionID: sessionID, TranscriptPath: transcriptPath, Cwd: cwd}
	if sessionID != "" {
		tr.FirstSeen = s.track(ctx, sessionID, transcriptPath, cwd)
		s.run(ctx, func(ctx context.Context) { s.touch(ctx, sessionID, time.Now()) })
	}
	ev := make(session.Doc)
	for k, v := range event {
		ev[k] = v
	}
	ev["account_id"] = s.AccountID
	ev["host"] = s.Host
	ev["created_at"] = time.Now()
	ictx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	if name, _ := ev.String("hook_event_name"); name == session.LimitsEventName && sessionID != "" {
		if state := s.liveStateOf(ictx, sessionID); state != "" {
			ev["live_state"] = state
		}
	}
	err := s.Store.InsertHookEvent(ictx, ev)
	if err == nil {
		if model := modeltrack.EventModel(event); model != "" && sessionID != "" {
			s.run(ctx, func(ctx context.Context) { s.observeModel(ctx, sessionID, model) })
		}
		if state, reason := livestate.StateOf(event); state != "" && sessionID != "" {
			at := ev["created_at"].(time.Time)
			s.run(ctx, func(ctx context.Context) { s.observeState(ctx, sessionID, state, reason, at) })
		}
		return tr, nil
	}
	s.logger().Warn("hook event insert failed; appending to WAL", "err", err)
	ev["created_at"] = ev["created_at"].(time.Time).UTC().Format(time.RFC3339Nano)
	if werr := s.AppendWAL(ev); werr != nil {
		s.logger().Error("WAL append failed", "err", werr)
		return tr, fmt.Errorf("hook event insert failed: %w; WAL append failed: %w", err, werr)
	}
	return tr, fmt.Errorf("%w: %w", ErrBuffered, err)
}

// AppendWAL buffers one event in the WAL, serialised against FlushWAL.
func (s *Service) AppendWAL(ev session.Doc) error {
	s.walMu.Lock()
	defer s.walMu.Unlock()
	return wal.Append(s.WALPath, ev)
}

// FlushWAL replays the buffered events into the store in order and removes
// the ones that were stored. A timed-out insert means the store is down, so
// the replay stops there rather than waiting out a timeout per line.
func (s *Service) FlushWAL(ctx context.Context) error {
	s.walMu.Lock()
	defer s.walMu.Unlock()
	return wal.Flush(ctx, s.WALPath, func(ctx context.Context, ev session.Doc) error {
		// Entries written by the fallback carry created_at as text.
		if text, ok := ev["created_at"].(string); ok {
			if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
				ev["created_at"] = at
			}
		} else if _, ok := ev["created_at"]; !ok {
			ev["created_at"] = time.Now()
		}
		ctx, cancel := context.WithTimeout(ctx, s.timeout())
		defer cancel()
		return s.Store.InsertHookEvent(ctx, ev)
	})
}

// track registers a session the first time it is seen and reports whether
// this was that first time. Later events trigger one git lookup at a time
// until an origin is known.
func (s *Service) track(ctx context.Context, sessionID, transcriptPath, cwd string) bool {
	// A session id becomes a path element for the daemon's tailers and the
	// relay's catch-up state, so one that is not a single local element is
	// stored as an event but never tracked as a session.
	if !filepath.IsLocal(sessionID) || filepath.Base(sessionID) != sessionID {
		s.logger().Warn("session_id is not a single safe path element; session not tracked", "session_id", sessionID)
		return false
	}
	s.mu.Lock()
	if s.seen == nil {
		s.seen = map[string]*sessionState{}
	}
	if state, seen := s.seen[sessionID]; seen {
		lookup := !state.gitOriginFound && !state.gitLookup && cwd != "" && s.Git != nil
		if lookup {
			state.gitLookup = true
		}
		s.mu.Unlock()
		if lookup {
			s.run(ctx, func(ctx context.Context) { s.fillGitInfo(ctx, sessionID, cwd, state) })
		}
		return false
	}
	state := &sessionState{}
	s.seen[sessionID] = state
	s.mu.Unlock()
	s.run(ctx, func(ctx context.Context) { s.upsertSession(ctx, sessionID, transcriptPath, cwd, state) })
	return true
}

func (s *Service) upsertSession(ctx context.Context, sessionID, transcriptPath, cwd string, state *sessionState) {
	origin, branch := s.lookupGit(ctx, cwd)
	if origin != "" {
		s.mu.Lock()
		state.gitOriginFound = true
		s.mu.Unlock()
	}
	sctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	err := s.Store.UpsertSession(sctx, session.Session{
		SessionID: sessionID, TranscriptPath: transcriptPath, Cwd: cwd,
		GitOrigin: origin, GitBranch: branch,
		AccountID: s.AccountID, Host: &s.Host, LastSeen: time.Now(),
	})
	if err != nil && ctx.Err() == nil {
		s.logger().Error("upsert session failed", "session_id", sessionID, "err", err)
	}
}

func (s *Service) fillGitInfo(ctx context.Context, sessionID, cwd string, state *sessionState) {
	origin, branch := s.lookupGit(ctx, cwd)
	s.mu.Lock()
	state.gitLookup = false
	state.gitOriginFound = state.gitOriginFound || origin != ""
	s.mu.Unlock()
	if origin == "" {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	if err := s.Store.SetGitInfoIfMissing(sctx, sessionID, origin, branch); err != nil && ctx.Err() == nil {
		s.logger().Error("set git info failed", "session_id", sessionID, "err", err)
	}
}

// observeModel records the model an event names (a LimitsSampled sample)
// as the session's. A session the store does not have yet (its first-sight
// upsert may still be in flight) is not an error worth logging.
func (s *Service) observeModel(ctx context.Context, sessionID, model string) {
	sctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	if _, err := modeltrack.Observe(sctx, s.Store, s.AccountID, sessionID, model, -1, time.Now(), modeltrack.SourceLimits); err != nil && !errors.Is(err, session.ErrNotFound) && ctx.Err() == nil {
		s.logger().Error("record session model failed", "session_id", sessionID, "err", err)
	}
}

// observeState records the live state an event puts the session in. A
// session the store does not have yet (its first-sight upsert may still be
// in flight) is not an error worth logging: the next event tries again.
func (s *Service) observeState(ctx context.Context, sessionID, state, reason string, at time.Time) {
	sctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	_, err := livestate.Observe(sctx, s.Store, s.AccountID, sessionID, state, reason, at)
	if err != nil {
		if !errors.Is(err, session.ErrNotFound) && ctx.Err() == nil {
			s.logger().Error("record session state failed", "session_id", sessionID, "err", err)
		}
		return
	}
	s.mu.Lock()
	if st := s.seen[sessionID]; st != nil {
		st.liveState = state
	}
	s.mu.Unlock()
}

// liveStateOf is the session's live state as last recorded by this service,
// else as stored (a short-lived relay has no memory of it); "" when unknown.
func (s *Service) liveStateOf(ctx context.Context, sessionID string) string {
	s.mu.Lock()
	st := s.seen[sessionID]
	known := st != nil && st.liveState != ""
	var state string
	if known {
		state = st.liveState
	}
	s.mu.Unlock()
	if known {
		return state
	}
	doc, err := s.Store.GetSession(ctx, s.AccountID, sessionID)
	if err != nil {
		return ""
	}
	state, _ = doc.String("live_state")
	return state
}

func (s *Service) touch(ctx context.Context, sessionID string, at time.Time) {
	sctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	if err := s.Store.TouchSession(sctx, s.AccountID, sessionID, at); err != nil && ctx.Err() == nil {
		s.logger().Error("touch session failed", "session_id", sessionID, "err", err)
	}
}

func (s *Service) lookupGit(ctx context.Context, cwd string) (origin, branch string) {
	if cwd == "" || s.Git == nil {
		return "", ""
	}
	return s.Git(ctx, cwd)
}

// run executes bookkeeping through Detach when one is set, inline otherwise.
func (s *Service) run(ctx context.Context, fn func(ctx context.Context)) {
	if s.Detach != nil {
		s.Detach(fn)
		return
	}
	fn(ctx)
}

func (s *Service) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
