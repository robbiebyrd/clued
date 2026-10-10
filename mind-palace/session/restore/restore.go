// Package restore rebuilds a captured session's files (transcript, subagent
// transcripts and the stored blobs) in the layout Claude Code reads them from.
package restore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	transcriptBatch = 500
	dirMode         = 0o755
	fileMode        = 0o644
)

// Args select the session to restore. ProjectPath overrides the project
// directory derived from the stored session; ProjectsDir is the root the
// project directory is created in.
type Args struct {
	SessionID   string `json:"session_id"`
	ProjectPath string `json:"project_path,omitempty"`
	ProjectsDir string `json:"projects_dir,omitempty"`
}

// Result counts what was written and names what the store did not have.
type Result struct {
	FilesWritten int      `json:"files_written"`
	BytesWritten int      `json:"bytes_written"`
	Missing      []string `json:"missing"`
}

// blobTypes are the stored blob types, in the order missing ones are reported.
var blobTypes = []string{"subagent-meta", "tool-result", "file-history"}

// writer counts the files and bytes it actually writes.
type writer struct{ result Result }

// countingFile adds the bytes written to it to the writer's result.
type countingFile struct {
	f *os.File
	w *writer
}

func (c countingFile) Write(p []byte) (int, error) {
	n, err := c.f.Write(p)
	c.w.result.BytesWritten += n
	return n, err
}

// create creates (or truncates) path, lets fill write its content, and counts
// the file once it is closed without error.
func (w *writer) create(path string, fill func(io.Writer) error) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return err
	}
	err = fill(countingFile{f, w})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	w.result.FilesWritten++
	return nil
}

// write creates path holding content.
func (w *writer) write(path string, content []byte) error {
	return w.create(path, func(out io.Writer) error {
		_, err := out.Write(content)
		return err
	})
}

// Session writes the session's files under args.ProjectsDir (transcript,
// subagents, subagent meta and tool results) and fileHistoryDir (file history).
func Session(ctx context.Context, store session.Store, accountID string, args Args, fileHistoryDir string) (Result, error) {
	stored, err := store.GetSession(ctx, accountID, args.SessionID)
	if err != nil {
		return Result{}, fmt.Errorf("session %s: %w", args.SessionID, err)
	}
	projDirName, err := projectDirName(stored, args)
	if err != nil {
		return Result{}, err
	}

	projectDir := filepath.Join(args.ProjectsDir, projDirName)
	sessionDir := filepath.Join(projectDir, args.SessionID)
	dirs := map[string]string{
		"subagent-meta": filepath.Join(sessionDir, "subagents"),
		"tool-result":   filepath.Join(sessionDir, "tool-results"),
		"file-history":  filepath.Join(fileHistoryDir, args.SessionID),
	}
	for _, d := range []string{projectDir, dirs["subagent-meta"], dirs["tool-result"], dirs["file-history"]} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			return Result{}, err
		}
	}

	w := &writer{result: Result{Missing: []string{}}}
	if err := restoreTranscript(ctx, store, accountID, args.SessionID, projectDir, w); err != nil {
		return Result{}, err
	}
	if err := restoreSubagents(ctx, store, accountID, args.SessionID, dirs["subagent-meta"], w); err != nil {
		return Result{}, err
	}
	if err := restoreBlobs(ctx, store, accountID, args.SessionID, dirs, w); err != nil {
		return Result{}, err
	}
	return w.result, nil
}

// projectDirName picks the project directory name: the project_path override,
// else the parent directory of the stored transcript path, else the stored
// project path.
func projectDirName(stored session.Doc, args Args) (string, error) {
	if args.ProjectPath != "" {
		return session.EncodeProjectPath(args.ProjectPath), nil
	}
	if p, _ := stored.String("transcript_path"); p != "" {
		return filepath.Base(filepath.Dir(p)), nil
	}
	if p, _ := stored.String("project_path"); p != "" {
		return session.EncodeProjectPath(p), nil
	}
	return "", fmt.Errorf("session %s has no path information to derive target directory", args.SessionID)
}

// jsonlRow is one line of a JSONL file; HTML is not escaped so the text
// matches what Claude Code wrote.
func jsonlRow(out io.Writer, line any) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(line)
}

func restoreTranscript(ctx context.Context, store session.Store, accountID, sessionID, projectDir string, w *writer) error {
	total, err := store.CountTranscriptLines(ctx, accountID, sessionID)
	if err != nil {
		return err
	}
	if total == 0 {
		w.result.Missing = append(w.result.Missing, "transcript_lines")
		return nil
	}
	return w.create(filepath.Join(projectDir, sessionID+".jsonl"), func(out io.Writer) error {
		for offset := 0; offset < total; offset += transcriptBatch {
			batch, err := store.TranscriptLines(ctx, session.LineQuery{
				AccountID: accountID, SessionID: sessionID, Offset: offset, Limit: transcriptBatch,
			})
			if err != nil {
				return err
			}
			for _, doc := range batch {
				if err := jsonlRow(out, doc["line"]); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func restoreSubagents(ctx context.Context, store session.Store, accountID, sessionID, subagentsDir string, w *writer) error {
	ids, err := store.SubagentIDs(ctx, accountID, sessionID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		lines, err := store.SubagentLines(ctx, accountID, sessionID, id)
		if err != nil {
			return err
		}
		err = w.create(filepath.Join(subagentsDir, id+".jsonl"), func(out io.Writer) error {
			for _, doc := range lines {
				if err := jsonlRow(out, doc["line"]); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func restoreBlobs(ctx context.Context, store session.Store, accountID, sessionID string, dirs map[string]string, w *writer) error {
	blobs, err := store.Blobs(ctx, accountID, sessionID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, b := range blobs {
		blobType, _ := b.String("blob_type")
		seen[blobType] = true
		dir, known := dirs[blobType]
		if !known {
			continue
		}
		name, _ := b.String("name")
		content, _ := b.String("content")
		data := []byte(content)
		if encoding, _ := b.String("encoding"); encoding == "base64" {
			if data, err = base64.StdEncoding.DecodeString(content); err != nil {
				return fmt.Errorf("blob %s/%s: %w", blobType, name, err)
			}
		}
		if err := w.write(filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	for _, t := range blobTypes {
		if !seen[t] {
			w.result.Missing = append(w.result.Missing, t)
		}
	}
	return nil
}
