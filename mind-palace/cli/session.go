package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/backfill"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/mongosession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/restore"
)

// openSessionStore connects the session store; tests replace it with an
// in-memory store.
var openSessionStore = func(ctx context.Context, cfg session.Config) (session.Store, error) {
	s := mongosession.New(cfg)
	return s, s.Connect(ctx)
}

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
		a.sessionFindCmd(), a.sessionSearchCommandsCmd(), a.sessionContextCmd(), a.sessionFullCmd(),
		a.sessionTranscriptCmd(), a.sessionRestoreCmd(), a.sessionBackfillCmd(),
	)
	return c
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
		svc := &query.Service{Store: store, AccountID: session.ReadAccountID(a.sessionConfig().ClaudeAppConfigPath)}
		result, err := fn(cmd.Context(), store, svc)
		if err != nil {
			return a.sessionFail(err)
		}
		a.print(a.Stdout, map[string]any{"ok": true, "result": result})
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
