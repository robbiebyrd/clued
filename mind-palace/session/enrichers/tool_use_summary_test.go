package enrichers

import (
	"context"
	"reflect"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func toolUseSummaryOf(t *testing.T, line map[string]any) toolUseSummaryResult {
	t.Helper()
	r, err := ToolUseSummary.Enrich(context.Background(), session.Doc{"line": line}, nil)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return r.(toolUseSummaryResult)
}

func toolUseBlock(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func TestToolUseSummaryMetadata(t *testing.T) {
	if ToolUseSummary.Name != "tool-use-summary" || ToolUseSummary.Collection != "transcript_lines" {
		t.Fatalf("name/collection = %q/%q", ToolUseSummary.Name, ToolUseSummary.Collection)
	}
	if !ToolUseSummary.Enabled || ToolUseSummary.BatchLimit != 0 {
		t.Fatalf("enabled/batch = %v/%d", ToolUseSummary.Enabled, ToolUseSummary.BatchLimit)
	}
}

func TestToolUseSummaryMatchesAllDocs(t *testing.T) {
	if !ToolUseSummary.Matches(session.Doc{}) {
		t.Fatal("expected match")
	}
}

func TestToolUseSummaryEmptyForNonAssistantLine(t *testing.T) {
	r := toolUseSummaryOf(t, map[string]any{"display": "hello", "sessionId": "x"})
	if r.Tools == nil || len(r.Tools) != 0 || r.HasThinking {
		t.Fatalf("got %#v", r)
	}
	if got := jsonString(t, r); got != `{"tools":[],"has_thinking":false}` {
		t.Fatalf("got %s", got)
	}
}

func TestToolUseSummaryExtractsToolUseBlocks(t *testing.T) {
	r := toolUseSummaryOf(t, lineWithAssistantBlocks(
		toolUseBlock("tu1", "Bash", map[string]any{"command": "ls", "cwd": "/tmp"}),
		map[string]any{"type": "text", "text": "done"},
	))
	want := toolUseSummaryResult{Tools: []toolSummary{{ID: "tu1", Name: "Bash", InputKeys: []string{"command", "cwd"}}}}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("got %#v", r)
	}
	if got := jsonString(t, r); got != `{"tools":[{"id":"tu1","name":"Bash","inputKeys":["command","cwd"]}],"has_thinking":false}` {
		t.Fatalf("got %s", got)
	}
}

func TestToolUseSummaryDetectsThinkingBlock(t *testing.T) {
	r := toolUseSummaryOf(t, lineWithAssistantBlocks(
		map[string]any{"type": "thinking", "thinking": "hmm"},
		toolUseBlock("tu2", "Read", map[string]any{"file_path": "/x"}),
	))
	if !r.HasThinking || len(r.Tools) != 1 || r.Tools[0].Name != "Read" {
		t.Fatalf("got %#v", r)
	}
}

func TestToolUseSummaryMultipleToolUseBlocks(t *testing.T) {
	r := toolUseSummaryOf(t, lineWithAssistantBlocks(
		toolUseBlock("a", "Bash", map[string]any{"command": "ls"}),
		toolUseBlock("b", "Read", map[string]any{"file_path": "/x"}),
	))
	if len(r.Tools) != 2 {
		t.Fatalf("got %#v", r)
	}
}

func TestToolUseSummaryEmptyForAssistantWithoutToolUseOrThinking(t *testing.T) {
	r := toolUseSummaryOf(t, lineWithAssistantBlocks(map[string]any{"type": "text", "text": "hi"}))
	if r.Tools == nil || len(r.Tools) != 0 || r.HasThinking {
		t.Fatalf("got %#v", r)
	}
}

func TestToolUseSummaryMissingContent(t *testing.T) {
	r := toolUseSummaryOf(t, map[string]any{"message": map[string]any{"role": "assistant"}})
	if r.Tools == nil || len(r.Tools) != 0 || r.HasThinking {
		t.Fatalf("got %#v", r)
	}
}

func TestToolUseSummaryInputKeysSortedAndNeverNull(t *testing.T) {
	r := toolUseSummaryOf(t, lineWithAssistantBlocks(
		toolUseBlock("a", "Write", map[string]any{"path": 1, "content": 2, "append": 3}),
		map[string]any{"type": "tool_use", "id": "b", "name": "NoInput"},
	))
	if !reflect.DeepEqual(r.Tools[0].InputKeys, []string{"append", "content", "path"}) {
		t.Fatalf("got %#v", r.Tools[0].InputKeys)
	}
	if r.Tools[1].InputKeys == nil || len(r.Tools[1].InputKeys) != 0 {
		t.Fatalf("got %#v", r.Tools[1].InputKeys)
	}
}
