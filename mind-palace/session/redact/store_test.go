package redact_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/catchup"
	"github.com/robbiebyrd/clued/mind-palace/session/ingest"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
)

const account = "acc"

var secrets = []string{
	"sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123",
	"AKIAABCDEFGHIJKLMNOP",
	"ghp_0123456789abcdefghijklmnopqrstuvwxyzAB",
	"my private diary",
}

func writeJSONL(t *testing.T, path string, lines ...map[string]any) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	var b strings.Builder
	for _, l := range lines {
		raw, err := json.Marshal(l)
		must(t, err)
		b.Write(raw)
		b.WriteByte('\n')
	}
	must(t, os.WriteFile(path, []byte(b.String()), 0o644))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// capture runs a session through the code a hook runs (ingest for the hook
// events, catchup for the transcript, subagent and tool-result files) into
// the store, and returns everything the store then holds for it as JSON.
func capture(t *testing.T, store session.Store, cwd string) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects")
	sid := "11111111-2222-3333-4444-555555555555"
	transcript := filepath.Join(projects, "-repo", sid+".jsonl")
	writeJSONL(t, transcript,
		map[string]any{"type": "summary", "summary": "set the key " + secrets[0]},
		map[string]any{"type": "user", "cwd": cwd, "sessionId": sid, "message": map[string]any{"role": "user", "content": "use " + secrets[1] + " and <private>my private diary</private>"}},
		map[string]any{"type": "assistant", "cwd": cwd, "sessionId": sid, "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "gh auth login --with-token " + secrets[2]}},
		}}},
	)
	writeJSONL(t, filepath.Join(projects, "-repo", sid, "subagents", "agent-a.jsonl"),
		map[string]any{"type": "user", "cwd": cwd, "message": map[string]any{"role": "user", "content": "the key is " + secrets[0]}})
	must(t, os.MkdirAll(filepath.Join(projects, "-repo", sid, "tool-results"), 0o755))
	must(t, os.WriteFile(filepath.Join(projects, "-repo", sid, "tool-results", "t1.txt"), []byte("AWS_ACCESS_KEY_ID="+secrets[1]), 0o644))

	svc := &ingest.Service{Store: store, AccountID: account, WALPath: filepath.Join(dir, "wal"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, ev := range []session.Doc{
		{"session_id": sid, "hook_event_name": "SessionStart", "cwd": cwd, "transcript_path": transcript},
		{"session_id": sid, "hook_event_name": "PreToolUse", "cwd": cwd, "tool_name": "Bash", "tool_input": map[string]any{"command": "export GITHUB_TOKEN=" + secrets[2]}},
		{"session_id": sid, "hook_event_name": "UserPromptSubmit", "cwd": cwd, "prompt": "remember " + secrets[0]},
	} {
		if _, err := svc.Handle(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := catchup.Run(ctx, store, catchup.Paths{SessionID: sid, AccountID: account, Transcript: transcript, ProjectsDir: projects, FileHistoryDir: filepath.Join(dir, "fh")}, filepath.Join(dir, "state")); err != nil {
		t.Fatal(err)
	}
	var all []any
	if full, err := store.FullSession(ctx, account, sid); err == nil {
		all = append(all, full)
	}
	lines, err := store.TranscriptLines(ctx, session.LineQuery{AccountID: account, SessionID: sid})
	must(t, err)
	blobs, err := store.Blobs(ctx, account, sid)
	must(t, err)
	subs, err := store.SubagentLines(ctx, account, sid, "agent-a")
	must(t, err)
	events, err := store.BashEvents(ctx, session.CommandQuery{AccountID: account, PatternRe: "."})
	must(t, err)
	all = append(all, lines, blobs, subs, events)
	raw, err := json.Marshal(session.JSONTimes(all))
	must(t, err)
	return string(raw)
}

func TestNoStoredDocumentHoldsASeededSecret(t *testing.T) {
	on := true
	inner := memsession.New()
	store, err := redact.FromConfig(inner, session.Config{RedactAtCapture: &on})
	must(t, err)
	stored := capture(t, store, "/repo")
	for _, s := range secrets {
		if strings.Contains(stored, s) {
			t.Errorf("the store holds %q", s)
		}
	}
	for _, mark := range []string{"[REDACTED:api-key]", "[REDACTED:aws-key]", "[REDACTED:github-token]", "[PRIVATE]", `"redacted":["`} {
		if !strings.Contains(stored, mark) {
			t.Errorf("the store holds no %s", mark)
		}
	}
	// What was captured is still there: the session, its lines, the blob.
	if !strings.Contains(stored, `"t1.txt"`) || !strings.Contains(stored, `"agent-a"`) || !strings.Contains(stored, `"seq":2`) {
		t.Errorf("capture lost documents: %s", stored)
	}
}

func TestWithoutRedactAtCaptureTheStoreHoldsTheOriginals(t *testing.T) {
	store, err := redact.FromConfig(memsession.New(), session.Config{})
	must(t, err)
	if _, wrapped := store.(*redact.Store); wrapped {
		t.Error("an empty config wrapped the store")
	}
	if stored := capture(t, store, "/repo"); !strings.Contains(stored, secrets[0]) {
		t.Error("the originals are missing without redaction")
	}
}

func TestASessionUnderAnExcludedPathIsNeverStored(t *testing.T) {
	inner := memsession.New()
	store, err := redact.FromConfig(inner, session.Config{ExcludePaths: []string{"/work/secret-*"}})
	must(t, err)
	capture(t, store, "/work/secret-repo/sub")
	ctx := context.Background()
	found, err := inner.FindSessions(ctx, session.SessionQuery{AccountID: account})
	must(t, err)
	events, err := inner.BashEvents(ctx, session.CommandQuery{AccountID: account, PatternRe: "."})
	must(t, err)
	n, err := inner.CountTranscriptLines(ctx, account, "11111111-2222-3333-4444-555555555555")
	must(t, err)
	blobs, err := inner.Blobs(ctx, account, "11111111-2222-3333-4444-555555555555")
	must(t, err)
	subs, err := inner.SubagentIDs(ctx, account, "11111111-2222-3333-4444-555555555555")
	must(t, err)
	if len(found) != 0 || len(events) != 0 || n != 0 || len(blobs) != 0 || len(subs) != 0 {
		t.Errorf("stored for an excluded path: %d sessions, %d events, %d lines, %d blobs, %d subagents", len(found), len(events), n, len(blobs), len(subs))
	}
	// Another path is captured as usual by the same store.
	capture(t, store, "/work/open")
}

func TestABadPatternIsReportedAndTheRestStillApply(t *testing.T) {
	on := true
	store, err := redact.FromConfig(memsession.New(), session.Config{RedactAtCapture: &on, RedactPatterns: []string{"("}, ExcludePaths: []string{"["}})
	if err == nil || !strings.Contains(err.Error(), "redactPatterns[0]") || !strings.Contains(err.Error(), "excludePaths[0]") {
		t.Fatalf("err = %v", err)
	}
	if stored := capture(t, store, "/repo"); strings.Contains(stored, secrets[0]) {
		t.Error("the built-in rules stopped applying")
	}
}
