package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/catchup"
	"github.com/robbiebyrd/clued/mind-palace/session/codesync"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/ingest"
	"github.com/robbiebyrd/clued/mind-palace/session/otel"
)

// otelBudget bounds the trace export a SessionEnd relay runs.
const otelBudget = 3 * time.Second

// relayBudget bounds one hook relay run end to end: the event insert, the
// catch-up and the WAL flush share it, and whatever is left when it ends is
// retried by the next hook.
const relayBudget = 5 * time.Second

// relayLogName is the file under the config directory that keeps one line
// per failed relay run.
const relayLogName = "relay.log"

// catchupDirName is the directory under the config directory that keeps the
// per-session catch-up state and lock files.
const catchupDirName = "catchup"

func (a *App) hookRelayCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "relay",
		Short: "Any hook, remote mode: store the event on the clued server, push the session's transcript delta, flush the WAL",
		Long: `Reads a hook input on stdin and, when the clued config names a server
(serverUrl / CLUED_SERVER_URL), does for that one event what the capture
daemon does in local mode: stores it under the token's account, tracks its
session, pushes the transcript lines and artifact files written since the
previous hook (always, including Stop and SessionEnd), and replays the
write-ahead log. With codeSync on, it then syncs the worktree's source
files to the server's code store for the events that change them
(SessionStart, SessionEnd, PostToolUse of Write, Edit, MultiEdit,
NotebookEdit and Bash, FileChanged, Stop, SubagentStop, CwdChanged,
WorktreeCreate, WorktreeRemove), last and with what is left of the
budget, at most 3 s. Everything shares a 5 s budget. The command prints nothing
and exits 0 whatever happened: a failed insert is buffered in the WAL, a
failed catch-up resumes from the same place next time, and every failure is
one line in relay.log next to the config file. In local mode (no server
configured) it does nothing; hooks/event-relay posts to the daemon instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a.relay(cmd.Context())
			return nil
		},
	}
	c.Flags().StringVar(&a.sess.configPath, "config", session.DefaultConfigPath(), "clued config file")
	return c
}

// relay runs one relay. It never fails the command: every problem is a
// line in relay.log.
func (a *App) relay(parent context.Context) {
	raw, readErr := io.ReadAll(a.Stdin)
	cfg := a.sessionConfig()
	if !cfg.Remote() {
		return
	}
	ctx, cancel := context.WithTimeout(parent, relayBudget)
	defer cancel()
	configDir := filepath.Dir(a.sess.configPath)
	log := relayLog{path: filepath.Join(configDir, relayLogName)}
	if readErr != nil {
		log.write("read hook input: " + readErr.Error())
		return
	}
	var event session.Doc
	if err := json.Unmarshal(raw, &event); err != nil {
		log.write(fmt.Sprintf("parse hook input: %v", err))
		return
	}
	if event == nil {
		log.write("parse hook input: not a JSON object")
		return
	}
	name, _ := event.String("hook_event_name")
	sid, _ := event.String("session_id")
	// The server stores a hook event only under a session id that is a
	// single path element (it names files on both sides). One that is not
	// would be refused for good, and buffering it would replay that refusal
	// on every hook, so it is dropped here with a line in the log.
	if !filepath.IsLocal(sid) || filepath.Base(sid) != sid {
		log.write(fmt.Sprintf("%s: session_id %q is not a single safe path element; event not stored", name, sid))
		return
	}
	defer a.closeSessionStore()
	store, err := a.sessionStore(ctx)
	if err != nil {
		log.write(fmt.Sprintf("%s session=%s: open remote store: %v", name, sid, err))
		return
	}

	problems := &problems{}
	host := hostinfo.Read()
	accountID := session.AccountIDFor(cfg)
	svc := &ingest.Service{
		Store:     store,
		AccountID: accountID,
		Host:      host,
		Git: func(ctx context.Context, dir string) (string, string) {
			return gitinfo.Origin(ctx, dir), gitinfo.Branch(ctx, dir)
		},
		Logger:  slog.New(problems),
		WALPath: cfg.WalPath,
		Timeout: relayBudget,
	}
	tr, err := svc.Handle(ctx, event)
	if err != nil {
		problems.add(err.Error()) // ErrBuffered included: the event is safe, the outage is worth a line
	}
	if tr.SessionID != "" && tr.TranscriptPath != "" {
		paths := catchup.Paths{
			SessionID: tr.SessionID, AccountID: accountID, Transcript: tr.TranscriptPath,
			ProjectsDir: cfg.ProjectsDir, FileHistoryDir: cfg.FileHistoryDir, Host: &host,
		}
		if _, err := catchup.Run(ctx, store, paths, filepath.Join(configDir, catchupDirName)); err != nil {
			problems.add("catch-up: " + err.Error())
		}
	}
	if err := svc.FlushWAL(ctx); err != nil {
		problems.add("WAL flush: " + err.Error())
	}
	// The session's trace goes out once its end and last lines are on the
	// server.
	if exp := otel.New(cfg); exp != nil && name == "SessionEnd" && tr.SessionID != "" {
		otelCtx, cancel := context.WithTimeout(ctx, otelBudget)
		if _, err := exp.ExportSession(otelCtx, store, accountID, tr.SessionID); err != nil {
			problems.add("OpenTelemetry: " + err.Error())
		}
		cancel()
	}
	// Code sync runs last, with what is left of the budget.
	a.relayCodeSync(ctx, cfg, configDir, event, problems)
	if msg := problems.String(); msg != "" {
		log.write(fmt.Sprintf("%s session=%s: %s", name, sid, msg))
	}
}

// relayCodeSync syncs the worktree an event changed (plan 0010, "Triggers
// in hook relay") within what is left of the relay's budget, capped at
// codesync.DefaultBudget. Its failures are problems of the run; it never
// fails the hook.
func (a *App) relayCodeSync(ctx context.Context, cfg session.Config, configDir string, event session.Doc, problems *problems) {
	if !cfg.CodeSyncEnabled() || session.NoCapture() {
		return
	}
	req, removeRoot, ok := codeSyncRequest(event)
	if !ok {
		return
	}
	budget := codesync.DefaultBudget
	if deadline, has := ctx.Deadline(); has {
		left := time.Until(deadline)
		if left <= 0 {
			return // spent: the next event picks the work up
		}
		budget = min(budget, left)
	}
	req.Budget = budget
	req.Config = cfg
	req.StateDir = codesync.StateDir(configDir)
	req.Log = func(msg string) { problems.add("code sync: " + msg) }
	if removeRoot != "" {
		if err := codesync.Remove(ctx, req, removeRoot); err != nil {
			problems.add("code sync: remove worktree: " + err.Error())
		}
		return
	}
	rep, err := codesync.Run(ctx, req)
	for _, w := range rep.Warnings {
		problems.add("code sync: " + w)
	}
	if err != nil {
		problems.add("code sync: " + err.Error())
	}
}

// codeSyncRequest maps a hook event to the code sync it triggers: the
// request, or the worktree root a WorktreeRemove drops; ok is false for an
// event that changes no file (PostToolUseFailure among them: nothing was
// written).
func codeSyncRequest(event session.Doc) (req codesync.Request, removeRoot string, ok bool) {
	name, _ := event.String("hook_event_name")
	req.SessionID, _ = event.String("session_id")
	req.Cwd, _ = event.String("cwd")
	switch name {
	case "SessionStart":
		// A full reconcile when the ref changed or the last one is older
		// than 6 hours (Run decides), else the pending queue.
		req.Touch = codesync.TouchStart
	case "SessionEnd":
		req.Touch = codesync.TouchEnd
	case "PostToolUse":
		tool, _ := event.String("tool_name")
		switch tool {
		case "Write", "Edit", "MultiEdit", "NotebookEdit":
			in := event.ToolInput()
			p, _ := in.String("file_path")
			if p == "" {
				p, _ = in.String("notebook_path")
			}
			if p == "" {
				return req, "", false
			}
			// Run keeps it only when it sits inside the worktree root.
			req.Paths = []string{p}
		case "Bash":
			// mv, rm, generators, checkouts, and the HEAD and upstream
			// check after a commit, push, pull, fetch or reset.
			req.Status = true
		default:
			return req, "", false
		}
	case "FileChanged":
		p, _ := event.String("file_path")
		if p == "" {
			return req, "", false
		}
		req.Paths = []string{p}
	case "Stop", "SubagentStop":
		req.Status = true
	case "CwdChanged":
		if dir, _ := event.String("new_cwd"); dir != "" {
			req.Cwd = dir
		}
		req.Touch = codesync.TouchStart
	case "WorktreeCreate":
		// The new worktree has no state yet, so its first run (here, or at
		// the first event inside it) is a full reconcile.
		dir, _ := event.String("worktree_path")
		if dir == "" {
			return req, "", false
		}
		req.Cwd = dir
		req.Full = true
	case "WorktreeRemove":
		dir, _ := event.String("worktree_path")
		if dir == "" || !filepath.IsAbs(dir) {
			return req, "", false
		}
		return req, filepath.Clean(dir), true
	default:
		return req, "", false
	}
	return req, "", true
}

// relayLog appends one timestamped line per failed run to relay.log.
type relayLog struct {
	path string
}

func (l relayLog) write(msg string) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line := time.Now().UTC().Format(time.RFC3339) + " " + strings.ReplaceAll(strings.TrimSpace(msg), "\n", " ") + "\n"
	_, _ = f.WriteString(line)
}

// problems collects what went wrong in one relay run: the relay's own
// notes and, as a slog.Handler, the errors the ingest service logs for the
// session bookkeeping it does not return. Warnings are dropped, since the
// buffered insert they describe is reported through ErrBuffered.
type problems struct {
	mu    sync.Mutex
	notes []string
}

func (p *problems) add(note string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notes = append(p.notes, note)
}

// String joins the notes with "; ", empty when nothing went wrong.
func (p *problems) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.notes, "; ")
}

func (p *problems) Enabled(_ context.Context, level slog.Level) bool { return level >= slog.LevelError }

func (p *problems) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(attr slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", attr.Key, attr.Value)
		return true
	})
	p.add(b.String())
	return nil
}

func (p *problems) WithAttrs([]slog.Attr) slog.Handler { return p }
func (p *problems) WithGroup(string) slog.Handler      { return p }
