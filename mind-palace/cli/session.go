package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/collect/prime"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/backfill"
	"github.com/robbiebyrd/clued/mind-palace/session/daemon"
	"github.com/robbiebyrd/clued/mind-palace/session/export"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/memory"
	"github.com/robbiebyrd/clued/mind-palace/session/mongosession"
	"github.com/robbiebyrd/clued/mind-palace/session/otel"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
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

// sessionStore connects the session store once per process, wrapped so
// that what is captured is redacted and excluded as the config says.
func (a *App) sessionStore(ctx context.Context) (session.Store, error) {
	if a.sess.store == nil {
		store, err := openSessionStore(ctx, a.sessionConfig())
		if err != nil {
			return nil, err
		}
		a.sess.store = capturing(store, a.sessionConfig())
	}
	return a.sess.store, nil
}

// capturing wraps store with the config's redaction and exclusion
// (redact.FromConfig); a pattern that does not compile is reported on the
// log and skipped.
func capturing(store session.Store, cfg session.Config) session.Store {
	wrapped, err := redact.FromConfig(store, cfg)
	if err != nil {
		log.Printf("clued: %v", err)
	}
	return wrapped
}

// sessionTools builds the session MCP tools; the store is connected on the
// first tool call, so serving them never needs the database to be reachable.
func (a *App) sessionTools() *sessionmcp.Tools {
	cfg := a.sessionConfig()
	connect := func(ctx context.Context) (session.Store, error) {
		store, err := openSessionStore(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return capturing(store, cfg), nil
	}
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
		a.sessionFindCmd(), a.sessionSearchCommandsCmd(), a.sessionSearchCmd(), a.sessionUsageCmd(), a.sessionForgetCmd(), a.sessionExportCmd(), a.sessionImportCmd(), a.sessionOTelExportCmd(), a.sessionContextCmd(), a.sessionFullCmd(),
		a.sessionTranscriptCmd(), a.sessionRestoreCmd(), a.sessionBackfillCmd(),
		a.sessionDaemonCmd(), a.sessionMCPCmd(), a.sessionWhoamiCmd(),
		a.sessionSearchMemoryCmd(), a.sessionLimitsCmd(), a.sessionActivitiesCmd(), a.sessionReportCmd(), a.sessionDigestCmd(), a.sessionMemoryCmd(), a.sessionMemoryIngestCmd(),
		a.sessionLoginCmd(), a.sessionLogoutCmd(), a.sessionConfigPullCmd(),
	)
	return c
}

func (a *App) sessionSearchMemoryCmd() *cobra.Command {
	var args query.SearchMemoryArgs
	c := &cobra.Command{
		Use:   "search-memory [pattern] [--kind k] [--project key] [--include-private] [--session id] [--git-origin re] [--project-path re] [--limit n]",
		Short: "Search clued's memory (session notes, roll-ups, notes, handoffs, pinned notes, digests, legacy claude-remember records), newest first",
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
	f.StringVar(&args.Kind, "kind", "", "only this kind of record: "+strings.Join(append(append([]string{}, memory.NativeKinds...), memory.LegacyKinds...), ", ")+", digest")
	f.StringVar(&args.Project, "project", "", "only the native records of this project key")
	f.BoolVar(&args.IncludePrivate, "include-private", false, "include private notes")
	f.StringVar(&args.SessionID, "session", "", "only this session")
	f.StringVar(&args.GitOrigin, "git-origin", "", "only sessions whose git origin matches this regular expression")
	f.StringVar(&args.ProjectPath, "project-path", "", "only sessions whose project path matches this regular expression")
	f.IntVar(&args.Limit, "limit", 0, "maximum records (default 20, at most 500)")
	return c
}

// sessionMemoryIngestCmd is the retired `session memory-ingest`: clued
// has its own memory and no longer reads claude-remember. It stays one
// release as a hidden no-op that accepts any flags and exits 0, so a
// claude-remember listener an older clued installed (and that the
// session-start hook has not removed yet) never fails inside
// claude-remember.
func (a *App) sessionMemoryIngestCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "memory-ingest",
		Short:              "Retired: does nothing (clued has its own memory; see `session memory`)",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE:               func(*cobra.Command, []string) error { return nil },
	}
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
		Use:   "activities [--session id] [--git-origin re] [--project-path re] [--kind k]… [--since t] [--include-rejected] [--limit n]",
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
	f.StringArrayVar(&args.Kinds, "kind", nil, "only this activity kind (user, response, thinking, bash, commit, edit, read, task, tool, result, compaction, api_error, turn, other, state); repeatable")
	f.StringVar(&args.Since, "since", "", "only lines after this RFC 3339 time, or within this duration back from now (e.g. 24h)")
	f.BoolVar(&args.IncludeRejected, "include-rejected", false, "keep the tool calls the harness refused to run, and their results")
	f.IntVar(&args.Limit, "limit", 0, "maximum lines read before repeats are collapsed (default 50, at most 500)")
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
		Use:   "find [--project-path re] [--git-origin re] [--query re] [--title re] [--live-state s] [--hotness h] [--limit n]",
		Short: "Find sessions by project path, git origin, text, live state or hotness, newest first",
		Args:  cobra.NoArgs,
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.FindSessions(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := a.validated(
			checkRegex("--project-path", args.ProjectPath), checkRegex("--git-origin", args.GitOrigin),
			checkRegex("--query", args.Query), checkRegex("--title", args.Title), checkNotNegative("--limit", args.Limit),
		); err != nil {
			return err
		}
		return run(cmd, nil)
	}
	f := c.Flags()
	f.StringVar(&args.ProjectPath, "project-path", "", "regular expression matching the project path")
	f.StringVar(&args.GitOrigin, "git-origin", "", "regular expression matching the git origin")
	f.StringVar(&args.Query, "query", "", "regular expression matching the project path or working directory")
	f.StringVar(&args.Title, "title", "", "regular expression matching the session's title")
	f.StringVar(&args.LiveState, "live-state", "", "only sessions in this live state: working, waiting or ended")
	f.StringVar(&args.Hotness, "hotness", "", "only sessions last active within this grade: hot (30 minutes), warm (an hour), cool (earlier today, UTC) or cold")
	f.IntVar(&args.Limit, "limit", 0, "maximum sessions (default 10, at most 500)")
	return c
}

func (a *App) sessionSearchCommandsCmd() *cobra.Command {
	var args query.SearchCommandsArgs
	c := &cobra.Command{
		Use:   "search-commands <pattern> [--session id] [--git-origin re] [--include-rejected] [--limit n]",
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
	f.BoolVar(&args.IncludeRejected, "include-rejected", false, "keep the commands the harness refused to run")
	f.IntVar(&args.Limit, "limit", 0, "maximum commands (default 20, at most 500)")
	return c
}

func (a *App) sessionExportCmd() *cobra.Command {
	var o export.Options
	var since string
	c := &cobra.Command{
		Use:   "export [--session id]... [--project re] [--since day] [--format md|html|jsonl|zip] [--out path] [--force] [--raw]",
		Short: "Write sessions to Markdown, HTML, Claude Code's JSONL or a zip",
		Long: `Writes the account's sessions to a file: one session as Markdown (md), a
self-contained HTML page (html) or Claude Code's JSONL (jsonl), or any
number as a zip holding, per session, session.json (what session import
reads back), the JSONL and the Markdown. Sessions are named by --session,
else matched by --project and --since. The export is redacted with the
capture rules and redactPatterns unless --raw. The file is written with
mode 0600 and never replaces another unless --force.`,
		Args: cobra.NoArgs,
	}
	run := a.sessionAction(func(ctx context.Context, store session.Store, svc *query.Service) (any, error) {
		cfg := a.sessionConfig()
		o.RedactPatterns, o.Prices = cfg.RedactPatterns, cfg.ModelPrices
		return export.Run(ctx, store, svc.AccountID, o)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		if err := a.validated(checkRegex("--project", o.ProjectPathRe)); err != nil {
			return err
		}
		if !export.ValidFormat(o.Format) {
			return a.sessionBadRequest(fmt.Sprintf("--format %q is not md, html, jsonl or zip", o.Format))
		}
		if since != "" {
			at, err := (query.ForgetArgs{Before: since}).BeforeTime()
			if err != nil {
				return a.sessionBadRequest("--since: " + err.Error())
			}
			o.Since = at
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringSliceVar(&o.SessionIDs, "session", nil, "export this session (repeatable)")
	f.StringVar(&o.ProjectPathRe, "project", "", "export sessions whose project path matches this regular expression")
	f.StringVar(&since, "since", "", "export sessions last seen on or after this day (YYYY-MM-DD, UTC) or RFC 3339 time")
	f.StringVar(&o.Format, "format", export.Markdown, "md, html, jsonl or zip")
	f.StringVar(&o.Out, "out", "", "file to write (default <session id>.<format>, or clued-sessions-<day>.zip)")
	f.BoolVar(&o.Force, "force", false, "replace the file when it exists")
	f.BoolVar(&o.Raw, "raw", false, "do not redact the export")
	return c
}

func (a *App) sessionOTelExportCmd() *cobra.Command {
	var ids []string
	var project, since string
	c := &cobra.Command{
		Use:   "otel-export [--session id]... [--project re] [--since day]",
		Short: "Send sessions to the OpenTelemetry collector as traces",
		Long: `Sends each session's trace (a span for the session, each prompt turn, tool
use and subagent, with token usage) to otelEndpoint as OTLP/HTTP JSON. The
daemon and the relay do this when a session ends; this command sends
older sessions or resends after an outage. Spans sent before are skipped
(the state is under <config dir>/otel).`,
		Args: cobra.NoArgs,
	}
	run := a.sessionAction(func(ctx context.Context, store session.Store, svc *query.Service) (any, error) {
		cfg := a.sessionConfig()
		exp := otel.New(cfg)
		if exp == nil {
			return nil, errors.New("no otelEndpoint is configured")
		}
		if len(ids) == 0 {
			found, err := svc.FindSessions(ctx, query.FindSessionsArgs{ProjectPath: project, Limit: 500})
			if err != nil {
				return nil, err
			}
			cutoff, _ := (query.ForgetArgs{Before: since}).BeforeTime()
			for _, d := range found {
				if seen, _ := d["last_seen"].(time.Time); !cutoff.IsZero() && seen.Before(cutoff) {
					continue
				}
				if id, _ := d.String("session_id"); id != "" {
					ids = append(ids, id)
				}
			}
		}
		out := []otel.Sent{}
		for _, id := range ids {
			sent, err := exp.ExportSession(ctx, store, svc.AccountID, id)
			if err != nil {
				return nil, err
			}
			out = append(out, sent)
		}
		return out, nil
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		if err := a.validated(checkRegex("--project", project)); err != nil {
			return err
		}
		if _, err := (query.ForgetArgs{Before: since}).BeforeTime(); err != nil {
			return a.sessionBadRequest("--since: " + err.Error())
		}
		if a.sessionConfig().OTelEndpoint == "" {
			return a.sessionBadRequest("no otelEndpoint is configured (set it in the config file or CLUED_OTEL_ENDPOINT)")
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringSliceVar(&ids, "session", nil, "send this session (repeatable)")
	f.StringVar(&project, "project", "", "send sessions whose project path matches this regular expression")
	f.StringVar(&since, "since", "", "send sessions last seen on or after this day (YYYY-MM-DD, UTC) or RFC 3339 time")
	return c
}

func (a *App) sessionImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file.jsonl|export.zip>",
		Short: "Read a JSONL transcript or a zip export back into the store",
		Long: `Reads a Claude Code JSONL transcript (the session id from the file name or
its lines, the project from their cwd) or a zip written by session export
into the store, under this machine's account. Lines and blobs are upserted,
so importing twice changes nothing; hook events are imported only for a
session the store did not hold. What is imported is redacted and excluded
as capture is (redactAtCapture, excludePaths).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.sessionAction(func(ctx context.Context, store session.Store, svc *query.Service) (any, error) {
				return export.Import(ctx, store, svc.AccountID, args[0])
			})(cmd, args)
		},
	}
}

func (a *App) sessionForgetCmd() *cobra.Command {
	var args query.ForgetArgs
	c := &cobra.Command{
		Use:   "forget [--session id]... [--project re] [--before day] [--limit n]",
		Short: "Delete captured sessions and everything stored for them",
		Long: `Deletes the account's sessions that match every criterion given (at least
one is required) from the store, local MongoDB or the clued server, with
their hook events, transcript and subagent lines and blobs. Claude Code's
own transcript files are not touched. It cannot be undone.`,
		Args: cobra.NoArgs,
	}
	run := a.sessionAction(func(ctx context.Context, _ session.Store, svc *query.Service) (any, error) {
		return svc.Forget(ctx, args)
	})
	c.RunE = func(cmd *cobra.Command, positional []string) error {
		if err := a.validated(checkRegex("--project", args.ProjectPath)); err != nil {
			return err
		}
		if len(args.SessionIDs) == 0 && args.ProjectPath == "" && args.Before == "" {
			return a.sessionBadRequest(query.ErrNothingToForget.Error())
		}
		if _, err := (query.ForgetArgs{Before: args.Before}).BeforeTime(); err != nil {
			return a.sessionBadRequest(err.Error())
		}
		return run(cmd, positional)
	}
	f := c.Flags()
	f.StringSliceVar(&args.SessionIDs, "session", nil, "forget this session (repeatable)")
	f.StringVar(&args.ProjectPath, "project", "", "forget sessions whose project path matches this regular expression")
	f.StringVar(&args.Before, "before", "", "forget sessions last seen before this day (YYYY-MM-DD, UTC) or RFC 3339 time")
	f.IntVar(&args.Limit, "limit", 0, "with --project or --before, forget at most this many sessions, the oldest first (more: true when others match)")
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
				OTel:   otel.New(cfg),
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
