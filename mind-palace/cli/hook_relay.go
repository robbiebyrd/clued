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
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/ingest"
)

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
write-ahead log. Everything shares a 5 s budget. The command prints nothing
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
	accountID := session.ReadAccountID(cfg.ClaudeAppConfigPath)
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
	if msg := problems.String(); msg != "" {
		log.write(fmt.Sprintf("%s session=%s: %s", name, sid, msg))
	}
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
