package digest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

const account = "acc"

var day1 = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

// seed stores two sessions on 2026-10-13 and one on 2026-10-14 with
// labelled activities.
func seed(t *testing.T) session.Store {
	t.Helper()
	ctx := context.Background()
	st := memsession.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: account, ProjectPath: "/work/alpha", GitOrigin: "git@x:alpha.git", LastSeen: day1.Add(2 * time.Hour)}))
	must(st.UpsertSession(ctx, session.Session{SessionID: "s2", AccountID: account, ProjectPath: "/work/beta", LastSeen: day1.Add(5 * time.Hour)}))
	must(st.UpsertSession(ctx, session.Session{SessionID: "s3", AccountID: account, ProjectPath: "/work/alpha", LastSeen: day1.AddDate(0, 0, 1).Add(time.Hour)}))
	must(st.SetSessionMeta(ctx, account, "s1", session.Doc{"title": "Fix the daemon"}))
	_, _, err := st.SetModel(ctx, account, "s1", "claude-fable-5-1", day1)
	must(err)
	label := func(sid string, seq int, at time.Time, activity session.Doc) {
		must(st.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: sid, Seq: seq, AccountID: account, Line: map[string]any{"type": "x", "timestamp": at.Format(time.RFC3339Nano)}}}))
		pending, err := st.Unenriched(ctx, "transcript_lines", "activity-kind", 0)
		must(err)
		for _, d := range pending {
			if d["session_id"] == sid && d["seq"] == seq {
				must(st.SetEnrichment(ctx, "transcript_lines", d["_id"], "activity-kind", activity))
			}
		}
	}
	label("s1", 0, day1, session.Doc{"kind": "user", "label": "user", "detail": "Fix the daemon restart"})
	label("s1", 1, day1.Add(time.Minute), session.Doc{"kind": "thinking", "label": "thinking", "detail": "Let me look"})
	label("s1", 2, day1.Add(2*time.Minute), session.Doc{"kind": "read", "label": "Read", "detail": "daemon.go", "tool_name": "Read", "tool_use_id": "t1"})
	label("s1", 3, day1.Add(3*time.Minute), session.Doc{"kind": "bash", "label": "Bash", "detail": "go test ./...", "tool_name": "Bash", "tool_use_id": "t2"})
	label("s1", 4, day1.Add(4*time.Minute), session.Doc{"kind": "bash", "label": "Bash", "detail": "go test ./...", "tool_name": "Bash", "tool_use_id": "t3"})
	label("s1", 5, day1.Add(5*time.Minute), session.Doc{"kind": "task", "label": "Task", "detail": "Review the diff", "tool_name": "Task", "tool_use_id": "t4"})
	label("s1", 6, day1.Add(6*time.Minute), session.Doc{"kind": "result", "label": "result", "detail": "Looks fine, one nit.", "tool_use_id": "t4"})
	label("s1", 7, day1.Add(7*time.Minute), session.Doc{"kind": "edit", "label": "Edit", "detail": "daemon.go", "tool_name": "Edit", "tool_use_id": "t5"})
	label("s1", 8, day1.Add(8*time.Minute), session.Doc{"kind": "commit", "label": "Bash", "detail": "Restart the daemon cleanly", "tool_name": "Bash", "tool_use_id": "t6"})
	label("s1", 9, day1.Add(9*time.Minute), session.Doc{"kind": "response", "label": "response", "detail": strings.Repeat("Done. ", 40)})
	label("s2", 0, day1.Add(4*time.Hour), session.Doc{"kind": "user", "label": "user", "detail": "Write the docs"})
	label("s3", 0, day1.AddDate(0, 0, 1), session.Doc{"kind": "user", "label": "user", "detail": "Next day"})
	return st
}

func newService(st session.Store, now time.Time) *Service {
	q := &query.Service{Store: st, AccountID: account}
	return &Service{Store: st, AccountID: account, Query: q, Now: func() time.Time { return now }}
}

func TestBuildRendersTheReport(t *testing.T) {
	st := seed(t)
	svc := newService(st, day1.AddDate(0, 0, 3))
	p, _ := Day("2026-10-13")
	rep, err := Build(context.Background(), svc.Query, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Sessions) != 2 || rep.Sessions[0].SessionID != "s1" || rep.Sessions[1].SessionID != "s2" {
		t.Fatalf("sessions = %+v", rep.Sessions)
	}
	s1 := rep.Sessions[0]
	if s1.Title != "Fix the daemon" || s1.Model != "claude-fable-5-1" || !s1.Start.Equal(day1) || !s1.End.Equal(day1.Add(9*time.Minute)) {
		t.Errorf("s1 = %+v", s1)
	}
	kinds := []string{}
	for _, r := range s1.Rows {
		kinds = append(kinds, r.Kind)
	}
	if strings.Join(kinds, ",") != "user,thinking,bash,task,edit,response" {
		t.Errorf("default kinds = %v", kinds)
	}
	if s1.Rows[2].Count != 2 || s1.Rows[3].Returned != "Looks fine, one nit." {
		t.Errorf("rows = %+v", s1.Rows)
	}
	md := rep.Markdown()
	for _, want := range []string{
		"# Activity report 2026-10-13",
		"## Session s1",
		"title: Fix the daemon · project: /work/alpha · repository: git@x:alpha.git · model: claude-fable-5-1 · from 2026-10-13 09:00 to 09:09 UTC",
		"[09:00] 👤 user: Fix the daemon restart",
		"[09:04] $ Bash: go test ./... ×2",
		"[09:05] 🧵 Task: Review the diff\n    ↳ returned: Looks fine, one nit.",
		"[09:07] ✏ Edit: daemon.go",
		"## Session s2",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Read") || strings.Contains(md, "Restart the daemon cleanly") {
		t.Error("reads and commits are not shown by default")
	}
	// The long reply is cut at 120 characters.
	for _, line := range strings.Split(md, "\n") {
		if strings.Contains(line, "response:") && len([]rune(line)) > 120+len("[09:09] 🤖 response: …") {
			t.Errorf("detail not cut: %d chars", len([]rune(line)))
		}
	}
	if rep.Tokens == 0 || rep.Tokens != len(md)/4 || rep.Warning != "" {
		t.Errorf("tokens %d warning %q", rep.Tokens, rep.Warning)
	}

	// --include selects the kinds; a project filter the sessions.
	rep, err = Build(context.Background(), svc.Query, p, Options{Kinds: []string{"read", "commit"}, ProjectPath: "alpha"})
	if err != nil || len(rep.Sessions) != 1 || len(rep.Sessions[0].Rows) != 2 || rep.Sessions[0].Rows[0].Kind != "read" || rep.Sessions[0].Rows[1].Kind != "commit" {
		t.Errorf("include = %+v (%v)", rep.Sessions, err)
	}
	// Another day shows that day's session only; an empty period says so.
	next, _ := Day("2026-10-14")
	rep, _ = Build(context.Background(), svc.Query, next, Options{})
	if len(rep.Sessions) != 1 || rep.Sessions[0].SessionID != "s3" {
		t.Errorf("next day = %+v", rep.Sessions)
	}
	empty, _ := Day("2026-10-20")
	rep, _ = Build(context.Background(), svc.Query, empty, Options{})
	if len(rep.Sessions) != 0 || !strings.Contains(rep.Markdown(), "No sessions were captured") {
		t.Errorf("empty = %+v", rep)
	}
}

func TestPeriods(t *testing.T) {
	if _, err := Day("13/10/2026"); err == nil {
		t.Error("a bad day must be an error")
	}
	if _, err := Range("2026-10-14", "2026-10-13"); err == nil {
		t.Error("a reversed range must be an error")
	}
	r, _ := Range("2026-10-13", "2026-10-15")
	if r.IsDay() || r.String() != "2026-10-13..2026-10-15" || strings.Join(r.Days(), ",") != "2026-10-13,2026-10-14,2026-10-15" {
		t.Errorf("range = %+v", r)
	}
	d, _ := Day("2026-10-13")
	if !d.IsDay() || d.String() != "2026-10-13" || d.FirstDay() != d.LastDay() {
		t.Errorf("day = %+v", d)
	}
}

// fakeEngine answers with a fixed text and counts its runs.
type fakeEngine struct {
	runs    int
	prompts []string
	fail    error
}

func (f *fakeEngine) Name() string { return "fake" }
func (f *fakeEngine) Run(_ context.Context, prompt, model string) (string, float64, error) {
	f.runs++
	f.prompts = append(f.prompts, prompt)
	if f.fail != nil {
		return "", 0, f.fail
	}
	return "## Context\nRetro " + model + " #" + string(rune('0'+f.runs)), 0.25, nil
}

func TestGenerateStoresOnceAndBuildsRangesFromDailies(t *testing.T) {
	st := seed(t)
	ctx := context.Background()
	svc := newService(st, day1.AddDate(0, 0, 3))
	eng := &fakeEngine{}
	p, _ := Day("2026-10-13")

	d, err := svc.Generate(ctx, p, Options{}, eng, "claude-fable-5-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Scope != "day" || d.From != "2026-10-13" || d.To != "2026-10-13" || d.Text != "## Context\nRetro claude-fable-5-1 #1" || d.Engine != "fake" || d.CostUSD != 0.25 || d.ReportTokens == 0 || d.Cached {
		t.Errorf("digest = %+v", d)
	}
	if !strings.Contains(eng.prompts[0], "Day: 2026-10-13") || !strings.Contains(eng.prompts[0], "[09:00] 👤 user: Fix the daemon restart") || !strings.Contains(eng.prompts[0], "## Open Questions / Next Steps") {
		t.Errorf("daily prompt = %s", eng.prompts[0])
	}
	// A finished day comes from the cache; --force regenerates it.
	again, err := svc.Generate(ctx, p, Options{}, eng, "", false)
	if err != nil || !again.Cached || again.Text != d.Text || eng.runs != 1 {
		t.Errorf("cached = %+v (%v), runs %d", again, err, eng.runs)
	}
	forced, err := svc.Generate(ctx, p, Options{}, eng, "", true)
	if err != nil || forced.Cached || eng.runs != 2 || forced.Text == d.Text {
		t.Errorf("forced = %+v (%v), runs %d", forced, err, eng.runs)
	}
	// Today is always regenerated.
	today := newService(st, day1.Add(10*time.Hour))
	if _, err := today.Generate(ctx, p, Options{}, eng, "", false); err != nil || eng.runs != 3 {
		t.Errorf("today: err %v runs %d", err, eng.runs)
	}
	// An empty day has nothing to digest.
	empty, _ := Day("2026-10-20")
	if _, err := svc.Generate(ctx, empty, Options{}, eng, "", false); !errors.Is(err, ErrNoSessions) {
		t.Errorf("empty day: err %v", err)
	}
	// The stored digest is a memory record of kind digest, found by search.
	hits, err := svc.Query.SearchMemory(ctx, query.SearchMemoryArgs{Kind: Kind, Pattern: "Retro"})
	if err != nil || len(hits) != 3 || hits[0].Kind != "digest" || hits[0].Day != "2026-10-13" || hits[0].File != "digest/2026-10-13.md" {
		t.Errorf("search_memory = %+v (%v)", hits, err)
	}

	// A range digest generates the missing dailies (14 October) and skips
	// empty days, wrapping each daily in a day element.
	eng.runs = 0
	eng.prompts = nil
	r, _ := Range("2026-10-13", "2026-10-15")
	rd, err := svc.Generate(ctx, r, Options{}, eng, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Scope != "range" || rd.From != "2026-10-13" || rd.To != "2026-10-15" || eng.runs != 2 {
		t.Errorf("range = %+v, runs %d", rd, eng.runs)
	}
	rangePrompt := eng.prompts[len(eng.prompts)-1]
	if !strings.Contains(rangePrompt, `<day date="2026-10-13">`) || !strings.Contains(rangePrompt, `<day date="2026-10-14">`) || strings.Contains(rangePrompt, `<day date="2026-10-15">`) || !strings.Contains(rangePrompt, "## Range Overview") {
		t.Errorf("range prompt = %s", rangePrompt)
	}
	cached, err := svc.Cached(ctx, r)
	if err != nil || cached == nil || cached.Text != rd.Text || !cached.Cached {
		t.Errorf("cached range = %+v (%v)", cached, err)
	}
	// An engine failure stores nothing.
	eng.fail = errors.New("claude: not found")
	bad, _ := Day("2026-10-14")
	if _, err := svc.Generate(ctx, bad, Options{}, eng, "", true); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("failure = %v", err)
	}
}

func TestPromptTemplatesCanBeOverridden(t *testing.T) {
	st := seed(t)
	svc := newService(st, day1.AddDate(0, 0, 3))
	svc.TemplateDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(svc.TemplateDir, "daily.md"), []byte("Custom {{.Day}}\n{{.Report}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := Day("2026-10-13")
	prompt, rep, err := svc.Prompt(context.Background(), p, Options{})
	if err != nil || !strings.HasPrefix(prompt, "Custom 2026-10-13\n# Activity report") || len(rep.Sessions) != 2 {
		t.Errorf("prompt = %q (%v)", prompt, err)
	}
	// A missing override falls back to the built-in template.
	r, _ := Range("2026-10-13", "2026-10-13")
	if prompt, _, err := svc.Prompt(context.Background(), Period{From: r.From, To: r.To.AddDate(0, 0, 1)}, Options{}); err != nil || !strings.Contains(prompt, "## Range Overview") {
		t.Errorf("range prompt = %q (%v)", prompt, err)
	}
}

func TestParseStream(t *testing.T) {
	out := []byte(`{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"## Context\nHello"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"## Context\nHello","total_cost_usd":0.0123}
`)
	text, cost, err := parseStream(out)
	if err != nil || text != "## Context\nHello" || cost != 0.0123 {
		t.Errorf("parsed %q %v %v", text, cost, err)
	}
	if text, _, err := parseStream([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"only"}]}}` + "\n")); err != nil || text != "only" {
		t.Errorf("without a result event: %q %v", text, err)
	}
	if _, _, err := parseStream([]byte(`{"type":"result","is_error":true,"result":"rate limited"}`)); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error result: %v", err)
	}
	if _, _, err := parseStream(nil); err == nil {
		t.Error("no output must be an error")
	}
}

func TestClaudeEngineRunsTheCommand(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > \"$CLUED_TEST_PROMPT\"\necho \"$@\" > \"$CLUED_TEST_ARGS\"\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"Retro text\",\"total_cost_usd\":0.5}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLUED_TEST_PROMPT", filepath.Join(dir, "prompt"))
	t.Setenv("CLUED_TEST_ARGS", filepath.Join(dir, "args"))
	text, cost, err := ClaudeEngine{Command: script}.Run(context.Background(), "the prompt", "claude-x")
	if err != nil || text != "Retro text" || cost != 0.5 {
		t.Fatalf("run: %q %v %v", text, cost, err)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, "prompt"))
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if string(prompt) != "the prompt" || strings.TrimSpace(string(args)) != "-p --no-session-persistence --output-format stream-json --verbose --model claude-x" {
		t.Errorf("prompt %q args %q", prompt, args)
	}
	if _, _, err := (ClaudeEngine{Command: filepath.Join(dir, "missing")}).Run(context.Background(), "x", ""); err == nil {
		t.Error("a missing command must be an error")
	}
}

func TestClaudeEngineSetsNoCapture(t *testing.T) {
	dir := t.TempDir()
	// The fake claude answers with what it sees of its environment.
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nprintf '{\"type\":\"result\",\"result\":\"CLUED_NO_CAPTURE=%s\",\"total_cost_usd\":0}\\n' \"$CLUED_NO_CAPTURE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLUED_NO_CAPTURE", "")
	text, _, err := ClaudeEngine{Command: script}.Run(context.Background(), "x", "")
	if err != nil || text != "CLUED_NO_CAPTURE=1" {
		t.Fatalf("the engine's environment: %q %v", text, err)
	}
}
