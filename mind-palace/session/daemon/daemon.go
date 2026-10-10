// Package daemon is the long-running ingest process the hook relay posts to.
// It stores hook events, tails session transcripts and artifacts, replays the
// write-ahead log and runs the enrichment loop.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/backfill"
	"github.com/robbiebyrd/clued/mind-palace/session/enrich"
	"github.com/robbiebyrd/clued/mind-palace/session/tail"
	"github.com/robbiebyrd/clued/mind-palace/session/wal"
)

const (
	defaultEnrichInterval   = 5 * time.Second
	defaultWALFlushInterval = 60 * time.Second
	readHeaderTimeout       = 10 * time.Second
	shutdownTimeout         = 5 * time.Second
)

// ErrAddrInUse is returned (wrapped) by Run when the listen address is taken,
// which means another daemon is already serving.
var ErrAddrInUse = errors.New("address already in use")

// Options configures a Daemon. Zero intervals select the defaults (enrichment
// every 5 s, WAL flush every 60 s); Git may be nil to skip git lookups.
type Options struct {
	Config           session.Config
	Store            session.Store
	Enrichers        []session.Enricher
	AccountID        string
	Host             session.HostInfo
	Git              backfill.GitInfo
	Logger           *slog.Logger
	Tail             tail.Options
	EnrichInterval   time.Duration
	WALFlushInterval time.Duration
}

// sessionState is what the daemon remembers about a session it has seen.
type sessionState struct {
	gitOriginFound bool
	gitLookup      bool // a late lookup is in flight
}

// Daemon ingests hook events and session files into a session.Store.
type Daemon struct {
	opts   Options
	log    *slog.Logger
	ctx    context.Context // ends on shutdown; scopes background store writes and git lookups
	cancel context.CancelFunc
	bg     sync.WaitGroup // background goroutines (git lookups)

	mu      sync.Mutex
	tracked map[string]*sessionState
	stops   []func() // tailers and watchers
	closed  bool

	walMu sync.Mutex // serialises WAL Append and Flush
}

// New builds a Daemon. Nothing runs until Run (or Handler) is used.
func New(opts Options) *Daemon {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.EnrichInterval <= 0 {
		opts.EnrichInterval = defaultEnrichInterval
	}
	if opts.WALFlushInterval <= 0 {
		opts.WALFlushInterval = defaultWALFlushInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{opts: opts, log: opts.Logger, ctx: ctx, cancel: cancel, tracked: map[string]*sessionState{}}
}

// Handler serves GET /health and POST /event.
func (d *Daemon) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case r.Method == http.MethodPost && r.URL.Path == "/event":
			d.handleEvent(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (d *Daemon) handleEvent(w http.ResponseWriter, r *http.Request) {
	var body session.Doc
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body == nil {
		http.Error(w, "event must be a JSON object", http.StatusBadRequest)
		return
	}
	sessionID, _ := body.String("session_id")
	transcriptPath, _ := body.String("transcript_path")
	cwd, _ := body.String("cwd")
	if sessionID != "" {
		d.trackSession(sessionID, transcriptPath, cwd)
		if err := d.opts.Store.TouchSession(r.Context(), d.opts.AccountID, sessionID, time.Now()); err != nil {
			d.log.Error("touch session failed", "session_id", sessionID, "err", err)
		}
	}
	ev := make(session.Doc, len(body)+3)
	for k, v := range body {
		ev[k] = v
	}
	ev["account_id"] = d.opts.AccountID
	ev["host"] = d.opts.Host
	ev["created_at"] = time.Now()
	if err := d.opts.Store.InsertHookEvent(r.Context(), ev); err != nil {
		d.log.Warn("hook event insert failed; appending to WAL", "err", err)
		ev["created_at"] = ev["created_at"].(time.Time).UTC().Format(time.RFC3339Nano)
		if err := d.appendWAL(ev); err != nil {
			d.log.Error("WAL append failed", "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (d *Daemon) appendWAL(ev session.Doc) error {
	d.walMu.Lock()
	defer d.walMu.Unlock()
	return wal.Append(d.opts.Config.WalPath, ev)
}

func (d *Daemon) flushWAL(ctx context.Context) {
	d.walMu.Lock()
	defer d.walMu.Unlock()
	err := wal.Flush(ctx, d.opts.Config.WalPath, func(ctx context.Context, ev session.Doc) error {
		// Entries written by the fallback carry created_at as text.
		if s, ok := ev["created_at"].(string); ok {
			if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
				ev["created_at"] = at
			}
		} else if _, ok := ev["created_at"]; !ok {
			ev["created_at"] = time.Now()
		}
		return d.opts.Store.InsertHookEvent(ctx, ev)
	})
	if err != nil {
		d.log.Warn("WAL flush failed", "err", err)
	}
}

// trackSession starts capturing a session the first time it is seen, and on
// later events fills in git info once a cwd is known.
func (d *Daemon) trackSession(sessionID, transcriptPath, cwd string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	if state, seen := d.tracked[sessionID]; seen {
		if !state.gitOriginFound && !state.gitLookup && cwd != "" && d.opts.Git != nil {
			state.gitLookup = true
			d.goBackground(func() { d.fillGitInfo(sessionID, cwd, state) })
		}
		return
	}
	d.tracked[sessionID] = &sessionState{}
	state := d.tracked[sessionID]

	d.goBackground(func() { d.upsertSession(sessionID, transcriptPath, cwd, state) })
	if transcriptPath == "" {
		return
	}
	d.stops = append(d.stops, d.tailTranscript(sessionID, transcriptPath))
	sessionDir := filepath.Join(filepath.Dir(transcriptPath), sessionID)
	fileHistory := filepath.Join(d.opts.Config.FileHistoryDir, sessionID)
	d.stops = append(d.stops, tail.WatchArtifacts(sessionID, sessionDir, fileHistory, d.opts.AccountID, &d.opts.Host, d.opts.Store, d.opts.Tail))
}

// goBackground runs fn on a tracked goroutine. Callers hold d.mu.
func (d *Daemon) goBackground(fn func()) {
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		fn()
	}()
}

func (d *Daemon) lookupGit(cwd string) (origin, branch string) {
	if cwd == "" || d.opts.Git == nil {
		return "", ""
	}
	return d.opts.Git(d.ctx, cwd)
}

func (d *Daemon) upsertSession(sessionID, transcriptPath, cwd string, state *sessionState) {
	origin, branch := d.lookupGit(cwd)
	if origin != "" {
		d.mu.Lock()
		state.gitOriginFound = true
		d.mu.Unlock()
	}
	err := d.opts.Store.UpsertSession(d.ctx, session.Session{
		SessionID: sessionID, TranscriptPath: transcriptPath, Cwd: cwd,
		GitOrigin: origin, GitBranch: branch,
		AccountID: d.opts.AccountID, Host: &d.opts.Host, LastSeen: time.Now(),
	})
	if err != nil && d.ctx.Err() == nil {
		d.log.Error("upsert session failed", "session_id", sessionID, "err", err)
	}
}

func (d *Daemon) fillGitInfo(sessionID, cwd string, state *sessionState) {
	origin, branch := d.lookupGit(cwd)
	d.mu.Lock()
	defer d.mu.Unlock()
	state.gitLookup = false
	if origin == "" {
		return
	}
	state.gitOriginFound = true
	if err := d.opts.Store.SetGitInfoIfMissing(d.ctx, sessionID, origin, branch); err != nil && d.ctx.Err() == nil {
		d.log.Error("set git info failed", "session_id", sessionID, "err", err)
	}
}

// tailTranscript stores each transcript line under {session_id, seq}, so a
// restart or backfill that sees the same lines does not duplicate them.
func (d *Daemon) tailTranscript(sessionID, path string) (stop func()) {
	seq := 0
	return tail.TailFile(path, func(raw string) {
		var line any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			line = map[string]any{"raw": raw}
		}
		err := d.opts.Store.UpsertTranscriptLines(d.ctx, []session.TranscriptLine{{
			SessionID: sessionID, Seq: seq, Line: line, AccountID: d.opts.AccountID, Host: &d.opts.Host,
		}})
		seq++
		if err != nil && d.ctx.Err() == nil {
			d.log.Error("store transcript line failed", "session_id", sessionID, "err", err)
		}
	}, d.opts.Tail)
}

// shutdown stops every tailer, watcher and background goroutine. It is idempotent.
func (d *Daemon) shutdown() {
	d.mu.Lock()
	d.closed = true
	stops := d.stops
	d.stops = nil
	d.mu.Unlock()
	d.cancel()
	for _, stop := range stops {
		stop()
	}
	d.bg.Wait()
}

// Run serves until ctx ends: it flushes the WAL (now and periodically), runs
// the enrichment loop and the HTTP server, then stops everything it started.
// It returns an error wrapping ErrAddrInUse when addr is already bound.
func (d *Daemon) Run(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("%w: %s", ErrAddrInUse, addr)
		}
		return err
	}
	// Listening first means a second daemon never replays the WAL the first owns.
	d.flushWAL(ctx)

	var loops sync.WaitGroup
	loops.Add(2)
	go func() {
		defer loops.Done()
		ticker := time.NewTicker(d.opts.WALFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.flushWAL(ctx)
			}
		}
	}()
	go func() {
		defer loops.Done()
		enrich.Loop(ctx, d.opts.Store, d.opts.Enrichers, d.opts.EnrichInterval, d.log)
	}()

	srv := &http.Server{Handler: d.Handler(), ReadHeaderTimeout: readHeaderTimeout}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	var result error
	select {
	case <-ctx.Done():
	case result = <-serveErr:
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil && result == nil {
		result = err
	}
	d.shutdown()
	loops.Wait()
	if result != nil && !errors.Is(result, http.ErrServerClosed) {
		return result
	}
	return nil
}
