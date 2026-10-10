// Package backfill loads existing Claude Code transcripts from the projects
// directory into a session store.
package backfill

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const concurrency = 5

// GitInfo returns the git origin URL and branch of dir, or empty strings when unknown.
type GitInfo func(ctx context.Context, dir string) (origin, branch string)

// Stats counts the sessions found and the transcript lines upserted.
type Stats struct {
	Sessions, Lines int
}

type transcript struct {
	projectPath, sessionID, filePath string
}

// Run upserts every session under projectsDir and its transcript lines, five
// sessions at a time. A nil git skips git lookup and a nil logger uses
// slog.Default(). An unreadable projectsDir yields empty Stats. A failing
// file is logged and skipped; Run returns an error only when ctx is cancelled.
func Run(ctx context.Context, store session.Store, projectsDir, accountID string, git GitInfo, logger *slog.Logger) (Stats, error) {
	if logger == nil {
		logger = slog.Default()
	}
	transcripts := findTranscripts(projectsDir)

	var lines atomic.Int64
	var wg sync.WaitGroup
	slots := make(chan struct{}, concurrency)
	for _, t := range transcripts {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			n, err := processSession(ctx, store, t, accountID, git)
			if err != nil {
				logger.Error("backfill error", "file", t.filePath, "error", err)
				return
			}
			lines.Add(int64(n))
		}()
	}
	wg.Wait()

	stats := Stats{Sessions: len(transcripts), Lines: int(lines.Load())}
	logger.Info("backfill complete", "sessions", stats.Sessions, "lines", stats.Lines)
	return stats, ctx.Err()
}

func findTranscripts(projectsDir string) []transcript {
	var found []transcript
	dirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		dirPath := filepath.Join(projectsDir, dir.Name())
		files, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if id, ok := strings.CutSuffix(f.Name(), ".jsonl"); ok {
				found = append(found, transcript{session.DecodeProjectPath(dir.Name()), id, filepath.Join(dirPath, f.Name())})
			}
		}
	}
	return found
}

func processSession(ctx context.Context, store session.Store, t transcript, accountID string, git GitInfo) (int, error) {
	s := session.Session{
		SessionID:      t.sessionID,
		ProjectPath:    t.projectPath,
		TranscriptPath: t.filePath,
		AccountID:      accountID,
		LastSeen:       time.Now(),
	}
	if git != nil {
		s.GitOrigin, s.GitBranch = git(ctx, t.projectPath)
	}
	if err := store.UpsertSession(ctx, s); err != nil {
		return 0, err
	}

	content, err := os.ReadFile(t.filePath)
	if err != nil {
		return 0, err
	}
	var batch []session.TranscriptLine
	for _, raw := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		batch = append(batch, session.TranscriptLine{
			SessionID: t.sessionID,
			Seq:       len(batch),
			Line:      parseLine(raw),
			AccountID: accountID,
		})
	}
	if len(batch) == 0 {
		return 0, nil
	}
	if err := store.UpsertTranscriptLines(ctx, batch); err != nil {
		return 0, err
	}
	return len(batch), nil
}

// parseLine decodes a transcript line, keeping undecodable text as {"raw": line}.
func parseLine(raw string) any {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return map[string]any{"raw": raw}
	}
	return v
}
