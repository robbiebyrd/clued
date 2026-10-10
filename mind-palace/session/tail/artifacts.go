package tail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// WatchDir calls onChange(name, fullPath) for every regular file in path that
// is new or whose mtime changed since the previous poll. Subdirectories are
// ignored and a missing directory is waited for.
func WatchDir(path string, onChange func(name, fullPath string), opts Options) (stop func()) {
	opts = opts.withDefaults()
	mtimes := map[string]int64{}
	return poll(opts, func() bool {
		entries, err := os.ReadDir(path)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if m := info.ModTime().UnixNano(); mtimes[e.Name()] != m {
				mtimes[e.Name()] = m
				onChange(e.Name(), filepath.Join(path, e.Name()))
			}
		}
		return true
	})
}

// WatchArtifacts mirrors a session's side artifacts into the store: subagent
// JSONL files are tailed (one tailer per subagent id, each with its own seq
// counter), subagent meta files and tool results are stored as utf8 blobs, and
// file-history files as base64 blobs. Store errors are logged, never fatal.
func WatchArtifacts(sessionID, sessionDir, fileHistoryPath, accountID string, host *session.HostInfo, store session.Store, opts Options) (stop func()) {
	opts = opts.withDefaults()
	ctx := context.Background()
	saveBlob := func(blobType, name, content, encoding string) {
		err := store.UpsertBlob(ctx, session.Blob{SessionID: sessionID, BlobType: blobType, Name: name, Content: content, Encoding: encoding, AccountID: accountID})
		if err != nil {
			opts.Logger.Error("store blob failed", "blob_type", blobType, "name", name, "err", err)
		}
	}
	readBlob := func(blobType, encoding string, encode func([]byte) string) func(name, fullPath string) {
		return func(name, fullPath string) {
			data, err := os.ReadFile(fullPath)
			if err != nil {
				return
			}
			saveBlob(blobType, name, encode(data), encoding)
		}
	}
	utf8 := func(b []byte) string { return string(b) }

	var tailers []func()
	started := map[string]bool{}
	onSubagentFile := func(name, fullPath string) {
		switch {
		case strings.HasSuffix(name, ".jsonl"):
			id := strings.TrimSuffix(name, ".jsonl")
			// A second tailer would restart seq at 0 and overwrite stored lines.
			if started[id] {
				return
			}
			started[id] = true
			seq := 0
			tailers = append(tailers, TailFile(fullPath, func(raw string) {
				var line any
				if json.Unmarshal([]byte(raw), &line) != nil {
					line = map[string]any{"raw": raw}
				}
				err := store.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: sessionID, SubagentID: id, Seq: seq, Line: line, AccountID: accountID, Host: host})
				if err != nil {
					opts.Logger.Error("store subagent line failed", "subagent_id", id, "seq", seq, "err", err)
				}
				seq++
			}, opts))
		case strings.HasSuffix(name, ".meta.json"):
			readBlob("subagent-meta", "utf8", utf8)(name, fullPath)
		}
	}

	stops := []func(){
		WatchDir(filepath.Join(sessionDir, "subagents"), onSubagentFile, opts),
		WatchDir(filepath.Join(sessionDir, "tool-results"), readBlob("tool-result", "utf8", utf8), opts),
		WatchDir(fileHistoryPath, readBlob("file-history", "base64", base64.StdEncoding.EncodeToString), opts),
	}
	return func() {
		for _, s := range stops {
			s() // after these return, nothing appends to tailers
		}
		for _, s := range tailers {
			s()
		}
	}
}
