package codesearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// repoRoot is this checkout's root (the clued repository).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadAll(t *testing.T) (TaskSet, Corpus) {
	t.Helper()
	ts, err := LoadTasks()
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadCorpus()
	if err != nil {
		t.Fatal(err)
	}
	return ts, c
}

func TestEmbeddedTaskSetIsValid(t *testing.T) {
	ts, c := loadAll(t)
	if err := Validate(ts, c); err != nil {
		t.Fatalf("Validate:\n%v", err)
	}
	own, perLang, perFamily := 0, map[string]int{}, map[string]int{}
	for _, task := range ts.Tasks {
		perFamily[task.Family]++
		if task.Repo == RepoClued || task.Repo == RepoCloud {
			own++
		} else {
			perLang[task.Lang]++
		}
	}
	if own < MinOwnTasks || own > 50 {
		t.Errorf("%d tasks on clued and clued-cloud, want about 40", own)
	}
	for _, l := range CorpusLanguages {
		if n := perLang[l]; n < 3 || n > 6 {
			t.Errorf("language %s: %d tasks, want about 3", l, n)
		}
	}
	for _, f := range []string{FamilyNamed, FamilyConcept, FamilyImpact} {
		if perFamily[f] < 20 {
			t.Errorf("family %s: only %d tasks", f, perFamily[f])
		}
	}
	if perFamily[FamilyEdit] < 4 {
		t.Errorf("family edit: only %d tasks", perFamily[FamilyEdit])
	}
	t.Logf("%d tasks (%d on clued and clued-cloud), %d corpus repositories; by family %v", len(ts.Tasks), own, len(c.Repos), perFamily)
}

func TestValidateReportsEveryProblem(t *testing.T) {
	c := Corpus{Repos: []Repo{
		{ID: "good", Lang: "python", URL: "https://github.com/a/b", Branch: "main", Commit: strings.Repeat("a", 40)},
		{ID: "Bad_ID", Lang: "cobol", URL: "http://example.com/x", Branch: "", Commit: "abc"},
		{ID: "unused", Lang: "c", URL: "https://github.com/c/d", Branch: "main", Commit: strings.Repeat("b", 40)},
	}}
	ts := TaskSet{Tasks: []Task{
		{ID: "one", Repo: "good", Lang: "python", Family: FamilyNamed, Prompt: "p", Truth: Truth{Files: []string{"a.py"}}},
		{ID: "one", Repo: "good", Lang: "ruby", Family: "guess", Prompt: " ", Truth: Truth{Files: []string{"../x.py", "/abs"}, Symbols: []string{" s"}, MinRecall: 2}},
		{ID: "edit", Repo: RepoClued, Lang: "go", Family: FamilyEdit, Prompt: "p", Truth: Truth{Files: []string{"x.go"}}},
		{ID: "chk", Repo: RepoClued, Lang: "cobol", Family: FamilyNamed, Prompt: "p", Truth: Truth{}, Check: "true"},
		{ID: "nowhere", Repo: "missing", Lang: "go", Family: FamilyNamed, Prompt: "p", Truth: Truth{Files: []string{"a"}}},
	}}
	err := Validate(ts, c)
	if err == nil {
		t.Fatal("Validate accepted a broken set")
	}
	for _, want := range []string{
		`corpus repo "Bad_ID": id must be`,
		`language "cobol" is not a corpus language`,
		`is not a GitHub https URL`,
		`commit "abc" is not a full 40-character SHA`,
		`corpus repo "Bad_ID": no branch`,
		`task one: duplicate id`,
		`family "guess" is not one of`,
		`task one: empty prompt`,
		`language "ruby" differs from repo good's "python"`,
		`truth file "../x.py" is not a clean repository-relative path`,
		`truth file "/abs" is not a clean`,
		`truth symbol " s" is blank or padded`,
		`min_recall 2 is outside [0, 1]`,
		`task edit: an edit needs a check command`,
		`task chk: only edits have a check command`,
		`task chk: ground truth names no file`,
		`task chk: language "cobol" is not one of go, markdown, shell`,
		`repo "missing" is not clued, clued-cloud or in the corpus`,
		`only 2 tasks on clued and clued-cloud`,
		`no concept task on clued or clued-cloud`,
		`language lua has 0 tasks`,
		`corpus repo "unused" has no task`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing problem %q in:\n%v", want, err)
		}
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	if _, err := ParseTasks([]byte("tasks:\n  - id: a\n    answer: nope\n")); err == nil || !strings.Contains(err.Error(), "answer") {
		t.Errorf("ParseTasks = %v, want an unknown-field error", err)
	}
	if _, err := ParseCorpus([]byte("repos:\n  - id: a\n    sha: nope\n")); err == nil || !strings.Contains(err.Error(), "sha") {
		t.Errorf("ParseCorpus = %v, want an unknown-field error", err)
	}
}

// TestCluedGroundTruth keeps the clued tasks honest: every truth file exists
// in this checkout and every truth symbol occurs in one of them. A rename
// that breaks it should move the task's ground truth along.
func TestCluedGroundTruth(t *testing.T) {
	ts, _ := loadAll(t)
	root := repoRoot(t)
	n := 0
	for _, task := range ts.Tasks {
		if task.Repo != RepoClued {
			continue
		}
		n++
		if err := VerifyTruth(root, task); err != nil {
			t.Error(err)
		}
	}
	if n == 0 {
		t.Fatal("no clued tasks")
	}
}

// TestCloudGroundTruth does the same for a clued-cloud checkout named by
// CLUED_EVAL_CLOUD_DIR.
func TestCloudGroundTruth(t *testing.T) {
	dir := os.Getenv("CLUED_EVAL_CLOUD_DIR")
	if dir == "" {
		t.Skip("set CLUED_EVAL_CLOUD_DIR to a clued-cloud checkout to check its ground truth")
	}
	ts, _ := loadAll(t)
	for _, task := range ts.Tasks {
		if task.Repo == RepoCloud {
			if err := VerifyTruth(dir, task); err != nil {
				t.Error(err)
			}
		}
	}
}

// TestCorpusGroundTruth fetches every pinned corpus commit and checks the
// ground truth of its tasks there. It needs the network, so it runs only
// with CLUED_EVAL_VERIFY=1 (scripts/eval-codesearch.sh --verify-only);
// CLUED_EVAL_CACHE keeps the fetched repositories between runs.
func TestCorpusGroundTruth(t *testing.T) {
	if os.Getenv("CLUED_EVAL_VERIFY") != "1" {
		t.Skip("set CLUED_EVAL_VERIFY=1 to fetch the pinned corpus and check its ground truth")
	}
	requireGit(t)
	ts, c := loadAll(t)
	cache := os.Getenv("CLUED_EVAL_CACHE")
	if cache == "" {
		cache = t.TempDir()
	}
	src := Sources{CacheDir: cache, Corpus: c}
	ctx := context.Background()
	for _, r := range c.Repos {
		dir, err := src.corpusCache(ctx, r)
		if err != nil {
			t.Errorf("%s: %v", r.ID, err)
			continue
		}
		head, err := git(ctx, dir, "rev-parse", "HEAD")
		if err != nil || head != r.Commit {
			t.Errorf("%s: checked out %q (%v), want %s", r.ID, head, err, r.Commit)
			continue
		}
		for _, task := range ts.Tasks {
			if task.Repo == r.ID {
				if err := VerifyTruth(dir, task); err != nil {
					t.Error(err)
				}
			}
		}
	}
}

func TestMentionsFile(t *testing.T) {
	for _, tc := range []struct {
		text, file string
		want       bool
	}{
		{"see mind-palace/service/documents.go:310", "mind-palace/service/documents.go", true},
		{"`service/documents.go` line 80", "mind-palace/service/documents.go", true},
		{"/tmp/x/mind-palace/service/documents.go", "mind-palace/service/documents.go", true},
		{"documents.go defines it", "mind-palace/service/documents.go", false},
		{"in mind-palace/service/documents.go.bak", "mind-palace/service/documents.go", true}, // "." after is punctuation
		{"in service/documents_test.go", "mind-palace/service/documents.go", false},
		{"cJSON.c:1227", "cJSON.c", true},
		{"cJSON_Utils.c", "cJSON.c", false},
		{"src/value/de.rs", "src/de.rs", false},
		{"src/de.rs and src/value/de.rs", "src/value/de.rs", true},
		{"lib/sinatra/base.rb#L1064", "lib/sinatra/base.rb", true},
		{"hooks/session-start", "hooks/session-start", true},
		{"hooks/session-starter", "hooks/session-start", false},
	} {
		if got := mentionsFile(tc.text, tc.file); got != tc.want {
			t.Errorf("mentionsFile(%q, %q) = %v, want %v", tc.text, tc.file, got, tc.want)
		}
	}
}

func TestMentionsSymbol(t *testing.T) {
	for _, tc := range []struct {
		text, sym string
		want      bool
	}{
		{"`allocateID` in documents.go", "allocateID", true},
		{"allocateIDs", "allocateID", false},
		{"s.allocateID(ctx)", "allocateID", true},
		{"DBImpl::Write calls BuildBatchGroup", "DBImpl::Write", true},
		{"DBImpl.Write", "DBImpl::Write", true},
		{"the Write method of DBImpl", "DBImpl::Write", true},
		{"WriteBatch only", "DBImpl::Write", false},
		{"function Picker:new(opts)", "Picker:new", true},
		{"pickers.new builds a Picker via Picker.new", "Picker:new", true},
		{"(defun -reduce (fn list)", "-reduce", true},
		{"only --reduce and -reduce-from", "-reduce", false},
		{"--reduce-from", "--reduce", false},
		{"route! dispatches", "route!", true},
		{"(defvar *dispatch-table* ...)", "*dispatch-table*", true},
		{"wrap-params-request", "wrap-params", false},
		{"Mojo::UserAgent::_dequeue", "_dequeue", true},
		{"health check at /health", "health", true},
	} {
		if got := mentionsSymbol(tc.text, tc.sym); got != tc.want {
			t.Errorf("mentionsSymbol(%q, %q) = %v, want %v", tc.text, tc.sym, got, tc.want)
		}
	}
}

func TestScoreAnswer(t *testing.T) {
	truth := Truth{Files: []string{"mind-palace/service/documents.go", "mind-palace/model/model.go"}, Symbols: []string{"allocateID", "SequenceOf"}}
	s := ScoreAnswer(truth, "allocateID (mind-palace/service/documents.go:310) uses model.SequenceOf in model/model.go:634.")
	if !s.Success || s.FileRecall != 1 || s.SymbolRecall != 1 || s.MissingFiles != nil || s.MissingSymbols != nil {
		t.Errorf("full answer: %+v", s)
	}
	s = ScoreAnswer(truth, "It is allocateID in service/documents.go.")
	if s.Success || s.FileRecall != 0.5 || s.SymbolRecall != 0.5 ||
		!slices.Equal(s.MissingFiles, []string{"mind-palace/model/model.go"}) || !slices.Equal(s.MissingSymbols, []string{"SequenceOf"}) {
		t.Errorf("half answer: %+v", s)
	}
	truth.MinRecall = 0.5
	if s := ScoreAnswer(truth, "It is allocateID in service/documents.go."); !s.Success {
		t.Errorf("half answer at min_recall 0.5: %+v", s)
	}
	if s := ScoreAnswer(Truth{Files: []string{"docs/server.md"}}, "docs/server.md, section Device login"); !s.Success || s.SymbolRecall != 1 {
		t.Errorf("files only: %+v", s)
	}
	two := Truth{Files: []string{"a/b.go", "a/c.go", "a/d.go"}, MinRecall: 0.66}
	if s := ScoreAnswer(two, "a/b.go and a/c.go"); !s.Success {
		t.Errorf("2 of 3 at min_recall 0.66 (rounded): %+v", s)
	}
}

func TestScoreEdit(t *testing.T) {
	truth := Truth{Files: []string{"mind-palace/hookguard/stop.go", "mind-palace/hookguard/stop_test.go"}}
	yes, no := true, false
	s := ScoreEdit(truth, []string{"mind-palace/hookguard/stop.go", "mind-palace/hookguard/stop_test.go", "notes.txt"}, &yes)
	if !s.Success || s.FileRecall != 1 || *s.CheckPassed != true {
		t.Errorf("complete edit: %+v", s)
	}
	if s := ScoreEdit(truth, []string{"mind-palace/hookguard/stop.go", "mind-palace/hookguard/stop_test.go"}, &no); s.Success {
		t.Errorf("failing check scored a success: %+v", s)
	}
	s = ScoreEdit(truth, []string{"mind-palace/hookguard/stop.go"}, &yes)
	if s.Success || s.FileRecall != 0.5 || !slices.Equal(s.MissingFiles, []string{"mind-palace/hookguard/stop_test.go"}) {
		t.Errorf("edit without its test: %+v", s)
	}
}

func TestMedian(t *testing.T) {
	for _, tc := range []struct {
		in   []float64
		want float64
	}{{nil, 0}, {[]float64{3}, 3}, {[]float64{5, 1, 3}, 3}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if got := Median(tc.in); got != tc.want {
			t.Errorf("Median(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func run(task, family, lang, arm string, tokens int64, turns int64, ok bool) Run {
	cost := float64(tokens) / 1e6
	return Run{Task: task, Family: family, Lang: lang, Arm: arm, Score: Score{Success: ok},
		Measures: Measures{InputTokens: tokens, Turns: turns, ToolCalls: int(turns), CostUSD: &cost}}
}

func TestSummarize(t *testing.T) {
	runs := []Run{
		// Task a (named, go): grep runs 100, 300, 200 → 200; graph 50, 70, 60 → 60.
		run("a", FamilyNamed, "go", "grep", 100, 4, true),
		run("a", FamilyNamed, "go", "graph", 50, 2, true),
		run("a", FamilyNamed, "go", "grep", 300, 6, false),
		run("a", FamilyNamed, "go", "graph", 70, 2, true),
		run("a", FamilyNamed, "go", "grep", 200, 5, true),
		run("a", FamilyNamed, "go", "graph", 60, 3, true),
		// Task b (named, python): grep 1000 x3; graph 400 x3 (one failed).
		run("b", FamilyNamed, "python", "grep", 1000, 10, true),
		run("b", FamilyNamed, "python", "graph", 400, 4, false),
		run("b", FamilyNamed, "python", "grep", 1000, 10, true),
		run("b", FamilyNamed, "python", "graph", 400, 4, true),
		run("b", FamilyNamed, "python", "grep", 1000, 10, true),
		run("b", FamilyNamed, "python", "graph", 400, 4, true),
	}
	runs[0].Error = "capture: slow"
	rep := Summarize(runs)
	if !slices.Equal(rep.Arms, []string{"grep", "graph"}) {
		t.Errorf("arms = %v", rep.Arms)
	}
	stat := func(stats []Stat, group, arm string) Stat {
		for _, s := range stats {
			if s.Group == group && s.Arm == arm {
				return s
			}
		}
		t.Fatalf("no stat %s/%s in %+v", group, arm, stats)
		return Stat{}
	}
	g := stat(rep.ByFamily, FamilyNamed, "grep")
	if g.Tasks != 2 || g.Runs != 6 || g.Errors != 1 || g.MedianTokens != 600 || g.MedianTurns != 7.5 || g.SuccessRate != 5.0/6 {
		t.Errorf("named/grep = %+v", g)
	}
	gr := stat(rep.ByFamily, FamilyNamed, "graph")
	if gr.MedianTokens != 230 || gr.SuccessRate != 5.0/6 {
		t.Errorf("named/graph = %+v", gr)
	}
	if s := stat(rep.ByLang, "go", "graph"); s.MedianTokens != 60 || s.Tasks != 1 || s.Runs != 3 {
		t.Errorf("go/graph = %+v", s)
	}
	if s := stat(rep.Overall, "all", "grep"); s.Runs != 6 {
		t.Errorf("overall/grep = %+v", s)
	}
	md := rep.Markdown()
	for _, want := range []string{"### By family", "### By language", "| named | graph | 2 | 6 | 83% | 230 | -62% |", "| named | grep | 2 | 6 (1 errors) | 83% | 600 | - | 7.5 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown lacks %q:\n%s", want, md)
		}
	}
}

func TestParseUsageAndActivities(t *testing.T) {
	m, err := ParseUsage([]byte(`{"ok":true,"result":{"group_by":"session","groups":[],"total":{"messages":7,"input_tokens":10,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000,"output_tokens":40,"peak_context_tokens":3210,"cost_usd":0.25},"unpriced_models":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Turns != 7 || m.Tokens() != 3250 || m.PeakContextTokens != 3210 || m.CostUSD == nil || *m.CostUSD != 0.25 {
		t.Errorf("usage = %+v", m)
	}
	acts := `{"ok":true,"result":[
		{"kind":"read","tool_name":"Read","count":3},
		{"kind":"tool","tool_name":"Grep","count":1},
		{"kind":"tool","tool_name":"mcp__plugin_clued_clued__code_search","count":2},
		{"kind":"bash","count":1},
		{"kind":"state","label":"working","count":1},
		{"kind":"response","count":1}]}`
	if err := ParseActivities([]byte(acts), &m); err != nil {
		t.Fatal(err)
	}
	if m.ToolCalls != 7 || m.CodeToolCalls != 2 || m.ByTool["Read"] != 3 || m.ByTool["bash"] != 1 || m.ByTool["mcp__plugin_clued_clued__code_search"] != 2 {
		t.Errorf("activities = %+v", m)
	}
	if _, err := ParseUsage([]byte(`{"ok":false,"error":{"kind":"not_found","message":"no session"}}`)); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Errorf("error envelope: %v", err)
	}
}

func TestClaudeCLIArgs(t *testing.T) {
	allowed, disallowed := Arms[2].Permissions(DefaultToolPrefix)
	args := ClaudeCLI{ExtraArgs: []string{"--plugin-dir", "/p"}}.Args(AgentRequest{
		SessionID: "s-1", Model: "opus", MaxBudgetUSD: 1.5, Allowed: allowed, Disallowed: disallowed,
	})
	got := strings.Join(args, " ")
	for _, want := range []string{
		"-p --output-format json --permission-mode acceptEdits --plugin-dir /p",
		"--session-id s-1", "--model opus", "--max-budget-usd 1.50",
		"--allowedTools Read,Grep,Glob,Bash,Edit,MultiEdit,Write,mcp__plugin_clued_clued__code_context",
		"--disallowedTools Task,Agent,WebSearch,WebFetch,mcp__plugin_clued_clued__code_search,mcp__plugin_clued_clued__code_symbol,mcp__plugin_clued_clued__code_neighbors,mcp__plugin_clued_clued__code_outline",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q lack %q", got, want)
		}
	}
	res, err := ParseClaudeResult([]byte(`{"type":"result","subtype":"success","is_error":false,"num_turns":4,"result":"done","session_id":"s-1","total_cost_usd":0.1}`))
	if err != nil || res.Answer != "done" || res.SessionID != "s-1" || res.NumTurns != 4 || res.IsError {
		t.Errorf("ParseClaudeResult = %+v, %v", res, err)
	}
	if _, err := ParseClaudeResult([]byte("Error: not logged in")); err == nil {
		t.Error("ParseClaudeResult accepted non-JSON output")
	}
}

func TestArms(t *testing.T) {
	allowed, disallowed := Arms[0].Permissions("p__")
	for _, tool := range CodeTools {
		if slices.Contains(allowed, "p__"+tool) || !slices.Contains(disallowed, "p__"+tool) {
			t.Errorf("grep arm: %s allowed=%v disallowed=%v", tool, allowed, disallowed)
		}
	}
	allowed, disallowed = Arms[1].Permissions("p__")
	for _, tool := range CodeTools {
		if !slices.Contains(allowed, "p__"+tool) || slices.Contains(disallowed, "p__"+tool) {
			t.Errorf("graph arm: %s allowed=%v disallowed=%v", tool, allowed, disallowed)
		}
	}
	if arms, err := ArmsNamed([]string{"context", "grep"}); err != nil || arms[0].Name != "context" || arms[1].Name != "grep" {
		t.Errorf("ArmsNamed = %v, %v", arms, err)
	}
	if _, err := ArmsNamed([]string{"vector"}); err == nil {
		t.Error("ArmsNamed accepted an unknown arm")
	}
	edit := PromptFor(Task{Family: FamilyEdit, Prompt: "Do X."})
	ask := PromptFor(Task{Family: FamilyNamed, Prompt: "Where is X?"})
	if !strings.HasPrefix(edit, "Do X.\n\n") || !strings.Contains(edit, "Do not commit") ||
		!strings.HasPrefix(ask, "Where is X?\n\n") || !strings.Contains(ask, "Do not modify any file") {
		t.Errorf("prompts:\n%s\n%s", edit, ask)
	}
	sel := Select([]Task{{ID: "clued-named-a"}, {ID: "py-named-b"}, {ID: "clued-edit-c"}}, regexp.MustCompile(`^clued-`))
	if len(sel) != 2 || sel[1].ID != "clued-edit-c" {
		t.Errorf("Select = %v", sel)
	}
}

type fakeCapture struct {
	mu    sync.Mutex
	calls map[string]int
	// readings per session: the n-th Measure call returns readings[min(n, len-1)].
	readings []Measures
	err      error
}

func (f *fakeCapture) Measure(_ context.Context, id string) (Measures, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	n := f.calls[id]
	f.calls[id]++
	if f.err != nil {
		return Measures{}, f.err
	}
	return f.readings[min(n, len(f.readings)-1)], nil
}

func TestWaitMeasure(t *testing.T) {
	ctx := context.Background()
	c := &fakeCapture{readings: []Measures{{}, {Turns: 2, OutputTokens: 5}, {Turns: 3, OutputTokens: 9}, {Turns: 3, OutputTokens: 9}}}
	m, err := WaitMeasure(ctx, c, "s", time.Second, time.Millisecond)
	if err != nil || m.Turns != 3 || m.OutputTokens != 9 || c.calls["s"] != 4 {
		t.Errorf("WaitMeasure = %+v, %v after %d calls", m, err, c.calls["s"])
	}
	_, err = WaitMeasure(ctx, &fakeCapture{readings: []Measures{{}}}, "s", 5*time.Millisecond, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no usage captured") {
		t.Errorf("empty capture: %v", err)
	}
	_, err = WaitMeasure(ctx, &fakeCapture{err: errors.New("server down")}, "s", 5*time.Millisecond, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "server down") {
		t.Errorf("failing capture: %v", err)
	}
}

// fakeAgent answers lookups with a fixed text and performs edits by writing
// files, recording each request.
type fakeAgent struct {
	mu      sync.Mutex
	reqs    []AgentRequest
	answer  string
	writes  map[string]string
	origins []string
}

func (f *fakeAgent) Run(ctx context.Context, req AgentRequest) (AgentResult, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	origin, _ := git(ctx, req.Dir, "remote", "get-url", "origin")
	f.mu.Lock()
	f.origins = append(f.origins, origin)
	f.mu.Unlock()
	if strings.Contains(req.Prompt, "Do not commit") {
		for p, body := range f.writes {
			if err := os.WriteFile(filepath.Join(req.Dir, p), []byte(body), 0o644); err != nil {
				return AgentResult{}, err
			}
		}
		return AgentResult{SessionID: req.SessionID, Answer: "edited", NumTurns: 3}, nil
	}
	return AgentResult{SessionID: req.SessionID, Answer: f.answer, NumTurns: 2}, nil
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "-m", "init"},
		{"remote", "add", "origin", "https://github.com/robbiebyrd/clued"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestExecute(t *testing.T) {
	requireGit(t)
	src := gitRepo(t, map[string]string{"pkg/a.go": "package pkg\n\nfunc Alpha() {}\n", "pkg/a_test.go": "package pkg\n"})
	// An uncommitted change in the source must not reach the checkouts.
	if err := os.WriteFile(filepath.Join(src, "pkg/a.go"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks := []Task{
		{ID: "look", Repo: RepoClued, Lang: "go", Family: FamilyNamed, Prompt: "Where is Alpha?",
			Truth: Truth{Files: []string{"pkg/a.go"}, Symbols: []string{"Alpha"}}},
		{ID: "change", Repo: RepoClued, Lang: "go", Family: FamilyEdit, Prompt: "Add Beta.",
			Truth: Truth{Files: []string{"pkg/a.go", "pkg/a_test.go"}}, Check: "grep -q Beta pkg/a.go && grep -q Beta pkg/a_test.go"},
		{ID: "cloud", Repo: RepoCloud, Lang: "go", Family: FamilyNamed, Prompt: "Where?", Truth: Truth{Files: []string{"x.go"}}},
	}
	agent := &fakeAgent{
		answer: "Alpha is in pkg/a.go:3.",
		writes: map[string]string{"pkg/a.go": "package pkg\n\nfunc Alpha() {}\nfunc Beta() {}\n", "pkg/a_test.go": "package pkg\n// Beta\n"},
	}
	capture := &fakeCapture{readings: []Measures{{Turns: 4, InputTokens: 100, ToolCalls: 3}}}
	var results, log bytes.Buffer
	ids := 0
	runs, err := Execute(context.Background(), tasks, Options{
		Arms: []Arm{Arms[0], Arms[2]}, Runs: 2, Agent: agent, Capture: capture,
		Sources: Sources{CluedDir: src}, WorkDir: t.TempDir(),
		CapturePoll: time.Millisecond, CaptureTimeout: time.Second,
		Results: &results, Log: &log,
		NewSessionID: func() string { ids++; return "sess-" + strconv.Itoa(ids) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 12 {
		t.Fatalf("%d runs, want 3 tasks x 2 arms x 2 runs", len(runs))
	}
	// Run by run, task by task, arms interleaved.
	var order []string
	for _, r := range runs[:6] {
		order = append(order, fmt.Sprintf("%s/%s/%d", r.Task, r.Arm, r.Index))
	}
	if want := []string{"look/grep/1", "look/context/1", "change/grep/1", "change/context/1", "cloud/grep/1", "cloud/context/1"}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	for _, r := range runs {
		switch r.Task {
		case "look":
			if !r.Score.Success || r.Measures.Turns != 4 || r.Error != "" || r.NumTurns != 2 || !strings.HasPrefix(r.SessionID, "sess-") {
				t.Errorf("look run: %+v", r)
			}
		case "change":
			if !r.Score.Success || r.Score.CheckPassed == nil || !*r.Score.CheckPassed || !slices.Equal(r.Score.Changed, []string{"pkg/a.go", "pkg/a_test.go"}) {
				t.Errorf("change run: %+v", r)
			}
		case "cloud":
			if r.Score.Success || !strings.Contains(r.Error, "no clued-cloud checkout configured") {
				t.Errorf("cloud run without a checkout: %+v", r)
			}
		}
	}
	if len(agent.reqs) != 8 {
		t.Fatalf("%d sessions, want 8", len(agent.reqs))
	}
	for i, req := range agent.reqs {
		if agent.origins[i] != "https://github.com/robbiebyrd/clued" {
			t.Errorf("session %d ran with origin %q", i, agent.origins[i])
		}
		if req.Prompt == "" || req.SessionID == "" || len(req.Allowed) == 0 {
			t.Errorf("session %d request: %+v", i, req)
		}
	}
	// The grep arm refuses every code tool; the context arm allows code_context.
	if slices.Contains(agent.reqs[0].Allowed, DefaultToolPrefix+"code_context") || !slices.Contains(agent.reqs[1].Allowed, DefaultToolPrefix+"code_context") {
		t.Errorf("arm permissions: %v / %v", agent.reqs[0].Allowed, agent.reqs[1].Allowed)
	}
	// One JSON line per run, as scored.
	lines := strings.Split(strings.TrimSpace(results.String()), "\n")
	if len(lines) != 12 {
		t.Fatalf("%d result lines", len(lines))
	}
	var first Run
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Task != "look" || first.Measures.InputTokens != 100 {
		t.Errorf("first line %s: %+v %v", lines[0], first, err)
	}
	if !strings.Contains(log.String(), "change grep #1: ok, 100 tokens, 4 turns, 3 tool calls") {
		t.Errorf("log:\n%s", log.String())
	}
	// Checkouts are removed unless Keep.
	if b, err := os.ReadFile(filepath.Join(src, "pkg/a.go")); err != nil || string(b) != "dirty" {
		t.Errorf("source changed: %q %v", b, err)
	}
}

func TestExecuteNeedsAgentAndCapture(t *testing.T) {
	if _, err := Execute(context.Background(), nil, Options{WorkDir: t.TempDir()}); err == nil {
		t.Error("Execute ran without an agent")
	}
	if _, err := Execute(context.Background(), nil, Options{Agent: &fakeAgent{}, Capture: &fakeCapture{}}); err == nil {
		t.Error("Execute ran without a work directory")
	}
}

func TestVerifyTruth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("func (d *DBImpl) Write() {}\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := Task{ID: "ok", Truth: Truth{Files: []string{"a.go"}, Symbols: []string{"Alpha", "DBImpl::Write"}}}
	if err := VerifyTruth(dir, ok); err != nil {
		t.Errorf("VerifyTruth: %v", err)
	}
	bad := Task{ID: "bad", Truth: Truth{Files: []string{"a.go", "gone.go"}, Symbols: []string{"Beta"}}}
	err := VerifyTruth(dir, bad)
	if err == nil || !strings.Contains(err.Error(), "truth file gone.go") || !strings.Contains(err.Error(), "symbol Beta occurs in none") {
		t.Errorf("VerifyTruth(bad) = %v", err)
	}
	edit := Task{ID: "edit", Family: FamilyEdit, Truth: Truth{Files: []string{"a.go", "a_test.go"}}}
	if err := VerifyTruth(dir, edit); err != nil {
		t.Errorf("an edit may add its test file: %v", err)
	}
}

// TestEvalLive is the evaluation itself: real `claude -p` sessions (paid)
// over the task set in each arm, measured from clued's capture. It runs only
// with CLUED_EVAL=1; scripts/eval-codesearch.sh sets it with the rest of the
// CLUED_EVAL_* settings and is the way to run it.
func TestEvalLive(t *testing.T) {
	if os.Getenv("CLUED_EVAL") != "1" {
		t.Skip("set CLUED_EVAL=1 (scripts/eval-codesearch.sh) to run the evaluation with real claude -p sessions")
	}
	requireGit(t)
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	ts, c := loadAll(t)
	if err := Validate(ts, c); err != nil {
		t.Fatal(err)
	}
	tasks := ts.Tasks
	if re := os.Getenv("CLUED_EVAL_TASKS"); re != "" {
		tasks = Select(tasks, regexp.MustCompile(re))
	}
	cloudDir := os.Getenv("CLUED_EVAL_CLOUD_DIR")
	if cloudDir == "" {
		tasks = slices.DeleteFunc(slices.Clone(tasks), func(task Task) bool { return task.Repo == RepoCloud })
		t.Log("CLUED_EVAL_CLOUD_DIR is not set: clued-cloud tasks skipped")
	}
	if len(tasks) == 0 {
		t.Fatal("no task selected")
	}
	arms, err := ArmsNamed(strings.Split(env("CLUED_EVAL_ARMS", "grep,graph,context"), ","))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := strconv.Atoi(env("CLUED_EVAL_RUNS", "3"))
	if err != nil {
		t.Fatalf("CLUED_EVAL_RUNS: %v", err)
	}
	budget, err := strconv.ParseFloat(env("CLUED_EVAL_BUDGET_USD", "2"), 64)
	if err != nil {
		t.Fatalf("CLUED_EVAL_BUDGET_USD: %v", err)
	}
	work := os.Getenv("CLUED_EVAL_WORK")
	if work == "" {
		work = t.TempDir()
	}
	out := env("CLUED_EVAL_OUT", work)
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := os.Getenv("CLUED_EVAL_CACHE")
	if cache == "" {
		cache = filepath.Join(work, "corpus")
	}
	var extra []string
	if dir := os.Getenv("CLUED_EVAL_PLUGIN_DIR"); dir != "" {
		extra = append(extra, "--plugin-dir", dir)
	}
	results, err := os.Create(filepath.Join(out, "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer results.Close()
	all, err := Execute(context.Background(), tasks, Options{
		Arms: arms, Runs: runs, Model: os.Getenv("CLUED_EVAL_MODEL"), MaxBudgetUSD: budget,
		ToolPrefix: env("CLUED_EVAL_TOOL_PREFIX", DefaultToolPrefix),
		Agent:      ClaudeCLI{Bin: env("CLUED_EVAL_CLAUDE", "claude"), ExtraArgs: extra},
		Capture:    CluedCLI{Bin: env("CLUED_EVAL_MIND_PALACE", "mind-palace"), Config: os.Getenv("CLUED_EVAL_CONFIG")},
		Sources:    Sources{CluedDir: env("CLUED_EVAL_CLUED_DIR", repoRoot(t)), CloudDir: cloudDir, CacheDir: cache, Corpus: c},
		WorkDir:    filepath.Join(work, "runs"), Keep: os.Getenv("CLUED_EVAL_KEEP") == "1",
		Results: results, Log: testLog{t},
	})
	rep := Summarize(all)
	md := rep.Markdown()
	if werr := os.WriteFile(filepath.Join(out, "report.md"), []byte(md), 0o644); werr != nil {
		t.Error(werr)
	}
	if b, jerr := json.MarshalIndent(rep, "", "  "); jerr == nil {
		if werr := os.WriteFile(filepath.Join(out, "report.json"), b, 0o644); werr != nil {
			t.Error(werr)
		}
	}
	t.Logf("results in %s\n%s", out, md)
	if err != nil {
		t.Fatal(err)
	}
}

// testLog writes progress lines to the test log.
type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
