// Package wal provides a write-ahead log for persisting session documents
// until they are successfully inserted into a store.
package wal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Append creates the parent directory and appends one JSON line to the file.
func Append(path string, event session.Doc) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	// Ensure file is 0600 in case it was previously created with different mode
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	return nil
}

// Flush re-inserts every entry in the file through insert and rewrites the file
// with the lines to keep (empty when none; each kept line followed by a newline).
// An insert error that is context.DeadlineExceeded or context.Canceled means the
// store is unreachable: Flush stops there and keeps the failed line and every
// unattempted line, rather than waiting out a timeout per line. Any other insert
// error keeps only the failed line and moves on to the next. Malformed lines are
// dropped. If the file does not exist, it is a no-op.
//
// Flush assumes a single writer: the daemon serialises its own Append and Flush
// calls; the hook script appends only while the daemon is down.
func Flush(ctx context.Context, path string, insert func(context.Context, session.Doc) error) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	// Filter out empty lines at the end
	var filtered []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			filtered = append(filtered, line)
		}
	}
	lines = filtered

	if len(lines) == 0 {
		return nil
	}

	var failed []string
	for i, line := range lines {
		var doc session.Doc
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			// Skip malformed lines
			continue
		}

		if err := insert(ctx, doc); err != nil {
			// Keep lines that failed
			failed = append(failed, line)
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				failed = append(failed, lines[i+1:]...)
				break
			}
		}
	}

	// Write back the failed lines, or empty file if all succeeded
	var content string
	if len(failed) > 0 {
		content = strings.Join(failed, "\n") + "\n"
	}

	// Use OpenFile with O_TRUNC to ensure 0600 mode
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	// Ensure file is 0600 in case it existed with different mode
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	return nil
}
