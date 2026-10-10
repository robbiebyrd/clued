package backfill_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/backfill"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const sessionID = "aaaabbbb-cccc-dddd-eeee-ffffffffffff"

func writeSession(t *testing.T, projectsDir, dirName, id, content string) string {
	t.Helper()
	dir := filepath.Join(projectsDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func run(t *testing.T, store session.Store, dir, account string, git backfill.GitInfo) backfill.Stats {
	t.Helper()
	stats, err := backfill.Run(context.Background(), store, dir, account, git, quietLogger())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return stats
}

func lines(t *testing.T, store session.Store, account, id string) []session.Doc {
	t.Helper()
	docs, err := store.TranscriptLines(context.Background(), session.LineQuery{AccountID: account, SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func TestRunUpsertsSessionsAndLines(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "-Users-test-myproject", sessionID,
		`{"type":"human","text":"hello"}`+"\n"+`{"type":"assistant","text":"world"}`+"\n")
	store := memsession.New()

	stats := run(t, store, dir, "", nil)

	if stats != (backfill.Stats{Sessions: 1, Lines: 2}) {
		t.Fatalf("stats = %+v", stats)
	}
	sess, err := store.GetSession(context.Background(), "", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := sess.String("project_path"); got != "/Users/test/myproject" {
		t.Errorf("project_path = %q", got)
	}
	if got, _ := sess.String("transcript_path"); got != path {
		t.Errorf("transcript_path = %q", got)
	}
	if sess["started_at"] == nil || sess["last_seen"] == nil {
		t.Errorf("started_at/last_seen missing: %v", sess)
	}
	got := lines(t, store, "", sessionID)
	if len(got) != 2 {
		t.Fatalf("lines = %d", len(got))
	}
	if typ, _ := got[0].Line().String("type"); typ != "human" {
		t.Errorf("line 0 type = %q", typ)
	}
	if typ, _ := got[1].Line().String("type"); typ != "assistant" {
		t.Errorf("line 1 type = %q", typ)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-test-myproject", sessionID, `{"a":1}`+"\n"+`{"a":2}`+"\n")
	store := memsession.New()

	run(t, store, dir, "", nil)
	run(t, store, dir, "", nil)

	n, err := store.CountTranscriptLines(context.Background(), "", sessionID)
	if err != nil || n != 2 {
		t.Fatalf("count = %d, err = %v", n, err)
	}
	found, _ := store.FindSessions(context.Background(), session.SessionQuery{})
	if len(found) != 1 {
		t.Fatalf("sessions = %d", len(found))
	}
}

func TestRunHandlesEmptyProjectDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "-Users-test-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if stats := run(t, memsession.New(), dir, "", nil); stats != (backfill.Stats{}) {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestRunMissingProjectsDirectoryIsNotAnError(t *testing.T) {
	stats := run(t, memsession.New(), filepath.Join(t.TempDir(), "absent"), "", nil)
	if stats != (backfill.Stats{}) {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestRunSetsGitInfoFromInjectedLookup(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-test-gitrepo", sessionID, `{"a":1}`+"\n")
	writeSession(t, dir, "-Users-test-plain", "plain-session", `{"a":1}`+"\n")
	git := func(_ context.Context, d string) (string, string) {
		if d == "/Users/test/gitrepo" {
			return "https://github.com/test/fake-repo.git", "main"
		}
		return "", ""
	}
	store := memsession.New()

	run(t, store, dir, "", git)

	sess, err := store.GetSession(context.Background(), "", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := sess.String("git_origin"); got != "https://github.com/test/fake-repo.git" {
		t.Errorf("git_origin = %q", got)
	}
	if got, _ := sess.String("git_branch"); got != "main" {
		t.Errorf("git_branch = %q", got)
	}
	plain, err := store.GetSession(context.Background(), "", "plain-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain["git_origin"]; ok {
		t.Errorf("git_origin set for non-git project: %v", plain)
	}
}

func TestRunStampsAccountID(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-test-acctproject", sessionID, `{"type":"human","text":"test"}`+"\n")
	store := memsession.New()

	run(t, store, dir, "backfill-account-uuid", nil)

	sess, err := store.GetSession(context.Background(), "backfill-account-uuid", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := sess.String("account_id"); got != "backfill-account-uuid" {
		t.Errorf("session account_id = %q", got)
	}
	got := lines(t, store, "backfill-account-uuid", sessionID)
	if len(got) != 1 {
		t.Fatalf("lines = %d", len(got))
	}
	if acct, _ := got[0].String("account_id"); acct != "backfill-account-uuid" {
		t.Errorf("line account_id = %q", acct)
	}
}

func TestRunStoresMalformedLineAsRawAndSkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-test-raw", sessionID, "not json\n\n   \n"+`{"ok":true}`+"\n")
	store := memsession.New()

	stats := run(t, store, dir, "", nil)

	if stats.Lines != 2 {
		t.Fatalf("lines upserted = %d", stats.Lines)
	}
	got := lines(t, store, "", sessionID)
	if len(got) != 2 {
		t.Fatalf("stored lines = %d", len(got))
	}
	if raw, _ := got[0].Line().String("raw"); raw != "not json" {
		t.Errorf("raw = %q", raw)
	}
	if got[1].Line()["ok"] != true {
		t.Errorf("line 1 = %v", got[1].Line())
	}
}

func TestRunLogsUnreadableFileAndContinues(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-test-mixed", sessionID, `{"a":1}`+"\n")
	// A directory named like a transcript cannot be read as a file.
	bad := filepath.Join(dir, "-Users-test-mixed", "broken.jsonl")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	store := memsession.New()

	stats, err := backfill.Run(context.Background(), store, dir, "", nil, slog.New(slog.NewTextHandler(&logs, nil)))

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Sessions != 2 || stats.Lines != 1 {
		t.Errorf("stats = %+v", stats)
	}
	if !strings.Contains(logs.String(), bad) {
		t.Errorf("log does not mention %s: %s", bad, logs.String())
	}
	if n, _ := store.CountTranscriptLines(context.Background(), "", sessionID); n != 1 {
		t.Errorf("good session lines = %d", n)
	}
}

func TestRunReturnsContextErrorWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-test-x", sessionID, `{"a":1}`+"\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := backfill.Run(ctx, memsession.New(), dir, "", nil, quietLogger())

	if err == nil {
		t.Fatal("expected context error")
	}
}
