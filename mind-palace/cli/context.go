package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/collect"
	"github.com/robbiebyrd/clued/mind-palace/collect/envprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/fsprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/ghprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/gitprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/jsonprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/prime"
	"github.com/robbiebyrd/clued/mind-palace/collect/sources/docsource"
	"github.com/robbiebyrd/clued/mind-palace/collect/sources/sessionsource"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
)

// contextState holds the flags of the context group.
type contextState struct {
	root  string
	limit int
}

// contextRoot is the project directory the probes read, made absolute.
func (a *App) contextRoot() string {
	root := a.ctx.root
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(collect.ExpandHome(root)); err == nil {
		return abs
	}
	return root
}

func (a *App) fsProbe() *fsprobe.Probe   { return &fsprobe.Probe{Root: a.contextRoot()} }
func (a *App) gitProbe() *gitprobe.Probe { return &gitprobe.Probe{Root: a.contextRoot()} }
func (a *App) ghProbe() *ghprobe.Probe   { return &ghprobe.Probe{Root: a.contextRoot()} }
func (a *App) envProbe() *envprobe.Probe { return &envprobe.Probe{Root: a.contextRoot()} }
func (a *App) jsonProbe() *jsonprobe.Probe {
	return &jsonprobe.Probe{Root: a.contextRoot()}
}

// primeCollector wires every probe and source for the given root. The
// returned function closes what was opened.
func (a *App) primeCollector(ctx context.Context, root string, opts prime.Options) (*prime.Collector, func()) {
	cfg := a.sessionConfig()
	c := &prime.Collector{
		Root:              root,
		FS:                &fsprobe.Probe{Root: root},
		Git:               &gitprobe.Probe{Root: root},
		GH:                &ghprobe.Probe{Root: root},
		Env:               &envprobe.Probe{Root: root},
		Host:              hostinfo.Read,
		AccountID:         func() string { return session.ReadAccountID(cfg.ClaudeAppConfigPath) },
		ConfigPath:        a.sess.configPath,
		DaemonPort:        cfg.Port,
		DisabledEnrichers: cfg.DisabledEnrichers,
	}
	var closers []func()
	if !opts.NoStore {
		src := sessionsource.New(func(ctx context.Context) (session.Store, error) { return openSessionStore(ctx, cfg) }, c.AccountID())
		c.Sessions = src
		closers = append(closers, func() { _ = src.Close(context.Background()) })
	}
	if !opts.NoDocs {
		if docs, err := docsource.Open(ctx, root); err == nil && docs != nil {
			c.Docs = docs
			closers = append(closers, func() { _ = docs.Close() })
		}
	}
	return c, func() {
		for _, f := range closers {
			f()
		}
	}
}

// contextCmd builds the `context` group: probes that answer one plain-text
// question each and the composite `prime` report. Every probe prints its
// answer on stdout and exits 0, mapping failures to sentinels.
func (a *App) contextCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "context",
		Short: "Collect plain-text context about the working directory for skills",
		Long: `Probes answer one question each with a single plain-text answer and always
exit 0; failures print a sentinel (yes/no, none, 0, unknown, not a git repo,
[FALLBACK: ...]). "prime" prints the composite report that primes a session.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				a.answer("unknown subcommand: " + args[0])
				return nil
			}
			return cmd.Help()
		},
	}
	pf := c.PersistentFlags()
	pf.StringVar(&a.ctx.root, "root", "", "project directory (default: the working directory)")
	pf.IntVar(&a.ctx.limit, "limit", 0, "keep only the first N lines of the answer")
	pf.StringVar(&a.sess.configPath, "config", session.DefaultConfigPath(), "clued config file")

	c.AddCommand(
		a.probe("file-exists <path>", "Is there a regular file at path? (yes/no)", 1, func(ctx context.Context, args []string) string {
			return a.fsProbe().FileExists(args[0])
		}),
		a.probe("dir-exists <path>", "Is there a directory at path? (yes/no)", 1, func(ctx context.Context, args []string) string {
			return a.fsProbe().DirExists(args[0])
		}),
		a.probe("count-files <glob>...", "Number of files matching the globs (** supported)", 1, func(ctx context.Context, args []string) string {
			return a.fsProbe().CountFiles(args)
		}),
		a.probe("list-files <glob>...", "Files matching the globs, one per line (none when empty)", 1, func(ctx context.Context, args []string) string {
			return a.fsProbe().ListFiles(args, a.ctx.limit)
		}),
		a.fileHeadCmd(),
		a.probe("count-lines <path>", "Number of newline characters in the file (0 when unreadable)", 1, func(ctx context.Context, args []string) string {
			return a.fsProbe().CountLines(args[0])
		}),
		a.findFilesCmd(),
		a.probe("count-files-with-content <glob> <regexp>", "Number of files matching the glob whose content matches the regexp", 2, func(ctx context.Context, args []string) string {
			return a.fsProbe().CountFilesWithContent(args[0], args[1])
		}),
		a.probe("pwd", "The absolute project directory", 0, func(ctx context.Context, args []string) string {
			return a.fsProbe().Pwd()
		}),

		a.passthroughCmd("git", "Run a read-only git command (log, status, diff, show, ...); anything else is refused", func(ctx context.Context, args []string, limit int) string {
			return a.gitProbe().Git(ctx, args, limit)
		}),
		a.probe("git-is-clean", "Is the working tree clean? (yes/no)", 0, func(ctx context.Context, args []string) string {
			return a.gitProbe().IsClean(ctx)
		}),
		a.probe("git-commits-since-tag", "Commits since the latest tag (unknown without tags)", 0, func(ctx context.Context, args []string) string {
			return a.gitProbe().CommitsSinceTag(ctx)
		}),
		a.probe("git-origin", "URL of the origin remote (none when absent)", 0, func(ctx context.Context, args []string) string {
			return orNone(a.gitProbe().Origin(ctx))
		}),
		a.probe("git-branch", "The checked-out branch (none on a detached HEAD or outside a repository)", 0, func(ctx context.Context, args []string) string {
			return orNone(a.gitProbe().Branch(ctx))
		}),
		a.probe("git-default-branch", "The base branch: origin/HEAD, then main, then master (none when absent)", 0, func(ctx context.Context, args []string) string {
			return orNone(a.gitProbe().DefaultBranch(ctx))
		}),

		a.passthroughCmd("gh", "Run a read-only gh command (pr/issue/run list|view|checks, api GET, ...); anything else is refused", func(ctx context.Context, args []string, limit int) string {
			return a.ghProbe().GH(ctx, args, limit)
		}),
		a.probe("gh-pr-for-branch", "The open pull request for the current branch as #number: title (none when absent)", 0, func(ctx context.Context, args []string) string {
			return a.ghProbe().PRForBranch(ctx, a.gitProbe().Branch(ctx))
		}),

		a.probe("json-field <file> <dot.path>", "A value from a JSON file (objects as compact JSON, none when absent)", 2, func(ctx context.Context, args []string) string {
			return a.jsonProbe().Field(args[0], args[1])
		}),
		a.probe("json-count <file> <dot.path>", "Length of the array or number of keys of the object at the path", 2, func(ctx context.Context, args []string) string {
			return a.jsonProbe().Count(args[0], args[1])
		}),
		a.probe("json-count-where <file> <dot.path> <key> <value>", "Number of array items whose key equals value", 4, func(ctx context.Context, args []string) string {
			return a.jsonProbe().CountWhere(args[0], args[1], args[2], args[3])
		}),
		a.probe("json-count-with-key <file> <key>", "Number of objects anywhere in the file that contain the key", 2, func(ctx context.Context, args []string) string {
			return a.jsonProbe().CountWithKey(args[0], args[1])
		}),
		a.probe("mcp-configured <keyword>", "Is an MCP server mentioning the keyword configured? (configured/not configured)", 1, func(ctx context.Context, args []string) string {
			return a.jsonProbe().MCPConfigured(args[0])
		}),

		a.probe("command-exists <name>", "Path of the command on PATH (empty when absent)", 1, func(ctx context.Context, args []string) string {
			return a.envProbe().CommandExists(args[0])
		}),
		a.probe("detect-stack", "Languages by marker file: Go TS Rust Java C# Python (unknown when none)", 0, func(ctx context.Context, args []string) string {
			return a.envProbe().DetectStack()
		}),
		a.probe("detect-coverage", "Coverage files present (none found when absent)", 0, func(ctx context.Context, args []string) string {
			return a.envProbe().DetectCoverage()
		}),
		a.probe("detect-frontend", "Is there a package.json or tsconfig.json? (yes/no)", 0, func(ctx context.Context, args []string) string {
			return a.envProbe().DetectFrontend()
		}),
		a.probe("detect-css-framework", "CSS framework named in package.json (unknown when none)", 0, func(ctx context.Context, args []string) string {
			return a.envProbe().DetectCSSFramework()
		}),
		a.probe("detect-build-tool", "Build system and toolchain versions (no build tool detected when none)", 0, func(ctx context.Context, args []string) string {
			return a.envProbe().DetectBuildTool(ctx)
		}),
		a.detectPortCmd(),

		a.probe("host", "This machine: hostname user platform/arch release", 0, func(ctx context.Context, args []string) string {
			return prime.HostLine(hostinfo.Read())
		}),
		a.probe("account", "The Claude account id clued stores sessions under (unknown when unavailable)", 0, func(ctx context.Context, args []string) string {
			return session.ReadAccountID(a.sessionConfig().ClaudeAppConfigPath)
		}),
		a.probe("project-path", "The ~/.claude/projects directory name of the project directory", 0, func(ctx context.Context, args []string) string {
			return session.EncodeProjectPath(filepath.ToSlash(a.contextRoot()))
		}),
		a.probe("daemon-status", "Is the clued daemon answering on its port? (healthy/not running)", 0, func(ctx context.Context, args []string) string {
			port := a.sessionConfig().Port
			if prime.DaemonHealthy(ctx, port) {
				return fmt.Sprintf("healthy (port %d)", port)
			}
			return fmt.Sprintf("not running (port %d)", port)
		}),
		a.probe("session", "The most recent captured session for the project directory, with its top commands", 0, func(ctx context.Context, args []string) string {
			cfg := a.sessionConfig()
			src := sessionsource.New(func(ctx context.Context) (session.Store, error) { return openSessionStore(ctx, cfg) }, session.ReadAccountID(cfg.ClaudeAppConfigPath))
			defer func() { _ = src.Close(context.Background()) }()
			snap, err := src.Current(ctx, filepath.ToSlash(a.contextRoot()))
			if err != nil {
				return collect.StoreUnavailable
			}
			return prime.SessionBody(snap)
		}),
		a.primeCmd(),
	)
	return c
}

// answer prints one probe answer.
func (a *App) answer(s string) {
	fmt.Fprintln(a.Stdout, s)
}

func orNone(s string) string {
	if s == "" {
		return collect.None
	}
	return s
}

// probe builds a subcommand that prints fn's answer and always exits 0.
// Missing arguments print the fallback instead of a usage error.
func (a *App) probe(use, short string, minArgs int, fn func(ctx context.Context, args []string) string) *cobra.Command {
	name := strings.Fields(use)[0]
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < minArgs {
				a.answer(collect.Fallback(name, args))
				return nil
			}
			a.answer(collect.Truncate(fn(cmd.Context(), args), a.ctx.limit))
			return nil
		},
	}
}

// passthroughCmd builds `git`/`gh`: cobra flag parsing is off so the tool's
// own flags pass through untouched; --limit, --root and --config are picked
// out by hand.
func (a *App) passthroughCmd(name, short string, fn func(ctx context.Context, args []string, limit int) string) *cobra.Command {
	return &cobra.Command{
		Use:                name + " <args>...",
		Short:              short,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			args, limitStr := extractFlag(args, "--limit")
			args, root := extractFlag(args, "--root")
			args, configPath := extractFlag(args, "--config")
			if root != "" {
				a.ctx.root = root
			}
			if configPath != "" {
				a.sess.configPath = configPath
			}
			limit := a.ctx.limit
			if limitStr != "" {
				limit = collect.ParseLimit(limitStr)
			}
			a.answer(fn(cmd.Context(), args, limit))
			return nil
		},
	}
}

// extractFlag removes the first `--flag value` or `--flag=value` from args
// and returns the remaining args with the value ("" when absent).
func extractFlag(args []string, flag string) ([]string, string) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return append(append([]string{}, args[:i]...), args[i+2:]...), args[i+1]
		}
		if strings.HasPrefix(arg, flag+"=") {
			return append(append([]string{}, args[:i]...), args[i+1:]...), strings.TrimPrefix(arg, flag+"=")
		}
	}
	return args, ""
}

func (a *App) fileHeadCmd() *cobra.Command {
	var lines int
	c := a.probe("file-head <path>", "The first lines of a file (default 10; no <file> when unreadable)", 1, func(ctx context.Context, args []string) string {
		return a.fsProbe().FileHead(args[0], lines)
	})
	c.Flags().IntVar(&lines, "lines", 10, "number of lines")
	return c
}

func (a *App) findFilesCmd() *cobra.Command {
	var names []string
	var maxDepth int
	var count bool
	c := a.probe("find-files [dir]", "Files under dir (default .) matching the --name patterns, hidden directories skipped", 0, func(ctx context.Context, args []string) string {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}
		return a.fsProbe().FindFiles(dir, names, maxDepth, a.ctx.limit, count)
	})
	c.Flags().StringArrayVar(&names, "name", nil, "name pattern such as '*.go' (repeatable)")
	c.Flags().IntVar(&maxDepth, "maxdepth", 0, "maximum depth, 1 = direct children (0 = unlimited)")
	c.Flags().BoolVar(&count, "count", false, "print the number of matches instead of the list")
	return c
}

func (a *App) detectPortCmd() *cobra.Command {
	return a.probe("detect-port <port>...", "The first of the ports something listens on at 127.0.0.1 (not detected when none)", 0, func(ctx context.Context, args []string) string {
		var ports []int
		for _, arg := range args {
			if n, err := strconv.Atoi(arg); err == nil {
				ports = append(ports, n)
			}
		}
		return a.envProbe().DetectPort(ports)
	})
}

func (a *App) primeCmd() *cobra.Command {
	var opts prime.Options
	c := &cobra.Command{
		Use:   "prime",
		Short: "The composite report: ENV, CLUED, FILES, BRANCH, LOG, DIFF-STAT, STORIES, PLANS, SESSION, BUILD",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			collector, closeAll := a.primeCollector(ctx, a.contextRoot(), opts)
			defer closeAll()
			a.answer(collector.Collect(ctx, opts).String())
			return nil
		},
	}
	c.Flags().BoolVar(&opts.NoStore, "no-store", false, "skip the SESSION lookup in MongoDB")
	c.Flags().BoolVar(&opts.NoDocs, "no-docs", false, "skip the STORIES and PLANS sections")
	return c
}
