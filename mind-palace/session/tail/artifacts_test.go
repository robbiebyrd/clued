package tail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const account = "acc-watcher"

var host = &session.HostInfo{Hostname: "test"}

func blobs(t *testing.T, st *memsession.Store, sessionID string) []session.Doc {
	t.Helper()
	docs, err := st.Blobs(context.Background(), account, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func blobNamed(t *testing.T, st *memsession.Store, sessionID, blobType, name string) session.Doc {
	t.Helper()
	for _, d := range blobs(t, st, sessionID) {
		if d["blob_type"] == blobType && d["name"] == name {
			return d
		}
	}
	return nil
}

func subLines(t *testing.T, st *memsession.Store, sessionID, agent string) []session.Doc {
	t.Helper()
	docs, err := st.SubagentLines(context.Background(), account, sessionID, agent)
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWatchArtifactsCapturesToolResultBlob(t *testing.T) {
	st := memsession.New()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	mkdir(t, sessionDir)
	stop := WatchArtifacts("sess-tr", sessionDir, filepath.Join(root, "fh"), account, host, st, fast)
	defer stop()

	dir := filepath.Join(sessionDir, "tool-results")
	mkdir(t, dir)
	write(t, filepath.Join(dir, "blob1.txt"), "tool output here")

	eventually(t, "tool-result blob", func() bool { return blobNamed(t, st, "sess-tr", "tool-result", "blob1.txt") != nil })
	d := blobNamed(t, st, "sess-tr", "tool-result", "blob1.txt")
	if d["content"] != "tool output here" || d["encoding"] != "utf8" {
		t.Fatalf("unexpected blob: %v", d)
	}
}

func TestWatchArtifactsCapturesFileHistoryAsBase64(t *testing.T) {
	st := memsession.New()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	fh := filepath.Join(root, "fh", "sess-fh")
	mkdir(t, sessionDir)
	stop := WatchArtifacts("sess-fh", sessionDir, fh, account, host, st, fast)
	defer stop()

	mkdir(t, fh)
	write(t, filepath.Join(fh, "abc123@v1"), "binary file content")

	eventually(t, "file-history blob", func() bool { return blobNamed(t, st, "sess-fh", "file-history", "abc123@v1") != nil })
	d := blobNamed(t, st, "sess-fh", "file-history", "abc123@v1")
	raw, err := base64.StdEncoding.DecodeString(d["content"].(string))
	if err != nil || string(raw) != "binary file content" || d["encoding"] != "base64" {
		t.Fatalf("unexpected blob: %v (err %v)", d, err)
	}
}

func TestWatchArtifactsTailsSubagentJSONLAndMeta(t *testing.T) {
	st := memsession.New()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	subDir := filepath.Join(sessionDir, "subagents")
	mkdir(t, subDir)
	agent := "agent-aabbccdd"
	write(t, filepath.Join(subDir, agent+".jsonl"), "{\"type\":\"user\",\"uuid\":\"u1\"}\nnot json\n{\"type\":\"assistant\",\"uuid\":\"u2\"}\n")
	write(t, filepath.Join(subDir, agent+".meta.json"), `{"agentId":"`+agent+`"}`)
	stop := WatchArtifacts("sess-sub", sessionDir, filepath.Join(root, "fh"), account, host, st, fast)
	defer stop()

	eventually(t, "3 subagent lines", func() bool { return len(subLines(t, st, "sess-sub", agent)) == 3 })
	lines := subLines(t, st, "sess-sub", agent)
	if lines[1]["line"].(map[string]any)["raw"] != "not json" {
		t.Fatalf("invalid JSON not wrapped: %v", lines[1])
	}
	if lines[0]["line"].(map[string]any)["uuid"] != "u1" {
		t.Fatalf("line not decoded: %v", lines[0])
	}
	if h, ok := lines[0]["host"].(session.Doc); !ok || h["hostname"] != "test" {
		t.Fatalf("host not passed through: %v", lines[0]["host"])
	}
	eventually(t, "meta blob", func() bool { return blobNamed(t, st, "sess-sub", "subagent-meta", agent+".meta.json") != nil })
	if d := blobNamed(t, st, "sess-sub", "subagent-meta", agent+".meta.json"); d["encoding"] != "utf8" {
		t.Fatalf("unexpected meta blob: %v", d)
	}
}

func TestWatchArtifactsUpdatesBlobWhenFileOverwritten(t *testing.T) {
	st := memsession.New()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	dir := filepath.Join(sessionDir, "tool-results")
	mkdir(t, dir)
	f := filepath.Join(dir, "mutable.txt")
	write(t, f, "version 1")
	stop := WatchArtifacts("sess-mut", sessionDir, filepath.Join(root, "fh"), account, host, st, fast)
	defer stop()

	content := func() any {
		if d := blobNamed(t, st, "sess-mut", "tool-result", "mutable.txt"); d != nil {
			return d["content"]
		}
		return nil
	}
	eventually(t, "version 1", func() bool { return content() == "version 1" })
	write(t, f, "version 2")
	bumpMtime(t, f)
	eventually(t, "version 2", func() bool { return content() == "version 2" })
	if n := len(blobs(t, st, "sess-mut")); n != 1 {
		t.Fatalf("blob duplicated on overwrite: %d", n)
	}
}

// bumpMtime moves the file's mtime forward so a coarse filesystem clock
// cannot hide the overwrite from the mtime comparison.
func bumpMtime(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	later := info.ModTime().Add(2e9)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// recordingStore wraps a real store and records every subagent line write, so
// a second tailer re-reading the file shows up even though upserts converge.
type recordingStore struct {
	*memsession.Store
	mu     sync.Mutex
	writes []int
}

func (r *recordingStore) UpsertSubagentLine(ctx context.Context, l session.SubagentLine) error {
	r.mu.Lock()
	r.writes = append(r.writes, l.Seq)
	r.mu.Unlock()
	return r.Store.UpsertSubagentLine(ctx, l)
}

func (r *recordingStore) seqs() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.writes...)
}

func TestWatchArtifactsDoesNotStartDuplicateTailerWhenMtimeChanges(t *testing.T) {
	st := &recordingStore{Store: memsession.New()}
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	subDir := filepath.Join(sessionDir, "subagents")
	mkdir(t, subDir)
	agent := "agent-noduptest"
	f := filepath.Join(subDir, agent+".jsonl")
	write(t, f, "{\"type\":\"user\"}\n")
	stop := WatchArtifacts("sess-nodup", sessionDir, filepath.Join(root, "fh"), account, host, st, fast)
	defer stop()

	eventually(t, "first line", func() bool { return len(st.seqs()) == 1 })
	appendTo(t, f, "{\"type\":\"assistant\"}\n")
	bumpMtime(t, f)
	eventually(t, "second line", func() bool { return len(st.seqs()) >= 2 })
	time.Sleep(10 * fast.Interval) // a duplicate tailer would have re-written by now

	if got := st.seqs(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("each line must be written exactly once with seq 0,1; got %v", got)
	}
	lines := subLines(t, st.Store, "sess-nodup", agent)
	if len(lines) != 2 || lines[1]["line"].(map[string]any)["type"] != "assistant" {
		t.Fatalf("unexpected lines: %v", lines)
	}
}

func TestWatchDirRetriesWhenOnChangeFails(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "x")
	var mu sync.Mutex
	calls := 0
	stop := WatchDir(dir, func(name, fullPath string) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	}, Options{Interval: fast.Interval, WaitInterval: fast.WaitInterval, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	defer stop()
	eventually(t, "third attempt", func() bool { mu.Lock(); defer mu.Unlock(); return calls >= 3 })
	time.Sleep(10 * fast.Interval)
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Fatalf("expected no calls after success, got %d", calls)
	}
}

func TestWatchArtifactsRetriesUnreadableBlobWithoutMtimeChange(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	st := memsession.New()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	dir := filepath.Join(sessionDir, "tool-results")
	mkdir(t, dir)
	f := filepath.Join(dir, "locked.txt")
	write(t, f, "late content")
	if err := os.Chmod(f, 0); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	opts := fast
	opts.Logger = slog.New(slog.NewTextHandler(logs, nil))
	stop := WatchArtifacts("sess-lock", sessionDir, filepath.Join(root, "fh"), account, host, st, opts)
	defer stop()

	eventually(t, "read failure logged", func() bool { return strings.Contains(logs.String(), "will retry") })
	if blobNamed(t, st, "sess-lock", "tool-result", "locked.txt") != nil {
		t.Fatal("blob stored from an unreadable file")
	}
	if err := os.Chmod(f, 0o644); err != nil {
		t.Fatal(err)
	}
	eventually(t, "blob after file became readable", func() bool {
		d := blobNamed(t, st, "sess-lock", "tool-result", "locked.txt")
		return d != nil && d["content"] == "late content"
	})
}

// lockedBuffer serialises access so the test can read the log while the
// watcher goroutine writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestMirrorArtifactsIsOneShotAndSkipsUnchangedFiles(t *testing.T) {
	st := &recordingStore{Store: memsession.New()}
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	fh := filepath.Join(root, "fh")
	mkdir(t, filepath.Join(sessionDir, "subagents"))
	mkdir(t, filepath.Join(sessionDir, "tool-results"))
	mkdir(t, fh)
	agent := filepath.Join(sessionDir, "subagents", "agent-1.jsonl")
	write(t, agent, "{\"a\":1}\n{\"b\":")
	write(t, filepath.Join(sessionDir, "subagents", "agent-1.meta.json"), "{}")
	write(t, filepath.Join(sessionDir, "subagents", "ignored.txt"), "x")
	write(t, filepath.Join(sessionDir, "tool-results", "r.txt"), "r")
	write(t, filepath.Join(fh, "h@v1"), "h")
	ctx := context.Background()

	seen, counts, err := MirrorArtifacts(ctx, st, account, "sess-once", sessionDir, fh, host, nil)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (Counts{Subagents: 1, Blobs: 3, Dirs: 3}) {
		t.Fatalf("counts: %+v", counts)
	}
	want := []string{"file-history/h@v1", "subagents/agent-1.jsonl", "subagents/agent-1.meta.json", "tool-results/r.txt"}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("stamped keys: %v", keys)
	}
	if s := seen["subagents/agent-1.jsonl"]; s.Offset != int64(len("{\"a\":1}\n")) || s.Seq != 1 {
		t.Fatalf("partial line consumed: %+v", s)
	}

	again, counts, err := MirrorArtifacts(ctx, st, account, "sess-once", sessionDir, fh, host, seen)
	if err != nil || counts != (Counts{Dirs: 3}) || !reflect.DeepEqual(again, seen) {
		t.Fatalf("second pass changed something: %+v %v %v", counts, err, again)
	}

	appendTo(t, agent, "2}\n")
	bumpMtime(t, agent)
	seen, counts, err = MirrorArtifacts(ctx, st, account, "sess-once", sessionDir, fh, host, seen)
	if err != nil || counts.Subagents != 1 || counts.Blobs != 0 {
		t.Fatalf("third pass: %+v %v", counts, err)
	}
	if got := st.seqs(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("subagent seqs: %v", got)
	}
	if s := seen["subagents/agent-1.jsonl"]; s.Seq != 2 {
		t.Fatalf("seq not advanced: %+v", s)
	}
}

func TestMirrorArtifactsResetsShrunkSubagentTranscript(t *testing.T) {
	st := &recordingStore{Store: memsession.New()}
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	agent := filepath.Join(sessionDir, "subagents", "agent-2.jsonl")
	mkdir(t, filepath.Dir(agent))
	write(t, agent, "{\"a\":1}\n{\"b\":2}\n{\"c\":3}\n")
	ctx := context.Background()
	seen, _, err := MirrorArtifacts(ctx, st, account, "sess-shrink", sessionDir, "", host, nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, agent, "{\"z\":9}\n")
	bumpMtime(t, agent)
	seen, counts, err := MirrorArtifacts(ctx, st, account, "sess-shrink", sessionDir, "", host, seen)
	if err != nil || counts.Subagents != 1 {
		t.Fatalf("after shrink: %+v %v", counts, err)
	}
	if s := seen["subagents/agent-2.jsonl"]; s.Seq != 1 || s.Offset != int64(len("{\"z\":9}\n")) {
		t.Fatalf("stamp after shrink: %+v", s)
	}
	if got := st.seqs(); !reflect.DeepEqual(got, []int{0, 1, 2, 0}) {
		t.Fatalf("seqs: %v", got)
	}
}

// refusingStore rejects every blob with the given error.
type refusingStore struct {
	*memsession.Store
	err error
}

func (r *refusingStore) UpsertBlob(context.Context, session.Blob) error { return r.err }

func TestMirrorArtifactsReportsFailuresPerFileAndLeavesThemUnstamped(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	mkdir(t, filepath.Join(sessionDir, "tool-results"))
	write(t, filepath.Join(sessionDir, "tool-results", "big.txt"), "big")
	st := &refusingStore{Store: memsession.New(), err: session.ErrTooLarge}
	seen, counts, err := MirrorArtifacts(context.Background(), st, account, "sess-fail", sessionDir, "", host, nil)
	if counts.Blobs != 0 || len(seen) != 0 {
		t.Fatalf("failed file stamped: %+v %v", counts, seen)
	}
	fes := FileErrors(err)
	if len(fes) != 1 || fes[0].Key != "tool-results/big.txt" || fes[0].Op != OpStore || !errors.Is(fes[0], session.ErrTooLarge) || fes[0].Stamp.Size != 3 {
		t.Fatalf("file errors: %v", err)
	}
	if !errors.Is(err, session.ErrTooLarge) {
		t.Fatalf("joined error does not unwrap: %v", err)
	}

	if os.Geteuid() != 0 {
		unreadable := filepath.Join(sessionDir, "tool-results", "locked.txt")
		write(t, unreadable, "x")
		if err := os.Chmod(unreadable, 0); err != nil {
			t.Fatal(err)
		}
		_, _, err = MirrorArtifacts(context.Background(), memsession.New(), account, "sess-fail", sessionDir, "", host, nil)
		var found bool
		for _, fe := range FileErrors(err) {
			found = found || (fe.Key == "tool-results/locked.txt" && fe.Op == OpRead)
		}
		if !found {
			t.Fatalf("read failure not reported: %v", err)
		}
	}
}

func TestMirrorArtifactsStopsOnCancelledContext(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	mkdir(t, filepath.Join(sessionDir, "tool-results"))
	write(t, filepath.Join(sessionDir, "tool-results", "r.txt"), "r")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prev := map[string]FileStamp{"tool-results/old.txt": {Size: 1}}
	seen, counts, err := MirrorArtifacts(ctx, memsession.New(), account, "sess-ctx", sessionDir, "", host, prev)
	if !errors.Is(err, context.Canceled) || counts.Blobs != 0 {
		t.Fatalf("expected context error, got %v (%+v)", err, counts)
	}
	if !reflect.DeepEqual(seen, prev) {
		t.Fatalf("previous stamps not kept: %v", seen)
	}
}

func TestReadLinesReportsOffsets(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, f, "{\"a\":1}\r\n\n  \n{\"b\":2}\n{\"c\":")
	lines, end, err := ReadLines(f, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Text != `{"a":1}` || lines[1].Text != `{"b":2}` {
		t.Fatalf("lines: %+v", lines)
	}
	if lines[0].End != 9 || lines[1].End != 21 || end != 21 {
		t.Fatalf("offsets: %+v end %d", lines, end)
	}
	lines, end, err = ReadLines(f, 21)
	if err != nil || len(lines) != 0 || end != 21 {
		t.Fatalf("partial tail: %+v %d %v", lines, end, err)
	}
	if _, _, err := ReadLines(filepath.Join(t.TempDir(), "missing"), 0); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if got := DecodeLine("nope"); got.(map[string]any)["raw"] != "nope" {
		t.Fatalf("DecodeLine: %v", got)
	}
}
