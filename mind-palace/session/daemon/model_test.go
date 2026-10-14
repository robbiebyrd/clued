package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func TestTranscriptModelIsRecordedOnTheSession(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		f := newFixture(t)
		transcript := filepath.Join(f.tmp, "projects", "sess-model.jsonl")
		if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(transcript, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"session_id":"sess-model","transcript_path":%q,"cwd":"/tmp"}`, transcript)
		if code, _ := f.post(t, body); code != 200 {
			t.Fatalf("status %d", code)
		}
		eventually(t, "session doc", func() bool { return sessionDoc(f.store, "sess-model") != nil })
		content := `{"type":"user","message":{"role":"user","content":"hello"}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-10-14T10:00:01Z","message":{"role":"assistant","model":"claude-a","content":[]}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-10-14T10:00:02Z","message":{"role":"assistant","model":"claude-a","content":[]}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-10-14T10:00:03Z","message":{"role":"assistant","model":"claude-b","content":[]}}` + "\n"
		fh, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fh.WriteString(content); err != nil {
			t.Fatal(err)
		}
		fh.Close()
		eventually(t, "session model", func() bool { return sessionDoc(f.store, "sess-model")["model"] == "claude-b" })
		full, err := f.store.FullSession(context.Background(), testAccount, "sess-model")
		if err != nil {
			t.Fatal(err)
		}
		// The memory store joins typed documents, MongoDB decoded ones.
		var changes []session.Doc
		for _, ev := range joined(full["hook_events"]) {
			if ev["hook_event_name"] == session.ModelChangedEventName {
				changes = append(changes, ev)
			}
		}
		if len(changes) != 2 || changes[0]["to"] != "claude-a" || changes[1]["from"] != "claude-a" || changes[1]["to"] != "claude-b" || fmt.Sprint(changes[1]["seq"]) != "3" {
			t.Fatalf("model changes = %v", changes)
		}
	})
}

// joined reads a list of a full session whatever the store decoded it to.
func joined(v any) []session.Doc {
	switch list := v.(type) {
	case []session.Doc:
		return list
	case []any:
		out := make([]session.Doc, 0, len(list))
		for _, item := range list {
			switch d := item.(type) {
			case session.Doc:
				out = append(out, d)
			case map[string]any:
				out = append(out, session.Doc(d))
			}
		}
		return out
	}
	return nil
}
