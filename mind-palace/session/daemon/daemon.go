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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/backfill"
	"github.com/robbiebyrd/clued/mind-palace/session/enrich"
	"github.com/robbiebyrd/clued/mind-palace/session/ingest"
	"github.com/robbiebyrd/clued/mind-palace/session/modeltrack"
	"github.com/robbiebyrd/clued/mind-palace/session/sidecar"
	"github.com/robbiebyrd/clued/mind-palace/session/tail"
)

const (
	defaultEnrichInterval   = 5 * time.Second
	defaultWALFlushInterval = 60 * time.Second
	readHeaderTimeout       = 10 * time.Second
	shutdownTimeout         = 5 * time.Second
	defaultStoreTimeout     = 10 * time.Second
	maxBodyBytes            = 16 << 20
)

// ErrAddrInUse is returned (wrapped) by Run when the listen address is taken,
// which means another daemon is already serving.
var ErrAddrInUse = errors.New("address already in use")

// Options configures a Daemon. Zero intervals select the defaults (enrichment
// every 5 s, WAL flush every 60 s, store calls bounded to 10 s); Git may be nil to skip git lookups.
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
	StoreTimeout     time.Duration
}

// Daemon ingests hook events and session files into a session.Store. The
// event bookkeeping (session upsert, git fill, touch, stamping and the WAL)
// is the ingest service's; the daemon adds the HTTP endpoint, the transcript
// and artifact tailers and the periodic loops.
type Daemon struct {
	opts   Options
	log    *slog.Logger
	ingest *ingest.Service
	ctx    context.Context // ends on shutdown; scopes background store writes and git lookups
	cancel context.CancelFunc
	bg     sync.WaitGroup // background goroutines (session bookkeeping)

	mu      sync.Mutex
	tracked map[string]struct{} // sessions whose tailers were considered
	stops   []func()            // tailers and watchers
	closed  bool
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
	if opts.StoreTimeout <= 0 {
		opts.StoreTimeout = defaultStoreTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{opts: opts, log: opts.Logger, ctx: ctx, cancel: cancel, tracked: map[string]struct{}{}}
	d.ingest = &ingest.Service{
		Store:     opts.Store,
		AccountID: opts.AccountID,
		Host:      opts.Host,
		Git:       opts.Git,
		Logger:    opts.Logger,
		WALPath:   opts.Config.WalPath,
		Timeout:   opts.StoreTimeout,
		// Session bookkeeping runs on tracked goroutines under the daemon's
		// own context, so a slow store never stalls a hook and shutdown waits
		// for the writes in flight.
		Detach: func(run func(ctx context.Context)) { d.background(func() { run(d.ctx) }) },
	}
	return d
}

// Handler serves GET /health and POST /event.
func (d *Daemon) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Browsers always send Origin on cross-origin POSTs; the hook's curl never does.
		if r.Header.Get("Origin") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
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
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		status := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	if body == nil {
		http.Error(w, "event must be a JSON object", http.StatusBadRequest)
		return
	}
	// Store calls outlive the request: a client that hangs up must not turn a
	// committed write into a WAL duplicate. A rejected insert is in the WAL
	// (and logged) by the time Handle returns, so the hook is answered ok.
	tr, _ := d.ingest.Handle(context.WithoutCancel(r.Context()), body)
	if tr.SessionID != "" {
		d.trackSession(tr.SessionID, tr.TranscriptPath)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (d *Daemon) appendWAL(ev session.Doc) error {
	return d.ingest.AppendWAL(ev)
}

func (d *Daemon) flushWAL(ctx context.Context) {
	if err := d.ingest.FlushWAL(ctx); err != nil {
		d.log.Warn("WAL flush failed", "err", err)
	}
}

// trackSession starts capturing a session's transcript and artifacts the
// first time the daemon sees it.
func (d *Daemon) trackSession(sessionID, transcriptPath string) {
	// Both checks are spelled out here (rather than behind helpers) so the
	// values that reach the file system are visibly validated at the point of
	// use: a session id must be a single local path element, and a transcript
	// must be an absolute path under the projects directory. The ingest
	// service has already warned about an unsafe session id.
	if !filepath.IsLocal(sessionID) || filepath.Base(sessionID) != sessionID {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	if _, seen := d.tracked[sessionID]; seen {
		return
	}
	d.tracked[sessionID] = struct{}{}
	if transcriptPath == "" {
		return
	}
	// Clean resolves any ".." segments, so a prefix check against the
	// projects directory is enough to keep the tailed path inside it.
	path := filepath.Clean(transcriptPath)
	if !filepath.IsAbs(path) || !strings.HasPrefix(path, projectsPrefix(d.opts.Config.ProjectsDir)) {
		d.log.Warn("transcript_path is outside the projects directory; not tailed", "session_id", sessionID, "transcript_path", transcriptPath)
		return
	}
	d.stops = append(d.stops, d.tailTranscript(sessionID, path))
	sessionDir := filepath.Join(filepath.Dir(path), sessionID)
	fileHistory := filepath.Join(d.opts.Config.FileHistoryDir, sessionID)
	d.stops = append(d.stops, tail.WatchArtifacts(sessionID, sessionDir, fileHistory, d.opts.AccountID, &d.opts.Host, d.opts.Store, d.opts.Tail))
}

// projectsPrefix is the cleaned projects directory with a trailing separator,
// so that a sibling such as "<root>-evil" does not match. An empty root yields
// a prefix no absolute path has.
func projectsPrefix(root string) string {
	if root == "" {
		return "\x00"
	}
	root = filepath.Clean(root)
	if strings.HasSuffix(root, string(filepath.Separator)) {
		return root
	}
	return root + string(filepath.Separator)
}

// background runs fn on a tracked goroutine unless the daemon is shutting down.
func (d *Daemon) background(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		fn()
	}()
}

// tailTranscript stores each transcript line under {session_id, seq}, so a
// restart or backfill that sees the same lines does not duplicate them.
func (d *Daemon) tailTranscript(sessionID, path string) (stop func()) {
	seq := 0
	model := "" // the model the lines last named, so each change is recorded once
	meta := &sidecar.Tracker{}
	return tail.TailFile(path, func(raw string) {
		var line any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			line = map[string]any{"raw": raw}
		}
		lines := []session.TranscriptLine{{
			SessionID: sessionID, Seq: seq, Line: line, AccountID: d.opts.AccountID, Host: &d.opts.Host,
		}}
		err := d.opts.Store.UpsertTranscriptLines(d.ctx, lines)
		seq++
		if err != nil && d.ctx.Err() == nil {
			d.log.Error("store transcript line failed", "session_id", sessionID, "err", err)
		}
		if err != nil {
			return
		}
		if model, err = modeltrack.Lines(d.ctx, d.opts.Store, d.opts.AccountID, lines, model); err != nil && d.ctx.Err() == nil {
			d.log.Error("record session model failed", "session_id", sessionID, "err", err)
		}
		if err := meta.Lines(d.ctx, d.opts.Store, d.opts.AccountID, sessionID, lines); err != nil && d.ctx.Err() == nil {
			d.log.Error("record session metadata failed", "session_id", sessionID, "err", err)
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
