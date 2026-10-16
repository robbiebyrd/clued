package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/digest"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/memory"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
	"github.com/robbiebyrd/clued/mind-palace/session/remotesession"
	"github.com/robbiebyrd/clued/mind-palace/session/summary"
)

// memoryLockWait is how long `session memory run --session` waits for
// another run of the project to finish.
var memoryLockWait = 60 * time.Second

// sessionMemoryCmd is `session memory`: run, inspect and edit native
// memory.
func (a *App) sessionMemoryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "memory",
		Short: "Run, show, edit, export and import clued's native memory (session notes, day, recent and archive roll-ups, notes, handoffs, pinned notes)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return a.sessionBadRequest(fmt.Sprintf("unknown memory command %q; see `%s --help`", args[0], cmd.CommandPath()))
			}
			return cmd.Help()
		},
	}
	c.AddCommand(a.memoryRunCmd(), a.memoryStatusCmd(), a.memoryShowCmd(), a.memoryEditCmd(), a.memoryRememberCmd(), a.memoryForgetCmd(), a.memoryExportCmd(), a.memoryImportCmd())
	return c
}

// projectFlags select the project a memory command acts on.
type projectFlags struct {
	project, cwd string
	all          bool
}

func (p *projectFlags) add(c *cobra.Command, all bool) {
	f := c.Flags()
	f.StringVar(&p.project, "project", "", "project key (the normalised git origin such as github.com/owner/repo, else the project path)")
	f.StringVar(&p.cwd, "cwd", "", "project directory whose key to use (default: the working directory)")
	if all {
		f.BoolVar(&p.all, "all", false, "every project")
	}
}

// resolve is the project key: --project, else the key of --cwd (or the
// working directory); "" with --all.
func (p *projectFlags) resolve(ctx context.Context) (string, error) {
	if p.all {
		return "", nil
	}
	if p.project != "" {
		return p.project, nil
	}
	dir := p.cwd
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return memory.ProjectKey(gitinfo.Origin(ctx, abs), abs), nil
}

// memoryDir holds the memory state (run lock, run counts) and prompt
// overrides: memory/ next to the config file.
func (a *App) memoryDir() string {
	path := a.sess.configPath
	if path == "" {
		path = session.DefaultConfigPath()
	}
	return filepath.Join(filepath.Dir(path), "memory")
}

// memoryService builds the memory service over the store with the
// config's limits; built-in and configured redaction always apply.
func (a *App) memoryService(store session.Store, engine string) *memory.Service {
	cfg := a.sessionConfig()
	account := session.AccountIDFor(cfg)
	r, errs := redact.New(cfg.RedactPatterns)
	for _, err := range errs {
		log.Printf("clued: %v", err)
	}
	dir := a.memoryDir()
	sum := &summary.Service{
		Store: store, AccountID: account, Redact: r, TemplateDir: dir,
		Query:      &query.Service{Store: store, AccountID: account, Prices: cfg.ModelPrices},
		MinPrompts: cfg.Memory.MinPrompts, Exclude: cfg.Memory.Exclude,
	}
	limits := memory.Limits{MaxRunsPerDay: cfg.Memory.MaxRunsPerDay, MinPrompts: cfg.Memory.MinPrompts}
	if engine == session.MemoryEngineHosted {
		limits.MaxRunsPerDay = -1 // the cloud's allowance is the cap
	}
	return &memory.Service{Store: store, AccountID: account, Summary: sum, Redact: r, Limits: limits, StateDir: dir, TemplateDir: dir}
}

// memoryEngine is the engine of a name; nil for off.
func (a *App) memoryEngine(name string) (digest.Engine, error) {
	switch name {
	case session.MemoryEngineExtract:
		return summary.ExtractEngine{}, nil
	case session.MemoryEngineClaude:
		return digest.ClaudeEngine{}, nil
	case session.MemoryEngineHosted:
		cfg := a.sessionConfig()
		client, err := remote.FromConfigE(cfg)
		if err != nil {
			return nil, err
		}
		if client == nil {
			return memory.HostedEngine{}, nil
		}
		hosted, _ := remotesession.New(client).(memory.HostedClient)
		return memory.HostedEngine{Client: hosted}, nil
	case session.MemoryEngineOff:
		return nil, nil
	}
	return nil, fmt.Errorf("unknown memory engine %q: use %s", name, strings.Join(session.MemoryEngines, ", "))
}

// withMemory connects the store and runs fn with the memory service.
func (a *App) withMemory(cmd *cobra.Command, engine string, fn func(ctx context.Context, svc *memory.Service) error) error {
	defer a.closeSessionStore()
	store, err := a.sessionStore(cmd.Context())
	if err != nil {
		return a.sessionFail(err)
	}
	return fn(cmd.Context(), a.memoryService(store, engine))
}

func (a *App) ok(v any) {
	a.print(a.Stdout, map[string]any{"ok": true, "result": session.JSONTimes(v)})
}

func (a *App) memoryRunCmd() *cobra.Command {
	var pf projectFlags
	var sessionID, engineName, model string
	var checkpoint, quiet bool
	var delay time.Duration
	c := &cobra.Command{
		Use:   "run [--project p | --cwd d | --all] [--session id [--checkpoint]] [--engine e] [--model m] [--delay d] [--quiet]",
		Short: "Bring memory up to date: session notes, then the day, recent and archive roll-ups",
		Long: `Writes the notes of the project's sessions that need one (ended, or idle for
30 minutes, within the last seven days), then one day record per finished UTC
day, the recent record over the last seven days and one archive per month of
older days. A run with nothing new makes no engine call. With --session the
session's note is written at once (what the SessionEnd hook runs, after
--delay so the capture can catch up), or with --checkpoint its checkpoint
(PreCompact). The engine is the config's memory.engine unless --engine:
extract (free, deterministic), claude (your own "claude -p", at most
memory.maxRunsPerDay calls a day, then extract), hosted (Clued Cloud's
allowance, then extract) or off (nothing runs).`,
		Args: cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg := a.sessionConfig()
		if engineName == "" {
			engineName = cfg.Memory.Engine
		}
		engine, err := a.memoryEngine(engineName)
		if err != nil {
			return a.sessionBadRequest(err.Error())
		}
		if checkpoint && sessionID == "" {
			return a.sessionBadRequest("--checkpoint needs --session")
		}
		if model == "" && engineName == session.MemoryEngineClaude {
			model = cfg.Memory.Model
		}
		report := func(v any) {
			if !quiet {
				a.ok(v)
			}
		}
		if engine == nil {
			report(map[string]any{"engine": "off", "skipped": "memory.engine is off"})
			return nil
		}
		if sessionID != "" && delay > 0 {
			select {
			case <-time.After(delay):
			case <-cmd.Context().Done():
				return nil
			}
		}
		return a.withMemory(cmd, engineName, func(ctx context.Context, svc *memory.Service) error {
			if sessionID != "" {
				var rep memory.RunReport
				err := retryLocked(ctx, func() error {
					if checkpoint {
						rep = memory.RunReport{Engine: engine.Name()}
						return svc.Checkpoint(ctx, sessionID, engine, model)
					}
					var err error
					rep, err = svc.RunSession(ctx, sessionID, engine, model)
					return err
				})
				switch {
				case errors.Is(err, summary.ErrSkipped):
					report(map[string]any{"engine": engine.Name(), "skipped": err.Error()})
					return nil
				case err != nil:
					return a.sessionFail(err)
				}
				report(rep)
				return nil
			}
			project, err := pf.resolve(ctx)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			rep, err := svc.Run(ctx, project, engine, model)
			if errors.Is(err, memory.ErrLocked) {
				return a.fail(&service.Error{Kind: service.KindConflict, Message: err.Error()})
			}
			if err != nil {
				return a.sessionFail(err)
			}
			report(rep)
			return nil
		})
	}
	pf.add(c, true)
	f := c.Flags()
	f.StringVar(&sessionID, "session", "", "write this session's note now (then run its project)")
	f.BoolVar(&checkpoint, "checkpoint", false, "with --session: write the session's checkpoint instead (PreCompact)")
	f.StringVar(&engineName, "engine", "", "extract, claude, hosted or off (default: the config's memory.engine)")
	f.StringVar(&model, "model", "", "model of the claude engine (default: the config's memory.model)")
	f.DurationVar(&delay, "delay", 0, "with --session: wait this long first, so the capture catches up with the session's last lines")
	f.BoolVar(&quiet, "quiet", false, "print nothing on success")
	return c
}

// retryLocked runs fn again while another run holds the project's lock,
// for at most memoryLockWait.
func retryLocked(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(memoryLockWait)
	for {
		err := fn()
		if !errors.Is(err, memory.ErrLocked) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return err
		}
	}
}

func (a *App) memoryStatusCmd() *cobra.Command {
	var pf projectFlags
	c := &cobra.Command{
		Use:   "status [--project p | --cwd d | --all]",
		Short: "Show the project's memory: records per layer, engine runs today against the cap, cost this month, the engine",
		Args:  cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg := a.sessionConfig()
		return a.withMemory(cmd, cfg.Memory.Engine, func(ctx context.Context, svc *memory.Service) error {
			project, err := pf.resolve(ctx)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			st, err := svc.Status(ctx, project)
			if err != nil {
				return a.sessionFail(err)
			}
			a.ok(map[string]any{
				"project": project, "engine": cfg.Memory.Engine, "model": cfg.Memory.Model,
				"budget_tokens": cfg.Memory.BudgetTokens, "min_prompts": cfg.Memory.MinPrompts, "exclude": nonNilStrings(cfg.Memory.Exclude),
				"records": st.Records, "private": st.Private, "edited": st.Edited,
				"runs_today": st.RunsToday, "max_runs_per_day": st.MaxRunsPerDay, "cost_month_usd": st.CostMonthUSD, "last_built": st.LastBuilt,
			})
			return nil
		})
	}
	pf.add(c, true)
	return c
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *App) memoryShowCmd() *cobra.Command {
	var pf projectFlags
	var kinds []string
	var private, bundle, compact bool
	c := &cobra.Command{
		Use:   "show [--project p | --cwd d | --all] [--kind k]… [--include-private] [--bundle [--compact]]",
		Short: "List the project's memory records newest first, or with --bundle what the prime report injects",
		Args:  cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg := a.sessionConfig()
		return a.withMemory(cmd, cfg.Memory.Engine, func(ctx context.Context, svc *memory.Service) error {
			project, err := pf.resolve(ctx)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			var notes []memory.Note
			if bundle {
				mode := memory.BundleStart
				if compact {
					mode = memory.BundleCompact
				}
				notes, err = svc.Bundle(ctx, project, cfg.Memory.BudgetTokens, mode)
			} else {
				notes, err = svc.Show(ctx, project, private, kinds...)
			}
			if err != nil {
				return a.sessionFail(err)
			}
			a.ok(notes)
			return nil
		})
	}
	pf.add(c, true)
	f := c.Flags()
	f.StringArrayVar(&kinds, "kind", nil, "only this kind (memory-pinned, memory-handoff, memory-note, session-summary, memory-checkpoint, memory-day, memory-recent, memory-archive); repeatable")
	f.BoolVar(&private, "include-private", false, "include private notes")
	f.BoolVar(&bundle, "bundle", false, "show the bundle the prime report injects, within memory.budgetTokens")
	f.BoolVar(&compact, "compact", false, "with --bundle: the smaller bundle after a compaction")
	return c
}

func (a *App) memoryEditCmd() *cobra.Command {
	var pf projectFlags
	c := &cobra.Command{
		Use:   "edit [--project p | --cwd d]",
		Short: "Edit the project's memory as Markdown in $VISUAL or $EDITOR: change a block to rewrite its record, delete it to forget it",
		Args:  cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg := a.sessionConfig()
		return a.withMemory(cmd, cfg.Memory.Engine, func(ctx context.Context, svc *memory.Service) error {
			project, err := pf.resolve(ctx)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			md, err := svc.EditMarkdown(ctx, project)
			if err != nil {
				return a.sessionFail(err)
			}
			f, err := os.CreateTemp("", "clued-memory-*.md")
			if err != nil {
				return a.sessionFail(err)
			}
			path := f.Name()
			_, werr := f.WriteString(md)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(path)
				return a.sessionFail(werr)
			}
			if err := a.runEditor(ctx, path); err != nil {
				os.Remove(path)
				return a.sessionBadRequest(err.Error())
			}
			edited, err := os.ReadFile(path)
			if err != nil {
				return a.sessionFail(err)
			}
			if bytes.Equal(edited, []byte(md)) {
				os.Remove(path)
				a.ok(memory.EditReport{Changed: []string{}, Removed: []string{}, Unchanged: strings.Count(md, "<!-- memory id=")})
				return nil
			}
			rep, err := svc.ApplyMarkdown(ctx, project, string(edited))
			if err != nil {
				return a.sessionBadRequest(fmt.Sprintf("%v (your edit is kept in %s)", err, path))
			}
			os.Remove(path)
			a.ok(rep)
			return nil
		})
	}
	pf.add(c, false)
	return c
}

// runEditor opens path in $VISUAL, else $EDITOR, else vi, attached to the
// terminal.
func (a *App) runEditor(ctx context.Context, path string) error {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	cmd := osexec.CommandContext(ctx, "sh", "-c", editor+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, a.Stderr, a.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %q: %w", editor, err)
	}
	return nil
}

func (a *App) memoryRememberCmd() *cobra.Command {
	var pf projectFlags
	var kind string
	var private bool
	c := &cobra.Command{
		Use:   "remember [--project p | --cwd d] [--kind note|handoff|pinned] [--private] text… (\"-\" reads stdin)",
		Short: "Write a note, a handoff for the next session or the project's pinned note",
		Args:  cobra.MinimumNArgs(1),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		text := strings.Join(args, " ")
		if text == "-" {
			raw, err := io.ReadAll(a.Stdin)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			text = string(raw)
		}
		cfg := a.sessionConfig()
		return a.withMemory(cmd, cfg.Memory.Engine, func(ctx context.Context, svc *memory.Service) error {
			project, err := pf.resolve(ctx)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			n, err := svc.Remember(ctx, project, kind, text, private)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			a.ok(n)
			return nil
		})
	}
	pf.add(c, false)
	c.Flags().StringVar(&kind, "kind", "note", "note, handoff (injected into the next sessions for seven days) or pinned (injected into every session; replaces the previous one)")
	c.Flags().BoolVar(&private, "private", false, "keep the note out of every bundle and export")
	return c
}

func (a *App) memoryForgetCmd() *cobra.Command {
	var pf projectFlags
	var ids []string
	var pattern string
	var yes bool
	c := &cobra.Command{
		Use:   "forget [--project p | --cwd d] [--id id]… [--pattern re] [--yes]",
		Short: "Delete memory records by id or by a regex over their text (lists them only, unless --yes)",
		Args:  cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := a.validated(checkRegex("--pattern", pattern)); err != nil {
			return err
		}
		cfg := a.sessionConfig()
		return a.withMemory(cmd, cfg.Memory.Engine, func(ctx context.Context, svc *memory.Service) error {
			project, err := pf.resolve(ctx)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			notes, err := svc.Forget(ctx, project, ids, pattern, !yes)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			a.ok(map[string]any{"dry_run": !yes, "project": project, "records": notes})
			return nil
		})
	}
	pf.add(c, false)
	f := c.Flags()
	f.StringArrayVar(&ids, "id", nil, "memory id to forget; repeatable")
	f.StringVar(&pattern, "pattern", "", "forget the records whose text matches this regular expression")
	f.BoolVar(&yes, "yes", false, "delete (without it the records are only listed)")
	return c
}

func (a *App) memoryExportCmd() *cobra.Command {
	var pf projectFlags
	var out string
	c := &cobra.Command{
		Use:   "export [--project p | --cwd d | --all] [--out file]",
		Short: "Write the project's memory as JSONL (stdout, or --out): a backup that import restores; private notes are never exported",
		Args:  cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg := a.sessionConfig()
		return a.withMemory(cmd, cfg.Memory.Engine, func(ctx context.Context, svc *memory.Service) error {
			project, err := pf.resolve(ctx)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			if out == "" {
				if err := svc.Export(ctx, a.Stdout, project); err != nil {
					return a.sessionFail(err)
				}
				return nil
			}
			var buf bytes.Buffer
			if err := svc.Export(ctx, &buf, project); err != nil {
				return a.sessionFail(err)
			}
			f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			_, werr := f.Write(buf.Bytes())
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				return a.sessionFail(werr)
			}
			a.ok(map[string]any{"path": out, "records": bytes.Count(buf.Bytes(), []byte("\n"))})
			return nil
		})
	}
	pf.add(c, true)
	c.Flags().StringVar(&out, "out", "", "file to write (created with mode 0600; an existing file is never replaced)")
	return c
}

func (a *App) memoryImportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "import file|-",
		Short: "Store the records of a memory export under this account, each once",
		Args:  cobra.ExactArgs(1),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		var r io.Reader = a.Stdin
		if args[0] != "-" {
			f, err := os.Open(args[0])
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			defer f.Close()
			r = f
		}
		cfg := a.sessionConfig()
		return a.withMemory(cmd, cfg.Memory.Engine, func(ctx context.Context, svc *memory.Service) error {
			n, err := svc.Import(ctx, r)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			a.ok(map[string]any{"imported": n})
			return nil
		})
	}
	return c
}
