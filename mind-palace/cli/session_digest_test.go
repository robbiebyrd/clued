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

var digestDay = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

// seedDigestSessions stores one session on 13 October 2026 with a labelled
// timeline: a prompt, a command run twice, a task with its result.
func seedDigestSessions(t *testing.T) session.Store {
	t.Helper()
	ctx := context.Background()
	st := memsession.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.UpsertSession(ctx, session.Session{SessionID: "d1", AccountID: testAccount, ProjectPath: "/work/alpha", LastSeen: digestDay.Add(time.Hour)}))
	for seq, a := range []session.Doc{
		{"kind": "user", "label": "user", "detail": "Fix the daemon restart"},
		{"kind": "bash", "label": "Bash", "detail": "go test ./...", "tool_name": "Bash", "tool_use_id": "t1"},
		{"kind": "bash", "label": "Bash", "detail": "go test ./...", "tool_name": "Bash", "tool_use_id": "t2"},
		{"kind": "task", "label": "Task", "detail": "Review the diff", "tool_name": "Task", "tool_use_id": "t3"},
		{"kind": "result", "label": "result", "detail": "Looks fine.", "tool_use_id": "t3"},
		{"kind": "read", "label": "Read", "detail": "daemon.go", "tool_name": "Read", "tool_use_id": "t4"},
	} {
		at := digestDay.Add(time.Duration(seq) * time.Minute)
		must(st.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: "d1", Seq: seq, AccountID: testAccount, Line: map[string]any{"type": "x", "timestamp": at.Format(time.RFC3339Nano)}}}))
		pending, err := st.Unenriched(ctx, "transcript_lines", "activity-kind", 0)
		must(err)
		for _, d := range pending {
			if d["session_id"] == "d1" && d["seq"] == seq {
				must(st.SetEnrichment(ctx, "transcript_lines", d["_id"], "activity-kind", a))
			}
		}
	}
	return st
}

func TestSessionReportRendersTheDay(t *testing.T) {
	st := seedDigestSessions(t)
	sessionEnv(t, st)
	r := exec(t, t.TempDir(), "session", "report", "--day", "2026-10-13")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("report: %+v", r)
	}
	for _, want := range []string{"# Activity report 2026-10-13", "## Session d1", "project: /work/alpha", "[09:00] 👤 user: Fix the daemon restart", "[09:02] $ Bash: go test ./... ×2", "[09:03] 🧵 Task: Review the diff\n    ↳ returned: Looks fine."} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("report lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "daemon.go") {
		t.Error("reads are not shown by default")
	}
	r = exec(t, t.TempDir(), "session", "report", "--day", "2026-10-13", "--include", "read,user", "--format", "json")
	if r.code != 0 {
		t.Fatalf("json report: %+v", r)
	}
	res := result(t, r)
	sessions := res["sessions"].([]any)
	if res["period"] != "2026-10-13" || len(sessions) != 1 || res["tokens"].(float64) <= 0 {
		t.Errorf("json report = %v", res)
	}
	rows := sessions[0].(map[string]any)["rows"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["kind"] != "user" || rows[1].(map[string]any)["kind"] != "read" {
		t.Errorf("rows = %v", rows)
	}
	if r := exec(t, t.TempDir(), "session", "report", "--day", "13/10/2026"); r.code == 0 {
		t.Error("a bad day must fail")
	}
	if r := exec(t, t.TempDir(), "session", "report", "--day", "2026-10-13", "--from", "2026-10-01"); r.code == 0 {
		t.Error("day and range together must fail")
	}
}

func TestSessionDigestRunsTheEngineOnceAndShowsTheCache(t *testing.T) {
	st := seedDigestSessions(t)
	sessionEnv(t, st)
	// A stubbed claude on PATH answers with a fixed retro and counts its runs.
	bin := t.TempDir()
	counter := filepath.Join(bin, "runs")
	script := "#!/bin/sh\ncat >/dev/null\necho run >> \"" + counter + "\"\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"## Context\\nA fine day.\",\"total_cost_usd\":0.02}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// --engine none prints the report and the prompt and stores nothing.
	r := exec(t, t.TempDir(), "session", "digest", "--day", "2026-10-13", "--engine", "none")
	if r.code != 0 || !strings.Contains(r.stdout, "# Activity report 2026-10-13") || !strings.Contains(r.stdout, "## Open Questions / Next Steps") || !strings.Contains(r.stdout, "Day: 2026-10-13") {
		t.Fatalf("engine none: %+v", r)
	}
	if r := exec(t, t.TempDir(), "session", "digest", "--day", "2026-10-13", "--show"); r.code == 0 {
		t.Error("nothing stored yet: --show must fail")
	}

	r = exec(t, t.TempDir(), "session", "digest", "--day", "2026-10-13", "--model", "claude-x")
	if r.code != 0 {
		t.Fatalf("digest: %+v", r)
	}
	d := result(t, r)
	if d["scope"] != "day" || d["from"] != "2026-10-13" || d["text"] != "## Context\nA fine day." || d["engine"] != "claude" || d["model"] != "claude-x" || d["cost_usd"] != 0.02 || d["cached"] != false {
		t.Errorf("digest = %v", d)
	}
	// Stored once: the second run answers from the store; --show reads it.
	r = exec(t, t.TempDir(), "session", "digest", "--day", "2026-10-13")
	if r.code != 0 || result(t, r)["cached"] != true {
		t.Errorf("second run: %+v", r)
	}
	r = exec(t, t.TempDir(), "session", "digest", "--day", "2026-10-13", "--show")
	if r.code != 0 || result(t, r)["text"] != "## Context\nA fine day." {
		t.Errorf("--show: %+v", r)
	}
	runs, _ := os.ReadFile(counter)
	if strings.Count(string(runs), "run") != 1 {
		t.Errorf("claude ran %d times, want 1", strings.Count(string(runs), "run"))
	}
	// A range digest wraps the stored daily; its empty days are skipped.
	r = exec(t, t.TempDir(), "session", "digest", "--from", "2026-10-12", "--to", "2026-10-13", "--engine", "none")
	if r.code != 0 || !strings.Contains(r.stdout, `<day date="2026-10-13">`) || strings.Contains(r.stdout, `<day date="2026-10-12">`) || !strings.Contains(r.stdout, "## Range Overview") {
		t.Errorf("range prompt: %+v", r)
	}
	r = exec(t, t.TempDir(), "session", "digest", "--from", "2026-10-12", "--to", "2026-10-13")
	if r.code != 0 || result(t, r)["scope"] != "range" {
		t.Errorf("range digest: %+v", r)
	}
	// An empty day is not found.
	if r := exec(t, t.TempDir(), "session", "digest", "--day", "2026-10-20"); r.code != 4 {
		t.Errorf("empty day: %+v", r)
	}
	// The digests are memory records of kind digest.
	r = exec(t, t.TempDir(), "session", "search-memory", "--kind", "digest")
	if r.code != 0 || !strings.Contains(r.stdout, `"kind": "digest"`) {
		t.Errorf("search-memory: %+v", r)
	}
}
