// Package wal provides a write-ahead log for persisting session documents
// until they are successfully inserted into a store.
package wal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Append creates the parent directory and appends one JSON line to the file.
func Append(path string, event session.Doc) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}

// Flush re-inserts every entry in the file through insert, keeps the lines
// whose insert returned an error, drops malformed lines, and rewrites the file
// (empty when nothing failed; failed lines each followed by a newline otherwise).
// If the file does not exist, it is a no-op.
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
	for _, line := range lines {
		var doc session.Doc
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			// Skip malformed lines
			continue
		}

		if err := insert(ctx, doc); err != nil {
			// Keep lines that failed
			failed = append(failed, line)
		}
	}

	// Write back the failed lines, or empty file if all succeeded
	var content string
	if len(failed) > 0 {
		content = strings.Join(failed, "\n") + "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}
