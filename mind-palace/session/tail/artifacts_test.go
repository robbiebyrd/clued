package tail

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

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

func TestWatchArtifactsDoesNotStartDuplicateTailerWhenMtimeChanges(t *testing.T) {
	st := memsession.New()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "s")
	subDir := filepath.Join(sessionDir, "subagents")
	mkdir(t, subDir)
	agent := "agent-noduptest"
	f := filepath.Join(subDir, agent+".jsonl")
	write(t, f, "{\"type\":\"user\"}\n")
	stop := WatchArtifacts("sess-nodup", sessionDir, filepath.Join(root, "fh"), account, host, st, fast)
	defer stop()

	eventually(t, "first line", func() bool { return len(subLines(t, st, "sess-nodup", agent)) == 1 })
	appendTo(t, f, "{\"type\":\"assistant\"}\n")
	bumpMtime(t, f)
	eventually(t, "second line", func() bool { return len(subLines(t, st, "sess-nodup", agent)) == 2 })

	lines := subLines(t, st, "sess-nodup", agent)
	if len(lines) != 2 || lines[0]["seq"] != 0 || lines[1]["seq"] != 1 {
		t.Fatalf("unexpected lines: %v", lines)
	}
	if lines[1]["line"].(map[string]any)["type"] != "assistant" {
		t.Fatalf("second line wrong: %v", lines[1])
	}
}
