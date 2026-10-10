// Package cli is the command line entrypoint of the plan service. It is the
// primary interface for agents, skills and tools: every command prints JSON
// to stdout ({"ok": true, "result": …}) and errors to stderr with an exit
// code that identifies the error kind.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/events"
	"github.com/robbiebyrd/clued/plan/ops"
	"github.com/robbiebyrd/clued/plan/server"
	"github.com/robbiebyrd/clued/plan/server/mcpserver"
	"github.com/robbiebyrd/clued/plan/service"
	"github.com/robbiebyrd/clued/plan/store"
)

// Exit codes by error kind.
var exitCodes = map[string]int{
	service.KindBadRequest:        2,
	service.KindValidation:        3,
	service.KindNotFound:          4,
	service.KindInvalidTransition: 5,
	service.KindImmutableField:    6,
	service.KindUnknownSection:    7,
	service.KindLinkedPlan:        8,
	service.KindConflict:          9,
	service.KindStorage:           10,
}

// ExitCode maps an error to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if code, ok := exitCodes[service.AsError(err).Kind]; ok {
		return code
	}
	return 1
}

// App holds the state shared by commands.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	configPath string
	dir        string
	format     string
	compact    bool

	cfg *config.Config
	svc *service.Service
	reg *ops.Registry
	st  *store.MultiStore
}

// New returns an App bound to the standard streams.
func New() *App {
	return &App{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, reg: ops.Default()}
}

// Main runs the CLI and returns the exit code.
func Main(args []string) int {
	app := New()
	root := app.Root()
	root.SetArgs(args)
	err := root.Execute()
	app.close()
	if err != nil {
		var cerr *cliError
		if !errors.As(err, &cerr) {
			// cobra usage errors
			return 2
		}
		return ExitCode(cerr.err)
	}
	return 0
}

type cliError struct{ err error }

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

func (a *App) close() {
	if a.st != nil {
		_ = a.st.Close()
	}
}

// init loads config and opens the stores lazily, once.
func (a *App) init(ctx context.Context) error {
	if a.svc != nil {
		return nil
	}
	cfg, err := config.Load(a.configPath)
	if err != nil {
		return &service.Error{Kind: service.KindBadRequest, Message: err.Error()}
	}
	if a.dir != "" {
		cfg.PlansDir = a.dir
	}
	st, err := store.OpenAll(ctx, cfg)
	if err != nil {
		return &service.Error{Kind: service.KindStorage, Message: err.Error()}
	}
	svc, err := service.New(cfg, st, events.New())
	if err != nil {
		_ = st.Close()
		return err
	}
	a.cfg, a.st, a.svc = cfg, st, svc
	return nil
}

// Service exposes the service (after init) for embedding callers.
func (a *App) Service(ctx context.Context) (*service.Service, error) {
	if err := a.init(ctx); err != nil {
		return nil, err
	}
	return a.svc, nil
}

func (a *App) fail(err error) error {
	e := service.AsError(err)
	body := map[string]any{"ok": false, "error": e}
	a.print(a.Stderr, body)
	return &cliError{err: e}
}

func (a *App) print(w io.Writer, v any) {
	switch a.format {
	case "yaml":
		b, err := yaml.Marshal(v)
		if err != nil {
			fmt.Fprintln(w, err)
			return
		}
		w.Write(b)
	default:
		enc := json.NewEncoder(w)
		if !a.compact {
			enc.SetIndent("", "  ")
		}
		_ = enc.Encode(v)
	}
}

// run executes an operation and prints its result.
func (a *App) run(cmd *cobra.Command, op string, params any) error {
	ctx := cmd.Context()
	if err := a.init(ctx); err != nil {
		return a.fail(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return a.fail(err)
	}
	res, err := a.reg.Invoke(ctx, a.svc, op, raw)
	if err != nil {
		return a.fail(err)
	}
	return a.emit(res, op)
}

func (a *App) emit(res *ops.Result, op string) error {
	// Raw text for content-ish results when asked for text output.
	if a.format == "text" {
		switch v := res.Value.(type) {
		case string:
			fmt.Fprint(a.Stdout, v)
			if !strings.HasSuffix(v, "\n") {
				fmt.Fprintln(a.Stdout)
			}
			return nil
		}
	}
	body := map[string]any{"ok": true, "result": res.Value}
	if res.Warning != nil {
		body["warning"] = res.Warning
		fmt.Fprintf(a.Stderr, "warning: %s\n", res.Warning.Message)
	}
	a.print(a.Stdout, body)
	return nil
}

// readInput reads a file argument, "-" for stdin, or an inline value when
// allowInline is set and the value is not a readable file.
func (a *App) readInput(arg string, allowInline bool) (string, error) {
	if arg == "-" {
		b, err := io.ReadAll(a.Stdin)
		return string(b), err
	}
	if strings.HasPrefix(arg, "@") {
		b, err := os.ReadFile(arg[1:])
		return string(b), err
	}
	if b, err := os.ReadFile(arg); err == nil {
		return string(b), nil
	} else if !allowInline {
		return "", err
	}
	return arg, nil
}

// Root builds the cobra command tree.
func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "plan",
		Short: "Backend plan manager: save, retrieve and monitor plans",
		Long: `plan is a CRUD service for Plans (design and implementation plans stored as
Markdown with YAML front matter). Every command prints JSON: {"ok": true, "result": ...}.
Errors go to stderr as {"ok": false, "error": {...}} with exit codes:
  2 BadRequest  3 ValidationError  4 NotFound  5 InvalidTransition
  6 ImmutableField  7 UnknownSection  8 LinkedPlan  9 Conflict  10 StorageError

<plan> arguments accept a plan id (0002-a3f) or the path to a plan file.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	pf := root.PersistentFlags()
	pf.StringVar(&a.configPath, "config", "", "config file (default: $PLAN_CONFIG, then plan.config.yaml/json in the working directory or its parents)")
	pf.StringVar(&a.dir, "dir", "", "plans directory for file storage (default: docs/plans)")
	pf.StringVarP(&a.format, "format", "f", "json", "output format: json, yaml or text (text prints content/templates raw)")
	pf.BoolVar(&a.compact, "compact", false, "single-line JSON output")

	root.AddCommand(
		a.createCmd(), a.getCmd(), a.listCmd(), a.updateCmd(), a.deleteCmd(), a.validateCmd(),
		a.templateCmd(),
		a.setTitleCmd(), a.setTypeCmd(), a.setStatusCmd(), a.transitionsCmd(), a.setPriorityCmd(), a.setEffortCmd(), a.clearEffortCmd(), a.patchCmd(),
		a.linkCmd(), a.unlinkCmd(), a.linkStoryCmd(), a.unlinkStoryCmd(), a.addSpecCmd(), a.removeSpecCmd(), a.setWebCmd(), a.removeWebCmd(), a.setRepoCmd(), a.clearRepoCmd(),
		a.progressCmd(), a.setProgressCmd(), a.progressStoryCmd(), a.progressUnstoryCmd(), a.removeProgressCmd(),
		a.syncCmd(), a.serveCmd(), a.mcpCmd(), a.callCmd(), a.opsCmd(), a.configCmd(), a.schemaCmd(), a.storesCmd(),
	)
	return root
}

// ---------------------------------------------------------------------------
// Plans

func (a *App) createCmd() *cobra.Command {
	var input, template string
	c := &cobra.Command{
		Use:   "create --input plan.json",
		Short: "Create a Plan from JSON matching the creation schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "--input is required (a file, - for stdin, or inline JSON)"})
			}
			data, err := a.readInput(input, true)
			if err != nil {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
			}
			return a.run(cmd, "create", ops.CreateParams{Input: json.RawMessage(data), Template: template})
		},
	}
	c.Flags().StringVarP(&input, "input", "i", "", "JSON input: file path, - for stdin, or inline JSON")
	c.Flags().StringVarP(&template, "template", "t", "", "content template id (default: default)")
	return c
}

func (a *App) getCmd() *cobra.Command {
	var fm, content bool
	c := &cobra.Command{
		Use:   "get <plan>",
		Short: "Retrieve a Plan (path, front matter and content)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case fm && content:
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "choose --front-matter or --content"})
			case fm:
				return a.run(cmd, "getFrontMatter", ops.PlanParams{Plan: args[0]})
			case content:
				if a.format == "json" && !cmd.Flags().Changed("format") {
					a.format = "text"
				}
				return a.run(cmd, "getContent", ops.PlanParams{Plan: args[0]})
			}
			return a.run(cmd, "get", ops.PlanParams{Plan: args[0]})
		},
	}
	c.Flags().BoolVar(&fm, "front-matter", false, "only the front matter")
	c.Flags().BoolVar(&content, "content", false, "only the content (printed raw unless --format is set)")
	return c
}

func (a *App) listCmd() *cobra.Command {
	var p ops.ListParams
	c := &cobra.Command{
		Use:   "list",
		Short: "List Plans (path and front matter) with optional filters",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return a.run(cmd, "list", p) },
	}
	c.Flags().StringVar(&p.Type, "type", "", "filter by plan type")
	c.Flags().StringVar(&p.Status, "status", "", "filter by status (synonyms accepted)")
	c.Flags().StringVar(&p.Priority, "priority", "", "filter by priority (number or label)")
	c.Flags().StringVar(&p.Plan, "plan", "", "filter by linked plan id")
	c.Flags().StringVar(&p.Story, "story", "", "filter by linked story id")
	c.Flags().BoolVar(&p.IncludeArchived, "archived", false, "include archived plans")
	return c
}

func (a *App) updateCmd() *cobra.Command {
	var content string
	c := &cobra.Command{
		Use:   "update <plan> --content body.md",
		Short: "Replace a Plan's content (front matter unchanged apart from updated)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if content == "" {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "--content is required (a file or - for stdin)"})
			}
			data, err := a.readInput(content, false)
			if err != nil {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
			}
			return a.run(cmd, "update", ops.UpdateParams{Plan: args[0], Content: data})
		},
	}
	c.Flags().StringVarP(&content, "content", "c", "", "Markdown file with the new content, or - for stdin")
	return c
}

func (a *App) deleteCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "delete <plan>",
		Short: "Delete a Plan (fails if other Plans link to it unless --force)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "delete", ops.DeleteParams{Plan: args[0], Force: force})
		},
	}
	c.Flags().BoolVar(&force, "force", false, "delete even when linked")
	return c
}

func (a *App) validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <plan>",
		Short: "Check a Plan without changing it and list problems",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.run(cmd, "validate", ops.PlanParams{Plan: args[0]}); err != nil {
				return err
			}
			rep, err := a.svc.Validate(cmd.Context(), args[0])
			if err == nil && !rep.Valid {
				return &cliError{err: &service.Error{Kind: service.KindValidation, Message: "plan is invalid", Problems: rep.Problems}}
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// Templates

func (a *App) templateCmd() *cobra.Command {
	c := &cobra.Command{Use: "template", Short: "Manage Plan content templates"}
	get := &cobra.Command{
		Use:   "get [template-id]",
		Short: "Print a template (default when no id is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			if a.format == "json" && !cmd.Flags().Changed("format") {
				a.format = "text"
			}
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			t, err := a.svc.GetTemplate(cmd.Context(), id)
			if err != nil {
				return a.fail(err)
			}
			if a.format == "text" {
				fmt.Fprint(a.Stdout, t.Content)
				return nil
			}
			return a.emit(&ops.Result{Value: t}, "getTemplate")
		},
	}
	list := &cobra.Command{Use: "list", Short: "List templates", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.run(cmd, "listTemplates", ops.Empty{}) }}
	var content string
	create := &cobra.Command{
		Use:   "create <template-id> --content template.md",
		Short: "Create a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := a.readInput(content, false)
			if err != nil {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
			}
			return a.run(cmd, "createTemplate", ops.TemplateContentParams{Template: args[0], Content: data})
		},
	}
	create.Flags().StringVarP(&content, "content", "c", "", "template file, or - for stdin")
	update := &cobra.Command{
		Use:   "update <template-id> --content template.md",
		Short: "Update a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := a.readInput(content, false)
			if err != nil {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
			}
			return a.run(cmd, "updateTemplate", ops.TemplateContentParams{Template: args[0], Content: data})
		},
	}
	update.Flags().StringVarP(&content, "content", "c", "", "template file, or - for stdin")
	del := &cobra.Command{Use: "delete <template-id>", Short: "Delete a template", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "deleteTemplate", ops.TemplateParams{Template: args[0]})
		}}
	c.AddCommand(get, list, create, update, del)
	return c
}

// ---------------------------------------------------------------------------
// Front matter: fields

func (a *App) setTitleCmd() *cobra.Command {
	var rename bool
	c := &cobra.Command{Use: "set-title <plan> <title>", Short: "Change the title and H1", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "setTitle", ops.SetTitleParams{Plan: args[0], Title: args[1], RenameFile: rename})
		}}
	c.Flags().BoolVar(&rename, "rename", false, "also rename the file's slug")
	return c
}

func (a *App) setTypeCmd() *cobra.Command {
	return &cobra.Command{Use: "set-type <plan> <type>", Short: "Change the plan type (renames the file)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "setType", ops.SetTypeParams{Plan: args[0], Type: args[1]})
		}}
}

func (a *App) setStatusCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{Use: "set-status <plan> <status>", Short: "Move the plan through the workflow", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "setStatus", ops.SetStatusParams{Plan: args[0], Status: args[1], Force: force})
		}}
	c.Flags().BoolVar(&force, "force", false, "skip the workflow check")
	return c
}

func (a *App) transitionsCmd() *cobra.Command {
	return &cobra.Command{Use: "transitions <plan>", Short: "List the statuses the plan can move to", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "getTransitions", ops.PlanParams{Plan: args[0]})
		}}
}

func (a *App) setPriorityCmd() *cobra.Command {
	return &cobra.Command{Use: "set-priority <plan> <priority>", Short: "Set priority (0–5, P1, Critical…)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "setPriority", ops.SetPriorityParams{Plan: args[0], Priority: args[1]})
		}}
}

func (a *App) setEffortCmd() *cobra.Command {
	return &cobra.Command{Use: "set-effort <plan> <effort>", Short: "Set effort (XS–XL, Medium, 5…)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "setEffort", ops.SetEffortParams{Plan: args[0], Effort: args[1]})
		}}
}

func (a *App) clearEffortCmd() *cobra.Command {
	return &cobra.Command{Use: "clear-effort <plan>", Short: "Remove effort", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "clearEffort", ops.PlanParams{Plan: args[0]})
		}}
}

func (a *App) patchCmd() *cobra.Command {
	var title, typ, status, priority, effort, raw string
	var clearEffort, force bool
	c := &cobra.Command{
		Use:   "patch <plan> [--title ...] [--type ...] [--status ...] [--priority ...] [--effort ...] [--json '{...}']",
		Short: "Set several front matter fields in one write",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			patch := map[string]any{}
			if raw != "" {
				data, err := a.readInput(raw, true)
				if err != nil {
					return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
				}
				if err := json.Unmarshal([]byte(data), &patch); err != nil {
					return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "--json must be a JSON object: " + err.Error()})
				}
			}
			if title != "" {
				patch["title"] = title
			}
			if typ != "" {
				patch["type"] = typ
			}
			if status != "" {
				patch["status"] = status
			}
			if priority != "" {
				patch["priority"] = priority
			}
			if effort != "" {
				patch["effort"] = effort
			}
			if clearEffort {
				patch["effort"] = nil
			}
			if len(patch) == 0 {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "nothing to patch"})
			}
			return a.run(cmd, "patchFrontMatter", ops.PatchParams{Plan: args[0], Patch: patch, Force: force})
		},
	}
	f := c.Flags()
	f.StringVar(&title, "title", "", "new title")
	f.StringVar(&typ, "type", "", "new type")
	f.StringVar(&status, "status", "", "new status")
	f.StringVar(&priority, "priority", "", "new priority")
	f.StringVar(&effort, "effort", "", "new effort")
	f.BoolVar(&clearEffort, "clear-effort", false, "remove effort")
	f.StringVar(&raw, "json", "", "JSON object with fields to set (plans, links, progress…); file, - or inline")
	f.BoolVar(&force, "force", false, "skip the workflow check for status")
	return c
}

// ---------------------------------------------------------------------------
// Front matter: links

func (a *App) linkCmd() *cobra.Command {
	return &cobra.Command{Use: "link <plan> <target-plan> <relation>", Short: "Add a plan link (parent, included, depends, blocks)", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "addPlanLink", ops.PlanLinkParams{Plan: args[0], Target: args[1], Relation: args[2]})
		}}
}

func (a *App) unlinkCmd() *cobra.Command {
	return &cobra.Command{Use: "unlink <plan> <target-plan> [relation]", Short: "Remove a plan link (every relation when omitted)", Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ops.PlanLinkParams{Plan: args[0], Target: args[1]}
			if len(args) == 3 {
				p.Relation = args[2]
			}
			return a.run(cmd, "removePlanLink", p)
		}}
}

func (a *App) linkStoryCmd() *cobra.Command {
	return &cobra.Command{Use: "link-story <plan> <story> <relation>", Short: "Add a story link (included, depends, blocks)", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "addStoryLink", ops.StoryLinkParams{Plan: args[0], Story: args[1], Relation: args[2]})
		}}
}

func (a *App) unlinkStoryCmd() *cobra.Command {
	return &cobra.Command{Use: "unlink-story <plan> <story> [relation]", Short: "Remove a story link", Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ops.StoryLinkParams{Plan: args[0], Story: args[1]}
			if len(args) == 3 {
				p.Relation = args[2]
			}
			return a.run(cmd, "removeStoryLink", p)
		}}
}

func (a *App) addSpecCmd() *cobra.Command {
	return &cobra.Command{Use: "add-spec <plan> <spec-path>", Short: "Add a spec to links.specs", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "addSpec", ops.SpecParams{Plan: args[0], Spec: args[1]})
		}}
}

func (a *App) removeSpecCmd() *cobra.Command {
	return &cobra.Command{Use: "remove-spec <plan> <spec-path>", Short: "Remove a spec from links.specs", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "removeSpec", ops.SpecParams{Plan: args[0], Spec: args[1]})
		}}
}

func (a *App) setWebCmd() *cobra.Command {
	return &cobra.Command{Use: "set-web <plan> <label> <url>", Short: "Add or replace a named web link", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "setWebLink", ops.WebLinkParams{Plan: args[0], Label: args[1], URL: args[2]})
		}}
}

func (a *App) removeWebCmd() *cobra.Command {
	return &cobra.Command{Use: "remove-web <plan> <label>", Short: "Remove a named web link", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "removeWebLink", ops.WebLinkParams{Plan: args[0], Label: args[1]})
		}}
}

func (a *App) setRepoCmd() *cobra.Command {
	var remote, local string
	c := &cobra.Command{Use: "set-repo <plan> [--remote url] [--local path]", Short: "Set links.repo", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "setRepo", ops.RepoParams{Plan: args[0], Remote: remote, Local: local})
		}}
	c.Flags().StringVar(&remote, "remote", "", "git remote URL")
	c.Flags().StringVar(&local, "local", "", "local checkout folder")
	return c
}

func (a *App) clearRepoCmd() *cobra.Command {
	return &cobra.Command{Use: "clear-repo <plan>", Short: "Remove links.repo", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "clearRepo", ops.PlanParams{Plan: args[0]})
		}}
}

// ---------------------------------------------------------------------------
// Front matter: progress

func (a *App) progressCmd() *cobra.Command {
	return &cobra.Command{Use: "progress <plan>", Short: "Show the status of every phase and section", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "getProgress", ops.PlanParams{Plan: args[0]})
		}}
}

func (a *App) setProgressCmd() *cobra.Command {
	return &cobra.Command{Use: "set-progress <plan> <section> <status>", Short: "Set a phase or section status", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "setProgress", ops.ProgressParams{Plan: args[0], Section: args[1], Status: args[2]})
		}}
}

func (a *App) progressStoryCmd() *cobra.Command {
	return &cobra.Command{Use: "progress-story <plan> <section> <story>", Short: "Link a story to a phase or section", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "addProgressStory", ops.ProgressParams{Plan: args[0], Section: args[1], Story: args[2]})
		}}
}

func (a *App) progressUnstoryCmd() *cobra.Command {
	return &cobra.Command{Use: "progress-unstory <plan> <section> <story>", Short: "Unlink a story from a phase or section", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "removeProgressStory", ops.ProgressParams{Plan: args[0], Section: args[1], Story: args[2]})
		}}
}

func (a *App) removeProgressCmd() *cobra.Command {
	return &cobra.Command{Use: "remove-progress <plan> <section>", Short: "Remove a phase or section's progress entry", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, "removeProgress", ops.ProgressParams{Plan: args[0], Section: args[1]})
		}}
}

// ---------------------------------------------------------------------------
// Storage, servers and introspection

func (a *App) syncCmd() *cobra.Command {
	var p ops.SyncParams
	c := &cobra.Command{Use: "sync --from <store> --to <store> [--on-conflict error|skip|overwrite]", Short: "Copy plans and templates between configured stores", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if p.From == "" || p.To == "" {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "--from and --to are required"})
			}
			return a.run(cmd, "sync", p)
		}}
	c.Flags().StringVar(&p.From, "from", "", "source store name")
	c.Flags().StringVar(&p.To, "to", "", "target store name")
	c.Flags().StringVar(&p.OnConflict, "on-conflict", "error", "error, skip or overwrite")
	return c
}

func (a *App) serveCmd() *cobra.Command {
	var addr, prefix string
	var noHTTP, noWS, noMCP, anyOrigin bool
	c := &cobra.Command{
		Use:   "serve [--addr host:port]",
		Short: "Serve the HTTP/REST, WebSocket and MCP (Streamable HTTP) entrypoints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			if addr == "" {
				addr = a.cfg.Server.Addr
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			logger := slog.New(slog.NewTextHandler(a.Stderr, nil))
			srv := server.New(a.svc, a.reg, server.Options{HTTP: !noHTTP, WebSocket: !noWS, MCP: !noMCP, MCPToolPrefix: prefix, Logger: logger, AllowAnyOrigin: anyOrigin})
			ready := make(chan string, 1)
			go func() {
				bound := <-ready
				fmt.Fprintf(a.Stderr, "plan service listening on http://%s  (http: %v, ws: %v, mcp: %v)\n", bound, !noHTTP, !noWS, !noMCP)
			}()
			if err := srv.ListenAndServe(ctx, addr, ready); err != nil {
				return a.fail(&service.Error{Kind: service.KindStorage, Message: err.Error()})
			}
			return nil
		},
	}
	c.Flags().StringVar(&addr, "addr", "", "listen address (default from config: 127.0.0.1:8087)")
	c.Flags().BoolVar(&noHTTP, "no-http", false, "disable the REST API")
	c.Flags().BoolVar(&noWS, "no-ws", false, "disable the WebSocket endpoint")
	c.Flags().BoolVar(&noMCP, "no-mcp", false, "disable the MCP endpoint")
	c.Flags().StringVar(&prefix, "mcp-tool-prefix", "", "prefix for MCP tool names")
	c.Flags().BoolVar(&anyOrigin, "allow-any-origin", false, "accept WebSocket connections from any browser origin")
	return c
}

func (a *App) mcpCmd() *cobra.Command {
	var prefix string
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the MCP server over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			srv := mcpserver.New(a.svc, a.reg, &mcpserver.Options{ToolPrefix: prefix})
			defer srv.Close()
			if err := srv.RunStdio(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return a.fail(&service.Error{Kind: service.KindStorage, Message: err.Error()})
			}
			return nil
		},
	}
	c.Flags().StringVar(&prefix, "mcp-tool-prefix", "", "prefix for MCP tool names")
	return c
}

func (a *App) callCmd() *cobra.Command {
	var params string
	c := &cobra.Command{
		Use:   "call <operation> [--params '{...}']",
		Short: "Call any operation by name with JSON parameters",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw := "{}"
			if params != "" {
				data, err := a.readInput(params, true)
				if err != nil {
					return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
				}
				raw = data
			}
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			res, err := a.reg.Invoke(cmd.Context(), a.svc, args[0], json.RawMessage(raw))
			if err != nil {
				return a.fail(err)
			}
			return a.emit(res, args[0])
		},
	}
	c.Flags().StringVarP(&params, "params", "p", "", "JSON parameters: file, - for stdin, or inline")
	return c
}

func (a *App) opsCmd() *cobra.Command {
	return &cobra.Command{Use: "ops", Short: "List every operation with its parameter schema", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			return a.emit(&ops.Result{Value: a.reg.Describe(a.svc)}, "ops")
		}}
}

func (a *App) configCmd() *cobra.Command {
	return &cobra.Command{Use: "config", Short: "Print the effective configuration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.run(cmd, "getConfig", ops.Empty{}) }}
}

func (a *App) schemaCmd() *cobra.Command {
	return &cobra.Command{Use: "schema", Short: "Print the Plan creation JSON Schema", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			a.Stdout.Write(a.svc.Schema())
			fmt.Fprintln(a.Stdout)
			return nil
		}}
}

func (a *App) storesCmd() *cobra.Command {
	return &cobra.Command{Use: "stores", Short: "List configured stores and registered storage plugins", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			return a.emit(&ops.Result{Value: map[string]any{"stores": a.svc.StoreNames(), "plugins": store.Kinds()}}, "stores")
		}}
}
