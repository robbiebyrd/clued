// Package cli is the command line entrypoint of the mind-palace. It is the
// primary interface for agents, skills and tools: every command prints JSON
// to stdout ({"ok": true, "result": …}) and errors to stderr with an exit
// code that identifies the error kind.
//
// The root command is `mind-palace` with a `plan` and a `story` command group
// holding the document operations; `serve`, `mcp`, `sync`, `config`, `stores`,
// `ops`, `schema` and `call` sit at the top level. The `plan` and `story`
// binaries run the matching group directly.
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
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/server/mcpserver"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Exit codes by error kind.
var exitCodes = map[string]int{
	service.KindBadRequest:         2,
	service.KindValidation:         3,
	service.KindNotFound:           4,
	service.KindInvalidTransition:  5,
	service.KindImmutableField:     6,
	service.KindUnknownSection:     7,
	service.KindLinkedPlan:         8,
	service.KindConflict:           9,
	service.KindStorage:            10,
	service.KindIncompleteCriteria: 11,
	service.KindUnknownStep:        12,
	service.KindUnknownCriterion:   13,
	service.KindLinkedStory:        14,
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
	plansDir   string
	storiesDir string
	format     string
	compact    bool

	cfg    *config.Config
	palace *service.Palace
	regs   ops.Registries
	st     *store.MultiStore
	sess   sessionState
}

// New returns an App bound to the standard streams.
func New() *App {
	return &App{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, regs: ops.All()}
}

// Main runs the mind-palace CLI and returns the exit code.
func Main(args []string) int {
	return New().Run(args)
}

// MainKind runs the CLI as the `plan` or `story` binary: document commands
// are routed to that kind's group, top-level commands pass through.
func MainKind(kindName string, args []string) int {
	return kindMain(New(), kindName, args)
}

func kindMain(app *App, kindName string, args []string) int {
	root := app.Root()
	i := firstPositional(root, args)
	switch {
	case i < 0:
		// Flags only (or nothing): show the group's help.
		args = append(args, kindName)
	case args[i] == "help" || args[i] == "completion":
	default:
		if !isTopLevel(root, args[i]) {
			args = append(args[:i:i], append([]string{kindName}, args[i:]...)...)
		}
	}
	root.Use = kindName
	return app.execute(root, args)
}

// firstPositional returns the index of the first argument that is not a
// persistent flag or a flag value, or -1.
func firstPositional(root *cobra.Command, args []string) int {
	flags := root.PersistentFlags()
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			return i
		}
		if strings.Contains(a, "=") {
			continue
		}
		var f *pflag.Flag
		if strings.HasPrefix(a, "--") {
			f = flags.Lookup(strings.TrimPrefix(a, "--"))
		} else if len(a) == 2 {
			f = flags.ShorthandLookup(a[1:])
		}
		if f != nil && f.Value.Type() != "bool" {
			i++ // skip the flag's value
		}
	}
	return -1
}

// isTopLevel reports whether name is a root command (a kind group, serve,
// sync…) or one of its aliases.
func isTopLevel(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return true
		}
	}
	return false
}

// Run executes the root command with args.
func (a *App) Run(args []string) int {
	return a.execute(a.Root(), args)
}

func (a *App) execute(root *cobra.Command, args []string) int {
	root.SetArgs(args)
	err := root.Execute()
	a.close()
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
	if a.palace != nil {
		return nil
	}
	cfg, err := config.Load(a.configPath)
	if err != nil {
		return &service.Error{Kind: service.KindBadRequest, Message: err.Error()}
	}
	if a.plansDir != "" {
		cfg.Plans.Dir = a.plansDir
	}
	if a.storiesDir != "" {
		cfg.Stories.Dir = a.storiesDir
	}
	st, err := store.OpenAll(ctx, cfg)
	if err != nil {
		return &service.Error{Kind: service.KindStorage, Message: err.Error()}
	}
	palace, err := service.NewPalace(cfg, st, events.New())
	if err != nil {
		_ = st.Close()
		return err
	}
	a.cfg, a.st, a.palace = cfg, st, palace
	return nil
}

// Palace exposes the palace (after init) for embedding callers.
func (a *App) Palace(ctx context.Context) (*service.Palace, error) {
	if err := a.init(ctx); err != nil {
		return nil, err
	}
	return a.palace, nil
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

// run executes an operation of a kind and prints its result.
func (a *App) run(cmd *cobra.Command, kindName, op string, params any) error {
	ctx := cmd.Context()
	if err := a.init(ctx); err != nil {
		return a.fail(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return a.fail(err)
	}
	svc, err := a.palace.Service(kindName)
	if err != nil {
		return a.fail(err)
	}
	res, err := a.regs[svc.Kind().Name].Invoke(ctx, svc, op, raw)
	if err != nil {
		return a.fail(err)
	}
	return a.emit(res)
}

func (a *App) emit(res *ops.Result) error {
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
		Use:   "mind-palace",
		Short: "Backend plan and story manager: save, retrieve and monitor plans and stories",
		Long: `mind-palace is a CRUD service for Plans (design and implementation plans) and
Stories (bugs, features, improvements, chores, tasks), stored as Markdown with
YAML front matter. Every command prints JSON: {"ok": true, "result": ...}.
Errors go to stderr as {"ok": false, "error": {...}} with exit codes:
  2 BadRequest  3 ValidationError  4 NotFound  5 InvalidTransition
  6 ImmutableField  7 UnknownSection  8 LinkedPlan  9 Conflict  10 StorageError
  11 IncompleteCriteria  12 UnknownStep  13 UnknownCriterion  14 LinkedStory

Document commands live under "plan" and "story" (the plan and story binaries
run those groups directly). <plan>/<story> arguments accept an id (0002-a3f)
or the path to the file.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	pf := root.PersistentFlags()
	pf.StringVar(&a.configPath, "config", "", "config file (default: $MIND_PALACE_CONFIG, then mind-palace.config.yaml/json in the working directory)")
	pf.StringVar(&a.plansDir, "dir", "", "plans directory for file storage (default: docs/plans)")
	pf.StringVar(&a.plansDir, "plans-dir", "", "plans directory for file storage (default: docs/plans)")
	pf.StringVar(&a.storiesDir, "stories-dir", "", "stories directory for file storage (default: docs/stories)")
	pf.StringVarP(&a.format, "format", "f", "json", "output format: json, yaml or text (text prints content/templates raw)")
	pf.BoolVar(&a.compact, "compact", false, "single-line JSON output")

	for _, k := range kind.All() {
		root.AddCommand(a.kindGroup(k))
	}
	root.AddCommand(a.syncCmd(), a.serveCmd(), a.mcpCmd(), a.callCmd(), a.opsCmd(), a.configCmd(), a.schemaCmd(), a.storesCmd(), a.sessionCmd())
	return root
}

// kindGroup builds the command group of one document kind.
func (a *App) kindGroup(k *kind.Kind) *cobra.Command {
	kc := &kindCommands{App: a, k: k}
	c := &cobra.Command{
		Use:     k.Name,
		Aliases: []string{k.Plural},
		Short:   fmt.Sprintf("Manage %ss", k.Title),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("unknown %s command %q; see `%s --help`", k.Name, args[0], cmd.CommandPath())})
			}
			return cmd.Help()
		},
	}
	c.AddCommand(
		kc.createCmd(), kc.getCmd(), kc.listCmd(), kc.updateCmd(), kc.deleteCmd(), kc.validateCmd(),
		kc.templateCmd(),
		kc.setTitleCmd(), kc.setTypeCmd(), kc.setStatusCmd(), kc.transitionsCmd(), kc.setPriorityCmd(), kc.setEffortCmd(), kc.clearEffortCmd(), kc.patchCmd(),
		kc.linkPlanCmd(), kc.unlinkPlanCmd(), kc.linkStoryCmd(), kc.unlinkStoryCmd(), kc.addSpecCmd(), kc.removeSpecCmd(), kc.setWebCmd(), kc.removeWebCmd(), kc.setRepoCmd(), kc.clearRepoCmd(),
		kc.progressCmd(), kc.setProgressCmd(), kc.removeProgressCmd(),
		kc.schemaCmd(), kc.opsCmd(),
	)
	if k.HasPurpose {
		c.AddCommand(kc.setPurposeCmd())
	}
	if k.PlanLinkSections {
		c.AddCommand(kc.planSectionsCmd())
	}
	if k.RepoFiles {
		c.AddCommand(kc.addFileCmd(), kc.removeFileCmd())
	}
	if k.ProgressStories {
		c.AddCommand(kc.progressStoryCmd(), kc.progressUnstoryCmd())
	}
	if k.CriteriaGate {
		c.AddCommand(kc.criteriaCmd(), kc.checkCmd(), kc.addCriterionCmd(), kc.removeCriterionCmd())
	}
	if k.WorkLog {
		c.AddCommand(kc.logCmd())
	}
	return c
}

// kindCommands builds the document commands of one kind.
type kindCommands struct {
	*App
	k *kind.Kind
}

func (kc *kindCommands) run(cmd *cobra.Command, op string, params any) error {
	return kc.App.run(cmd, kc.k.Name, op, params)
}

func (kc *kindCommands) arg() string { return "<" + kc.k.Name + ">" }

func (kc *kindCommands) badRequest(msg string) error {
	return kc.fail(&service.Error{Kind: service.KindBadRequest, Message: msg})
}

// ---------------------------------------------------------------------------
// Documents

func (kc *kindCommands) createCmd() *cobra.Command {
	var input, template string
	c := &cobra.Command{
		Use:   "create --input " + kc.k.Name + ".json",
		Short: fmt.Sprintf("Create a %s from JSON matching the creation schema", kc.k.Title),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				return kc.badRequest("--input is required (a file, - for stdin, or inline JSON)")
			}
			data, err := kc.readInput(input, true)
			if err != nil {
				return kc.badRequest(err.Error())
			}
			return kc.run(cmd, "create", ops.CreateParams{Input: json.RawMessage(data), Template: template})
		},
	}
	c.Flags().StringVarP(&input, "input", "i", "", "JSON input: file path, - for stdin, or inline JSON")
	c.Flags().StringVarP(&template, "template", "t", "", "content template id (default: default)")
	return c
}

func (kc *kindCommands) getCmd() *cobra.Command {
	var fm, content bool
	c := &cobra.Command{
		Use:   "get " + kc.arg(),
		Short: fmt.Sprintf("Retrieve a %s (path, front matter and content)", kc.k.Title),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case fm && content:
				return kc.badRequest("choose --front-matter or --content")
			case fm:
				return kc.run(cmd, "getFrontMatter", ops.DocParams{Document: args[0]})
			case content:
				if kc.format == "json" && !cmd.Flags().Changed("format") {
					kc.format = "text"
				}
				return kc.run(cmd, "getContent", ops.DocParams{Document: args[0]})
			}
			return kc.run(cmd, "get", ops.DocParams{Document: args[0]})
		},
	}
	c.Flags().BoolVar(&fm, "front-matter", false, "only the front matter")
	c.Flags().BoolVar(&content, "content", false, "only the content (printed raw unless --format is set)")
	return c
}

func (kc *kindCommands) listCmd() *cobra.Command {
	var p ops.ListParams
	c := &cobra.Command{
		Use:   "list",
		Short: fmt.Sprintf("List %ss (path and front matter) with optional filters", kc.k.Title),
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return kc.run(cmd, "list", p) },
	}
	c.Flags().StringVar(&p.Type, "type", "", "filter by type")
	c.Flags().StringVar(&p.Status, "status", "", "filter by status (synonyms accepted)")
	c.Flags().StringVar(&p.Priority, "priority", "", "filter by priority (number or label)")
	c.Flags().StringVar(&p.Plan, "plan", "", "filter by linked plan id")
	c.Flags().StringVar(&p.Story, "story", "", "filter by linked story id")
	c.Flags().BoolVar(&p.IncludeArchived, "archived", false, "include archived documents")
	return c
}

func (kc *kindCommands) updateCmd() *cobra.Command {
	var content string
	c := &cobra.Command{
		Use:   "update " + kc.arg() + " --content body.md",
		Short: fmt.Sprintf("Replace a %s's content (front matter unchanged apart from updated)", kc.k.Title),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if content == "" {
				return kc.badRequest("--content is required (a file or - for stdin)")
			}
			data, err := kc.readInput(content, false)
			if err != nil {
				return kc.badRequest(err.Error())
			}
			return kc.run(cmd, "update", ops.UpdateParams{Document: args[0], Content: data})
		},
	}
	c.Flags().StringVarP(&content, "content", "c", "", "Markdown file with the new content, or - for stdin")
	return c
}

func (kc *kindCommands) deleteCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "delete " + kc.arg(),
		Short: fmt.Sprintf("Delete a %s (fails if other documents link to it unless --force)", kc.k.Title),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "delete", ops.DeleteParams{Document: args[0], Force: force})
		},
	}
	c.Flags().BoolVar(&force, "force", false, "delete even when linked")
	return c
}

func (kc *kindCommands) validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate " + kc.arg(),
		Short: fmt.Sprintf("Check a %s without changing it and list problems", kc.k.Title),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := kc.run(cmd, "validate", ops.DocParams{Document: args[0]}); err != nil {
				return err
			}
			svc, _ := kc.palace.Service(kc.k.Name)
			rep, err := svc.Validate(cmd.Context(), args[0])
			if err == nil && !rep.Valid {
				return &cliError{err: &service.Error{Kind: service.KindValidation, Message: kc.k.Name + " is invalid", Problems: rep.Problems}}
			}
			return nil
		},
	}
}

func (kc *kindCommands) schemaCmd() *cobra.Command {
	return &cobra.Command{Use: "schema", Short: fmt.Sprintf("Print the %s creation JSON Schema", kc.k.Title), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := kc.init(cmd.Context()); err != nil {
				return kc.fail(err)
			}
			svc, _ := kc.palace.Service(kc.k.Name)
			kc.Stdout.Write(svc.Schema())
			fmt.Fprintln(kc.Stdout)
			return nil
		}}
}

func (kc *kindCommands) opsCmd() *cobra.Command {
	return &cobra.Command{Use: "ops", Short: fmt.Sprintf("List every %s operation with its parameter schema", kc.k.Title), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := kc.init(cmd.Context()); err != nil {
				return kc.fail(err)
			}
			svc, _ := kc.palace.Service(kc.k.Name)
			return kc.emit(&ops.Result{Value: kc.regs[kc.k.Name].Describe(svc)})
		}}
}

// ---------------------------------------------------------------------------
// Templates

func (kc *kindCommands) templateCmd() *cobra.Command {
	c := &cobra.Command{Use: "template", Short: fmt.Sprintf("Manage %s content templates", kc.k.Title)}
	get := &cobra.Command{
		Use:   "get [template-id]",
		Short: "Print a template (default when no id is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			if kc.format == "json" && !cmd.Flags().Changed("format") {
				kc.format = "text"
			}
			if err := kc.init(cmd.Context()); err != nil {
				return kc.fail(err)
			}
			svc, _ := kc.palace.Service(kc.k.Name)
			t, err := svc.GetTemplate(cmd.Context(), id)
			if err != nil {
				return kc.fail(err)
			}
			if kc.format == "text" {
				fmt.Fprint(kc.Stdout, t.Content)
				return nil
			}
			return kc.emit(&ops.Result{Value: t})
		},
	}
	list := &cobra.Command{Use: "list", Short: "List templates", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return kc.run(cmd, "listTemplates", ops.Empty{}) }}
	var content string
	create := &cobra.Command{
		Use:   "create <template-id> --content template.md",
		Short: "Create a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := kc.readInput(content, false)
			if err != nil {
				return kc.badRequest(err.Error())
			}
			return kc.run(cmd, "createTemplate", ops.TemplateContentParams{Template: args[0], Content: data})
		},
	}
	create.Flags().StringVarP(&content, "content", "c", "", "template file, or - for stdin")
	update := &cobra.Command{
		Use:   "update <template-id> --content template.md",
		Short: "Update a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := kc.readInput(content, false)
			if err != nil {
				return kc.badRequest(err.Error())
			}
			return kc.run(cmd, "updateTemplate", ops.TemplateContentParams{Template: args[0], Content: data})
		},
	}
	update.Flags().StringVarP(&content, "content", "c", "", "template file, or - for stdin")
	del := &cobra.Command{Use: "delete <template-id>", Short: "Delete a template", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "deleteTemplate", ops.TemplateParams{Template: args[0]})
		}}
	c.AddCommand(get, list, create, update, del)
	return c
}

// ---------------------------------------------------------------------------
// Front matter: fields

func (kc *kindCommands) setTitleCmd() *cobra.Command {
	var rename bool
	c := &cobra.Command{Use: "set-title " + kc.arg() + " <title>", Short: "Change the title and H1", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "setTitle", ops.SetTitleParams{Document: args[0], Title: args[1], RenameFile: rename})
		}}
	c.Flags().BoolVar(&rename, "rename", false, "also rename the file's slug")
	return c
}

func (kc *kindCommands) setPurposeCmd() *cobra.Command {
	return &cobra.Command{Use: "set-purpose " + kc.arg() + " <purpose>", Short: "Set or replace purpose", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "setPurpose", ops.SetPurposeParams{Document: args[0], Purpose: args[1]})
		}}
}

func (kc *kindCommands) setTypeCmd() *cobra.Command {
	return &cobra.Command{Use: "set-type " + kc.arg() + " <type>", Short: "Change the type (renames the file)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "setType", ops.SetTypeParams{Document: args[0], Type: args[1]})
		}}
}

func (kc *kindCommands) setStatusCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{Use: "set-status " + kc.arg() + " <status>", Short: "Move the document through the workflow", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "setStatus", ops.SetStatusParams{Document: args[0], Status: args[1], Force: force})
		}}
	c.Flags().BoolVar(&force, "force", false, "skip the workflow check")
	return c
}

func (kc *kindCommands) transitionsCmd() *cobra.Command {
	return &cobra.Command{Use: "transitions " + kc.arg(), Short: "List the statuses the document can move to", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "getTransitions", ops.DocParams{Document: args[0]})
		}}
}

func (kc *kindCommands) setPriorityCmd() *cobra.Command {
	return &cobra.Command{Use: "set-priority " + kc.arg() + " <priority>", Short: "Set priority (0–5, P1, Critical…)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "setPriority", ops.SetPriorityParams{Document: args[0], Priority: args[1]})
		}}
}

func (kc *kindCommands) setEffortCmd() *cobra.Command {
	return &cobra.Command{Use: "set-effort " + kc.arg() + " <effort>", Short: "Set effort (XS–XL, Medium, 5…)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "setEffort", ops.SetEffortParams{Document: args[0], Effort: args[1]})
		}}
}

func (kc *kindCommands) clearEffortCmd() *cobra.Command {
	return &cobra.Command{Use: "clear-effort " + kc.arg(), Short: "Remove effort", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "clearEffort", ops.DocParams{Document: args[0]})
		}}
}

func (kc *kindCommands) patchCmd() *cobra.Command {
	var title, purpose, typ, status, priority, effort, raw string
	var clearEffort, force bool
	c := &cobra.Command{
		Use:   "patch " + kc.arg() + " [--title ...] [--type ...] [--status ...] [--priority ...] [--effort ...] [--json '{...}']",
		Short: "Set several front matter fields in one write",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			patch := map[string]any{}
			if raw != "" {
				data, err := kc.readInput(raw, true)
				if err != nil {
					return kc.badRequest(err.Error())
				}
				if err := json.Unmarshal([]byte(data), &patch); err != nil {
					return kc.badRequest("--json must be a JSON object: " + err.Error())
				}
			}
			for key, val := range map[string]string{"title": title, "purpose": purpose, "type": typ, "status": status, "priority": priority, "effort": effort} {
				if val != "" {
					patch[key] = val
				}
			}
			if clearEffort {
				patch["effort"] = nil
			}
			if len(patch) == 0 {
				return kc.badRequest("nothing to patch")
			}
			return kc.run(cmd, "patchFrontMatter", ops.PatchParams{Document: args[0], Patch: patch, Force: force})
		},
	}
	f := c.Flags()
	f.StringVar(&title, "title", "", "new title")
	if kc.k.HasPurpose {
		f.StringVar(&purpose, "purpose", "", "new purpose")
	}
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

func splitSections(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func (kc *kindCommands) linkPlanCmd() *cobra.Command {
	use, short := "link", "Add a plan link"
	if kc.k.Name == kind.Story {
		use, short = "link-plan", "Add a plan link, optionally with the plan sections this story implements"
	}
	var sections string
	c := &cobra.Command{Use: use + " " + kc.arg() + " <target-plan> <relation>", Short: short + " (" + strings.Join(kc.k.PlanRelations, ", ") + ")", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kc.k.PlanLinkSections {
				return kc.run(cmd, "addPlanLink", ops.StoryPlanLinkParams{Document: args[0], Target: args[1], Relation: args[2], Sections: splitSections(sections)})
			}
			return kc.run(cmd, "addPlanLink", ops.PlanLinkParams{Document: args[0], Target: args[1], Relation: args[2]})
		}}
	if kc.k.PlanLinkSections {
		c.Flags().StringVar(&sections, "sections", "", "comma-separated plan section numbers this story implements")
	}
	return c
}

func (kc *kindCommands) unlinkPlanCmd() *cobra.Command {
	use := "unlink"
	if kc.k.Name == kind.Story {
		use = "unlink-plan"
	}
	return &cobra.Command{Use: use + " " + kc.arg() + " <target-plan> [relation]", Short: "Remove a plan link (every relation when omitted)", Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ops.PlanLinkParams{Document: args[0], Target: args[1]}
			if len(args) == 3 {
				p.Relation = args[2]
			}
			return kc.run(cmd, "removePlanLink", p)
		}}
}

func (kc *kindCommands) planSectionsCmd() *cobra.Command {
	return &cobra.Command{Use: "plan-sections " + kc.arg() + " <plan> [sections]", Short: "Replace the section list on a plan link (omit to remove it)", Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ops.PlanSectionsParams{Document: args[0], Target: args[1], Sections: []string{}}
			if len(args) == 3 {
				p.Sections = splitSections(args[2])
			}
			return kc.run(cmd, "setPlanSections", p)
		}}
}

func (kc *kindCommands) linkStoryCmd() *cobra.Command {
	use := "link-story"
	if kc.k.Name == kind.Story {
		use = "link"
	}
	return &cobra.Command{Use: use + " " + kc.arg() + " <target-story> <relation>", Short: "Add a story link (" + strings.Join(kc.k.StoryRelations, ", ") + ")", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "addStoryLink", ops.StoryLinkParams{Document: args[0], Target: args[1], Relation: args[2]})
		}}
}

func (kc *kindCommands) unlinkStoryCmd() *cobra.Command {
	use := "unlink-story"
	if kc.k.Name == kind.Story {
		use = "unlink"
	}
	return &cobra.Command{Use: use + " " + kc.arg() + " <target-story> [relation]", Short: "Remove a story link", Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ops.StoryLinkParams{Document: args[0], Target: args[1]}
			if len(args) == 3 {
				p.Relation = args[2]
			}
			return kc.run(cmd, "removeStoryLink", p)
		}}
}

func (kc *kindCommands) addSpecCmd() *cobra.Command {
	return &cobra.Command{Use: "add-spec " + kc.arg() + " <spec>", Short: "Add a spec path or URL to links.specs", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "addSpec", ops.SpecParams{Document: args[0], Spec: args[1]})
		}}
}

func (kc *kindCommands) removeSpecCmd() *cobra.Command {
	return &cobra.Command{Use: "remove-spec " + kc.arg() + " <spec>", Short: "Remove an entry from links.specs", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "removeSpec", ops.SpecParams{Document: args[0], Spec: args[1]})
		}}
}

func (kc *kindCommands) setWebCmd() *cobra.Command {
	return &cobra.Command{Use: "set-web " + kc.arg() + " <label> <url>", Short: "Add or replace a named web link", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "setWebLink", ops.WebLinkParams{Document: args[0], Label: args[1], URL: args[2]})
		}}
}

func (kc *kindCommands) removeWebCmd() *cobra.Command {
	return &cobra.Command{Use: "remove-web " + kc.arg() + " <label>", Short: "Remove a named web link", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "removeWebLink", ops.WebLinkParams{Document: args[0], Label: args[1]})
		}}
}

func (kc *kindCommands) setRepoCmd() *cobra.Command {
	var remote, local, pr string
	use := "set-repo " + kc.arg() + " [--remote url] [--local path]"
	if kc.k.RepoFiles {
		use += " [--pull-request url]"
	}
	c := &cobra.Command{Use: use, Short: "Set links.repo", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kc.k.RepoFiles {
				return kc.run(cmd, "setRepo", ops.StoryRepoParams{Document: args[0], Remote: remote, Local: local, PullRequest: pr})
			}
			return kc.run(cmd, "setRepo", ops.RepoParams{Document: args[0], Remote: remote, Local: local})
		}}
	c.Flags().StringVar(&remote, "remote", "", "git remote URL")
	c.Flags().StringVar(&local, "local", "", "local checkout folder")
	if kc.k.RepoFiles {
		c.Flags().StringVar(&pr, "pull-request", "", "pull request URL")
	}
	return c
}

func (kc *kindCommands) clearRepoCmd() *cobra.Command {
	return &cobra.Command{Use: "clear-repo " + kc.arg(), Short: "Remove links.repo", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "clearRepo", ops.DocParams{Document: args[0]})
		}}
}

func (kc *kindCommands) addFileCmd() *cobra.Command {
	return &cobra.Command{Use: "add-file " + kc.arg() + " <path>", Short: "Add a repository-relative path to links.repo.files", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "addFile", ops.FileParams{Document: args[0], Path: args[1]})
		}}
}

func (kc *kindCommands) removeFileCmd() *cobra.Command {
	return &cobra.Command{Use: "remove-file " + kc.arg() + " <path>", Short: "Remove a path from links.repo.files", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "removeFile", ops.FileParams{Document: args[0], Path: args[1]})
		}}
}

// ---------------------------------------------------------------------------
// Front matter: progress

func (kc *kindCommands) progressCmd() *cobra.Command {
	return &cobra.Command{Use: "progress " + kc.arg(), Short: fmt.Sprintf("Show the status of every numbered %s", kc.k.ProgressKeyNoun), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "getProgress", ops.DocParams{Document: args[0]})
		}}
}

func (kc *kindCommands) setProgressCmd() *cobra.Command {
	noun := kc.k.ProgressKeyNoun
	return &cobra.Command{Use: fmt.Sprintf("set-progress %s <%s> <status>", kc.arg(), noun), Short: fmt.Sprintf("Set a %s status", noun), Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "setProgress", ops.ProgressParams{Document: args[0], Section: args[1], Status: args[2]})
		}}
}

func (kc *kindCommands) progressStoryCmd() *cobra.Command {
	return &cobra.Command{Use: "progress-story " + kc.arg() + " <section> <story>", Short: "Link a story to a phase or section", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "addProgressStory", ops.ProgressStoryParams{Document: args[0], Section: args[1], Story: args[2]})
		}}
}

func (kc *kindCommands) progressUnstoryCmd() *cobra.Command {
	return &cobra.Command{Use: "progress-unstory " + kc.arg() + " <section> <story>", Short: "Unlink a story from a phase or section", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "removeProgressStory", ops.ProgressStoryParams{Document: args[0], Section: args[1], Story: args[2]})
		}}
}

func (kc *kindCommands) removeProgressCmd() *cobra.Command {
	noun := kc.k.ProgressKeyNoun
	return &cobra.Command{Use: fmt.Sprintf("remove-progress %s <%s>", kc.arg(), noun), Short: fmt.Sprintf("Remove a %s's progress entry", noun), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "removeProgress", ops.ProgressParams{Document: args[0], Section: args[1]})
		}}
}

// ---------------------------------------------------------------------------
// Content: acceptance criteria and work log

func (kc *kindCommands) criteriaCmd() *cobra.Command {
	return &cobra.Command{Use: "criteria " + kc.arg(), Short: "List the acceptance criteria with position, kind and state", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "getCriteria", ops.DocParams{Document: args[0]})
		}}
}

func position(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("position must be a positive number, got %q", s)
	}
	return n, nil
}

func (kc *kindCommands) checkCmd() *cobra.Command {
	return &cobra.Command{Use: "check " + kc.arg() + " <position> [state]", Short: "Set an acceptance criterion's state (done by default; open, not_applicable)", Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			pos, err := position(args[1])
			if err != nil {
				return kc.badRequest(err.Error())
			}
			state := "done"
			if len(args) == 3 {
				state = args[2]
			}
			return kc.run(cmd, "setCriterion", ops.CriterionParams{Document: args[0], Position: pos, State: state})
		}}
}

func (kc *kindCommands) addCriterionCmd() *cobra.Command {
	var kindName string
	c := &cobra.Command{Use: "add-criterion " + kc.arg() + " <text>", Short: "Append an acceptance criterion", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "addCriterion", ops.AddCriterionParams{Document: args[0], Text: args[1], Kind: kindName})
		}}
	c.Flags().StringVar(&kindName, "kind", "", "verify or manual")
	return c
}

func (kc *kindCommands) removeCriterionCmd() *cobra.Command {
	return &cobra.Command{Use: "remove-criterion " + kc.arg() + " <position>", Short: "Remove an acceptance criterion", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pos, err := position(args[1])
			if err != nil {
				return kc.badRequest(err.Error())
			}
			return kc.run(cmd, "removeCriterion", ops.CriterionParams{Document: args[0], Position: pos})
		}}
}

func (kc *kindCommands) logCmd() *cobra.Command {
	return &cobra.Command{Use: "log " + kc.arg() + " <entry>", Short: "Append a work log entry (the service adds the timestamp)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kc.run(cmd, "appendWorkLog", ops.WorkLogParams{Document: args[0], Entry: args[1]})
		}}
}

// ---------------------------------------------------------------------------
// Storage, servers and introspection (top level)

func (a *App) syncCmd() *cobra.Command {
	var p ops.SyncParams
	c := &cobra.Command{Use: "sync --from <store> --to <store> [--on-conflict error|skip|overwrite]", Short: "Copy plans, stories and templates between configured stores", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if p.From == "" || p.To == "" {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "--from and --to are required"})
			}
			return a.run(cmd, kind.Plan, "sync", p)
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
			srv := server.New(a.palace, a.regs, server.Options{HTTP: !noHTTP, WebSocket: !noWS, MCP: !noMCP, MCPToolPrefix: prefix, Logger: logger, AllowAnyOrigin: anyOrigin})
			ready := make(chan string, 1)
			go func() {
				bound := <-ready
				fmt.Fprintf(a.Stderr, "mind-palace listening on http://%s  (http: %v, ws: %v, mcp: %v)\n", bound, !noHTTP, !noWS, !noMCP)
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
			srv := mcpserver.New(a.palace, a.regs, &mcpserver.Options{ToolPrefix: prefix})
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
	var params, kindName string
	c := &cobra.Command{
		Use:   "call <operation> [--kind plan|story] [--params '{...}']",
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
			return a.run(cmd, kindName, args[0], json.RawMessage(raw))
		},
	}
	c.Flags().StringVarP(&params, "params", "p", "", "JSON parameters: file, - for stdin, or inline")
	c.Flags().StringVarP(&kindName, "kind", "k", kind.Plan, "document kind the operation applies to (plan or story)")
	return c
}

func (a *App) opsCmd() *cobra.Command {
	return &cobra.Command{Use: "ops", Short: "List every operation of every kind with its parameter schema", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			var all []ops.Describe
			for _, svc := range a.palace.Services() {
				all = append(all, a.regs[svc.Kind().Name].Describe(svc)...)
			}
			return a.emit(&ops.Result{Value: all})
		}}
}

func (a *App) configCmd() *cobra.Command {
	return &cobra.Command{Use: "config", Short: "Print the effective configuration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.run(cmd, kind.Plan, "getConfig", ops.Empty{}) }}
}

func (a *App) schemaCmd() *cobra.Command {
	var kindName string
	c := &cobra.Command{Use: "schema [--kind plan|story]", Short: "Print a creation JSON Schema", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			svc, err := a.palace.Service(kindName)
			if err != nil {
				return a.fail(err)
			}
			a.Stdout.Write(svc.Schema())
			fmt.Fprintln(a.Stdout)
			return nil
		}}
	c.Flags().StringVarP(&kindName, "kind", "k", kind.Plan, "document kind (plan or story)")
	return c
}

func (a *App) storesCmd() *cobra.Command {
	return &cobra.Command{Use: "stores", Short: "List configured stores and registered storage plugins", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.init(cmd.Context()); err != nil {
				return a.fail(err)
			}
			return a.emit(&ops.Result{Value: map[string]any{"stores": a.palace.StoreNames(), "plugins": store.Drivers()}})
		}}
}
