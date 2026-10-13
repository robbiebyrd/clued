package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// fixtureStore writes a claude-remember store with every file kind.
func fixtureStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"now.md":                "## 09:15 | main\nFixed the daemon restart; tests green.\n\n## 2:30 PM | feature/webdav\nAdded the WebDAV store, conformance suite passes.\n",
		"today-2026-10-11.md":   "## 18:02 | main\nMerged #15 (write guard).\n",
		"recent.md":             "## Week of 2026-10-05\n- shipped bundled binaries\n",
		"archive.md":            "",
		"archive-2026-09-30.md": "## 2026-09-01\nold history\n",
		"remember.md":           "Next: wire the listener.\n",
		"remember.abc123.md":    "Per-session note.\n",
		"identity.md":           "You are Max.\n",
		"config.json":           "{}",
		"notes.txt":             "not memory",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, sub := range []string{"logs", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScanProducesOneRecordPerEntryAndFile(t *testing.T) {
	recs, err := Scan(fixtureStore(t))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range recs {
		got = append(got, r.Kind+":"+r.File+":"+r.Time+":"+r.Day+":"+r.Branch+":"+r.SessionID)
	}
	want := []string{
		"archive:archive-2026-09-30.md::2026-09-30::",
		"identity:identity.md::::",
		"entry:now.md:09:15::main:",
		"entry:now.md:2:30 PM::feature/webdav:",
		"recent:recent.md::::",
		"handoff:remember.abc123.md::::abc123",
		"handoff:remember.md::::",
		"day:today-2026-10-11.md:18:02:2026-10-11:main:",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, r := range recs {
		if r.Text == "" || r.Hash != HashText(r.Text) || len(r.Hash) != 64 {
			t.Errorf("record %s has text %q hash %q", r.File, r.Text, r.Hash)
		}
	}
	if recs[2].Text != "## 09:15 | main\nFixed the daemon restart; tests green." {
		t.Errorf("entry text = %q", recs[2].Text)
	}
}

func TestEntryMovedIntoTodayKeepsItsHash(t *testing.T) {
	now := parseEntries(KindEntry, "now.md", "", "## 09:15 | main\nSame work.\n")
	day := parseEntries(KindDay, "today-2026-10-12.md", "2026-10-12", "## 09:15 | main\nSame work.\n\n")
	if len(now) != 1 || len(day) != 1 || now[0].Hash != day[0].Hash {
		t.Errorf("hashes differ: %+v %+v", now, day)
	}
	if n := parseEntries(KindEntry, "now.md", "", "## 09:15 | main\n\n## 09:16 | main\nreal\n"); len(n) != 1 || n[0].Time != "09:16" {
		t.Errorf("empty entry should be dropped: %+v", n)
	}
	if n := parseEntries(KindEntry, "now.md", "", "## 9:00 AM standup notes\nprose\n"); len(n) != 0 {
		t.Errorf("a time without a pipe is prose, not an entry: %+v", n)
	}
}

func TestLocateAndSlug(t *testing.T) {
	if Slug("/home/alice/projects/my-app.v2") != "-home-alice-projects-my-app-v2" {
		t.Errorf("slug = %s", Slug("/home/alice/projects/my-app.v2"))
	}
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(cwd, ".remember"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".remember", Slug(cwd)), 0o755); err != nil {
		t.Fatal(err)
	}
	got := Locate(cwd, home)
	if len(got) != 2 || got[0] != filepath.Join(cwd, ".remember") || got[1] != filepath.Join(home, ".remember", Slug(cwd)) {
		t.Errorf("locate = %v", got)
	}
	if got := Locate(t.TempDir(), t.TempDir()); len(got) != 0 {
		t.Errorf("nothing should be located: %v", got)
	}
}

func TestStateRoundTripAndNew(t *testing.T) {
	stateDir := t.TempDir()
	st, err := LoadState(stateDir, "/some/store")
	if err != nil || len(st.Hashes) != 0 {
		t.Fatalf("empty state: %+v %v", st, err)
	}
	recs := []Record{{Hash: "a", Text: "a"}, {Hash: "b", Text: "b"}, {Hash: "a", Text: "a again"}}
	if fresh := New(recs, st); len(fresh) != 2 {
		t.Errorf("new = %+v", fresh)
	}
	st.Add("a")
	if err := st.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(StatePath(stateDir, "/some/store"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state file: %v %v", info, err)
	}
	again, err := LoadState(stateDir, "/some/store")
	if err != nil || !again.Has("a") || again.Has("b") {
		t.Fatalf("reloaded: %+v %v", again, err)
	}
	if fresh := New(recs, again); len(fresh) != 1 || fresh[0].Hash != "b" {
		t.Errorf("new after save = %+v", fresh)
	}
}

func TestNewestSession(t *testing.T) {
	projects := t.TempDir()
	cwd := "/home/alice/proj"
	dir := filepath.Join(projects, Slug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"old.jsonl", "new.jsonl", "ignored.txt"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := fileTime(i)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if got := NewestSession(projects, cwd); got != "new" {
		t.Errorf("newest = %q", got)
	}
	if got := NewestSession(projects, "/nowhere"); got != "" {
		t.Errorf("missing project dir = %q", got)
	}
}

// daemonStub records the events posted to it and can refuse them.
type daemonStub struct {
	srv    *httptest.Server
	events []session.Doc
	refuse bool
}

func newDaemonStub(t *testing.T) *daemonStub {
	t.Helper()
	d := &daemonStub{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.refuse {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/event" || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var doc session.Doc
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		d.events = append(d.events, doc)
		w.Write([]byte("ok"))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *daemonStub) port(t *testing.T) int {
	t.Helper()
	u, _ := url.Parse(d.srv.URL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

func TestEventDocShape(t *testing.T) {
	ev := Event{Record: Record{Kind: KindEntry, File: "now.md", Time: "09:15", Branch: "main", Text: "x", Hash: "h"}, Cwd: "/p", Dir: "/p/.remember", SessionID: "s1"}
	d := ev.Doc()
	if d["hook_event_name"] != EventName || d["session_id"] != "s1" || d["cwd"] != "/p" || d["saved_at"] == "" {
		t.Errorf("doc = %v", d)
	}
	mem := d.Map("memory")
	if mem["kind"] != "entry" || mem["time"] != "09:15" || mem["branch"] != "main" || mem["slug"] != "-p" || mem["remember_dir"] != "/p/.remember" || mem["hash"] != "h" {
		t.Errorf("memory = %v", mem)
	}
	if _, has := mem["day"]; has {
		t.Error("empty day should be omitted")
	}
	// A per-session handoff names its own session.
	ev2 := Event{Record: Record{Kind: KindHandoff, File: "remember.abc.md", SessionID: "abc", Text: "t", Hash: "h2"}, Cwd: "/p", SessionID: "s1"}
	if ev2.Doc()["session_id"] != "abc" {
		t.Errorf("handoff session = %v", ev2.Doc()["session_id"])
	}
	noSession := Event{Record: Record{Text: "t"}}
	if _, has := noSession.Doc()["session_id"]; has {
		t.Error("no session should be omitted")
	}
}

func TestIngestPostsOnlyNewRecordsAndIsIdempotent(t *testing.T) {
	store := fixtureStore(t)
	stub := newDaemonStub(t)
	opts := IngestOptions{Dirs: []string{store}, Cwd: "/home/alice/proj", StateDir: t.TempDir(), SessionID: "sess-1", Poster: DaemonPoster{Port: stub.port(t)}}
	res, err := Ingest(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 8 || res.Posted != 8 || res.Session != "sess-1" || len(stub.events) != 8 {
		t.Fatalf("first run: %+v, %d events", res, len(stub.events))
	}
	if stub.events[0].Map("memory")["kind"] != "archive" || stub.events[0]["session_id"] != "sess-1" {
		t.Errorf("first event = %v", stub.events[0])
	}
	res, err = Ingest(context.Background(), opts)
	if err != nil || res.Posted != 0 || len(stub.events) != 8 {
		t.Fatalf("second run should post nothing: %+v %v, %d events", res, err, len(stub.events))
	}
	// A new entry appended to now.md is the only thing posted next time.
	f, err := os.OpenFile(filepath.Join(store, "now.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n## 16:40 | main\nWired the listener.\n")
	f.Close()
	res, err = Ingest(context.Background(), opts)
	if err != nil || res.Posted != 1 || stub.events[8].Map("memory")["time"] != "16:40" {
		t.Fatalf("third run: %+v %v", res, err)
	}
}

func TestIngestFallsBackToTheWALAndKeepsStateHonest(t *testing.T) {
	store := fixtureStore(t)
	stub := newDaemonStub(t)
	stub.refuse = true
	walPath := filepath.Join(t.TempDir(), "events.wal")
	stateDir := t.TempDir()
	opts := IngestOptions{Dirs: []string{store}, Cwd: "/p", StateDir: stateDir, Poster: DaemonPoster{Port: stub.port(t), WalPath: walPath}}
	res, err := Ingest(context.Background(), opts)
	if err != nil || res.Posted != 8 {
		t.Fatalf("wal fallback: %+v %v", res, err)
	}
	data, err := os.ReadFile(walPath)
	if err != nil || strings.Count(string(data), "\n") != 8 || !strings.Contains(string(data), `"hook_event_name":"MemorySaved"`) {
		t.Errorf("wal = %q %v", data, err)
	}
	// Without a WAL a refusal is an error and nothing is marked as posted.
	opts2 := IngestOptions{Dirs: []string{store}, Cwd: "/p", StateDir: t.TempDir(), Poster: DaemonPoster{Port: stub.port(t)}}
	if res, err := Ingest(context.Background(), opts2); err == nil || res.Posted != 0 {
		t.Errorf("refusal without wal: %+v %v", res, err)
	}
	st, _ := LoadState(opts2.StateDir, store)
	if len(st.Hashes) != 0 {
		t.Errorf("state claims %d records after a failed post", len(st.Hashes))
	}
}

func TestIngestLocatesStoresAndNewestSession(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "proj")
	storeDir := filepath.Join(home, ".remember", Slug(cwd))
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "remember.md"), []byte("handoff"), 0o644); err != nil {
		t.Fatal(err)
	}
	projects := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projects, Slug(cwd)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, Slug(cwd), "sess-9.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := newDaemonStub(t)
	res, err := Ingest(context.Background(), IngestOptions{Cwd: cwd, Home: home, ProjectsDir: projects, StateDir: t.TempDir(), Poster: DaemonPoster{Port: stub.port(t)}})
	if err != nil || res.Posted != 1 || res.Session != "sess-9" || len(res.Dirs) != 1 {
		t.Fatalf("got %+v %v", res, err)
	}
	if stub.events[0]["session_id"] != "sess-9" || stub.events[0]["cwd"] != cwd {
		t.Errorf("event = %v", stub.events[0])
	}
	// No store at all is not an error.
	if res, err := Ingest(context.Background(), IngestOptions{Cwd: t.TempDir(), Home: t.TempDir(), StateDir: t.TempDir()}); err != nil || res.Posted != 0 {
		t.Errorf("no store: %+v %v", res, err)
	}
}
