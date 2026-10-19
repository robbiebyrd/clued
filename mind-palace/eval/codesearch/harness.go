// Package codesearch is the evaluation of the code graph tools (plan
// 0012-cgi, 5.1): a fixed task set with ground truth on clued, clued-cloud
// and a pinned corpus of public repositories, a runner that answers each
// task in a headless Claude Code session (`claude -p`) once per arm and run,
// and the scoring and report. Tokens, turns and tool calls come from
// clued's own capture of those sessions (`session usage`, `session
// activities`), never from the agent's self-report.
//
// The loader, validator and scorer are deterministic and tested on every
// `go test`; the full run is manual (scripts/eval-codesearch.sh, which sets
// CLUED_EVAL=1), like scripts/verify-prime.sh.
package codesearch

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed tasks.yaml corpus.yaml
var embedded embed.FS

// Task families: a named lookup ("where is X defined and who calls it?"), a
// concept lookup ("where are plan sequence numbers allocated?"), an impact
// question ("what breaks if X changes?") and a small edit over two or three
// files, checked by a command.
const (
	FamilyNamed   = "named"
	FamilyConcept = "concept"
	FamilyImpact  = "impact"
	FamilyEdit    = "edit"
)

// Families lists the task families in report order.
var Families = []string{FamilyNamed, FamilyConcept, FamilyImpact, FamilyEdit}

// The repositories the tasks run on besides the corpus: this checkout and a
// clued-cloud checkout.
const (
	RepoClued = "clued"
	RepoCloud = "clued-cloud"
)

// OwnLanguages are the languages of the tasks on clued and clued-cloud.
var OwnLanguages = []string{"go", "markdown", "shell"}

// CorpusLanguages are the other languages the code graph parses (plan
// 0012-cgi, Languages); each has about three tasks on the pinned corpus.
// "lisp" covers Common Lisp, Emacs Lisp and Clojure (a repo's Dialect).
var CorpusLanguages = []string{
	"python", "c", "cpp", "csharp", "objc", "swift", "java", "javascript", "typescript",
	"sql", "rust", "php", "perl", "ocaml", "dart", "ruby", "kotlin", "lua", "lisp",
}

// MinTasksPerLanguage is how many tasks each corpus language needs.
const MinTasksPerLanguage = 3

// MinOwnTasks is the least number of tasks on clued and clued-cloud.
const MinOwnTasks = 36

// Truth is what a correct answer names: every file (a repository-relative
// path) and every symbol. MinRecall (default 1) is the share of each list
// an answer must name to succeed; for an edit, Files are the files the
// change must touch.
type Truth struct {
	Files     []string `yaml:"files" json:"files"`
	Symbols   []string `yaml:"symbols,omitempty" json:"symbols,omitempty"`
	MinRecall float64  `yaml:"min_recall,omitempty" json:"min_recall,omitempty"`
}

// Task is one question or edit with its ground truth. Check, for an edit,
// is a bash command run in the checkout's root after the session; the edit
// succeeds only when it exits 0.
type Task struct {
	ID     string `yaml:"id" json:"id"`
	Repo   string `yaml:"repo" json:"repo"`
	Lang   string `yaml:"lang" json:"lang"`
	Family string `yaml:"family" json:"family"`
	Prompt string `yaml:"prompt" json:"prompt"`
	Truth  Truth  `yaml:"truth" json:"truth"`
	Check  string `yaml:"check,omitempty" json:"check,omitempty"`
}

// TaskSet is tasks.yaml.
type TaskSet struct {
	Tasks []Task `yaml:"tasks"`
}

// Repo is one pinned public repository of the corpus: Commit is the full
// SHA the tasks' ground truth was read at, Branch the branch it was the head
// of when pinned.
type Repo struct {
	ID      string `yaml:"id" json:"id"`
	Lang    string `yaml:"lang" json:"lang"`
	Dialect string `yaml:"dialect,omitempty" json:"dialect,omitempty"`
	URL     string `yaml:"url" json:"url"`
	Branch  string `yaml:"branch" json:"branch"`
	Commit  string `yaml:"commit" json:"commit"`
}

// Corpus is corpus.yaml.
type Corpus struct {
	Repos []Repo `yaml:"repos"`
}

// Repo returns the corpus repository with id.
func (c Corpus) Repo(id string) (Repo, bool) {
	for _, r := range c.Repos {
		if r.ID == id {
			return r, true
		}
	}
	return Repo{}, false
}

// LoadTasks parses the embedded tasks.yaml.
func LoadTasks() (TaskSet, error) {
	b, err := embedded.ReadFile("tasks.yaml")
	if err != nil {
		return TaskSet{}, err
	}
	return ParseTasks(b)
}

// LoadCorpus parses the embedded corpus.yaml.
func LoadCorpus() (Corpus, error) {
	b, err := embedded.ReadFile("corpus.yaml")
	if err != nil {
		return Corpus{}, err
	}
	return ParseCorpus(b)
}

// ParseTasks decodes a task set, refusing unknown keys.
func ParseTasks(b []byte) (TaskSet, error) {
	var ts TaskSet
	if err := decodeStrict(b, &ts); err != nil {
		return TaskSet{}, fmt.Errorf("tasks: %w", err)
	}
	return ts, nil
}

// ParseCorpus decodes a corpus, refusing unknown keys.
func ParseCorpus(b []byte) (Corpus, error) {
	var c Corpus
	if err := decodeStrict(b, &c); err != nil {
		return Corpus{}, fmt.Errorf("corpus: %w", err)
	}
	return c, nil
}

func decodeStrict(b []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(v)
}

var (
	idRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	commitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Validate checks a task set against its corpus: unique ids, known repos,
// languages and families, ground truth present and relative, a check on
// every edit (and only there), each corpus repo pinned to a full commit and
// used, at least MinOwnTasks tasks on clued and clued-cloud covering every
// family, and MinTasksPerLanguage tasks for each corpus language. It returns
// every problem joined.
func Validate(ts TaskSet, c Corpus) error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	repoIDs := map[string]bool{}
	for _, r := range c.Repos {
		switch {
		case !idRe.MatchString(r.ID):
			bad("corpus repo %q: id must be lower-case letters, digits and dashes", r.ID)
		case repoIDs[r.ID] || r.ID == RepoClued || r.ID == RepoCloud:
			bad("corpus repo %q: duplicate id", r.ID)
		}
		repoIDs[r.ID] = true
		if !slices.Contains(CorpusLanguages, r.Lang) {
			bad("corpus repo %q: language %q is not a corpus language", r.ID, r.Lang)
		}
		if !strings.HasPrefix(r.URL, "https://github.com/") {
			bad("corpus repo %q: url %q is not a GitHub https URL", r.ID, r.URL)
		}
		if !commitRe.MatchString(r.Commit) {
			bad("corpus repo %q: commit %q is not a full 40-character SHA", r.ID, r.Commit)
		}
		if r.Branch == "" {
			bad("corpus repo %q: no branch", r.ID)
		}
	}

	ids := map[string]bool{}
	used := map[string]bool{}
	perLang := map[string]int{}
	ownFamilies := map[string]bool{}
	own := 0
	for i, t := range ts.Tasks {
		name := t.ID
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		if !idRe.MatchString(t.ID) {
			bad("task %s: id must be lower-case letters, digits and dashes", name)
		}
		if ids[t.ID] {
			bad("task %s: duplicate id", name)
		}
		ids[t.ID] = true
		if !slices.Contains(Families, t.Family) {
			bad("task %s: family %q is not one of %s", name, t.Family, strings.Join(Families, ", "))
		}
		if strings.TrimSpace(t.Prompt) == "" {
			bad("task %s: empty prompt", name)
		}
		switch t.Repo {
		case RepoClued, RepoCloud:
			own++
			ownFamilies[t.Family] = true
			if !slices.Contains(OwnLanguages, t.Lang) {
				bad("task %s: language %q is not one of %s", name, t.Lang, strings.Join(OwnLanguages, ", "))
			}
		default:
			r, ok := c.Repo(t.Repo)
			if !ok {
				bad("task %s: repo %q is not clued, clued-cloud or in the corpus", name, t.Repo)
				break
			}
			used[t.Repo] = true
			if t.Lang != r.Lang {
				bad("task %s: language %q differs from repo %s's %q", name, t.Lang, r.ID, r.Lang)
			}
			perLang[t.Lang]++
		}
		if len(t.Truth.Files) == 0 {
			bad("task %s: ground truth names no file", name)
		}
		for _, f := range t.Truth.Files {
			if !filepath.IsLocal(f) || path.Clean(f) != f || strings.Contains(f, `\`) {
				bad("task %s: truth file %q is not a clean repository-relative path", name, f)
			}
		}
		for _, s := range t.Truth.Symbols {
			if strings.TrimSpace(s) == "" || strings.TrimSpace(s) != s {
				bad("task %s: truth symbol %q is blank or padded", name, s)
			}
		}
		if t.Truth.MinRecall < 0 || t.Truth.MinRecall > 1 {
			bad("task %s: min_recall %v is outside [0, 1]", name, t.Truth.MinRecall)
		}
		if t.Family == FamilyEdit && strings.TrimSpace(t.Check) == "" {
			bad("task %s: an edit needs a check command", name)
		}
		if t.Family != FamilyEdit && t.Check != "" {
			bad("task %s: only edits have a check command", name)
		}
	}
	if own < MinOwnTasks {
		bad("only %d tasks on clued and clued-cloud, want at least %d", own, MinOwnTasks)
	}
	for _, f := range Families {
		if !ownFamilies[f] {
			bad("no %s task on clued or clued-cloud", f)
		}
	}
	for _, l := range CorpusLanguages {
		if perLang[l] < MinTasksPerLanguage {
			bad("language %s has %d tasks, want at least %d", l, perLang[l], MinTasksPerLanguage)
		}
	}
	for _, r := range c.Repos {
		if !used[r.ID] {
			bad("corpus repo %q has no task", r.ID)
		}
	}
	return errors.Join(errs...)
}

// Select keeps the tasks whose id matches re (all when re is nil).
func Select(tasks []Task, re *regexp.Regexp) []Task {
	if re == nil {
		return tasks
	}
	var out []Task
	for _, t := range tasks {
		if re.MatchString(t.ID) {
			out = append(out, t)
		}
	}
	return out
}

// CodeTools are the graph tools of the clued MCP server (plan 0012-cgi,
// 4.2), without their MCP prefix.
var CodeTools = []string{"code_search", "code_symbol", "code_neighbors", "code_outline", "code_context"}

// DefaultToolPrefix is the MCP prefix of the clued server's tools when the
// plugin is installed (the tool names a session sees).
const DefaultToolPrefix = "mcp__plugin_clued_clued__"

// BaseTools are the tools every arm may use without a permission prompt.
var BaseTools = []string{"Read", "Grep", "Glob", "Bash", "Edit", "MultiEdit", "Write"}

// AlwaysDisallowed are refused in every arm so that all the work happens in
// the measured session: sub-agents run in transcripts of their own, and the
// web is not part of the task.
var AlwaysDisallowed = []string{"Task", "Agent", "WebSearch", "WebFetch"}

// Arm is one configuration a task runs in; Tools are the code tools it may
// use (the others are disallowed).
type Arm struct {
	Name  string   `json:"name"`
	Tools []string `json:"tools"`
}

// Arms are the three arms of the plan: grep and read only; plus every graph
// tool; plus code_context only.
var Arms = []Arm{
	{Name: "grep"},
	{Name: "graph", Tools: CodeTools},
	{Name: "context", Tools: []string{"code_context"}},
}

// ArmsNamed returns the arms with these names, in Arms order.
func ArmsNamed(names []string) ([]Arm, error) {
	var out []Arm
	for _, n := range names {
		i := slices.IndexFunc(Arms, func(a Arm) bool { return a.Name == n })
		if i < 0 {
			return nil, fmt.Errorf("unknown arm %q (want grep, graph or context)", n)
		}
		out = append(out, Arms[i])
	}
	return out, nil
}

// Permissions returns the tools the arm pre-approves and the ones it
// refuses, the code tools named with prefix.
func (a Arm) Permissions(prefix string) (allowed, disallowed []string) {
	allowed = slices.Clone(BaseTools)
	disallowed = slices.Clone(AlwaysDisallowed)
	for _, t := range CodeTools {
		if slices.Contains(a.Tools, t) {
			allowed = append(allowed, prefix+t)
		} else {
			disallowed = append(disallowed, prefix+t)
		}
	}
	return allowed, disallowed
}

// PromptFor is the prompt a session gets: the task's, with the same answer
// instructions in every arm.
func PromptFor(t Task) string {
	if t.Family == FamilyEdit {
		return strings.TrimSpace(t.Prompt) + "\n\nMake the change in this checkout and run the relevant tests. Do not commit. " +
			"End with the list of files you changed."
	}
	return strings.TrimSpace(t.Prompt) + "\n\nDo not modify any file. Answer with the repository-relative path of every file " +
		"involved (path:line where you can) and the exact names of the functions, methods, types or other symbols involved."
}

// AgentRequest is one headless session: Prompt in Dir, under SessionID.
type AgentRequest struct {
	Dir          string
	Prompt       string
	SessionID    string
	Model        string
	MaxBudgetUSD float64
	Allowed      []string
	Disallowed   []string
}

// AgentResult is what the session printed: its final answer and the
// session id it ran under.
type AgentResult struct {
	SessionID string
	Answer    string
	NumTurns  int
	IsError   bool
}

// Agent runs one session.
type Agent interface {
	Run(ctx context.Context, req AgentRequest) (AgentResult, error)
}

// ClaudeCLI runs `claude -p` (the prompt on stdin, JSON output). Env, when
// set, replaces the environment; ExtraArgs (e.g. --plugin-dir) go before
// the arm's flags.
type ClaudeCLI struct {
	Bin       string
	Env       []string
	ExtraArgs []string
}

// Args are the command-line arguments of a session.
func (c ClaudeCLI) Args(req AgentRequest) []string {
	args := []string{"-p", "--output-format", "json", "--permission-mode", "acceptEdits"}
	args = append(args, c.ExtraArgs...)
	if req.SessionID != "" {
		args = append(args, "--session-id", req.SessionID)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", req.MaxBudgetUSD))
	}
	if len(req.Allowed) > 0 {
		args = append(args, "--allowedTools", strings.Join(req.Allowed, ","))
	}
	if len(req.Disallowed) > 0 {
		args = append(args, "--disallowedTools", strings.Join(req.Disallowed, ","))
	}
	return args
}

// Run starts the session and waits for it.
func (c ClaudeCLI) Run(ctx context.Context, req AgentRequest) (AgentResult, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, c.Args(req)...)
	cmd.Dir = req.Dir
	cmd.Stdin = strings.NewReader(req.Prompt)
	if c.Env != nil {
		cmd.Env = c.Env
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	res, perr := ParseClaudeResult(out)
	if perr != nil {
		if err != nil {
			return AgentResult{}, fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return AgentResult{}, perr
	}
	if res.SessionID == "" {
		res.SessionID = req.SessionID
	}
	return res, nil
}

// ParseClaudeResult reads the JSON `claude -p --output-format json` prints.
func ParseClaudeResult(b []byte) (AgentResult, error) {
	var r struct {
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
		NumTurns  int    `json:"num_turns"`
		IsError   bool   `json:"is_error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &r); err != nil {
		return AgentResult{}, fmt.Errorf("claude output: %w", err)
	}
	return AgentResult{SessionID: r.SessionID, Answer: r.Result, NumTurns: r.NumTurns, IsError: r.IsError}, nil
}

// Measures are a session's numbers as clued captured them. Turns are the
// model's turns (distinct assistant messages); ToolCalls the tool uses the
// harness ran, ByTool by tool name (or activity kind when the capture names
// no tool), CodeToolCalls those of the code graph tools.
type Measures struct {
	Turns               int64          `json:"turns"`
	InputTokens         int64          `json:"input_tokens"`
	CacheCreationTokens int64          `json:"cache_creation_tokens"`
	CacheReadTokens     int64          `json:"cache_read_tokens"`
	OutputTokens        int64          `json:"output_tokens"`
	PeakContextTokens   int64          `json:"peak_context_tokens"`
	CostUSD             *float64       `json:"cost_usd,omitempty"`
	ToolCalls           int            `json:"tool_calls"`
	CodeToolCalls       int            `json:"code_tool_calls"`
	ByTool              map[string]int `json:"by_tool,omitempty"`
}

// Tokens is every token the session's turns consumed or produced.
func (m Measures) Tokens() int64 {
	return m.InputTokens + m.CacheCreationTokens + m.CacheReadTokens + m.OutputTokens
}

// Capture reads a session's measures from clued.
type Capture interface {
	Measure(ctx context.Context, sessionID string) (Measures, error)
}

// CluedCLI reads the capture through the mind-palace binary: `session
// usage` (the usage tool) and `session activities` (get_activities).
type CluedCLI struct {
	Bin    string
	Config string
	Env    []string
}

// Measure runs both queries for the session.
func (c CluedCLI) Measure(ctx context.Context, sessionID string) (Measures, error) {
	usage, err := c.run(ctx, "session", "usage", "--session", sessionID)
	if err != nil {
		return Measures{}, err
	}
	m, err := ParseUsage(usage)
	if err != nil {
		return Measures{}, err
	}
	acts, err := c.run(ctx, "session", "activities", "--session", sessionID, "--limit", "500",
		"--kind", "bash", "--kind", "edit", "--kind", "read", "--kind", "task", "--kind", "tool")
	if err != nil {
		return Measures{}, err
	}
	if err := ParseActivities(acts, &m); err != nil {
		return Measures{}, err
	}
	return m, nil
}

func (c CluedCLI) run(ctx context.Context, args ...string) ([]byte, error) {
	bin := c.Bin
	if bin == "" {
		bin = "mind-palace"
	}
	args = append(args, "--compact")
	if c.Config != "" {
		args = append(args, "--config", c.Config)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if c.Env != nil {
		cmd.Env = c.Env
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args[:2], " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// envelope is the CLI's {"ok": …, "result" | "error": …} wrapper.
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func unwrap(b []byte) (json.RawMessage, error) {
	var e envelope
	if err := json.Unmarshal(bytes.TrimSpace(b), &e); err != nil {
		return nil, fmt.Errorf("mind-palace output: %w", err)
	}
	if !e.OK {
		return nil, fmt.Errorf("mind-palace: %s", bytes.TrimSpace(e.Error))
	}
	return e.Result, nil
}

// ParseUsage reads `session usage --session <id>` output into the token
// counters and turns of m.
func ParseUsage(b []byte) (Measures, error) {
	raw, err := unwrap(b)
	if err != nil {
		return Measures{}, err
	}
	var r struct {
		Total struct {
			Messages                 int64    `json:"messages"`
			InputTokens              int64    `json:"input_tokens"`
			CacheCreationInputTokens int64    `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64    `json:"cache_read_input_tokens"`
			OutputTokens             int64    `json:"output_tokens"`
			PeakContextTokens        int64    `json:"peak_context_tokens"`
			CostUSD                  *float64 `json:"cost_usd"`
		} `json:"total"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Measures{}, fmt.Errorf("usage: %w", err)
	}
	t := r.Total
	return Measures{
		Turns: t.Messages, InputTokens: t.InputTokens, CacheCreationTokens: t.CacheCreationInputTokens,
		CacheReadTokens: t.CacheReadInputTokens, OutputTokens: t.OutputTokens,
		PeakContextTokens: t.PeakContextTokens, CostUSD: t.CostUSD,
	}, nil
}

// toolKinds are the activity kinds that are tool uses.
var toolKinds = map[string]bool{"bash": true, "edit": true, "read": true, "task": true, "tool": true}

// ParseActivities counts the tool uses of `session activities` output into
// m (an entry stands for Count consecutive identical uses).
func ParseActivities(b []byte, m *Measures) error {
	raw, err := unwrap(b)
	if err != nil {
		return err
	}
	var acts []struct {
		Kind     string `json:"kind"`
		ToolName string `json:"tool_name"`
		Count    int    `json:"count"`
	}
	if err := json.Unmarshal(raw, &acts); err != nil {
		return fmt.Errorf("activities: %w", err)
	}
	for _, a := range acts {
		if !toolKinds[a.Kind] {
			continue
		}
		n := max(a.Count, 1)
		name := a.ToolName
		if name == "" {
			name = a.Kind
		}
		if m.ByTool == nil {
			m.ByTool = map[string]int{}
		}
		m.ByTool[name] += n
		m.ToolCalls += n
		if isCodeTool(name) {
			m.CodeToolCalls += n
		}
	}
	return nil
}

func isCodeTool(name string) bool {
	for _, t := range CodeTools {
		if name == t || strings.HasSuffix(name, "__"+t) {
			return true
		}
	}
	return false
}

// WaitMeasure polls the capture until the session's numbers are there and
// two readings poll apart agree (the relay and enrichers write after the
// session ends), or timeout passes; it then returns the last reading.
func WaitMeasure(ctx context.Context, c Capture, sessionID string, timeout, poll time.Duration) (Measures, error) {
	deadline := time.Now().Add(timeout)
	var last Measures
	var lastErr error
	seen := false
	for {
		m, err := c.Measure(ctx, sessionID)
		switch {
		case err != nil:
			lastErr = err
		case m.Turns > 0 && seen && sameMeasures(m, last):
			return m, nil
		case m.Turns > 0:
			last, seen, lastErr = m, true, nil
		}
		if !time.Now().Before(deadline) {
			if seen {
				return last, nil
			}
			if lastErr == nil {
				lastErr = errors.New("no usage captured")
			}
			return Measures{}, fmt.Errorf("session %s: %w", sessionID, lastErr)
		}
		select {
		case <-ctx.Done():
			return Measures{}, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func sameMeasures(a, b Measures) bool {
	return a.Turns == b.Turns && a.Tokens() == b.Tokens() && a.ToolCalls == b.ToolCalls
}

// Sources are where checkouts come from: the clued and clued-cloud
// repositories (their committed HEAD) and a cache of corpus clones.
type Sources struct {
	CluedDir string
	CloudDir string
	CacheDir string
	Corpus   Corpus
}

// Checkout puts a clean copy of the task's repository at dest: a shared
// clone at the source's HEAD (clued, clued-cloud) or at the pinned commit
// (corpus), with origin set to the source's remote so the session is
// recorded under the same project.
func (s Sources) Checkout(ctx context.Context, t Task, dest string) error {
	var src, rev, origin string
	switch t.Repo {
	case RepoClued, RepoCloud:
		src = s.CluedDir
		if t.Repo == RepoCloud {
			src = s.CloudDir
		}
		if src == "" {
			return fmt.Errorf("no %s checkout configured", t.Repo)
		}
		head, err := git(ctx, src, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		rev = head
		origin, _ = git(ctx, src, "remote", "get-url", "origin")
	default:
		r, ok := s.Corpus.Repo(t.Repo)
		if !ok {
			return fmt.Errorf("repo %q is not in the corpus", t.Repo)
		}
		cache, err := s.corpusCache(ctx, r)
		if err != nil {
			return err
		}
		src, rev, origin = cache, r.Commit, r.URL
	}
	if _, err := git(ctx, "", "clone", "--quiet", "--shared", "--no-checkout", src, dest); err != nil {
		return err
	}
	if _, err := git(ctx, dest, "checkout", "--quiet", "--detach", rev); err != nil {
		return err
	}
	if origin != "" {
		if _, err := git(ctx, dest, "remote", "set-url", "origin", origin); err != nil {
			return err
		}
	}
	return nil
}

// corpusCache fetches r's pinned commit once into CacheDir/<id>.
func (s Sources) corpusCache(ctx context.Context, r Repo) (string, error) {
	if s.CacheDir == "" {
		return "", errors.New("no corpus cache directory configured")
	}
	dir := filepath.Join(s.CacheDir, r.ID)
	if have, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", r.Commit+"^{commit}"); err == nil && have == r.Commit {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if _, err := git(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		if _, err := git(ctx, dir, "init", "--quiet"); err != nil {
			return "", err
		}
		if _, err := git(ctx, dir, "remote", "add", "origin", r.URL); err != nil {
			return "", err
		}
	}
	if _, err := git(ctx, dir, "fetch", "--quiet", "--depth", "1", "origin", r.Commit); err != nil {
		return "", err
	}
	if _, err := git(ctx, dir, "checkout", "--quiet", "--detach", r.Commit); err != nil {
		return "", err
	}
	return dir, nil
}

// ChangedFiles lists the repository-relative paths git sees as changed or
// new in dir, sorted.
func ChangedFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		p := line[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		files = append(files, strings.Trim(p, `"`))
	}
	slices.Sort(files)
	return files, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// RunCheck runs an edit's check with bash in dir and reports whether it
// exited 0, with its combined output.
func RunCheck(ctx context.Context, dir, check string, timeout time.Duration) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", check)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

// VerifyTruth checks a task's ground truth against a checkout: every truth
// file exists, and every symbol occurs in at least one of them (each part
// of a qualified symbol, for Class.method and the like). It is how the
// truth is kept honest as the repositories move.
func VerifyTruth(dir string, t Task) error {
	var errs []error
	var texts []string
	for _, f := range t.Truth.Files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			if t.Family == FamilyEdit && errors.Is(err, os.ErrNotExist) && strings.HasSuffix(f, "_test.go") {
				continue // an edit may add the test file
			}
			errs = append(errs, fmt.Errorf("task %s: truth file %s: %w", t.ID, f, err))
			continue
		}
		texts = append(texts, string(b))
	}
	for _, s := range t.Truth.Symbols {
		found := false
		for _, text := range texts {
			if mentionsSymbol(text, s) {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("task %s: symbol %s occurs in none of its truth files", t.ID, s))
		}
	}
	return errors.Join(errs...)
}

// Options configure a run of the evaluation.
type Options struct {
	Arms           []Arm
	Runs           int
	Model          string
	MaxBudgetUSD   float64
	ToolPrefix     string
	Agent          Agent
	Capture        Capture
	Sources        Sources
	WorkDir        string
	Keep           bool
	CaptureTimeout time.Duration
	CapturePoll    time.Duration
	CheckTimeout   time.Duration
	// Results, when set, receives each run as one JSON line as soon as it
	// is scored, so an interrupted evaluation keeps what it measured.
	Results io.Writer
	// Log, when set, receives one progress line per run.
	Log io.Writer
	// NewSessionID returns a fresh session id (a random UUID by default).
	NewSessionID func() string
}

// Run is one session of one task in one arm.
type Run struct {
	Task      string        `json:"task"`
	Repo      string        `json:"repo"`
	Lang      string        `json:"lang"`
	Family    string        `json:"family"`
	Arm       string        `json:"arm"`
	Index     int           `json:"index"`
	SessionID string        `json:"session_id"`
	NumTurns  int           `json:"num_turns"`
	Measures  Measures      `json:"measures"`
	Score     Score         `json:"score"`
	Answer    string        `json:"answer,omitempty"`
	Duration  time.Duration `json:"duration_ns"`
	Error     string        `json:"error,omitempty"`
}

func (o Options) withDefaults() Options {
	if len(o.Arms) == 0 {
		o.Arms = Arms
	}
	if o.Runs <= 0 {
		o.Runs = 3
	}
	if o.ToolPrefix == "" {
		o.ToolPrefix = DefaultToolPrefix
	}
	if o.CaptureTimeout <= 0 {
		o.CaptureTimeout = 2 * time.Minute
	}
	if o.CapturePoll <= 0 {
		o.CapturePoll = 5 * time.Second
	}
	if o.CheckTimeout <= 0 {
		o.CheckTimeout = 10 * time.Minute
	}
	if o.NewSessionID == nil {
		o.NewSessionID = newUUID
	}
	return o
}

// Execute runs every task in every arm Runs times, run by run with the arms
// interleaved so that drift (model, server load) spreads over all of them,
// and returns the runs in that order. A run that fails is kept with its
// Error and scored as a failure; only a cancelled context stops early.
func Execute(ctx context.Context, tasks []Task, o Options) ([]Run, error) {
	o = o.withDefaults()
	if o.Agent == nil || o.Capture == nil {
		return nil, errors.New("codesearch: Options.Agent and Options.Capture are required")
	}
	if o.WorkDir == "" {
		return nil, errors.New("codesearch: Options.WorkDir is required")
	}
	var runs []Run
	enc := (*json.Encoder)(nil)
	if o.Results != nil {
		enc = json.NewEncoder(o.Results)
	}
	for i := 1; i <= o.Runs; i++ {
		for _, t := range tasks {
			for _, arm := range o.Arms {
				if err := ctx.Err(); err != nil {
					return runs, err
				}
				r := runOne(ctx, t, arm, i, o)
				runs = append(runs, r)
				if enc != nil {
					if err := enc.Encode(r); err != nil {
						return runs, err
					}
				}
				if o.Log != nil {
					status := "ok"
					if !r.Score.Success {
						status = "fail"
					}
					if r.Error != "" {
						status = "error: " + r.Error
					}
					fmt.Fprintf(o.Log, "%s %s #%d: %s, %d tokens, %d turns, %d tool calls\n",
						t.ID, arm.Name, i, status, r.Measures.Tokens(), r.Measures.Turns, r.Measures.ToolCalls)
				}
			}
		}
	}
	return runs, nil
}

func runOne(ctx context.Context, t Task, arm Arm, index int, o Options) Run {
	r := Run{Task: t.ID, Repo: t.Repo, Lang: t.Lang, Family: t.Family, Arm: arm.Name, Index: index}
	start := time.Now()
	defer func() { r.Duration = time.Since(start) }()
	dir := filepath.Join(o.WorkDir, fmt.Sprintf("%s-%s-%d", t.ID, arm.Name, index))
	_ = os.RemoveAll(dir)
	if !o.Keep {
		defer os.RemoveAll(dir)
	}
	if err := o.Sources.Checkout(ctx, t, dir); err != nil {
		r.Error = "checkout: " + err.Error()
		return r
	}
	allowed, disallowed := arm.Permissions(o.ToolPrefix)
	res, err := o.Agent.Run(ctx, AgentRequest{
		Dir: dir, Prompt: PromptFor(t), SessionID: o.NewSessionID(), Model: o.Model,
		MaxBudgetUSD: o.MaxBudgetUSD, Allowed: allowed, Disallowed: disallowed,
	})
	if err != nil {
		r.Error = "session: " + err.Error()
		return r
	}
	r.SessionID, r.Answer, r.NumTurns = res.SessionID, res.Answer, res.NumTurns
	m, err := WaitMeasure(ctx, o.Capture, res.SessionID, o.CaptureTimeout, o.CapturePoll)
	if err != nil {
		r.Error = "capture: " + err.Error()
	}
	r.Measures = m
	if t.Family == FamilyEdit {
		changed, err := ChangedFiles(ctx, dir)
		if err != nil {
			r.Error = "changes: " + err.Error()
			return r
		}
		passed, _ := RunCheck(ctx, dir, t.Check, o.CheckTimeout)
		r.Score = ScoreEdit(t.Truth, changed, &passed)
	} else {
		r.Score = ScoreAnswer(t.Truth, res.Answer)
	}
	if res.IsError {
		r.Score.Success = false
		if r.Error == "" {
			r.Error = "session ended with an error"
		}
	}
	return r
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
