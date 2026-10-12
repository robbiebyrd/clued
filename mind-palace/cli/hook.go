package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/hookguard"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// hookCmd groups the Claude Code hooks served by the binary. Each reads the
// hook's JSON input on stdin and prints exactly what Claude Code expects on
// stdout (no {"ok": ...} envelope), so hooks.json can call it directly.
func (a *App) hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Claude Code hooks served by the binary (read hook JSON on stdin)",
	}
	cmd.AddCommand(a.hookStopGuardCmd(), a.hookWriteGuardCmd(), a.hookStatusReviewCmd())
	return cmd
}

func (a *App) hookWriteGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "write-guard",
		Short: "PreToolUse hook: route writes to plans and stories through mind-palace",
		Long: `Reads a PreToolUse hook input on stdin and decides whether the tool call may
touch the plans and stories mind-palace manages (the directories configured
for the hook's cwd, docs/plans and docs/stories by default, and
docs/superpowers/plans anywhere). Creating a document, overwriting one,
editing its front matter, H1, acceptance criteria or work log, editing a
template, and changing those directories from the shell are denied with
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",
"permissionDecisionReason":...}} naming the MCP tool and CLI command to use
instead; an edit of a document's body proceeds with additionalContext; a
git commit that closes a story that is not complete is denied; EnterPlanMode
and ExitPlanMode add context telling the assistant to save the plan with
plan_create. Nothing is printed for calls the guard does not concern.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, err := io.ReadAll(a.Stdin)
			if err != nil {
				return fmt.Errorf("hook write-guard: read stdin: %w", err)
			}
			var in hookguard.WriteInput
			if err := json.Unmarshal(input, &in); err != nil {
				return fmt.Errorf("hook write-guard: parse hook input: %w", err)
			}
			decision := hookguard.CheckWrite(in, a.guardDirs(in.Cwd))
			if decision == nil {
				return nil
			}
			return json.NewEncoder(a.Stdout).Encode(decision)
		},
	}
}

// guardDirs resolves the plans and stories directories the write guard
// protects: the configuration found from cwd (or --config), with the
// --plans-dir and --stories-dir flags on top, made absolute against cwd.
// When no configuration loads, the defaults under cwd apply.
func (a *App) guardDirs(cwd string) hookguard.Dirs {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if cwd != "" {
		if wd, err := os.Getwd(); err == nil && wd != cwd {
			if err := os.Chdir(cwd); err == nil {
				defer func() { _ = os.Chdir(wd) }()
			}
		}
	}
	cfg, err := config.Load(a.configPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "hook write-guard: %v; guarding the default directories\n", err)
		cfg = config.Default()
	}
	plans, stories := cfg.Plans.Dir, cfg.Stories.Dir
	if a.plansDir != "" {
		plans = a.plansDir
	}
	if a.storiesDir != "" {
		stories = a.storiesDir
	}
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(cwd, p)
	}
	return hookguard.Dirs{Plans: abs(plans), Stories: abs(stories)}
}

func (a *App) hookStopGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop-guard",
		Short: "Stop hook: block stopping on ownership-dodging, deferring or permission-seeking phrases",
		Long: `Reads the Stop hook input on stdin. When last_assistant_message contains a
phrase that dodges ownership of a failure (" pre-existing", "unrelated to my
changes"), defers work ("next session", "future work") or asks whether to
continue work already asked for ("shall I continue"), it prints
{"decision":"block","reason":"STOP HOOK VIOLATION: ..."} so Claude Code sends
the assistant back to work. Nothing is printed when the assistant may stop,
including when stop_hook_active is set (the guard already fired this turn).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, err := io.ReadAll(a.Stdin)
			if err != nil {
				return fmt.Errorf("hook stop-guard: read stdin: %w", err)
			}
			decision, err := hookguard.CheckStopJSON(input)
			if err != nil {
				return fmt.Errorf("hook stop-guard: parse hook input: %w", err)
			}
			if decision == nil {
				return nil
			}
			enc := json.NewEncoder(a.Stdout)
			return enc.Encode(decision)
		},
	}
}

// hookInput is the part of any hook's JSON input the status review reads.
type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	Cwd           string `json:"cwd"`
}

// hookContext is the JSON a hook prints to add context to the conversation.
type hookContext struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func (a *App) hookStatusReviewCmd() *cobra.Command {
	var every time.Duration
	var stateDir string
	cmd := &cobra.Command{
		Use:   "status-review",
		Short: "SessionStart/UserPromptSubmit hook: ask about plans and stories whose work is finished but still open",
		Long: `Reads the hook input on stdin, reviews the plans and stories of the working
directory (the hook's cwd, else the current directory) and, when a plan has
all of its stories complete or a story has all of its steps complete and all
of its acceptance criteria done while its status is still open, prints
{"hookSpecificOutput":{"hookEventName":...,"additionalContext":...}} telling
the assistant to ask the user whether to close them. Nothing is printed when
nothing needs attention, when the directory holds no plans or stories, or
when --every is set and the last review of this directory ran more recently
than that (the time is kept per directory under --state-dir).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := io.ReadAll(a.Stdin)
			if err != nil {
				return fmt.Errorf("hook status-review: read stdin: %w", err)
			}
			var in hookInput
			if len(strings.TrimSpace(string(raw))) > 0 {
				if err := json.Unmarshal(raw, &in); err != nil {
					return fmt.Errorf("hook status-review: parse hook input: %w", err)
				}
			}
			dir := in.Cwd
			if dir == "" {
				dir, _ = os.Getwd()
			}
			if every > 0 && !dueFor(stateDir, dir, every) {
				return nil
			}
			if dir != "" {
				if err := os.Chdir(dir); err != nil {
					return nil // nothing to review where the hook cannot look
				}
			}
			ctx := cmd.Context()
			if err := a.init(ctx); err != nil {
				// A directory without mind-palace documents or a usable
				// config is not an error for a hook: stay silent.
				fmt.Fprintf(a.Stderr, "hook status-review: %v\n", err)
				return nil
			}
			r, err := a.palace.Review(ctx)
			if err != nil {
				fmt.Fprintf(a.Stderr, "hook status-review: %v\n", err)
				return nil
			}
			if every > 0 {
				markReviewed(stateDir, dir)
			}
			if r.IsEmpty() {
				return nil
			}
			var out hookContext
			out.HookSpecificOutput.HookEventName = in.HookEventName
			if out.HookSpecificOutput.HookEventName == "" {
				out.HookSpecificOutput.HookEventName = "UserPromptSubmit"
			}
			out.HookSpecificOutput.AdditionalContext = reviewContext(r)
			return json.NewEncoder(a.Stdout).Encode(out)
		},
	}
	cmd.Flags().DurationVar(&every, "every", 0, "run at most once per this interval per directory (0: every time)")
	cmd.Flags().StringVar(&stateDir, "state-dir", defaultReviewStateDir(), "where the last-review times are kept")
	return cmd
}

// reviewContext words the review as an instruction to the assistant.
func reviewContext(r *service.Review) string {
	var b strings.Builder
	b.WriteString("mind-palace status review: the following documents look finished but are still open. Ask the user whether to close them; do not change their status without an answer.\n")
	for _, p := range r.Plans {
		fmt.Fprintf(&b, "- Plan %s %q (status %s): %s (%s). To complete it: plan_setStatus or `mind-palace plan set-status %s complete`.\n", p.ID, p.Title, p.Status, p.Reason, strings.Join(p.Stories, ", "), p.ID)
	}
	for _, s := range r.Stories {
		fmt.Fprintf(&b, "- Story %s %q (status %s): %s. To close it: story_setStatus or `mind-palace story set-status %s complete`.\n", s.ID, s.Title, s.Status, s.Reason, s.ID)
	}
	return b.String()
}

func defaultReviewStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "mind-palace-status-review")
	}
	return filepath.Join(home, ".claude", "plugins", "data", "clued", "status-review")
}

func reviewStamp(stateDir, dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(stateDir, hex.EncodeToString(sum[:8]))
}

// dueFor reports whether dir has not been reviewed within every.
func dueFor(stateDir, dir string, every time.Duration) bool {
	info, err := os.Stat(reviewStamp(stateDir, dir))
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) >= every
}

func markReviewed(stateDir, dir string) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return
	}
	stamp := reviewStamp(stateDir, dir)
	now := time.Now()
	if err := os.Chtimes(stamp, now, now); err != nil {
		_ = os.WriteFile(stamp, []byte(dir+"\n"), 0o600)
	}
}
