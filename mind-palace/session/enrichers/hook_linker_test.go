package enrichers

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

func insertHookEvent(t *testing.T, store *memsession.Store, sessionID, toolName, toolUseID string) {
	t.Helper()
	ev := session.Doc{"session_id": sessionID, "tool_name": toolName, "tool_use_id": toolUseID, "created_at": time.Now()}
	if err := store.InsertHookEvent(context.Background(), ev); err != nil {
		t.Fatalf("InsertHookEvent: %v", err)
	}
}

func hookLinkerOf(t *testing.T, store *memsession.Store, doc session.Doc) hookLinkerResult {
	t.Helper()
	r, err := HookLinker.Enrich(context.Background(), doc, store)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return r.(hookLinkerResult)
}

func TestHookLinkerMetadata(t *testing.T) {
	if HookLinker.Name != "hook-linker" || HookLinker.Collection != "transcript_lines" {
		t.Fatalf("name/collection = %q/%q", HookLinker.Name, HookLinker.Collection)
	}
	if !HookLinker.Enabled || HookLinker.BatchLimit != 20 {
		t.Fatalf("enabled/batch = %v/%d", HookLinker.Enabled, HookLinker.BatchLimit)
	}
}

func TestHookLinkerMatchesAllDocs(t *testing.T) {
	if !HookLinker.Matches(session.Doc{}) || !HookLinker.Matches(session.Doc{"anything": 1}) {
		t.Fatal("expected match")
	}
}

func TestHookLinkerEmptyForLineWithoutToolUse(t *testing.T) {
	r := hookLinkerOf(t, memsession.New(), session.Doc{"line": map[string]any{"display": "hello", "sessionId": "x"}})
	if r.ToolUseIDs == nil || r.HookEventIDs == nil || len(r.ToolUseIDs) != 0 || len(r.HookEventIDs) != 0 {
		t.Fatalf("got %#v", r)
	}
	if got := jsonString(t, r); got != `{"tool_use_ids":[],"hook_event_ids":[]}` {
		t.Fatalf("got %s", got)
	}
}

func TestHookLinkerLinksAttachmentToolUseID(t *testing.T) {
	store := memsession.New()
	insertHookEvent(t, store, "sess-hook-test", "Bash", "test-tuid-1")
	insertHookEvent(t, store, "sess-hook-test", "Bash", "other")

	r := hookLinkerOf(t, store, session.Doc{
		"session_id": "sess-hook-test",
		"line":       map[string]any{"attachment": map[string]any{"type": "hook_success", "toolUseID": "test-tuid-1", "hookEvent": "PostToolUse"}},
	})
	if !reflect.DeepEqual(r.ToolUseIDs, []string{"test-tuid-1"}) || len(r.HookEventIDs) != 1 {
		t.Fatalf("got %#v", r)
	}
	want, _ := store.HookEventIDsByToolUse(context.Background(), "sess-hook-test", []string{"test-tuid-1"})
	if !reflect.DeepEqual(r.HookEventIDs, want) {
		t.Fatalf("ids = %v, want %v", r.HookEventIDs, want)
	}
}

func TestHookLinkerLinksAssistantToolUseBlocks(t *testing.T) {
	store := memsession.New()
	insertHookEvent(t, store, "sess-hook-test-2", "Read", "test-tuid-2")

	r := hookLinkerOf(t, store, session.Doc{
		"session_id": "sess-hook-test-2",
		"line":       lineWithAssistantBlocks(map[string]any{"type": "tool_use", "id": "test-tuid-2", "name": "Read", "input": map[string]any{"file_path": "/x"}}),
	})
	if !reflect.DeepEqual(r.ToolUseIDs, []string{"test-tuid-2"}) || len(r.HookEventIDs) != 1 {
		t.Fatalf("got %#v", r)
	}
}

func TestHookLinkerNoMatchingHookEvent(t *testing.T) {
	r := hookLinkerOf(t, memsession.New(), session.Doc{
		"session_id": "no-session",
		"line":       map[string]any{"attachment": map[string]any{"type": "hook_success", "toolUseID": "nonexistent-id"}},
	})
	if !reflect.DeepEqual(r.ToolUseIDs, []string{"nonexistent-id"}) || r.HookEventIDs == nil || len(r.HookEventIDs) != 0 {
		t.Fatalf("got %#v", r)
	}
}

func TestHookLinkerDeduplicatesToolUseIDs(t *testing.T) {
	line := lineWithAssistantBlocks(map[string]any{"type": "tool_use", "id": "duplicate-id", "name": "Bash", "input": map[string]any{}})
	line["attachment"] = map[string]any{"type": "hook_success", "toolUseID": "duplicate-id"}
	r := hookLinkerOf(t, memsession.New(), session.Doc{"session_id": "sess-dedup", "line": line})
	if !reflect.DeepEqual(r.ToolUseIDs, []string{"duplicate-id"}) {
		t.Fatalf("got %#v", r.ToolUseIDs)
	}
}

func TestHookLinkerKeepsFirstSeenOrder(t *testing.T) {
	line := lineWithAssistantBlocks(
		map[string]any{"type": "tool_use", "id": "b"},
		map[string]any{"type": "tool_use", "id": "c"},
	)
	line["attachment"] = map[string]any{"toolUseID": "a"}
	r := hookLinkerOf(t, memsession.New(), session.Doc{"session_id": "s", "line": line})
	if !reflect.DeepEqual(r.ToolUseIDs, []string{"a", "b", "c"}) {
		t.Fatalf("got %#v", r.ToolUseIDs)
	}
}

func TestHookLinkerOnlyLinksSameSession(t *testing.T) {
	store := memsession.New()
	insertHookEvent(t, store, "other-session", "Bash", "shared-id")
	r := hookLinkerOf(t, store, session.Doc{
		"session_id": "this-session",
		"line":       map[string]any{"attachment": map[string]any{"toolUseID": "shared-id"}},
	})
	if len(r.HookEventIDs) != 0 {
		t.Fatalf("got %#v", r)
	}
}

func TestHookLinkerIgnoresNonStringIDs(t *testing.T) {
	r := hookLinkerOf(t, memsession.New(), session.Doc{"session_id": "s", "line": lineWithAssistantBlocks(
		map[string]any{"type": "tool_use", "id": 7},
		map[string]any{"type": "text", "id": "not-a-tool-use"},
	)})
	if len(r.ToolUseIDs) != 0 {
		t.Fatalf("got %#v", r.ToolUseIDs)
	}
}
