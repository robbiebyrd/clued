package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/session/codesync"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
)

// sessionCodeSyncCmd is `session code-sync`: one code sync of a worktree
// with no budget (a first import or a manual repair), and `status`, the
// local state of the worktrees this machine syncs.
func (a *App) sessionCodeSyncCmd() *cobra.Command {
	var full, dryRun bool
	var dir string
	c := &cobra.Command{
		Use:   "code-sync [--full] [--dry-run] [--path DIR]",
		Short: "Sync a worktree's source files to the clued server's code store now, with no time or size budget",
		Long: `Runs the code sync the hook relay runs on file-changing events, for the
worktree containing --path (the working directory by default), without the
relay's 3 s and 8 MiB budget: the way to do a first import of a large
repository or to repair a worktree in one go. Without --full it sends what
git status reports changed, the files it holds as dirty and the pending
queue (a run whose ref changed, or whose last full reconcile is older than
6 hours, reconciles everything anyway); --full reconciles every file the
worktree has (tracked plus untracked-but-not-ignored); --dry-run works out what would be sent without
sending it or saving the state. It needs remote mode and codeSync on
(config.json or CLUED_CODE_SYNC); otherwise, and for a repository that opted
out or has no origin, the result says why it skipped. ` + "`code-sync status`" + `
shows the local state only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := a.sessionConfig()
			if !cfg.Remote() {
				return a.sessionBadRequest("code sync needs remote mode: no serverUrl in " + a.sess.configPath + " (or CLUED_SERVER_URL)")
			}
			cwd, err := codeSyncDir(dir)
			if err != nil {
				return a.sessionBadRequest(err.Error())
			}
			req := codesync.Request{
				Cwd: cwd,
				// Without --full, what git status reports changed and the
				// dirty files, plus the pending queue.
				Status:   true,
				Full:     full,
				Budget:   -1,
				MaxBytes: -1,
				DryRun:   dryRun,
				Config:   cfg,
				StateDir: codesync.StateDir(filepath.Dir(a.sess.configPath)),
				Log:      func(msg string) { fmt.Fprintln(a.Stderr, "clued: "+msg) },
			}
			rep, err := codesync.Run(cmd.Context(), req)
			for _, w := range rep.Warnings {
				fmt.Fprintln(a.Stderr, "warning: "+w)
			}
			if err != nil {
				return a.sessionFail(err)
			}
			res := codeSyncReport(rep)
			res["dry_run"] = dryRun
			a.print(a.Stdout, map[string]any{"ok": true, "result": res})
			return nil
		},
	}
	f := c.Flags()
	f.BoolVar(&full, "full", false, "reconcile every file of the worktree")
	f.BoolVar(&dryRun, "dry-run", false, "show what would be sent; send nothing and keep the state")
	f.StringVar(&dir, "path", "", "a directory inside the worktree (default: the working directory)")
	c.AddCommand(a.sessionCodeSyncStatusCmd())
	return c
}

// sessionCodeSyncStatusCmd is `session code-sync status`: what the state
// files under <config dir>/codesync hold. It reads nothing from the server.
func (a *App) sessionCodeSyncStatusCmd() *cobra.Command {
	var dir string
	c := &cobra.Command{
		Use:   "status [--path DIR]",
		Short: "Show the local code sync state: each worktree's ref, files, pending and refused paths, last full reconcile and back-off",
		Long: `Reads the state files code sync keeps under <config dir>/codesync, one
per worktree, and asks the server nothing. With --path only the worktree
containing DIR is shown (an empty list when it was never synced).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := a.sessionConfig()
			stateDir := codesync.StateDir(filepath.Dir(a.sess.configPath))
			var ids []string
			if dir != "" {
				cwd, err := codeSyncDir(dir)
				if err != nil {
					return a.sessionBadRequest(err.Error())
				}
				root := gitinfo.Root(cmd.Context(), cwd)
				if root == "" {
					return a.sessionBadRequest(cwd + " is not in a git repository")
				}
				ids = []string{codesync.WorktreeID(hostinfo.Read().Hostname, filepath.Clean(root))}
			} else {
				var err error
				if ids, err = codeSyncStateIDs(stateDir); err != nil {
					return a.sessionFail(err)
				}
			}
			worktrees := []map[string]any{}
			for _, id := range ids {
				p, err := codesync.StatePath(stateDir, id)
				if err != nil {
					continue
				}
				if _, err := os.Stat(p); err != nil {
					continue
				}
				st, err := codesync.LoadState(stateDir, id)
				if err != nil {
					fmt.Fprintln(a.Stderr, "warning: "+err.Error())
					continue
				}
				worktrees = append(worktrees, codeSyncStateSummary(id, st))
			}
			a.print(a.Stdout, map[string]any{"ok": true, "result": map[string]any{
				"enabled":   cfg.CodeSyncEnabled(),
				"remote":    cfg.Remote(),
				"state_dir": stateDir,
				"worktrees": worktrees,
			}})
			return nil
		},
	}
	c.Flags().StringVar(&dir, "path", "", "only the worktree containing this directory")
	return c
}

// codeSyncDir is the absolute directory --path names, or the working
// directory.
func codeSyncDir(dir string) (string, error) {
	if dir == "" {
		return os.Getwd()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("--path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--path: %s is not a directory", abs)
	}
	return abs, nil
}

// codeSyncStateIDs lists the worktree ids that have a state file in dir.
func codeSyncStateIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if ok && e.Type().IsRegular() && codesync.ValidWorktreeID(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// codeSyncReport renders a run's report for the JSON envelope.
func codeSyncReport(rep codesync.Report) map[string]any {
	res := map[string]any{"summary": rep.String()}
	if rep.Skipped != "" {
		res["skipped"] = rep.Skipped
		return res
	}
	res["worktree"], res["root"], res["project"] = rep.Worktree, rep.Root, rep.Project
	res["ref"], res["head"] = rep.Ref, rep.Head
	res["full"], res["full_committed"] = rep.Full, rep.FullCommitted
	res["upserts"], res["deletes"] = rep.Upserts, rep.Deletes
	res["hashed"], res["hashed_bytes"] = rep.Hashed, rep.HashedBytes
	res["uploaded"], res["uploaded_bytes"] = rep.Uploaded, rep.UploadedBytes
	res["refused"], res["pending"], res["seq"] = rep.Refused, rep.Pending, rep.Seq
	if rep.BackedOff != "" {
		res["backed_off"] = rep.BackedOff
	}
	return res
}

// codeSyncStateSummary renders one worktree's state: counts rather than
// the per-file cache, and the pending and refused paths.
func codeSyncStateSummary(id string, st codesync.State) map[string]any {
	synced, dirty, unpushed := 0, 0, 0
	for _, f := range st.Files {
		if f.Synced {
			synced++
		}
		if f.Dirty {
			dirty++
		}
		if f.Unpushed {
			unpushed++
		}
	}
	refused := make([]string, 0, len(st.Refused))
	for p := range st.Refused {
		refused = append(refused, p)
	}
	sort.Strings(refused)
	out := map[string]any{
		"worktree":        id,
		"root":            st.Root,
		"project":         st.Project,
		"ref":             st.Ref,
		"head":            st.Head,
		"upstream":        st.Upstream,
		"ahead":           st.Ahead,
		"default_branch":  st.DefaultBranch,
		"files":           len(st.Files),
		"synced":          synced,
		"dirty":           dirty,
		"unpushed":        unpushed,
		"pending_upserts": nonNilStrings(st.Pending.Upserts),
		"pending_deletes": nonNilStrings(st.Pending.Deletes),
		"refused":         refused,
	}
	if !st.LastFull.IsZero() {
		out["last_full"] = st.LastFull.UTC().Format(time.RFC3339)
	}
	if !st.UnsupportedUntil.IsZero() {
		out["unsupported_until"] = st.UnsupportedUntil.UTC().Format(time.RFC3339)
	}
	return out
}
