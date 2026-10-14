package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/collect/prime"
	"github.com/robbiebyrd/clued/mind-palace/memory"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/backfill"
	"github.com/robbiebyrd/clued/mind-palace/session/daemon"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/ingest"
	"github.com/robbiebyrd/clued/mind-palace/session/mongosession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/remotesession"
	"github.com/robbiebyrd/clued/mind-palace/session/restore"
	"github.com/robbiebyrd/clued/mind-palace/session/sessionmcp"
)

// openSessionStore connects the session store: the remote adapter when the
// config names a server (serverUrl / CLUED_SERVER_URL), the local MongoDB
// otherwise. A server URL the remote client refuses, such as plain http://
// off loopback without serverInsecure, is the error. Tests replace it with
// an in-memory store.
var openSessionStore = func(ctx context.Context, cfg session.Config) (session.Store, error) {
	client, err := remote.FromConfigE(cfg)
	if err != nil {
		return nil, err
	}
	if client != nil {
		return remotesession.New(client), nil
	}
	return openMongoSessionStore(ctx, cfg)
}

// openMongoSessionStore connects the local MongoDB store; the remote-mode
// test replaces it to prove it is never reached.
var openMongoSessionStore = func(ctx context.Context, cfg session.Config) (session.Store, error) {
	s := mongosession.New(cfg)
	return s, s.Connect(ctx)
}

// stdioTransport is the transport the MCP commands serve on; tests replace it
// with an in-memory one.
var stdioTransport = func() mcp.Transport { return &mcp.StdioTransport{} }

// sessionState is what the session commands share within one process: the
// --config path, the loaded config and the connected store.
type sessionState struct {
	configPath string
	cfg        *session.Config
	store      session.Store
}

// sessionConfig loads the capture configuration once.
func (a *App) sessionConfig() session.Config {
	if a.sess.cfg == nil {
		cfg := session.LoadConfig(a.sess.configPath)
		a.sess.cfg = &cfg
	}
	return *a.sess.cfg
}

// sessionStore connects the session store once per process.
func (a *App) sessionStore(ctx context.Context) (session.Store, error) {
	if a.sess.store == nil {
		store, err := openSessionStore(ctx, a.sessionConfig())
		if err != nil {
			return nil, err
		}
		a.sess.store = store
	}
	return a.sess.store, nil
}

// sessionTools builds the session MCP tools; the store is connected on the
// first tool call, so serving them never needs the database to be reachable.
func (a *App) sessionTools() *sessionmcp.Tools {
	cfg := a.sessionConfig()
	connect := func(ctx context.Context) (session.Store, error) { return openSessionStore(ctx, cfg) }
	tools := sessionmcp.New(cfg, connect, session.ReadAccountID(cfg.ClaudeAppConfigPath))
	tools.ConfigPath = a.sess.configPath
	return tools
}

// closeSessionStore disconnects the store if one was opened.
func (a *App) closeSessionStore() {
	if a.sess.store != nil {
		_ = a.sess.store.Close(context.Background())
		a.sess.store = nil
	}
}

// sessionCmd builds the `session` group. Add further subcommands to the
// list below.
func (a *App) sessionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "session",
		Short: "Query, restore and backfill captured Claude Code sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return a.sessionBadRequest(fmt.Sprintf("unknown session command %q; see `%s --help`", args[0], cmd.CommandPath()))
			}
			return cmd.Help()
		},
	}
	c.PersistentFlags().StringVar(&a.sess.configPath, "config", session.DefaultConfigPath(), "clued config file")
	c.AddCommand(
		a.sessionFindCmd(), a.sessionSearchCommandsCmd(), a.sessionSearchCmd(), a.sessionUsageCmd(), a.sessionContextCmd(), a.sessionFullCmd(),
		a.sessionTranscriptCmd(), a.sessionRestoreCmd(), a.sessionBackfillCmd(),
		a.sessionDaemonCmd(), a.sessionMCPCmd(), a.sessionWhoamiCmd(),
		a.sessionSearchMemoryCmd(), a.sessionLimitsCmd(), a.sessionActivitiesCmd(), a.sessionMemoryIngestCmd(),
		a.sessionLoginCmd(), a.sessionLogoutCmd(), a.sessionConfigPullCmd(),
	)
	return c
}

func (a *App) sessionSearchMemoryCmd() *cobra.Command {
	var args query.SearchMemoryArgs
	c := &cobra.Command{
		Use:   "search-memory [pattern] [--kind k] [--session id] [--git-origin re] [--project-path re] [--limit n]",
		Short: "Search the claude-remember summaries, handoffs and identity notes captured as MemorySaved events, newest first",
		Args:  cobra.MaximumNArgs(1),
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.SearchMemory(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		if len(positional) == 1 {
			args.Pattern = positional[0]
		}
		if err := a.validated(
			checkRegex("pattern", args.Pattern), checkRegex("--git-origin", args.GitOrigin),
			checkRegex("--project-path", args.ProjectPath), checkNotNegative("--limit", args.Limit),
		); err != nil {
			return err
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringVar(&args.Kind, "kind", "", "only this kind of record: "+strings.Join(memory.Kinds, ", "))
	f.StringVar(&args.SessionID, "session", "", "only this session")
	f.StringVar(&args.GitOrigin, "git-origin", "", "only sessions whose git origin matches this regular expression")
	f.StringVar(&args.ProjectPath, "project-path", "", "only sessions whose project path matches this regular expression")
	f.IntVar(&args.Limit, "limit", 0, "maximum records (default 20, at most 500)")
	return c
}

func (a *App) sessionMemoryIngestCmd() *cobra.Command {
	var opts memory.IngestOptions
	var quiet bool
	c := &cobra.Command{
		Use:   "memory-ingest [--dir d]... [--cwd c] [--session id] [--quiet]",
		Short: "Post the new records of a claude-remember store to the daemon as MemorySaved events",
		Long: `Reads the claude-remember memory store of the working directory (its
.remember/ folder and ~/.remember/<slug>/, or the --dir folders given) and
posts every record not posted before to the daemon as a MemorySaved event;
when the daemon is unreachable the events go to the write-ahead log it
replays. In remote mode the events are stored on the clued server instead,
with the same write-ahead log as the fallback. A state file under the clued
data directory keeps the ingest idempotent. The claude-remember hooks.d listener and the clued session hooks
run this; it prints nothing with --quiet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := a.sessionConfig()
			if opts.Cwd == "" {
				wd, err := os.Getwd()
				if err != nil {
					return a.sessionBadRequest("cannot determine the working directory: " + err.Error())
				}
				opts.Cwd = wd
			}
			opts.Home, _ = os.UserHomeDir()
			opts.ProjectsDir = cfg.ProjectsDir
			opts.StateDir = memoryStateDir(cfg)
			opts.Poster = memory.DaemonPoster{Port: cfg.Port, WalPath: cfg.WalPath}
			if cfg.Remote() {
				store, err := a.sessionStore(cmd.Context())
				if err != nil {
					return a.sessionFail(err)
				}
				defer a.closeSessionStore()
				opts.Poster = storePoster{&ingest.Service{
					Store:     store,
					AccountID: session.ReadAccountID(cfg.ClaudeAppConfigPath),
					Host:      hostinfo.Read(),
					Logger:    slog.New(slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
					WALPath:   cfg.WalPath,
				}}
			}
			res, err := memory.Ingest(cmd.Context(), opts)
			if err != nil {
				return a.sessionFail(err)
			}
			if !quiet {
				a.print(a.Stdout, map[string]any{"ok": true, "result": res})
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringArrayVar(&opts.Dirs, "dir", nil, "claude-remember store to read (default: the stores of --cwd)")
	f.StringVar(&opts.Cwd, "cwd", "", "project directory (default: the working directory)")
	f.StringVar(&opts.SessionID, "session", "", "session id to attach (default: the project's newest transcript)")
	f.BoolVar(&quiet, "quiet", false, "print nothing on success")
	return c
}

// storePoster delivers memory events straight into a session store (remote
// mode, where no daemon runs) through the same ingest service the hook relay
// uses: the event is stamped, inserted, and buffered in the WAL when the
// server is down. ErrBuffered counts as delivered, as it does for the relay.
type storePoster struct {
	svc *ingest.Service
}

func (p storePoster) Post(ctx context.Context, ev memory.Event) error {
	_, err := p.svc.Handle(ctx, ev.Doc())
	if err != nil && !errors.Is(err, ingest.ErrBuffered) {
		return err
	}
	return nil
}

// sessionLimitsCmd is `session limits`: the LimitsSampled events `hook status`
// posted, newest first.
func (a *App) sessionLimitsCmd() *cobra.Command {
	var args query.LimitsArgs
	c := &cobra.Command{
		Use:   "limits [--session id] [--git-origin re] [--project-path re] [--since t] [--limit n]",
		Short: "Show the context-window and usage-limit samples the status line captured (LimitsSampled events), newest first",
		Args:  cobra.NoArgs,
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.Limits(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		if err := a.validated(
			checkRegex("--git-origin", args.GitOrigin), checkRegex("--project-path", args.ProjectPath),
			checkNotNegative("--limit", args.Limit),
		); err != nil {
			return err
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringVar(&args.SessionID, "session", "", "only this session")
	f.StringVar(&args.GitOrigin, "git-origin", "", "only sessions whose git origin matches this regular expression")
	f.StringVar(&args.ProjectPath, "project-path", "", "only sessions whose project path matches this regular expression")
	f.StringVar(&args.Since, "since", "", "only samples after this RFC 3339 time, or within this duration back from now (e.g. 24h)")
	f.IntVar(&args.Limit, "limit", 0, "maximum samples (default 20, at most 500)")
	return c
}

// sessionActivitiesCmd is `session activities`: the timeline the
// activity-kind enricher labelled, what the get_activities tool returns.
func (a *App) sessionActivitiesCmd() *cobra.Command {
	var args query.ActivitiesArgs
	c := &cobra.Command{
		Use:   "activities [--session id] [--git-origin re] [--project-path re] [--kind k]… [--since t] [--limit n]",
		Short: "Show what the agent did (prompts, replies, thoughts, commands, edits, reads, tasks, commits), newest first, repeats collapsed",
		Args:  cobra.NoArgs,
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.Activities(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		if err := a.validated(
			checkRegex("--git-origin", args.GitOrigin), checkRegex("--project-path", args.ProjectPath),
			checkNotNegative("--limit", args.Limit),
		); err != nil {
			return err
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringVar(&args.SessionID, "session", "", "only this session")
	f.StringVar(&args.GitOrigin, "git-origin", "", "only sessions whose git origin matches this regular expression")
	f.StringVar(&args.ProjectPath, "project-path", "", "only sessions whose project path matches this regular expression")
	f.StringArrayVar(&args.Kinds, "kind", nil, "only this activity kind (user, response, thinking, bash, commit, edit, read, task, tool, result, other); repeatable")
	f.StringVar(&args.Since, "since", "", "only lines after this RFC 3339 time, or within this duration back from now (e.g. 24h)")
	f.IntVar(&args.Limit, "limit", 0, "maximum lines read before repeats are collapsed (default 50, at most 500)")
	return c
}

// memoryStateDir is where the ingest keeps its per-store state: next to the
// write-ahead log, which lives next to the config file.
func memoryStateDir(cfg session.Config) string {
	base := cfg.WalPath
	if base == "" {
		base = session.DefaultConfigPath()
	}
	return filepath.Join(filepath.Dir(base), "remember")
}

func (a *App) sessionBadRequest(msg string) error {
	return a.fail(&service.Error{Kind: service.KindBadRequest, Message: msg})
}

// sessionFail reports a session error: a missing session is NotFound, anything
// else a storage error.
func (a *App) sessionFail(err error) error {
	kindName := service.KindStorage
	if errors.Is(err, session.ErrNotFound) {
		kindName = service.KindNotFound
	}
	return a.fail(&service.Error{Kind: kindName, Message: err.Error()})
}

// sessionAction runs fn against the connected store and the account's query
// service, prints its result in the ok envelope, and closes the store.
func (a *App) sessionAction(fn func(ctx context.Context, store session.Store, svc *query.Service) (any, error)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		defer a.closeSessionStore()
		store, err := a.sessionStore(cmd.Context())
		if err != nil {
			return a.sessionFail(err)
		}
		cfg := a.sessionConfig()
		svc := &query.Service{Store: store, AccountID: session.ReadAccountID(cfg.ClaudeAppConfigPath), Prices: cfg.ModelPrices}
		result, err := fn(cmd.Context(), store, svc)
		if err != nil {
			return a.sessionFail(err)
		}
		a.print(a.Stdout, map[string]any{"ok": true, "result": session.JSONTimes(result)})
		return nil
	}
}

// checkRegex rejects a pattern the stores could not compile.
func checkRegex(flag, pattern string) error {
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("%s is not a valid regular expression: %w", flag, err)
	}
	return nil
}

// checkNotNegative rejects a negative count flag.
func checkNotNegative(flag string, n int) error {
	if n < 0 {
		return fmt.Errorf("%s must not be negative, got %d", flag, n)
	}
	return nil
}

// validated prints a BadRequest for the first non-nil check error.
func (a *App) validated(checks ...error) error {
	for _, err := range checks {
		if err != nil {
			return a.sessionBadRequest(err.Error())
		}
	}
	return nil
}

func (a *App) sessionFindCmd() *cobra.Command {
	var args query.FindSessionsArgs
	c := &cobra.Command{
		Use:   "find [--project-path re] [--git-origin re] [--query re] [--limit n]",
		Short: "Find sessions by project path, git origin or text, newest first",
		Args:  cobra.NoArgs,
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.FindSessions(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := a.validated(
			checkRegex("--project-path", args.ProjectPath), checkRegex("--git-origin", args.GitOrigin),
			checkRegex("--query", args.Query), checkNotNegative("--limit", args.Limit),
		); err != nil {
			return err
		}
		return run(cmd, nil)
	}
	f := c.Flags()
	f.StringVar(&args.ProjectPath, "project-path", "", "regular expression matching the project path")
	f.StringVar(&args.GitOrigin, "git-origin", "", "regular expression matching the git origin")
	f.StringVar(&args.Query, "query", "", "regular expression matching the project path or working directory")
	f.IntVar(&args.Limit, "limit", 0, "maximum sessions (default 10, at most 500)")
	return c
}

func (a *App) sessionSearchCommandsCmd() *cobra.Command {
	var args query.SearchCommandsArgs
	c := &cobra.Command{
		Use:   "search-commands <pattern> [--session id] [--git-origin re] [--limit n]",
		Short: "Search the Bash commands run in sessions, newest first",
		Args:  cobra.ExactArgs(1),
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.SearchCommands(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		args.Pattern = positional[0]
		if err := a.validated(
			checkRegex("pattern", args.Pattern), checkRegex("--git-origin", args.GitOrigin),
			checkNotNegative("--limit", args.Limit),
		); err != nil {
			return err
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringVar(&args.SessionID, "session", "", "only this session")
	f.StringVar(&args.GitOrigin, "git-origin", "", "only sessions whose git origin matches this regular expression")
	f.IntVar(&args.Limit, "limit", 0, "maximum commands (default 20, at most 500)")
	return c
}

func (a *App) sessionUsageCmd() *cobra.Command {
	var args query.UsageArgs
	c := &cobra.Command{
		Use:   "usage [--by session|model|day] [--session id] [--git-origin re] [--project re] [--since day] [--until day] [--limit n]",
		Short: "Token usage and estimated cost by session, model or day",
		Args:  cobra.NoArgs,
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.Usage(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		if err := a.validated(
			checkRegex("--git-origin", args.GitOrigin), checkRegex("--project", args.ProjectPath),
			checkNotNegative("--limit", args.Limit),
		); err != nil {
			return err
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringVar(&args.GroupBy, "by", "", "group by session (default), model or day")
	f.StringVar(&args.SessionID, "session", "", "only this session")
	f.StringVar(&args.GitOrigin, "git-origin", "", "only sessions whose git origin matches this regular expression")
	f.StringVar(&args.ProjectPath, "project", "", "only sessions whose project path matches this regular expression")
	f.StringVar(&args.Since, "since", "", "first day to count (YYYY-MM-DD, UTC)")
	f.StringVar(&args.Until, "until", "", "last day to count (YYYY-MM-DD, UTC)")
	f.IntVar(&args.Limit, "limit", 0, "maximum groups (default 50, at most 500)")
	return c
}

func (a *App) sessionSearchCmd() *cobra.Command {
	var args query.SearchTranscriptsArgs
	c := &cobra.Command{
		Use:   "search <words…> [--session id] [--git-origin re] [--project re] [--limit n]",
		Short: "Search what was said in sessions by words, best match first",
		Args:  cobra.MinimumNArgs(1),
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.SearchTranscripts(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		args.Query = strings.Join(positional, " ")
		if err := a.validated(
			checkRegex("--git-origin", args.GitOrigin), checkRegex("--project", args.ProjectPath),
			checkNotNegative("--limit", args.Limit),
		); err != nil {
			return err
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringVar(&args.SessionID, "session", "", "only this session")
	f.StringVar(&args.GitOrigin, "git-origin", "", "only sessions whose git origin matches this regular expression")
	f.StringVar(&args.ProjectPath, "project", "", "only sessions whose project path matches this regular expression")
	f.IntVar(&args.Limit, "limit", 0, "maximum lines (default 20, at most 500)")
	return c
}

func (a *App) sessionContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "context <session>",
		Short: "Show a session's metadata, recent commands and first and last transcript lines",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
				return svc.SessionContext(ctx, args[0])
			})(cmd, args)
		},
	}
}

func (a *App) sessionFullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "full <session>",
		Short: "Show a session joined with its transcript, subagents, blobs and hook events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
				return svc.FullSession(ctx, args[0])
			})(cmd, args)
		},
	}
}

func (a *App) sessionTranscriptCmd() *cobra.Command {
	var args query.ReadTranscriptArgs
	c := &cobra.Command{
		Use:   "transcript <session> [--offset n] [--limit n]",
		Short: "Read a session's transcript lines",
		Args:  cobra.ExactArgs(1),
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.ReadTranscript(ctx, args, nil)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		args.SessionID = positional[0]
		if err := a.validated(checkNotNegative("--offset", args.Offset), checkNotNegative("--limit", args.Limit)); err != nil {
			return err
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.IntVar(&args.Offset, "offset", 0, "first line to read")
	f.IntVar(&args.Limit, "limit", 0, "maximum lines (default 200, at most 500)")
	return c
}

func (a *App) sessionRestoreCmd() *cobra.Command {
	var args restore.Args
	c := &cobra.Command{
		Use:   "restore <session> [--project-path p] [--projects-dir d]",
		Short: "Rebuild a session's transcript, subagent and blob files for Claude Code",
		Args:  cobra.ExactArgs(1),
	}
	run := a.sessionAction(func(ctx context.Context, store session.Store, svc *query.Service) (any, error) {
		cfg := a.sessionConfig()
		if args.ProjectsDir == "" {
			args.ProjectsDir = cfg.ProjectsDir
		}
		return restore.Session(ctx, store, svc.AccountID, args, cfg.FileHistoryDir)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		args.SessionID = positional[0]
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringVar(&args.ProjectPath, "project-path", "", "project directory to restore into (default: the session's own)")
	f.StringVar(&args.ProjectsDir, "projects-dir", "", "Claude projects directory (default from the config)")
	return c
}

func (a *App) sessionBackfillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backfill",
		Short: "Load the transcripts under the Claude projects directory into the store",
		Args:  cobra.NoArgs,
		RunE: a.sessionAction(func(ctx context.Context, store session.Store, svc *query.Service) (any, error) {
			git := func(ctx context.Context, dir string) (string, string) {
				return gitinfo.Origin(ctx, dir), gitinfo.Branch(ctx, dir)
			}
			// Failing files are reported at warning level; the counts are the result.
			logger := slog.New(slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
			stats, err := backfill.Run(ctx, store, a.sessionConfig().ProjectsDir, svc.AccountID, git, logger)
			return map[string]int{"sessions": stats.Sessions, "lines": stats.Lines}, err
		}),
	}
}

func (a *App) sessionWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Ask the configured clued server who the token is: name, account, projects, server version",
		Long: `Calls GET /api/v1/whoami on the server named by serverUrl (CLUED_SERVER_URL)
with the configured token and prints the principal it resolves to. The
command fails when no server is configured (BadRequest), when the token is
rejected or the server cannot be reached (StorageError), which is what
check-setup and /clued-setup use as the connectivity probe.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := a.sessionConfig()
			client, err := remote.FromConfigE(cfg)
			if err != nil {
				return a.sessionFail(err)
			}
			if client == nil {
				return a.sessionBadRequest(fmt.Sprintf("no server configured: set serverUrl in %s (or CLUED_SERVER_URL)", a.sess.configPath))
			}
			p, err := prime.Whoami(cmd.Context(), client)
			if err != nil {
				return a.sessionFail(err)
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": map[string]any{
				"name": p.Name, "account_id": p.AccountID, "projects": p.Projects, "version": p.Version, "server": client.BaseURL(),
			}})
			return nil
		},
	}
}

// KindConnect is the error kind of a failed store connection; it has no
// dedicated exit code, so the process exits 1.
const kindConnect = "ConnectError"

func (a *App) sessionDaemonCmd() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:   "daemon [--addr host:port]",
		Short: "Run the capture daemon that receives hook events and tails session files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			defer a.closeSessionStore()
			cfg := a.sessionConfig()
			if addr == "" {
				addr = fmt.Sprintf("127.0.0.1:%d", cfg.Port)
			}
			store, err := a.sessionStore(cmd.Context())
			if err != nil {
				return a.fail(&service.Error{Kind: kindConnect, Message: err.Error()})
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			d := daemon.New(daemon.Options{
				Config:    cfg,
				Store:     store,
				Enrichers: session.Active(cfg.DisabledEnrichers, cfg.EnabledEnrichers),
				AccountID: session.ReadAccountID(cfg.ClaudeAppConfigPath),
				Host:      hostinfo.Read(),
				Git: func(ctx context.Context, dir string) (string, string) {
					return gitinfo.Origin(ctx, dir), gitinfo.Branch(ctx, dir)
				},
				Logger: slog.New(slog.NewTextHandler(a.Stderr, nil)),
			})
			// daemon.Run does not report the address it bound, so the configured one is printed.
			fmt.Fprintf(a.Stderr, "clued daemon listening on http://%s\n", addr)
			err = d.Run(ctx, addr)
			switch {
			case errors.Is(err, daemon.ErrAddrInUse):
				fmt.Fprintf(a.Stderr, "clued daemon already running on %s\n", addr)
				return nil
			case err != nil:
				return a.sessionFail(err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&addr, "addr", "", "listen address (default 127.0.0.1:<port from the clued config>)")
	return c
}

func (a *App) sessionMCPCmd() *cobra.Command {
	var prefix string
	c := &cobra.Command{
		Use:   "mcp [--mcp-tool-prefix p]",
		Short: "Serve only the session tools over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			srv := mcp.NewServer(&mcp.Implementation{Name: "clued", Version: sessionmcp.Version}, &mcp.ServerOptions{Instructions: sessionmcp.Instructions})
			a.sessionTools().AddTools(srv, prefix)
			if err := srv.Run(ctx, stdioTransport()); err != nil && !errors.Is(err, context.Canceled) {
				return a.sessionFail(err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&prefix, "mcp-tool-prefix", "", "prefix for MCP tool names")
	return c
}
