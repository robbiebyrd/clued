package export_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/export"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/sessiontest"
)

const (
	account = "acc"
	secret  = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123"
)

var ctx = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func seed(t *testing.T, st session.Store, id, project string) {
	t.Helper()
	must(t, st.UpsertSession(ctx, session.Session{SessionID: id, AccountID: account, ProjectPath: project, Cwd: project, GitOrigin: "git@github.com:me/r.git", LastSeen: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}))
	must(t, st.UpsertTranscriptLines(ctx, []session.TranscriptLine{
		{SessionID: id, Seq: 0, AccountID: account, Line: map[string]any{"type": "user", "sessionId": id, "cwd": project, "message": map[string]any{"role": "user", "content": "use the key " + secret}}},
		{SessionID: id, Seq: 1, AccountID: account, Line: map[string]any{"type": "assistant", "sessionId": id, "cwd": project, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Done <ok> & tested."}}}}},
	}))
	must(t, st.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: id, SubagentID: "agent-a", Seq: 0, AccountID: account, Line: map[string]any{"type": "user", "n": 1.0}}))
	must(t, st.UpsertBlob(ctx, session.Blob{SessionID: id, BlobType: "tool-result", Name: "r.txt", Content: "output " + secret, Encoding: "utf8", AccountID: account}))
	must(t, st.InsertHookEvent(ctx, session.Doc{"session_id": id, "account_id": account, "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}, "created_at": time.Now()}))
}

func lines(t *testing.T, st session.Store, id string) []any {
	t.Helper()
	docs, err := st.TranscriptLines(ctx, session.LineQuery{AccountID: account, SessionID: id})
	must(t, err)
	out := make([]any, len(docs))
	for i, d := range docs {
		raw, _ := json.Marshal(d["line"])
		var v any
		_ = json.Unmarshal(raw, &v)
		out[i] = v
	}
	return out
}

func TestMarkdownAndHTMLAreRedactedByDefault(t *testing.T) {
	st := memsession.New()
	seed(t, st, "s1", "/repo")
	dir := t.TempDir()
	for _, f := range []string{export.Markdown, export.HTML, export.JSONL} {
		out := filepath.Join(dir, "s1."+f)
		res, err := export.Run(ctx, st, account, export.Options{SessionIDs: []string{"s1"}, Format: f, Out: out})
		must(t, err)
		raw, err := os.ReadFile(res.Path)
		must(t, err)
		if strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "[REDACTED:api-key]") || !res.Redacted {
			t.Errorf("%s export is not redacted: %s", f, raw)
		}
		if info, _ := os.Stat(out); info.Mode().Perm() != 0o600 {
			t.Errorf("%s export mode = %v, want 0600", f, info.Mode().Perm())
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "s1.html"))
	if !strings.Contains(string(raw), "Done &lt;ok&gt; &amp; tested.") {
		t.Error("the HTML does not escape the conversation")
	}
	if _, err := export.Run(ctx, st, account, export.Options{SessionIDs: []string{"s1"}, Format: export.Markdown, Out: filepath.Join(dir, "s1.md")}); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("an existing file was replaced: %v", err)
	}
	res, err := export.Run(ctx, st, account, export.Options{SessionIDs: []string{"s1"}, Format: export.Markdown, Out: filepath.Join(dir, "s1.md"), Force: true, Raw: true})
	must(t, err)
	if raw, _ := os.ReadFile(res.Path); !strings.Contains(string(raw), secret) || res.Redacted {
		t.Error("--raw did not keep the original")
	}
}

func TestAZipRoundTripsThroughImport(t *testing.T) {
	sessiontest.EachStore(t, func(t *testing.T, st session.Store) { zipRoundTrip(t, st) })
}

func zipRoundTrip(t *testing.T, st session.Store) {
	seed(t, st, "s1", "/repo/a")
	seed(t, st, "s2", "/repo/b")
	seed(t, st, "s3", "/other")
	out := filepath.Join(t.TempDir(), "all.zip")
	res, err := export.Run(ctx, st, account, export.Options{ProjectPathRe: "^/repo/", Format: export.Zip, Out: out, Raw: true})
	must(t, err)
	if !reflect.DeepEqual(res.Sessions, []string{"s1", "s2"}) && !reflect.DeepEqual(res.Sessions, []string{"s2", "s1"}) {
		t.Fatalf("zip sessions = %v", res.Sessions)
	}
	zr, err := zip.OpenReader(out)
	must(t, err)
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	zr.Close()
	for _, want := range []string{"s1/session.json", "s1/s1.jsonl", "s1/s1.md", "s2/session.json"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Errorf("the zip lacks %s: %v", want, names)
		}
	}

	fresh := memsession.New()
	imported, err := export.Import(ctx, fresh, account, out)
	must(t, err)
	if len(imported.Sessions) != 2 || imported.Lines != 4 || imported.Blobs != 2 || imported.HookEvents != 2 || imported.Subagents != 2 {
		t.Errorf("imported = %+v", imported)
	}
	for _, id := range []string{"s1", "s2"} {
		if !reflect.DeepEqual(lines(t, fresh, id), lines(t, st, id)) {
			t.Errorf("%s lines differ after the round trip", id)
		}
		got, err := fresh.GetSession(ctx, account, id)
		must(t, err)
		if got["git_origin"] != "git@github.com:me/r.git" {
			t.Errorf("%s session = %v", id, got)
		}
		blobs, _ := fresh.Blobs(ctx, account, id)
		if len(blobs) != 1 || blobs[0]["content"] != "output "+secret {
			t.Errorf("%s blobs = %v", id, blobs)
		}
	}
	// A second import changes nothing (no duplicate hook events).
	again, err := export.Import(ctx, fresh, account, out)
	must(t, err)
	events, _ := fresh.BashEvents(ctx, session.CommandQuery{AccountID: account, PatternRe: "."})
	if again.HookEvents != 0 || len(events) != 2 {
		t.Errorf("a second import added hook events: %d imported, %d stored", again.HookEvents, len(events))
	}
}

func TestAJSONLRoundTripsThroughImport(t *testing.T) {
	st := memsession.New()
	seed(t, st, "11111111-2222-3333-4444-555555555555", "/repo")
	out := filepath.Join(t.TempDir(), "transcript.jsonl")
	_, err := export.Run(ctx, st, account, export.Options{SessionIDs: []string{"11111111-2222-3333-4444-555555555555"}, Format: export.JSONL, Out: out, Raw: true})
	must(t, err)
	fresh := memsession.New()
	imported, err := export.Import(ctx, fresh, account, out)
	must(t, err)
	if len(imported.Sessions) != 1 || imported.Sessions[0] != "11111111-2222-3333-4444-555555555555" || imported.Lines != 2 {
		t.Fatalf("imported = %+v", imported)
	}
	if !reflect.DeepEqual(lines(t, fresh, imported.Sessions[0]), lines(t, st, imported.Sessions[0])) {
		t.Error("lines differ after the JSONL round trip")
	}
	got, _ := fresh.GetSession(ctx, account, imported.Sessions[0])
	if got["project_path"] != "/repo" {
		t.Errorf("the project was not read from the lines' cwd: %v", got)
	}
}

func TestRunRefusesWhatItCannotWrite(t *testing.T) {
	st := memsession.New()
	seed(t, st, "s1", "/repo")
	seed(t, st, "s2", "/repo")
	dir := t.TempDir()
	if _, err := export.Run(ctx, st, account, export.Options{ProjectPathRe: "repo", Format: export.Markdown, Out: filepath.Join(dir, "x.md")}); err == nil || !strings.Contains(err.Error(), "2 sessions match") {
		t.Errorf("two sessions to Markdown: %v", err)
	}
	if _, err := export.Run(ctx, st, account, export.Options{ProjectPathRe: "nothing", Format: export.Zip, Out: filepath.Join(dir, "x.zip")}); err != export.ErrNoSessions {
		t.Errorf("nothing selected: %v", err)
	}
	if _, err := export.Run(ctx, st, account, export.Options{SessionIDs: []string{"nope"}, Format: export.Markdown}); err == nil {
		t.Error("a missing session exported")
	}
	if _, err := export.Run(ctx, st, account, export.Options{SessionIDs: []string{"s1"}, Format: "pdf"}); err == nil {
		t.Error("format pdf accepted")
	}
}
