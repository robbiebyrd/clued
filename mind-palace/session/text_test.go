package session

import (
	"strings"
	"testing"
)

func TestLineText(t *testing.T) {
	line := Doc{"line": map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "thinking", "thinking": "secret plan"},
		map[string]any{"type": "text", "text": "Running the tests now."},
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "go test ./..."}},
		map[string]any{"type": "tool_result", "content": []any{map[string]any{"type": "text", "text": "ok  pkg"}}},
	}}}}
	got := LineText(line)
	want := "Running the tests now.\nBash {\"command\":\"go test ./...\"}\nok  pkg"
	if got != want {
		t.Errorf("LineText = %q, want %q", got, want)
	}
	if strings.Contains(got, "secret plan") {
		t.Error("thinking text included")
	}
	if got := LineText(Doc{"line": map[string]any{"message": map[string]any{"content": "plain prompt"}}}); got != "plain prompt" {
		t.Errorf("string content = %q", got)
	}
	if got := LineText(Doc{"line": map[string]any{"type": "summary", "summary": "Fixed the build"}}); got != "Fixed the build" {
		t.Errorf("summary line = %q", got)
	}
}

func TestSnippet(t *testing.T) {
	text := strings.Repeat("filler words here ", 30) + "the OAuth migration broke login " + strings.Repeat("more filler ", 30)
	s := Snippet(text, []string{"oauth"}, 80)
	if !strings.Contains(s, "OAuth migration") || !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") || len(s) > 80+2*len("…") {
		t.Errorf("Snippet = %q", s)
	}
	if s := Snippet("short   text", []string{"x"}, 80); s != "short text" {
		t.Errorf("short = %q", s)
	}
	if s := Snippet(strings.Repeat("é", 100), nil, 11); !strings.HasSuffix(s, "…") {
		t.Errorf("rune boundary = %q", s)
	}
}
