package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

// seedMemorySessions stores, for testAccount, a session of two prompts in
// /work/alpha at each of the times.
func seedMemorySessions(t *testing.T, starts ...time.Time) session.Store {
	t.Helper()
	ctx := context.Background()
	st := memsession.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, start := range starts {
		sid := "m" + string(rune('a'+i))
		must(st.UpsertSession(ctx, session.Session{SessionID: sid, AccountID: testAccount, ProjectPath: "/work/alpha", LastSeen: start.Add(3 * time.Minute)}))
		for seq, a := range []session.Doc{
			{"kind": "user", "label": "user", "detail": "Fix the daemon in " + sid},
			{"kind": "edit", "label": "Edit", "detail": "daemon.go", "tool_name": "Edit", "tool_use_id": sid + "-e"},
			{"kind": "user", "label": "user", "detail": "Now the docs"},
			{"kind": "response", "label": "response", "detail": "Done in " + sid},
		} {
			at := start.Add(time.Duration(seq) * time.Minute)
			must(st.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: sid, Seq: seq, AccountID: testAccount, Line: map[string]any{"type": "x", "timestamp": at.Format(time.RFC3339Nano)}}}))
			pending, err := st.Unenriched(ctx, "transcript_lines", "activity-kind", 0)
			must(err)
			for _, d := range pending {
				if d["session_id"] == sid && d["seq"] == seq {
					must(st.SetEnrichment(ctx, "transcript_lines", d["_id"], "activity-kind", a))
				}
			}
		}
	}
	return st
}

// memoryConfig writes a config file with the memory block and returns its
// path.
func memoryConfig(t *testing.T, memory string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"memory": `+memory+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"CLUED_MEMORY_ENGINE", "CLUED_MEMORY_BUDGET", "CLUED_SERVER_URL", "CLUED_SERVER_TOKEN", "CLUED_SERVER_KEY"} {
		t.Setenv(k, "")
	}
	return path
}

func TestMemoryRunThenStatus(t *testing.T) {
	now := time.Now().UTC()
	st := seedMemorySessions(t, now.Add(-50*time.Hour), now.Add(-2*time.Hour))
	sessionEnv(t, st)
	cfg := memoryConfig(t, `{"engine": "extract"}`)
	dir := t.TempDir()
	r := exec(t, dir, "session", "--config", cfg, "memory", "run", "--project", "/work/alpha")
	if r.code != 0 {
		t.Fatalf("run: %+v", r)
	}
	rep := result(t, r)
	if rep["notes"].(float64) != 2 || rep["days"].(float64) < 1 || rep["engine"] != "extract" || rep["engine_runs"].(float64) != 0 {
		t.Errorf("run = %v", rep)
	}
	r = exec(t, dir, "session", "--config", cfg, "memory", "status", "--project", "/work/alpha")
	if r.code != 0 {
		t.Fatalf("status: %+v", r)
	}
	status := result(t, r)
	records := status["records"].(map[string]any)
	if records["session-summary"].(float64) != 2 || records["memory-day"].(float64) < 1 || status["engine"] != "extract" || status["runs_today"].(float64) != 0 || status["cost_month_usd"].(float64) != 0 || status["max_runs_per_day"].(float64) != 20 {
		t.Errorf("status = %v", status)
	}
	// A second run writes nothing.
	r = exec(t, dir, "session", "--config", cfg, "memory", "run", "--project", "/work/alpha")
	if rep := result(t, r); rep["notes"].(float64)+rep["days"].(float64)+rep["recent"].(float64) != 0 {
		t.Errorf("second run = %v", rep)
	}
	// Engine off: nothing runs.
	off := memoryConfig(t, `{"engine": "off"}`)
	if r := exec(t, dir, "session", "--config", off, "memory", "run", "--quiet"); r.code != 0 || r.stdout != "" {
		t.Errorf("off: %+v", r)
	}
	if r := exec(t, dir, "session", "--config", cfg, "memory", "run", "--engine", "gpt"); r.code == 0 {
		t.Error("an unknown engine must fail")
	}
}

func TestMemoryRunClaudeEngineWithAFakeClaudeOnPath(t *testing.T) {
	now := time.Now().UTC()
	st := seedMemorySessions(t, now.Add(-2*time.Hour))
	sessionEnv(t, st)
	bin := t.TempDir()
	envFile := filepath.Join(t.TempDir(), "env")
	fake := "#!/bin/sh\ncat >/dev/null\necho \"CLUED_NO_CAPTURE=$CLUED_NO_CAPTURE model=$*\" >> \"" + envFile + "\"\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"## Outcome\\nThe daemon is fixed.\",\"total_cost_usd\":0.004}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := memoryConfig(t, `{"engine": "claude", "maxRunsPerDay": 1}`)
	r := exec(t, t.TempDir(), "session", "--config", cfg, "memory", "run", "--session", "ma")
	if r.code != 0 {
		t.Fatalf("run: %+v", r)
	}
	rep := result(t, r)
	if rep["engine"] != "claude" || rep["engine_runs"].(float64) != 1 || rep["notes"].(float64) != 1 || rep["cost_usd"].(float64) != 0.004 {
		t.Errorf("run = %v", rep)
	}
	seen, _ := os.ReadFile(envFile)
	if !strings.Contains(string(seen), "CLUED_NO_CAPTURE=1") || !strings.Contains(string(seen), "--model haiku") {
		t.Errorf("the fake claude saw %q", seen)
	}
	events, _ := st.MemoryEvents(context.Background(), session.MemoryQuery{AccountID: testAccount, Kinds: []string{"session-summary"}})
	if len(events) != 1 || events[0].Map("memory")["engine"] != "claude" || events[0].Map("memory")["text"] != "## Outcome\nThe daemon is fixed." {
		t.Errorf("note = %v", events)
	}
	r = exec(t, t.TempDir(), "session", "--config", cfg, "memory", "status", "--project", "/work/alpha")
	if status := result(t, r); status["runs_today"].(float64) != 1 || status["cost_month_usd"].(float64) != 0.004 {
		t.Errorf("status = %v", status)
	}
	// A checkpoint past the day's cap of 1 falls back to the free engine.
	r = exec(t, t.TempDir(), "session", "--config", cfg, "memory", "run", "--session", "ma", "--checkpoint")
	if r.code != 0 {
		t.Fatalf("checkpoint: %+v", r)
	}
	if lines := strings.Count(string(mustRead(t, envFile)), "\n"); lines != 1 {
		t.Errorf("the capped checkpoint called claude: %d calls", lines)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMemoryRememberEditForget(t *testing.T) {
	st := memsession.New()
	sessionEnv(t, st)
	cfg := memoryConfig(t, `{}`)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"remember", "--project", "p", "--kind", "pinned", "Always", "run", "make", "check."},
		{"remember", "--project", "p", "The daemon listens on 8085."},
	} {
		if r := exec(t, dir, append([]string{"session", "--config", cfg, "memory"}, args...)...); r.code != 0 {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	editor := filepath.Join(t.TempDir(), "editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nsed -i.bak 's/8085/9090/' \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editor)
	r := exec(t, dir, "session", "--config", cfg, "memory", "edit", "--project", "p")
	if r.code != 0 {
		t.Fatalf("edit: %+v", r)
	}
	if rep := result(t, r); len(rep["changed"].([]any)) != 1 || rep["unchanged"].(float64) != 1 {
		t.Errorf("edit = %v", rep)
	}
	r = exec(t, dir, "session", "--config", cfg, "memory", "show", "--project", "p", "--kind", "memory-note")
	if !strings.Contains(r.stdout, "9090") || !strings.Contains(r.stdout, `"edited": true`) {
		t.Errorf("show after edit: %s", r.stdout)
	}
	r = exec(t, dir, "session", "--config", cfg, "memory", "forget", "--project", "p", "--pattern", "9090")
	if rep := result(t, r); rep["dry_run"] != true || len(rep["records"].([]any)) != 1 {
		t.Errorf("forget dry run = %v", rep)
	}
	r = exec(t, dir, "session", "--config", cfg, "memory", "forget", "--project", "p", "--pattern", "9090", "--yes")
	if rep := result(t, r); rep["dry_run"] != false {
		t.Errorf("forget = %v", rep)
	}
	r = exec(t, dir, "session", "--config", cfg, "memory", "show", "--project", "p", "--bundle")
	if !strings.Contains(r.stdout, "Always run make check.") || strings.Contains(r.stdout, "9090") {
		t.Errorf("bundle = %s", r.stdout)
	}
}

func TestMemoryExportImportRoundTrip(t *testing.T) {
	now := time.Now().UTC()
	st := seedMemorySessions(t, now.Add(-50*time.Hour))
	sessionEnv(t, st)
	cfg := memoryConfig(t, `{}`)
	dir := t.TempDir()
	if r := exec(t, dir, "session", "--config", cfg, "memory", "run", "--all"); r.code != 0 {
		t.Fatalf("run: %+v", r)
	}
	exec(t, dir, "session", "--config", cfg, "memory", "remember", "--project", "/work/alpha", "--private", "PRIVATE-NOTE")
	out := filepath.Join(t.TempDir(), "memory.jsonl")
	r := exec(t, dir, "session", "--config", cfg, "memory", "export", "--all", "--out", out)
	if r.code != 0 {
		t.Fatalf("export: %+v", r)
	}
	data := mustRead(t, out)
	if strings.Contains(string(data), "PRIVATE-NOTE") {
		t.Error("a private note was exported")
	}
	if info, _ := os.Stat(out); info.Mode().Perm() != 0o600 {
		t.Errorf("export mode = %o", info.Mode().Perm())
	}
	exported := int(result(t, r)["records"].(float64))
	if exported < 2 {
		t.Fatalf("exported %d records", exported)
	}
	if r := exec(t, dir, "session", "--config", cfg, "memory", "export", "--all", "--out", out); r.code == 0 {
		t.Error("an existing export must not be replaced")
	}

	fresh := memsession.New()
	sessionEnv(t, fresh)
	r = exec(t, dir, "session", "--config", cfg, "memory", "import", out)
	if r.code != 0 || int(result(t, r)["imported"].(float64)) != exported {
		t.Fatalf("import: %+v", r)
	}
	if r := exec(t, dir, "session", "--config", cfg, "memory", "import", out); int(result(t, r)["imported"].(float64)) != 0 {
		t.Errorf("a second import stored again: %s", r.stdout)
	}
	events, _ := fresh.MemoryEvents(context.Background(), session.MemoryQuery{AccountID: testAccount, IncludePrivate: true})
	if len(events) != exported {
		t.Errorf("%d records after the imports, want %d", len(events), exported)
	}
}
