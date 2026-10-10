package restore_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/restore"
)

const (
	account  = "acc-restore"
	sid      = "sess-restore-001"
	projDir  = "-Users-testuser-Projects-myproject"
	projPath = "/Users/testuser/Projects/myproject"
)

var ctx = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	must(t, err)
	return string(b)
}

func seedSession(t *testing.T, st session.Store, s session.Session) {
	t.Helper()
	s.SessionID, s.AccountID, s.LastSeen = sid, account, time.Now()
	must(t, st.UpsertSession(ctx, s))
}

func seedTranscript(t *testing.T, st session.Store, n int) {
	t.Helper()
	lines := make([]session.TranscriptLine, n)
	for i := range lines {
		lines[i] = session.TranscriptLine{SessionID: sid, Seq: i, AccountID: account, Line: map[string]any{"type": "user", "content": i}}
	}
	must(t, st.UpsertTranscriptLines(ctx, lines))
}

// full seeds a session with transcript, a subagent and the three blob types,
// as restore.test.ts does.
func full(t *testing.T) *memsession.Store {
	t.Helper()
	st := memsession.New()
	seedSession(t, st, session.Session{
		ProjectPath:    projPath,
		TranscriptPath: filepath.Join("/source", projDir, sid+".jsonl"),
	})
	seedTranscript(t, st, 5)
	must(t, st.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: sid, SubagentID: "agent-aabb", Seq: 0, AccountID: account, Line: map[string]any{"type": "user", "content": "subagent line"}}))
	for _, b := range []session.Blob{
		{BlobType: "subagent-meta", Name: "agent-aabb.meta.json", Content: `{"agentId":"agent-aabb"}`, Encoding: "utf8"},
		{BlobType: "tool-result", Name: "result1.txt", Content: "tool output", Encoding: "utf8"},
		{BlobType: "file-history", Name: "abc@v1", Content: base64.StdEncoding.EncodeToString([]byte("raw binary")), Encoding: "base64"},
	} {
		b.SessionID, b.AccountID = sid, account
		must(t, st.UpsertBlob(ctx, b))
	}
	return st
}

func TestRestoresEverything(t *testing.T) {
	st := full(t)
	tmp := t.TempDir()
	projects, history := filepath.Join(tmp, "projects"), filepath.Join(tmp, "history")

	res, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: projects}, history)
	must(t, err)

	transcript := readFile(t, projects, projDir, sid+".jsonl")
	if lines := strings.Split(strings.TrimSuffix(transcript, "\n"), "\n"); len(lines) != 5 {
		t.Fatalf("transcript has %d lines", len(lines))
	}
	if !strings.HasPrefix(transcript, `{"content":0,"type":"user"}`+"\n") {
		t.Fatalf("transcript = %q", transcript)
	}
	sub := filepath.Join(projects, projDir, sid, "subagents")
	if got := readFile(t, sub, "agent-aabb.jsonl"); got != `{"content":"subagent line","type":"user"}`+"\n" {
		t.Fatalf("subagent jsonl = %q", got)
	}
	if got := readFile(t, sub, "agent-aabb.meta.json"); got != `{"agentId":"agent-aabb"}` {
		t.Fatalf("meta = %q", got)
	}
	if got := readFile(t, projects, projDir, sid, "tool-results", "result1.txt"); got != "tool output" {
		t.Fatalf("tool result = %q", got)
	}
	if got := readFile(t, history, sid, "abc@v1"); got != "raw binary" {
		t.Fatalf("file history = %q", got)
	}

	wantBytes := len(transcript) + len(`{"content":"subagent line","type":"user"}`+"\n") +
		len(`{"agentId":"agent-aabb"}`) + len("tool output") + len("raw binary")
	if res.FilesWritten != 5 || res.BytesWritten != wantBytes || len(res.Missing) != 0 {
		t.Fatalf("result = %+v, want 5 files, %d bytes, none missing", res, wantBytes)
	}
	if res.Missing == nil {
		t.Fatal("Missing must be a non-nil empty list")
	}
}

func TestRestoreUsesStandardPermissions(t *testing.T) {
	st := full(t)
	tmp := t.TempDir()
	_, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: filepath.Join(tmp, "p")}, filepath.Join(tmp, "h"))
	must(t, err)
	check := func(path string, want os.FileMode) {
		t.Helper()
		info, err := os.Stat(path)
		must(t, err)
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
	check(filepath.Join(tmp, "p", projDir, sid+".jsonl"), 0o644)
	check(filepath.Join(tmp, "p", projDir, sid, "subagents"), 0o755)
	check(filepath.Join(tmp, "h", sid), 0o755)
}

func TestRestoreIsIdempotent(t *testing.T) {
	st := full(t)
	tmp := t.TempDir()
	args := restore.Args{SessionID: sid, ProjectsDir: filepath.Join(tmp, "p")}
	first, err := restore.Session(ctx, st, account, args, filepath.Join(tmp, "h"))
	must(t, err)
	before := readFile(t, tmp, "p", projDir, sid+".jsonl")
	second, err := restore.Session(ctx, st, account, args, filepath.Join(tmp, "h"))
	must(t, err)
	if before != readFile(t, tmp, "p", projDir, sid+".jsonl") || !reflect.DeepEqual(first, second) {
		t.Fatalf("second run differs: %+v vs %+v", first, second)
	}
}

func TestRestoreWritesTranscriptInMultipleBatches(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, session.Session{ProjectPath: projPath})
	seedTranscript(t, st, 1201)
	tmp := t.TempDir()
	_, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: tmp}, filepath.Join(tmp, "h"))
	must(t, err)
	lines := strings.Split(strings.TrimSuffix(readFile(t, tmp, projDir, sid+".jsonl"), "\n"), "\n")
	if len(lines) != 1201 || lines[0] != `{"content":0,"type":"user"}` || lines[1200] != `{"content":1200,"type":"user"}` {
		t.Fatalf("got %d lines, ends %q", len(lines), lines[len(lines)-1])
	}
}

func TestRestoreDoesNotEscapeHTMLInTranscript(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, session.Session{ProjectPath: projPath})
	must(t, st.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: sid, AccountID: account, Line: map[string]any{"c": "a<b>&c"}}}))
	tmp := t.TempDir()
	_, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: tmp}, filepath.Join(tmp, "h"))
	must(t, err)
	if got := readFile(t, tmp, projDir, sid+".jsonl"); got != `{"c":"a<b>&c"}`+"\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRestoreProjectPathOverride(t *testing.T) {
	st := full(t)
	tmp := t.TempDir()
	_, err := restore.Session(ctx, st, account,
		restore.Args{SessionID: sid, ProjectPath: "/Users/otheruser/Projects/myproject", ProjectsDir: tmp}, filepath.Join(tmp, "h"))
	must(t, err)
	if _, err := os.Stat(filepath.Join(tmp, "-Users-otheruser-Projects-myproject", sid+".jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreDerivesDirectory(t *testing.T) {
	cases := []struct {
		name string
		s    session.Session
		want string
	}{
		{"transcript path parent", session.Session{TranscriptPath: "/x/-From-Transcript/s.jsonl", ProjectPath: "/Ignored/Path"}, "-From-Transcript"},
		{"encoded stored project path", session.Session{ProjectPath: "/Stored/Project"}, "-Stored-Project"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := memsession.New()
			seedSession(t, st, c.s)
			seedTranscript(t, st, 1)
			tmp := t.TempDir()
			_, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: tmp}, filepath.Join(tmp, "h"))
			must(t, err)
			if _, err := os.Stat(filepath.Join(tmp, c.want, sid+".jsonl")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRestoreReportsMissing(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, session.Session{ProjectPath: projPath})
	tmp := t.TempDir()
	res, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: tmp}, filepath.Join(tmp, "h"))
	must(t, err)
	want := []string{"transcript_lines", "subagent-meta", "tool-result", "file-history"}
	if !reflect.DeepEqual(res.Missing, want) || res.FilesWritten != 0 || res.BytesWritten != 0 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(tmp, projDir, sid+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("no transcript file expected, stat err = %v", err)
	}
}

func TestRestoreReportsOnlyAbsentBlobTypes(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, session.Session{ProjectPath: projPath})
	seedTranscript(t, st, 1)
	must(t, st.UpsertBlob(ctx, session.Blob{SessionID: sid, AccountID: account, BlobType: "tool-result", Name: "r.txt", Content: "x", Encoding: "utf8"}))
	tmp := t.TempDir()
	res, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: tmp}, filepath.Join(tmp, "h"))
	must(t, err)
	if want := []string{"subagent-meta", "file-history"}; !reflect.DeepEqual(res.Missing, want) {
		t.Fatalf("missing = %v", res.Missing)
	}
}

func TestRestoreErrors(t *testing.T) {
	st := full(t)
	tmp := t.TempDir()
	for _, id := range []string{"no-such-session"} {
		if _, err := restore.Session(ctx, st, account, restore.Args{SessionID: id, ProjectsDir: tmp}, tmp); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("unknown: %v", err)
		}
	}
	if _, err := restore.Session(ctx, st, "other-account", restore.Args{SessionID: sid, ProjectsDir: tmp}, tmp); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("foreign: %v", err)
	}

	bare := memsession.New()
	seedSession(t, bare, session.Session{})
	_, err := restore.Session(ctx, bare, account, restore.Args{SessionID: sid, ProjectsDir: tmp}, tmp)
	if err == nil || !strings.Contains(err.Error(), "no path information") {
		t.Fatalf("no path info: %v", err)
	}

	badBlob := memsession.New()
	seedSession(t, badBlob, session.Session{ProjectPath: projPath})
	must(t, badBlob.UpsertBlob(ctx, session.Blob{SessionID: sid, AccountID: account, BlobType: "file-history", Name: "x", Content: "!!not base64!!", Encoding: "base64"}))
	if _, err := restore.Session(ctx, badBlob, account, restore.Args{SessionID: sid, ProjectsDir: tmp}, tmp); err == nil {
		t.Fatal("invalid base64 must be an error")
	}
}

func TestRestoreIgnoresOtherAccountsData(t *testing.T) {
	st := full(t)
	must(t, st.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: sid, Seq: 99, AccountID: "intruder", Line: map[string]any{"x": 1}}}))
	tmp := t.TempDir()
	_, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: tmp}, filepath.Join(tmp, "h"))
	must(t, err)
	if lines := strings.Split(strings.TrimSuffix(readFile(t, tmp, projDir, sid+".jsonl"), "\n"), "\n"); len(lines) != 5 {
		t.Fatalf("transcript has %d lines", len(lines))
	}
}

// treeFiles lists every file under root, relative to it.
func treeFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	must(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return err
	}))
	return out
}

func TestRestoreSkipsUnsafeBlobAndSubagentNames(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, session.Session{ProjectPath: projPath})
	for _, b := range []session.Blob{
		{BlobType: "tool-result", Name: "../../../../escape.txt", Content: "x", Encoding: "utf8"},
		{BlobType: "file-history", Name: "sub/dir", Content: "x", Encoding: "utf8"},
		{BlobType: "subagent-meta", Name: "/abs.json", Content: "x", Encoding: "utf8"},
		{BlobType: "tool-result", Name: "ok.txt", Content: "fine", Encoding: "utf8"},
	} {
		b.SessionID, b.AccountID = sid, account
		must(t, st.UpsertBlob(ctx, b))
	}
	must(t, st.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: sid, SubagentID: "../evil", Seq: 0, AccountID: account, Line: map[string]any{"a": 1}}))

	outer := t.TempDir()
	root := filepath.Join(outer, "root", "deep")
	projects, history := filepath.Join(root, "projects"), filepath.Join(root, "history")
	res, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: projects}, history)
	must(t, err)

	wantMissing := []string{
		"transcript_lines",
		"unsafe-name:subagent-lines/../evil",
		"unsafe-name:file-history/sub/dir",
		"unsafe-name:subagent-meta//abs.json",
		"unsafe-name:tool-result/../../../../escape.txt",
	}
	if !reflect.DeepEqual(res.Missing, wantMissing) {
		t.Fatalf("missing = %q, want %q", res.Missing, wantMissing)
	}
	if res.FilesWritten != 1 || res.BytesWritten != len("fine") {
		t.Fatalf("result = %+v", res)
	}
	if got := treeFiles(t, outer); !reflect.DeepEqual(got, []string{filepath.Join("root", "deep", "projects", projDir, sid, "tool-results", "ok.txt")}) {
		t.Fatalf("files under outer = %v", got)
	}
}

func TestRestoreRejectsUnsafeSessionIDBeforeCreatingAnything(t *testing.T) {
	st := memsession.New()
	for _, id := range []string{"../escape", "a/b", "..", ""} {
		must(t, st.UpsertSession(ctx, session.Session{SessionID: id, AccountID: account, ProjectPath: projPath, LastSeen: time.Now()}))
		tmp := t.TempDir()
		projects, history := filepath.Join(tmp, "projects"), filepath.Join(tmp, "history")
		_, err := restore.Session(ctx, st, account, restore.Args{SessionID: id, ProjectsDir: projects}, history)
		if err == nil || !strings.Contains(err.Error(), "unsafe session id") {
			t.Fatalf("%q: err = %v", id, err)
		}
		if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
			t.Fatalf("%q: created %v", id, entries)
		}
	}
}

func TestRestoreRejectsUnsafeTranscriptDirectoryName(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, session.Session{TranscriptPath: "/x/../s.jsonl"})
	tmp := t.TempDir()
	_, err := restore.Session(ctx, st, account, restore.Args{SessionID: sid, ProjectsDir: filepath.Join(tmp, "p")}, filepath.Join(tmp, "h"))
	if err == nil || !strings.Contains(err.Error(), "unsafe project directory name") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("created %v", entries)
	}
}
