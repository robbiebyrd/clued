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
