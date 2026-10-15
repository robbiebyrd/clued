package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/ingest"
	"github.com/robbiebyrd/clued/mind-palace/session/limits"
)

const (
	// statusThenTimeout bounds the status line command run after the sample.
	statusThenTimeout = 3 * time.Second
	// statusPostTimeout bounds the post of a sample, so a slow daemon or
	// server never holds the status line.
	statusPostTimeout = 2 * time.Second
	// statusThenFile is where register-hooks keeps the status line command
	// that was configured before clued's, to run after the sample.
	statusThenFile = "statusline.json"
)

// hookStatusCmd is `hook status`, Claude Code's status line command when
// the config enables it: it samples the context window and the usage
// limits into a LimitsSampled event and prints the status line.
func (a *App) hookStatusCmd() *cobra.Command {
	var then, stateDir string
	var quiet bool
	cmd := &cobra.Command{
		Use:   "status [--then <command>] [--state-dir dir] [--quiet]",
		Short: "Status line command: sample the context window and usage limits into LimitsSampled events, then print the line",
		Long: `Reads the JSON Claude Code pipes to its status line command on stdin and
prints the status line first: the output of --then (a shell command run with
the same stdin, by default the command register-hooks found configured
before clued's, kept in statusline.json next to the config file), else a
line of its own such as "ctx 37% of 1M · 5h 12% · 7d 40% · Fable 5.1".
Then, when the sample is worth keeping (the first of the session, a change
of a point in the context's fill, any change of a usage window, or five
minutes since the last one, never within fifteen seconds of it), it posts
a LimitsSampled event with the session's context_window, rate_limits,
model, version and cost: to the daemon in local mode (the write-ahead log
when it is down), to the clued server in remote mode. The last stored
sample of each session is kept under --state-dir. It never fails the
status line: problems are notes on stderr and the exit code is 0.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := io.ReadAll(a.Stdin)
			if err != nil {
				return fmt.Errorf("hook status: read stdin: %w", err)
			}
			cfg := a.sessionConfig()
			if then == "" {
				then = statusThen(cfg)
			}
			in, perr := limits.Parse(raw)
			if !quiet {
				a.printStatusLine(cmd.Context(), in, raw, then)
			}
			if perr != nil {
				fmt.Fprintf(a.Stderr, "hook status: %v\n", perr)
				return nil
			}
			if err := in.Validate(); err != nil {
				if !errors.Is(err, limits.ErrNoSession) {
					fmt.Fprintf(a.Stderr, "hook status: %v\n", err)
				}
				return nil
			}
			now := time.Now()
			sample := in.Sample(now)
			if stateDir == "" {
				stateDir = statusStateDir(cfg)
			}
			if !limits.Due(limits.LoadState(stateDir, in.SessionID), sample) {
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), statusPostTimeout)
			defer cancel()
			if err := a.postSample(ctx, cfg, in.Event(now)); err != nil {
				fmt.Fprintf(a.Stderr, "hook status: %v\n", err)
				return nil
			}
			if err := limits.SaveState(stateDir, in.SessionID, sample); err != nil {
				fmt.Fprintf(a.Stderr, "hook status: %v\n", err)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&then, "then", "", "status line command to run with the same stdin and print (default: the one kept in statusline.json next to the config file)")
	f.StringVar(&stateDir, "state-dir", "", "where the last stored sample of each session is kept (default: limits/ next to the config file)")
	f.BoolVar(&quiet, "quiet", false, "print no status line, only sample")
	return cmd
}

// printStatusLine prints the line: the --then command's output, else the
// sample's own line.
func (a *App) printStatusLine(ctx context.Context, in limits.Input, raw []byte, then string) {
	if then == "" {
		if line := in.Line(); line != "" {
			fmt.Fprintln(a.Stdout, line)
		}
		return
	}
	ctx, cancel := context.WithTimeout(ctx, statusThenTimeout)
	defer cancel()
	c := osexec.CommandContext(ctx, "sh", "-c", then)
	c.Stdin = strings.NewReader(string(raw))
	c.Stdout = a.Stdout
	c.Stderr = a.Stderr
	if err := c.Run(); err != nil {
		fmt.Fprintf(a.Stderr, "hook status: the status line command failed: %v\n", err)
		if line := in.Line(); line != "" {
			fmt.Fprintln(a.Stdout, line)
		}
	}
}

// postSample stores one event: through the daemon (or its write-ahead
// log) in local mode, straight into the clued server in remote mode.
func (a *App) postSample(ctx context.Context, cfg session.Config, doc session.Doc) error {
	if !cfg.Remote() {
		return postToDaemon(ctx, cfg.Port, cfg.WalPath, doc)
	}
	store, err := a.sessionStore(ctx)
	if err != nil {
		return err
	}
	defer a.closeSessionStore()
	svc := &ingest.Service{
		Store:     store,
		AccountID: session.ReadAccountID(cfg.ClaudeAppConfigPath),
		Host:      hostinfo.Read(),
		Logger:    slog.New(slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		WALPath:   cfg.WalPath,
	}
	if _, err := svc.Handle(ctx, doc); err != nil && !errors.Is(err, ingest.ErrBuffered) {
		return err
	}
	return nil
}

// statusThen is the status line command configured before clued's, which
// register-hooks keeps in statusline.json next to the config file as
// {"then": "<command>"}.
func statusThen(cfg session.Config) string {
	b, err := os.ReadFile(filepath.Join(configDir(cfg), statusThenFile))
	if err != nil {
		return ""
	}
	var state struct {
		Then string `json:"then"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return ""
	}
	return strings.TrimSpace(state.Then)
}

// statusStateDir is where the sampler keeps its state: limits/ next to the
// write-ahead log, which lives next to the config file.
func statusStateDir(cfg session.Config) string {
	return filepath.Join(configDir(cfg), "limits")
}

// configDir is the plugin's data directory: where the write-ahead log and
// the config file live.
func configDir(cfg session.Config) string {
	base := cfg.WalPath
	if base == "" {
		base = session.DefaultConfigPath()
	}
	return filepath.Dir(base)
}
