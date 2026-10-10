package wal_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/wal"
)

func TestAppendCreatesFileAndWritesAJsonLine(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "test.jsonl")

	doc := session.Doc{"type": "test", "session_id": "abc"}
	if err := wal.Append(f, doc); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}

	var parsed session.Doc
	if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if parsed["type"] != "test" {
		t.Errorf("expected type=test, got %v", parsed["type"])
	}
	if parsed["session_id"] != "abc" {
		t.Errorf("expected session_id=abc, got %v", parsed["session_id"])
	}
}

func TestAppendAppendsSucessiveEventsAsSeparateLines(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "test.jsonl")

	doc1 := session.Doc{"type": "a"}
	doc2 := session.Doc{"type": "b"}

	if err := wal.Append(f, doc1); err != nil {
		t.Fatalf("First append failed: %v", err)
	}
	if err := wal.Append(f, doc2); err != nil {
		t.Fatalf("Second append failed: %v", err)
	}

	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}

	var parsed1, parsed2 session.Doc
	if err := json.Unmarshal([]byte(lines[0]), &parsed1); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &parsed2); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if parsed1["type"] != "a" {
		t.Errorf("expected first line type=a, got %v", parsed1["type"])
	}
	if parsed2["type"] != "b" {
		t.Errorf("expected second line type=b, got %v", parsed2["type"])
	}
}

func TestFlushCallsInsertForEveryEntryAndClearsTheFile(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "test.jsonl")

	// Create a WAL file with two entries
	content := `{"type":"a"}
{"type":"b"}
`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	var inserted []session.Doc
	insert := func(ctx context.Context, doc session.Doc) error {
		inserted = append(inserted, doc)
		return nil
	}

	if err := wal.Flush(context.Background(), f, insert); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if len(inserted) != 2 {
		t.Errorf("expected 2 inserts, got %d", len(inserted))
	}

	remaining, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if strings.TrimSpace(string(remaining)) != "" {
		t.Errorf("expected empty file, got %q", string(remaining))
	}
}

func TestFlushRetainsOnlyEntriesWhoseInsertThrows(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "test.jsonl")

	// Create a WAL file with three entries
	content := `{"type":"a"}
{"type":"b"}
{"type":"c"}
`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	insert := func(ctx context.Context, doc session.Doc) error {
		if doc["type"] == "b" {
			return errors.New("mongo down")
		}
		return nil
	}

	if err := wal.Flush(context.Background(), f, insert); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	remaining, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(remaining)), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 failed line, got %d", len(lines))
	}

	var parsed session.Doc
	if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if parsed["type"] != "b" {
		t.Errorf("expected failed line type=b, got %v", parsed["type"])
	}
}

func TestFlushDoesNothingWhenWalFileDoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "nonexistent.jsonl")

	insert := func(ctx context.Context, doc session.Doc) error {
		t.Error("insert should not be called")
		return errors.New("should not be called")
	}

	if err := wal.Flush(context.Background(), f, insert); err != nil {
		t.Fatalf("Flush should not fail for nonexistent file: %v", err)
	}

	if _, err := os.Stat(f); err == nil {
		t.Error("file should not exist")
	}
}

func TestFlushSkipsMalformedLinesWithoutThrowing(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "test.jsonl")

	// Create a WAL file with one valid, one malformed, and one valid entry
	content := `{"type":"a"}
not-json
{"type":"c"}
`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	var inserted []session.Doc
	insert := func(ctx context.Context, doc session.Doc) error {
		inserted = append(inserted, doc)
		return nil
	}

	if err := wal.Flush(context.Background(), f, insert); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if len(inserted) != 2 {
		t.Errorf("expected 2 inserts, got %d", len(inserted))
	}
}
