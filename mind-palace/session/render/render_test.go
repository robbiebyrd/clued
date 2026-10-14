package render_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/render"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func line(seq int, l map[string]any) session.Doc { return session.Doc{"seq": seq, "line": l} }

func fixture() render.Input {
	cost := 0.0123
	return render.Input{
		Session: session.Doc{
			"session_id": "11111111-2222-3333-4444-555555555555", "project_path": "/home/me/shop",
			"git_origin": "git@github.com:me/shop.git", "git_branch": "fix/checkout",
			"started_at": time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), "last_seen": "2026-10-14T09:42:00Z",
		},
		Usage: &render.Usage{Messages: 3, InputTokens: 1200, OutputTokens: 900, CacheTokens: 40000, CostUSD: &cost},
		Lines: []session.Doc{
			line(0, map[string]any{"type": "file-history-snapshot", "snapshot": map[string]any{}}),
			line(1, map[string]any{"type": "user", "timestamp": "2026-10-14T09:00:05Z", "message": map[string]any{"role": "user", "content": "The checkout total is <wrong> when a coupon & tax apply."}}),
			line(2, map[string]any{"type": "assistant", "timestamp": "2026-10-14T09:00:09Z", "message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "private reasoning that is left out"},
				map[string]any{"type": "text", "text": "I'll look at the **totals** code."},
				map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "go test ./checkout/...\necho done"}},
			}}}),
			line(3, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "t1", "is_error": true, "content": "--- FAIL: TestTotal\n```\nwant 10, got 12"},
			}}}),
			line(4, map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "t2", "name": "Edit", "input": map[string]any{"file_path": "/home/me/shop/checkout/total.go", "old_string": "a", "new_string": "b"}},
			}}}),
			line(5, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "t2", "content": []any{map[string]any{"type": "text", "text": "The file was updated."}}},
			}}}),
			line(6, map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"role": "user", "content": "<command-name>/clear</command-name>"}}),
			line(7, map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "Fixed: the coupon is applied before tax now."},
			}}, "redacted_by_test": true}),
		},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./session/render/ -update)", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from the golden file:\n%s", name, got)
	}
}

func TestMarkdownGolden(t *testing.T) {
	golden(t, "session.md", render.Markdown(render.Build(fixture())))
}

func TestHTMLGolden(t *testing.T) { golden(t, "session.html", render.HTML(render.Build(fixture()))) }

func TestModelGroupsTurnsAndLeavesOutBookkeeping(t *testing.T) {
	m := render.Build(fixture())
	if len(m.Turns) != 2 {
		t.Fatalf("turns = %d, want 2 (the user, the assistant with its tool results)", len(m.Turns))
	}
	if m.Turns[1].Role != "assistant" || len(m.Turns[1].Entries) != 6 {
		t.Errorf("the tool results did not join the assistant's turn: %+v", m.Turns[1])
	}
	all := string(render.Markdown(m))
	for _, gone := range []string{"private reasoning", "/clear", "snapshot"} {
		if strings.Contains(all, gone) {
			t.Errorf("the Markdown holds %q", gone)
		}
	}
	html := string(render.HTML(m))
	if strings.Contains(html, "<wrong>") || !strings.Contains(html, "&lt;wrong&gt;") || strings.Contains(html, "<script") {
		t.Error("the HTML does not escape the conversation or holds a script")
	}
}

func TestRedactedLinesAreNoted(t *testing.T) {
	in := fixture()
	in.Lines[7]["redacted"] = []any{"api-key"}
	if m := render.Build(in); !m.Redacted || !strings.Contains(string(render.Markdown(m)), "masked") {
		t.Error("a redacted line is not noted")
	}
}
