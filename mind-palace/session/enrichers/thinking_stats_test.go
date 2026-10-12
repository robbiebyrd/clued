package enrichers

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func thinkingStatsOf(t *testing.T, message map[string]any) thinkingStatsResult {
	t.Helper()
	doc := session.Doc{"line": map[string]any{"type": "assistant", "message": message}}
	r, err := ThinkingStats.Enrich(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return r.(thinkingStatsResult)
}

func TestThinkingStatsMetadata(t *testing.T) {
	if ThinkingStats.Name != "thinking-stats" || ThinkingStats.Collection != "transcript_lines" {
		t.Fatalf("name/collection = %q/%q", ThinkingStats.Name, ThinkingStats.Collection)
	}
	if !ThinkingStats.Enabled || ThinkingStats.BatchLimit != 0 {
		t.Fatalf("enabled/batch = %v/%d", ThinkingStats.Enabled, ThinkingStats.BatchLimit)
	}
}

func TestThinkingStatsMatchesAssistantLineTypeOnly(t *testing.T) {
	cases := []struct {
		doc  session.Doc
		want bool
	}{
		{session.Doc{"line": map[string]any{"type": "assistant"}}, true},
		{session.Doc{"line": map[string]any{"type": "user"}}, false},
		{session.Doc{"line": map[string]any{"type": "attachment"}}, false},
		{session.Doc{}, false},
	}
	for _, c := range cases {
		if got := ThinkingStats.Matches(c.doc); got != c.want {
			t.Errorf("Matches(%v) = %v, want %v", c.doc, got, c.want)
		}
	}
}

func TestThinkingStatsZerosForNonAssistantRole(t *testing.T) {
	if r := thinkingStatsOf(t, map[string]any{"role": "user", "content": []any{}}); r != (thinkingStatsResult{}) {
		t.Fatalf("got %+v", r)
	}
}

func TestThinkingStatsCountsThinkingChars(t *testing.T) {
	r := thinkingStatsOf(t, map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "thinking", "thinking": "hmm let me think"},
	}})
	if r != (thinkingStatsResult{ThinkingChars: 16}) {
		t.Fatalf("got %+v", r)
	}
}

func TestThinkingStatsCountsTextChars(t *testing.T) {
	r := thinkingStatsOf(t, map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "text", "text": "hello world"},
	}})
	if r != (thinkingStatsResult{TextChars: 11}) {
		t.Fatalf("got %+v", r)
	}
}

func TestThinkingStatsAccumulatesMultipleBlocks(t *testing.T) {
	r := thinkingStatsOf(t, map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "thinking", "thinking": "abc"},
		map[string]any{"type": "thinking", "thinking": "de"},
		map[string]any{"type": "text", "text": "fgh"},
	}})
	if r != (thinkingStatsResult{ThinkingChars: 5, TextChars: 3}) {
		t.Fatalf("got %+v", r)
	}
}

func TestThinkingStatsOtherBlocksDoNotCount(t *testing.T) {
	r := thinkingStatsOf(t, map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": "x", "name": "Bash", "input": map[string]any{}},
	}})
	if r != (thinkingStatsResult{}) {
		t.Fatalf("got %+v", r)
	}
}

func TestThinkingStatsZerosWhenContentMissing(t *testing.T) {
	if r := thinkingStatsOf(t, map[string]any{"role": "assistant"}); r != (thinkingStatsResult{}) {
		t.Fatalf("got %+v", r)
	}
}

func TestThinkingStatsCountsUTF16Units(t *testing.T) {
	r := thinkingStatsOf(t, map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "text", "text": "😀"},
	}})
	if r.TextChars != 2 {
		t.Fatalf("got %+v", r)
	}
}
