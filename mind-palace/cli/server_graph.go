package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/mongocode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/falkor"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

// serverGraphURLEnv is the variable `server graph` falls back to for the
// FalkorDB URL (redis://[user:password@]host:port[/db] or rediss://), the
// graph `server run --graph` indexes into.
const serverGraphURLEnv = "CLUED_SERVER_GRAPH"

// defaultGraphWait is how long `server graph rebuild` waits for a project
// another worker is indexing.
const defaultGraphWait = 2 * time.Minute

// graphCmdOptions is what the `server graph` commands resolved from their
// flags and the environment.
type graphCmdOptions struct {
	// GraphURL is the FalkorDB URL (--graph, CLUED_SERVER_GRAPH).
	GraphURL string
	// CodeStore is the code store (--code-store, CLUED_SERVER_CODE_STORE,
	// mongo); only mongo is shared with a running server.
	CodeStore string
	// SessionConfig names the MongoDB (mongoUrl, dbName) holding the code
	// stores and the indexer's cursors.
	SessionConfig string
}

// graphEnv is what the `server graph` commands index with: the code stores,
// the graphs and the cursors a running server's indexer shares.
type graphEnv struct {
	// Projects lists the projects Run and RunChecks visit (`server run`);
	// nil for the commands, which name their projects.
	Projects func(ctx context.Context) ([]string, error)
	Code     func(ctx context.Context, project string) (code.Store, error)
	Graphs   codegraph.Opener
	Cursors  codeindex.Cursors
	Close    func(ctx context.Context) error
}

// openGraphEnv opens the graph environment of o; tests replace it.
var openGraphEnv = func(ctx context.Context, o graphCmdOptions) (*graphEnv, error) {
	if o.GraphURL == "" {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: "--graph is required (or $" + serverGraphURLEnv + "): the FalkorDB URL the server indexes into"}
	}
	if o.CodeStore != codeStoreMongo {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("--code-store %q: only mongo is shared with the server (a memory code store lives in the server process)", o.CodeStore)}
	}
	graphs, err := falkor.New(o.GraphURL)
	if err != nil {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: err.Error()}
	}
	cfg := session.LoadConfig(o.SessionConfig)
	client, err := connectCodeMongo(ctx, cfg.MongoURL)
	if err != nil {
		_ = graphs.Close()
		return nil, &service.Error{Kind: kindConnect, Message: "code store: " + err.Error()}
	}
	db := client.Database(cfg.DBName)
	return &graphEnv{
		Code: func(ctx context.Context, key string) (code.Store, error) {
			return mongocode.Open(ctx, db, mongocode.ProjectPrefix("", key))
		},
		Graphs:  graphs,
		Cursors: codeindex.NewMongoCursors(db, "", nil),
		Close: func(ctx context.Context) error {
			return errors.Join(graphs.Close(), client.Disconnect(ctx))
		},
	}, nil
}

// graphIndexer is the code-graph indexer over env, configured as the
// server's: the FileApplier writes the files.
func graphIndexer(env *graphEnv, logger *slog.Logger) (*codeindex.Service, error) {
	return codeindex.New(codeindex.Options{
		Projects: env.Projects,
		Code:     env.Code,
		Graphs:   env.Graphs,
		Cursors:  env.Cursors,
		Apply:    codeindex.FileApplier{},
		Logger:   logger,
	})
}

// serverGraphCmd is `server graph rebuild|status`: operations on the code
// graphs of a clued server's projects, run beside the server against the
// same MongoDB and FalkorDB.
func (a *App) serverGraphCmd() *cobra.Command {
	var o graphCmdOptions
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Rebuild and inspect the code graphs of the server's projects",
		Long: `The code graph is a cache derived from the collected code: any project's
graph can be dropped and rebuilt from its current trees. These commands work
on the stores the server uses: the code stores and the indexer's cursors in
the session config's MongoDB (mongoUrl, dbName) and the FalkorDB of --graph
($` + serverGraphURLEnv + `). A project is named by its namespaced key, as
the code store names it. They take the project's indexing lease, so they
never write while the server's indexer does.`,
	}
	f := cmd.PersistentFlags()
	f.StringVar(&o.GraphURL, "graph", "", "FalkorDB URL, redis://[user:password@]host:port[/db] (default: $"+serverGraphURLEnv+")")
	f.StringVar(&o.CodeStore, "code-store", "", "code store: mongo (default: $"+envServerCodeStore+", then mongo)")
	f.StringVar(&o.SessionConfig, "session-config", session.DefaultConfigPath(), "clued session config file (mongoUrl, dbName)")
	resolve := func() graphCmdOptions {
		r := o
		r.GraphURL = firstNonEmpty(o.GraphURL, os.Getenv(serverGraphURLEnv))
		r.CodeStore = firstNonEmpty(o.CodeStore, os.Getenv(envServerCodeStore), codeStoreMongo)
		return r
	}
	cmd.AddCommand(a.serverGraphRebuildCmd(resolve), a.serverGraphStatusCmd(resolve))
	return cmd
}

// withGraphIndexer opens the environment, runs fn with an indexer over it
// and closes it.
func (a *App) withGraphIndexer(ctx context.Context, o graphCmdOptions, fn func(*codeindex.Service) error) error {
	env, err := openGraphEnv(ctx, o)
	if err != nil {
		return a.fail(err)
	}
	defer func() {
		if env.Close != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = env.Close(closeCtx)
		}
	}()
	svc, err := graphIndexer(env, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return a.fail(err)
	}
	return fn(svc)
}

// graphError maps an indexer error to the CLI's kinds.
func graphError(project string, err error) error {
	switch {
	case errors.Is(err, codegraph.ErrInvalid):
		return &service.Error{Kind: service.KindBadRequest, Message: err.Error(), Cause: err}
	case errors.Is(err, codeindex.ErrLeased):
		return &service.Error{Kind: service.KindConflict, Message: fmt.Sprintf("%s: another worker is indexing it; try again, or raise --wait", project), Cause: err}
	case errors.Is(err, codegraph.ErrUnavailable):
		return &service.Error{Kind: kindConnect, Message: err.Error(), Cause: err}
	}
	return &service.Error{Kind: service.KindStorage, Message: err.Error(), Cause: err}
}

func (a *App) serverGraphRebuildCmd(resolve func() graphCmdOptions) *cobra.Command {
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "rebuild <project>...",
		Short: "Drop a project's code graph and rebuild it from the project's current trees",
		Long: `Drops each project's graph and writes it again from the project's current
trees through the same selection and file writes as the incremental indexer,
then moves the indexer's cursor to the end of the log. Use it after a schema
or parser change, when "status --check" reports drift, or to load a graph
that was unloaded. A project another worker is indexing is retried until
--wait runs out.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return a.withGraphIndexer(ctx, resolve(), func(svc *codeindex.Service) error {
				results := make([]codeindex.RebuildResult, 0, len(args))
				for _, p := range args {
					deadline := time.Now().Add(wait)
					for {
						res, err := svc.Rebuild(ctx, p)
						if errors.Is(err, codeindex.ErrLeased) && time.Now().Before(deadline) {
							select {
							case <-ctx.Done():
								return a.fail(graphError(p, ctx.Err()))
							case <-time.After(graphRetryEvery):
							}
							continue
						}
						if err != nil {
							return a.fail(graphError(p, err))
						}
						results = append(results, res)
						break
					}
				}
				a.print(a.Stdout, map[string]any{"ok": true, "result": results})
				return nil
			})
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", defaultGraphWait, "how long to wait for a project another worker is indexing")
	return cmd
}

// graphRetryEvery is the pause between attempts on a leased project.
var graphRetryEvery = time.Second

// graphStatus is one project's `server graph status` result.
type graphStatus struct {
	codeindex.Status
	Check *codeindex.CheckReport `json:"check,omitempty"`
}

func (a *App) serverGraphStatusCmd(resolve func() graphCmdOptions) *cobra.Command {
	var check, repair bool
	cmd := &cobra.Command{
		Use:   "status <project>... [--check [--repair]]",
		Short: "Show where a project's indexing stands: cursor, pending changes, lease and layers",
		Long: `Prints, per project, the indexer's cursor and its last save, the changes
not yet indexed, the worker holding the lease, and the graph's layers with
their file counts and file hashes. --check also compares each layer with the
selection of the project's current trees (file counts, hashes and layer
nodes) under the lease, and --repair writes the difference when they
disagree. A project whose log is ahead of its cursor is reported "behind"
and not compared.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if repair && !check {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: "--repair needs --check"})
			}
			ctx := cmd.Context()
			return a.withGraphIndexer(ctx, resolve(), func(svc *codeindex.Service) error {
				out := make([]graphStatus, 0, len(args))
				for _, p := range args {
					st, err := svc.Status(ctx, p)
					if err != nil {
						return a.fail(graphError(p, err))
					}
					gs := graphStatus{Status: st}
					if check {
						rep, err := svc.Check(ctx, p, repair)
						if err != nil {
							return a.fail(graphError(p, err))
						}
						gs.Check = &rep
					}
					out = append(out, gs)
				}
				a.print(a.Stdout, map[string]any{"ok": true, "result": out})
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "compare the graph with the code store, layer by layer")
	cmd.Flags().BoolVar(&repair, "repair", false, "with --check, write the difference when the graph drifted")
	return cmd
}
