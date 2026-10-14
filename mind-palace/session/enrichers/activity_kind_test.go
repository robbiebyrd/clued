package enrichers

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func TestActivityKindNameAndCollection(t *testing.T) {
	requireTranscriptEnricher(t, ActivityKind, "activity-kind")
	requireBSONTagsMirrorJSON(t, ActivityKindResult{})
	if !ActivityKind.Matches(session.Doc{}) || !ActivityKind.Matches(session.Doc{"line": map[string]any{"type": "system"}}) {
		t.Error("the enricher labels every line")
	}
}

// transcriptLine is a line of the given type whose message has the role and
// content.
func transcriptLine(lineType, role string, content any) session.Doc {
	return session.Doc{"seq": 3, "line": map[string]any{"type": lineType, "message": map[string]any{"role": role, "content": content}}}
}

func toolUse(name, id string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "name": name, "id": id, "input": input}
}

func activityOf(t *testing.T, doc session.Doc) ActivityKindResult {
	t.Helper()
	got, err := ActivityKind.Enrich(t.Context(), doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(ActivityKindResult)
}

func TestActivityKindPromptsRepliesAndThoughts(t *testing.T) {
	cases := []struct {
		name string
		doc  session.Doc
		want ActivityKindResult
	}{
		{"typed prompt", transcriptLine("user", "user", "fix the build\nplease"), ActivityKindResult{Kind: "user", Label: "user", Detail: "fix the build please"}},
		{"prompt as blocks", transcriptLine("user", "user", []any{map[string]any{"type": "text", "text": "hello"}}), ActivityKindResult{Kind: "user", Label: "user", Detail: "hello"}},
		{"reply", transcriptLine("assistant", "assistant", []any{map[string]any{"type": "text", "text": "Done."}}), ActivityKindResult{Kind: "response", Label: "response", Detail: "Done."}},
		{"thought", transcriptLine("assistant", "assistant", []any{map[string]any{"type": "thinking", "thinking": "Let me check."}}), ActivityKindResult{Kind: "thinking", Label: "thinking", Detail: "Let me check."}},
		{"redacted thought", transcriptLine("assistant", "assistant", []any{map[string]any{"type": "redacted_thinking", "data": "x"}}), ActivityKindResult{Kind: "thinking", Label: "thinking"}},
		{"several blocks take the first kind", transcriptLine("assistant", "assistant", []any{
			map[string]any{"type": "thinking", "thinking": "hm"},
			map[string]any{"type": "text", "text": "ok"},
			toolUse("Bash", "tu1", map[string]any{"command": "ls"}),
		}), ActivityKindResult{Kind: "thinking", Label: "thinking", Detail: "hm", Blocks: []string{"response", "bash"}}},
	}
	for _, c := range cases {
		if got := activityOf(t, c.doc); !equalActivity(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
	long := strings.Repeat("word ", 100)
	if got := activityOf(t, transcriptLine("user", "user", long)); utf8.RuneCountInString(got.Detail) > activityDetailChars+1 || !strings.HasPrefix(got.Detail, "word word") {
		t.Errorf("detail not bounded: %d characters", len(got.Detail))
	}
}

func TestActivityKindToolCalls(t *testing.T) {
	call := func(name string, input map[string]any) session.Doc {
		return transcriptLine("assistant", "assistant", []any{toolUse(name, "tu-"+name, input)})
	}
	cases := []struct {
		name string
		doc  session.Doc
		want ActivityKindResult
	}{
		{"bash", call("Bash", map[string]any{"command": "\x1b[32mgo\x1b[0m test ./...", "description": "Run tests"}), ActivityKindResult{Kind: "bash", Label: "Bash", Detail: "go test ./...", ToolName: "Bash", ToolUseID: "tu-Bash"}},
		{"commit", call("Bash", map[string]any{"command": `git add -A && git commit -m "Fix the build" && git push`}), ActivityKindResult{Kind: "commit", Label: "Bash", Detail: "Fix the build", ToolName: "Bash", ToolUseID: "tu-Bash"}},
		{"commit heredoc", call("Bash", map[string]any{"command": "git commit -m \"$(cat <<'EOF'\nAdd the thing\n\nBody here.\nEOF\n)\""}), ActivityKindResult{Kind: "commit", Label: "Bash", Detail: "Add the thing", ToolName: "Bash", ToolUseID: "tu-Bash"}},
		{"commit with git options", call("Bash", map[string]any{"command": "cd x; git -C /repo commit --amend --message='Reword it'"}), ActivityKindResult{Kind: "commit", Label: "Bash", Detail: "Reword it", ToolName: "Bash", ToolUseID: "tu-Bash"}},
		{"commit without inline message", call("Bash", map[string]any{"command": "git commit -F msg.txt"}), ActivityKindResult{Kind: "commit", Label: "Bash", Detail: "git commit -F msg.txt", ToolName: "Bash", ToolUseID: "tu-Bash"}},
		{"not a commit", call("Bash", map[string]any{"command": "git log --oneline | grep commit"}), ActivityKindResult{Kind: "bash", Label: "Bash", Detail: "git log --oneline | grep commit", ToolName: "Bash", ToolUseID: "tu-Bash"}},
		{"edit", call("Edit", map[string]any{"file_path": "/repo/pkg/a.go", "old_string": "x", "new_string": "y"}), ActivityKindResult{Kind: "edit", Label: "Edit", Detail: "a.go", ToolName: "Edit", ToolUseID: "tu-Edit"}},
		{"write", call("Write", map[string]any{"file_path": `C:\repo\b.go`}), ActivityKindResult{Kind: "edit", Label: "Write", Detail: "b.go", ToolName: "Write", ToolUseID: "tu-Write"}},
		{"notebook", call("NotebookEdit", map[string]any{"notebook_path": "/n/c.ipynb"}), ActivityKindResult{Kind: "edit", Label: "NotebookEdit", Detail: "c.ipynb", ToolName: "NotebookEdit", ToolUseID: "tu-NotebookEdit"}},
		{"read", call("Read", map[string]any{"file_path": "/repo/README.md"}), ActivityKindResult{Kind: "read", Label: "Read", Detail: "README.md", ToolName: "Read", ToolUseID: "tu-Read"}},
		{"grep", call("Grep", map[string]any{"pattern": "func main", "path": "/repo"}), ActivityKindResult{Kind: "read", Label: "Grep", Detail: "func main", ToolName: "Grep", ToolUseID: "tu-Grep"}},
		{"glob", call("Glob", map[string]any{"pattern": "**/*.go"}), ActivityKindResult{Kind: "read", Label: "Glob", Detail: "**/*.go", ToolName: "Glob", ToolUseID: "tu-Glob"}},
		{"task", call("Task", map[string]any{"description": "Review the diff", "prompt": "Please review…"}), ActivityKindResult{Kind: "task", Label: "Task", Detail: "Review the diff", ToolName: "Task", ToolUseID: "tu-Task"}},
		{"agent without description", call("Agent", map[string]any{"prompt": "Find the callers"}), ActivityKindResult{Kind: "task", Label: "Agent", Detail: "Find the callers", ToolName: "Agent", ToolUseID: "tu-Agent"}},
		{"other tool", call("WebFetch", map[string]any{"url": "https://example.com", "prompt": "summarize"}), ActivityKindResult{Kind: "tool", Label: "WebFetch", Detail: "https://example.com", ToolName: "WebFetch", ToolUseID: "tu-WebFetch"}},
		{"tool without input", call("TodoWrite", nil), ActivityKindResult{Kind: "tool", Label: "TodoWrite", ToolName: "TodoWrite", ToolUseID: "tu-TodoWrite"}},
	}
	for _, c := range cases {
		if got := activityOf(t, c.doc); !equalActivity(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestActivityKindResultsAndOtherLines(t *testing.T) {
	result := transcriptLine("user", "user", []any{map[string]any{"type": "tool_result", "tool_use_id": "tu1", "is_error": true, "content": []any{map[string]any{"type": "text", "text": "exit status 1"}}}})
	if got := activityOf(t, result); !equalActivity(got, ActivityKindResult{Kind: "result", Label: "result", Detail: "exit status 1", ToolUseID: "tu1", IsError: true}) {
		t.Errorf("tool result: %+v", got)
	}
	plain := transcriptLine("user", "user", []any{map[string]any{"type": "tool_result", "tool_use_id": "tu2", "content": "ok"}})
	if got := activityOf(t, plain); got.Kind != "result" || got.Detail != "ok" || got.IsError || got.ToolUseID != "tu2" {
		t.Errorf("string result: %+v", got)
	}
	cases := []struct {
		name  string
		doc   session.Doc
		label string
	}{
		{"empty", session.Doc{}, "other"},
		{"system", session.Doc{"line": map[string]any{"type": "system", "subtype": "stop_hook_summary"}}, "system:stop_hook_summary"},
		{"hook attachment", session.Doc{"line": map[string]any{"type": "attachment", "attachment": map[string]any{"type": "hook_success"}}}, "attachment:hook_success"},
		{"last prompt", session.Doc{"line": map[string]any{"type": "last-prompt", "display": "x", "sessionId": "s"}}, "last-prompt"},
		{"summary", session.Doc{"line": map[string]any{"type": "summary", "summary": "…"}}, "summary"},
		{"assistant without content", transcriptLine("assistant", "assistant", nil), "assistant"},
	}
	for _, c := range cases {
		if got := activityOf(t, c.doc); got.Kind != "other" || got.Label != c.label || got.Detail != "" {
			t.Errorf("%s: got %+v, want other/%s", c.name, got, c.label)
		}
	}
	if got := marshalEnrichment(t, ActivityKind, session.Doc{}); got != `{"kind":"other","label":"other","detail":"","tool_name":"","tool_use_id":""}` {
		t.Errorf("stored shape = %s", got)
	}
}

func equalActivity(a, b ActivityKindResult) bool {
	if len(a.Blocks) != len(b.Blocks) {
		return false
	}
	for i := range a.Blocks {
		if a.Blocks[i] != b.Blocks[i] {
			return false
		}
	}
	return a.Kind == b.Kind && a.Label == b.Label && a.Detail == b.Detail && a.ToolName == b.ToolName && a.ToolUseID == b.ToolUseID && a.IsError == b.IsError
}

func TestActivityKindSystemRecords(t *testing.T) {
	cases := []struct {
		name string
		doc  session.Doc
		want ActivityKindResult
	}{
		{"compaction", session.Doc{"line": map[string]any{"type": "system", "subtype": "compact_boundary", "compactMetadata": map[string]any{"trigger": "auto", "preTokens": 791988.0}}}, ActivityKindResult{Kind: "compaction", Label: "compaction", Detail: "auto 791988 tokens"}},
		{"api error", session.Doc{"line": map[string]any{"type": "system", "subtype": "api_error", "error": map[string]any{"status": 529.0, "message": "Overloaded"}}}, ActivityKindResult{Kind: "api_error", Label: "api_error", Detail: "529 Overloaded"}},
		{"turn", session.Doc{"line": map[string]any{"type": "system", "subtype": "turn_duration", "durationMs": 12345.0}}, ActivityKindResult{Kind: "turn", Label: "turn", Detail: "12.345s"}},
	}
	for _, c := range cases {
		if got := activityOf(t, c.doc); !equalActivity(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
