package enrichers

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func TestSearchTextIndexesReadableTextWithSecretsMasked(t *testing.T) {
	doc := session.Doc{"line": map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "thinking", "thinking": "private reasoning"},
		map[string]any{"type": "text", "text": "Set OPENAI key sk-proj-abcdefghijklmnopqrstuvwxyz0123 and mail ops@example.com about the OAuth migration."},
	}}}}
	got, err := SearchText.Enrich(context.Background(), doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := got.(string)
	if strings.Contains(text, "sk-proj-") || !strings.Contains(text, "[REDACTED:api-key]") {
		t.Errorf("secret not masked: %q", text)
	}
	if !strings.Contains(text, "ops@example.com") || !strings.Contains(text, "OAuth migration") {
		t.Errorf("searchable text lost: %q", text)
	}
	if strings.Contains(text, "private reasoning") {
		t.Error("thinking indexed")
	}
	long := session.Doc{"line": map[string]any{"message": map[string]any{"content": strings.Repeat("é", searchTextLimit)}}}
	got, _ = SearchText.Enrich(context.Background(), long, nil)
	if s := got.(string); len(s) > searchTextLimit || !utf8.ValidString(s) {
		t.Errorf("long text: %d bytes, valid UTF-8 %v", len(s), utf8.ValidString(s))
	}
	if !SearchText.Enabled || SearchText.Collection != "transcript_lines" {
		t.Error("search-text should run on transcript lines by default")
	}
}
