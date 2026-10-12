package catchup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const account = "acc-catchup"

// countingStore records every write the catch-up makes to the store.
type countingStore struct {
	*memsession.Store
	mu         sync.Mutex
	lineCalls  int   // UpsertTranscriptLines calls
	batchSizes []int // lines per call
	subagent   int   // UpsertSubagentLine calls
	blob       int   // UpsertBlob calls
	refuse     func(session.Blob) error
}

func newStore() *countingStore { return &countingStore{Store: memsession.New()} }

func (c *countingStore) UpsertTranscriptLines(ctx context.Context, lines []session.TranscriptLine) error {
	c.mu.Lock()
	c.lineCalls++
	c.batchSizes = append(c.batchSizes, len(lines))
	c.mu.Unlock()
	return c.Store.UpsertTranscriptLines(ctx, lines)
}

func (c *countingStore) UpsertSubagentLine(ctx context.Context, l session.SubagentLine) error {
	c.mu.Lock()
	c.subagent++
	c.mu.Unlock()
	return c.Store.UpsertSubagentLine(ctx, l)
}

func (c *countingStore) UpsertBlob(ctx context.Context, b session.Blob) error {
	c.mu.Lock()
	c.blob++
	refuse := c.refuse
	c.mu.Unlock()
	if refuse != nil {
		if err := refuse(b); err != nil {
			return err
		}
	}
	return c.Store.UpsertBlob(ctx, b)
}

func (c *countingStore) calls() (lines, subagent, blob int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lineCalls, c.subagent, c.blob
}

// fixture is a projects directory with one session's transcript, its
// artifact directory and a file-history directory, plus a state directory.
type fixture struct {
	t          *testing.T
	sid        string
	projects   string
	transcript string
	sessionDir string
	fhRoot     string
	stateDir   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, sid: "sess-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")), projects: filepath.Join(root, "projects")}
	project := filepath.Join(f.projects, "-home-me-repo")
	f.transcript = filepath.Join(project, f.sid+".jsonl")
	f.sessionDir = filepath.Join(project, f.sid)
	f.fhRoot = filepath.Join(root, "file-history")
	f.stateDir = filepath.Join(root, "state", "catchup")
	mkdir(t, project)
	return f
}

func (f *fixture) paths() Paths {
	return Paths{SessionID: f.sid, AccountID: account, Transcript: f.transcript, ProjectsDir: f.projects, FileHistoryDir: f.fhRoot, Host: &session.HostInfo{Hostname: "test"}}
}

func (f *fixture) run(st session.Store) (Report, error) {
	return Run(context.Background(), st, f.paths(), f.stateDir)
}

func (f *fixture) mustRun(st session.Store) Report {
	f.t.Helper()
	rep, err := f.run(st)
	if err != nil {
		f.t.Fatalf("Run: %v", err)
	}
	return rep
}

func (f *fixture) state() state {
	f.t.Helper()
	st, err := loadState(filepath.Join(f.stateDir, f.sid+".json"))
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

// bumpMtime moves a file's mtime forward so a coarse filesystem clock cannot
// hide an overwrite from the stamp comparison.
func bumpMtime(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	later := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func lines(n int, from int) string {
	var b strings.Builder
	for i := from; i < from+n; i++ {
		fmt.Fprintf(&b, "{\"uuid\":\"u%d\"}\n", i)
	}
	return b.String()
}

func storedLines(t *testing.T, st session.Store, sid string) []session.Doc {
	t.Helper()
	docs, err := st.TranscriptLines(context.Background(), session.LineQuery{AccountID: account, SessionID: sid, Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func seqs(docs []session.Doc) []int {
	out := make([]int, 0, len(docs))
	for _, d := range docs {
		out = append(out, d["seq"].(int))
	}
	return out
}

func uuid(d session.Doc) string { return d["line"].(map[string]any)["uuid"].(string) }

func TestRunResumesFromSavedOffsetWithNextSeq(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(3, 0))
	rep := f.mustRun(st)
	if rep.Lines != 3 || rep.Reset || rep.Skipped {
		t.Fatalf("first report: %+v", rep)
	}
	saved := f.state()
	if saved.Seq != 3 || saved.Offset != int64(len(lines(3, 0))) {
		t.Fatalf("saved state: %+v", saved)
	}

	appendTo(t, f.transcript, lines(2, 3))
	rep = f.mustRun(st)
	if rep.Lines != 2 || rep.Reset {
		t.Fatalf("second report: %+v", rep)
	}
	docs := storedLines(t, st, f.sid)
	if got := seqs(docs); len(got) != 5 || got[3] != 3 || got[4] != 4 {
		t.Fatalf("seqs: %v", got)
	}
	if uuid(docs[3]) != "u3" || uuid(docs[4]) != "u4" {
		t.Fatalf("resumed lines: %v %v", docs[3], docs[4])
	}
	if h, ok := docs[0]["host"].(session.Doc); !ok || h["hostname"] != "test" {
		t.Fatalf("host not stamped: %v", docs[0]["host"])
	}
	if lineCalls, _, _ := st.calls(); lineCalls != 2 {
		t.Fatalf("expected one upsert per run, got %d", lineCalls)
	}
}

func TestRunMakesNoStoreCallsWithoutNewData(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(2, 0))
	write(t, filepath.Join(f.sessionDir, "subagents", "agent-a.jsonl"), "{\"type\":\"user\"}\n")
	write(t, filepath.Join(f.sessionDir, "subagents", "agent-a.meta.json"), `{"agentId":"agent-a"}`)
	write(t, filepath.Join(f.sessionDir, "tool-results", "r1.txt"), "result")
	write(t, filepath.Join(f.fhRoot, f.sid, "abc@v1"), "history")
	rep := f.mustRun(st)
	if rep.Lines != 2 || rep.Subagents != 1 || rep.Blobs != 3 {
		t.Fatalf("first report: %+v", rep)
	}
	l, s, b := st.calls()
	rep = f.mustRun(st)
	if rep != (Report{}) {
		t.Fatalf("second report should be empty: %+v", rep)
	}
	if l2, s2, b2 := st.calls(); l2 != l || s2 != s || b2 != b {
		t.Fatalf("store called on the second run: lines %d->%d subagent %d->%d blob %d->%d", l, l2, s, s2, b, b2)
	}
}

func TestRunResetsWhenTranscriptShrinksAndReupsertIsIdempotent(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(10, 0))
	f.mustRun(st)

	write(t, f.transcript, lines(4, 100)) // rewritten and shorter
	rep := f.mustRun(st)
	if !rep.Reset || rep.Lines != 4 {
		t.Fatalf("report after truncation: %+v", rep)
	}
	saved := f.state()
	if saved.Seq != 4 || saved.Offset != int64(len(lines(4, 100))) {
		t.Fatalf("state after truncation: %+v", saved)
	}
	docs := storedLines(t, st, f.sid)
	if len(docs) != 10 {
		t.Fatalf("re-upsert duplicated lines: %d stored", len(docs))
	}
	for i := 0; i < 4; i++ {
		if docs[i]["seq"] != i || uuid(docs[i]) != fmt.Sprintf("u%d", 100+i) {
			t.Fatalf("seq %d not overwritten in place: %v", i, docs[i])
		}
	}
	if n, _ := st.CountTranscriptLines(context.Background(), account, f.sid); n != 10 {
		t.Fatalf("count: %d", n)
	}
}

func TestRunHoldsTrailingPartialLine(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, "{\"uuid\":\"u0\"}\n{\"uuid\":")
	rep := f.mustRun(st)
	if rep.Lines != 1 {
		t.Fatalf("partial line sent: %+v", rep)
	}
	if saved := f.state(); saved.Offset != int64(len("{\"uuid\":\"u0\"}\n")) || saved.Seq != 1 {
		t.Fatalf("state: %+v", saved)
	}
	appendTo(t, f.transcript, "\"u1\"}\r\n\n")
	rep = f.mustRun(st)
	if rep.Lines != 1 {
		t.Fatalf("completed line not sent: %+v", rep)
	}
	docs := storedLines(t, st, f.sid)
	if len(docs) != 2 || uuid(docs[1]) != "u1" || docs[1]["seq"] != 1 {
		t.Fatalf("lines: %v", docs)
	}
	if saved := f.state(); saved.Offset != int64(len("{\"uuid\":\"u0\"}\n{\"uuid\":\"u1\"}\r\n\n")) {
		t.Fatalf("offset did not consume the trailing blank line: %+v", saved)
	}
}

func TestRunUpsertsInBatchesOf500(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(1201, 0))
	rep := f.mustRun(st)
	if rep.Lines != 1201 {
		t.Fatalf("report: %+v", rep)
	}
	st.mu.Lock()
	sizes := append([]int(nil), st.batchSizes...)
	st.mu.Unlock()
	if len(sizes) != 3 || sizes[0] != 500 || sizes[1] != 500 || sizes[2] != 201 {
		t.Fatalf("batch sizes: %v", sizes)
	}
	docs := storedLines(t, st, f.sid)
	if len(docs) != 1201 || docs[1200]["seq"] != 1200 || uuid(docs[1200]) != "u1200" {
		t.Fatalf("last line: %v (of %d)", docs[len(docs)-1], len(docs))
	}
}

// failOnce fails the second upsert call, then recovers.
type failOnce struct {
	*countingStore
	fail int
}

func (s *failOnce) UpsertTranscriptLines(ctx context.Context, l []session.TranscriptLine) error {
	s.countingStore.mu.Lock()
	n := s.countingStore.lineCalls
	s.countingStore.mu.Unlock()
	if n == s.fail {
		s.countingStore.mu.Lock()
		s.countingStore.lineCalls++
		s.countingStore.mu.Unlock()
		return errors.New("store down")
	}
	return s.countingStore.UpsertTranscriptLines(ctx, l)
}

func TestRunKeepsOffsetOfLastStoredBatchOnStoreError(t *testing.T) {
	f := newFixture(t)
	st := &failOnce{countingStore: newStore(), fail: 1}
	write(t, f.transcript, lines(1201, 0))
	rep, err := f.run(st)
	if err == nil || rep.Lines != 500 {
		t.Fatalf("expected the second batch to fail after 500 lines: %+v, %v", rep, err)
	}
	if saved := f.state(); saved.Seq != 500 || saved.Offset != int64(len(lines(500, 0))) {
		t.Fatalf("state after failure: %+v", saved)
	}
	rep = f.mustRun(st)
	if rep.Lines != 701 {
		t.Fatalf("retry report: %+v", rep)
	}
	if docs := storedLines(t, st, f.sid); len(docs) != 1201 || uuid(docs[500]) != "u500" {
		t.Fatalf("lines after retry: %d", len(docs))
	}
}

func TestRunMirrorsChangedArtifactsOnceAndSkipsUnchanged(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(1, 0))
	sub := filepath.Join(f.sessionDir, "subagents", "agent-x.jsonl")
	write(t, sub, "{\"type\":\"user\",\"uuid\":\"s0\"}\n")
	fh := filepath.Join(f.fhRoot, f.sid, "abc@v1")
	write(t, fh, "v1")
	rep := f.mustRun(st)
	if rep.Subagents != 1 || rep.Blobs != 1 {
		t.Fatalf("first report: %+v", rep)
	}
	_, s1, b1 := st.calls()

	appendTo(t, sub, "not json\n{\"type\":\"assistant\",\"uuid\":\"s2\"}\n")
	bumpMtime(t, sub)
	write(t, fh, "v2")
	bumpMtime(t, fh)
	rep = f.mustRun(st)
	if rep.Subagents != 2 || rep.Blobs != 1 {
		t.Fatalf("second report: %+v", rep)
	}
	if _, s2, b2 := st.calls(); s2 != s1+2 || b2 != b1+1 {
		t.Fatalf("expected two subagent and one blob write: subagent %d->%d blob %d->%d", s1, s2, b1, b2)
	}
	docs, err := st.SubagentLines(context.Background(), account, f.sid, "agent-x")
	if err != nil || len(docs) != 3 {
		t.Fatalf("subagent lines: %v %v", docs, err)
	}
	if docs[1]["seq"] != 1 || docs[1]["line"].(map[string]any)["raw"] != "not json" || docs[2]["seq"] != 2 {
		t.Fatalf("subagent seq continued wrongly: %v", docs)
	}
	blobs, err := st.Blobs(context.Background(), account, f.sid)
	if err != nil || len(blobs) != 1 {
		t.Fatalf("blobs: %v %v", blobs, err)
	}
	raw, _ := base64.StdEncoding.DecodeString(blobs[0]["content"].(string))
	if string(raw) != "v2" || blobs[0]["encoding"] != "base64" || blobs[0]["name"] != "abc@v1" {
		t.Fatalf("file-history blob: %v", blobs[0])
	}

	rep = f.mustRun(st)
	if rep != (Report{}) {
		t.Fatalf("third run should push nothing: %+v", rep)
	}
	if _, s3, b3 := st.calls(); s3 != s1+2 || b3 != b1+1 {
		t.Fatal("store called on the unchanged run")
	}
}

func TestRunRecordsRefusedBlobAndDoesNotRetryIt(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	st.refuse = func(b session.Blob) error {
		if b.Name == "huge.txt" {
			return fmt.Errorf("413: %w", session.ErrTooLarge)
		}
		return nil
	}
	write(t, f.transcript, lines(1, 0))
	huge := filepath.Join(f.sessionDir, "tool-results", "huge.txt")
	write(t, huge, strings.Repeat("x", 1000))
	write(t, filepath.Join(f.sessionDir, "tool-results", "small.txt"), "ok")
	rep, err := f.run(st)
	if err != nil {
		t.Fatalf("a refused blob must not fail the run: %v", err)
	}
	if rep.Refused != 1 || rep.Blobs != 1 {
		t.Fatalf("report: %+v", rep)
	}
	saved := f.state()
	if !saved.Refused["tool-results/huge.txt"] {
		t.Fatalf("refusal not recorded: %+v", saved)
	}
	if _, ok := saved.Artifacts["tool-results/huge.txt"]; !ok {
		t.Fatalf("refused file not stamped: %+v", saved)
	}
	_, _, b1 := st.calls()
	rep = f.mustRun(st)
	if _, _, b2 := st.calls(); b2 != b1 || rep.Refused != 0 {
		t.Fatalf("refused blob retried: calls %d->%d report %+v", b1, b2, rep)
	}

	// Once the file changes and is accepted, the refusal is cleared.
	st.mu.Lock()
	st.refuse = nil
	st.mu.Unlock()
	write(t, huge, "now small")
	bumpMtime(t, huge)
	rep = f.mustRun(st)
	if rep.Blobs != 1 || f.state().Refused["tool-results/huge.txt"] {
		t.Fatalf("changed file not re-sent or refusal not cleared: %+v %+v", rep, f.state())
	}
}

func TestRunReturnsOtherStoreErrorsAndRetriesTheFile(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	st.refuse = func(session.Blob) error { return errors.New("network") }
	write(t, f.transcript, lines(1, 0))
	write(t, filepath.Join(f.sessionDir, "tool-results", "r.txt"), "x")
	rep, err := f.run(st)
	if err == nil || rep.Lines != 1 || rep.Blobs != 0 {
		t.Fatalf("expected an error and the transcript pushed: %+v %v", rep, err)
	}
	if saved := f.state(); saved.Seq != 1 || len(saved.Artifacts) != 0 || len(saved.Refused) != 0 {
		t.Fatalf("state after store error: %+v", saved)
	}
	st.mu.Lock()
	st.refuse = nil
	st.mu.Unlock()
	if rep := f.mustRun(st); rep.Blobs != 1 {
		t.Fatalf("blob not retried: %+v", rep)
	}
}

func TestRunSkipsWhenLockHeld(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(1, 0))
	mkdir(t, f.stateDir)
	lock := filepath.Join(f.stateDir, f.sid+".lock")
	write(t, lock, "")
	rep, err := f.run(st)
	if err != nil || !rep.Skipped || rep.Lines != 0 {
		t.Fatalf("expected a skip: %+v %v", rep, err)
	}
	if l, _, _ := st.calls(); l != 0 {
		t.Fatal("store called while the lock was held")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("the other run's lock was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.stateDir, f.sid+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state written on a skipped run: %v", err)
	}
}

func TestRunReclaimsStaleLockAndReleasesIt(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(1, 0))
	mkdir(t, f.stateDir)
	lock := filepath.Join(f.stateDir, f.sid+".lock")
	write(t, lock, "")
	old := time.Now().Add(-2 * staleAfter)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	rep := f.mustRun(st)
	if rep.Skipped || rep.Lines != 1 {
		t.Fatalf("stale lock not reclaimed: %+v", rep)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock not released after the run: %v", err)
	}
}

func TestRunWritesStateAtomically(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(2, 0))
	f.mustRun(st)
	entries, err := os.ReadDir(f.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != f.sid+".json" {
		t.Fatalf("state dir holds %v; expected only the state file (no temp file, no lock)", names)
	}
	data, err := os.ReadFile(filepath.Join(f.stateDir, f.sid+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("state file is not JSON: %v\n%s", err, data)
	}
	if raw["offset"].(float64) != float64(len(lines(2, 0))) || raw["seq"].(float64) != 2 {
		t.Fatalf("state content: %v", raw)
	}

	// A write that cannot be completed leaves the previous state intact.
	if err := saveState(filepath.Join(f.stateDir, "missing-dir", "x.json"), state{Seq: 9}); err == nil {
		t.Fatal("expected saveState to fail in a missing directory")
	}
	if got := f.state(); got.Seq != 2 {
		t.Fatalf("state changed by a failed save: %+v", got)
	}
}

func TestRunRejectsTranscriptOutsideProjectsDir(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	outside := filepath.Join(t.TempDir(), "other.jsonl")
	write(t, outside, lines(1, 0))
	cases := map[string]Paths{
		"elsewhere":   {SessionID: f.sid, AccountID: account, Transcript: outside, ProjectsDir: f.projects},
		"dotdot":      {SessionID: f.sid, AccountID: account, Transcript: filepath.Join(f.projects, "..", "x", "t.jsonl"), ProjectsDir: f.projects},
		"sibling":     {SessionID: f.sid, AccountID: account, Transcript: f.projects + "-evil/t.jsonl", ProjectsDir: f.projects},
		"relative":    {SessionID: f.sid, AccountID: account, Transcript: "projects/t.jsonl", ProjectsDir: f.projects},
		"no-projects": {SessionID: f.sid, AccountID: account, Transcript: f.transcript, ProjectsDir: ""},
	}
	for name, p := range cases {
		rep, err := Run(context.Background(), st, p, f.stateDir)
		if !errors.Is(err, ErrOutsideProjects) || rep.Lines != 0 {
			t.Errorf("%s: expected ErrOutsideProjects, got %v (%+v)", name, err, rep)
		}
	}
	bad := f.paths()
	bad.SessionID = "../escape"
	if _, err := Run(context.Background(), st, bad, f.stateDir); !errors.Is(err, ErrBadSessionID) {
		t.Fatalf("expected ErrBadSessionID, got %v", err)
	}
	if _, err := os.Stat(f.stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state directory created for a rejected run")
	}
}

func TestRunWithoutTranscriptFileStillMirrorsArtifacts(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, filepath.Join(f.sessionDir, "tool-results", "r.txt"), "x")
	rep := f.mustRun(st)
	if rep.Lines != 0 || rep.Blobs != 1 || rep.Reset {
		t.Fatalf("report: %+v", rep)
	}
}

func TestRunWithoutFileHistoryDirSkipsIt(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	write(t, f.transcript, lines(1, 0))
	p := f.paths()
	p.FileHistoryDir = ""
	rep, err := Run(context.Background(), st, p, f.stateDir)
	if err != nil || rep.Lines != 1 || rep.Blobs != 0 {
		t.Fatalf("report: %+v %v", rep, err)
	}
}
