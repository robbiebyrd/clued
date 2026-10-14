package enrichers

import (
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func resultLine(toolUseID string, result any) session.Doc {
	return session.Doc{"seq": 5, "line": map[string]any{
		"type":          "user",
		"message":       map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": "ok"}}},
		"toolUseResult": result,
	}}
}

func detailOf(t *testing.T, doc session.Doc) ToolResultDetail {
	t.Helper()
	got, err := ToolResultDetailEnricher.Enrich(t.Context(), doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(ToolResultDetail)
}

func TestToolResultDetailNameAndMatching(t *testing.T) {
	requireTranscriptEnricher(t, ToolResultDetailEnricher, "tool-result-detail")
	requireBSONTagsMirrorJSON(t, ToolResultDetail{})
	if !ToolResultDetailEnricher.Matches(resultLine("t1", "x")) {
		t.Error("a line with a toolUseResult matches")
	}
	if ToolResultDetailEnricher.Matches(session.Doc{"line": map[string]any{"type": "user"}}) || ToolResultDetailEnricher.Matches(session.Doc{}) {
		t.Error("a line without a toolUseResult does not match")
	}
}

func TestToolResultDetailEditsAndWrites(t *testing.T) {
	patch := []any{map[string]any{"oldStart": 10.0, "oldLines": 3.0, "newStart": 10.0, "newLines": 5.0, "lines": []any{" a", "-b", "+c", "+d", "+e", " f"}}}
	edit := detailOf(t, resultLine("t-edit", map[string]any{"filePath": "/repo/pkg/a.go", "oldString": "b", "newString": "c\nd\ne", "structuredPatch": patch, "userModified": false}))
	if edit.Tool != "edit" || edit.ToolUseID != "t-edit" || edit.FilePath != "/repo/pkg/a.go" || edit.Span != "L10-14" || edit.LinesAdded != 3 || edit.LinesRemoved != 1 || len(edit.Hunks) != 1 {
		t.Errorf("edit = %+v", edit)
	}
	if edit.Summary != "a.go L10-14 +3 -1" || !strings.HasPrefix(edit.Diff, "@@ -10,3 +10,5 @@\n a\n-b\n+c\n") {
		t.Errorf("edit summary %q diff %q", edit.Summary, edit.Diff)
	}
	write := detailOf(t, resultLine("t-write", map[string]any{"type": "create", "filePath": "/repo/new.go", "content": "x\ny\n", "structuredPatch": []any{map[string]any{"oldStart": 0, "oldLines": 0, "newStart": 1, "newLines": 2, "lines": []any{"+x", "+y"}}}}))
	if write.Tool != "write" || write.Summary != "new.go L1-2 +2 -0" || write.LinesAdded != 2 {
		t.Errorf("write = %+v", write)
	}
	// A huge diff is capped.
	var lines []any
	for i := 0; i < 2000; i++ {
		lines = append(lines, "+"+strings.Repeat("x", 20))
	}
	big := detailOf(t, resultLine("t-big", map[string]any{"filePath": "/repo/big.go", "structuredPatch": []any{map[string]any{"oldStart": 1, "oldLines": 0, "newStart": 1, "newLines": 2000, "lines": lines}}}))
	if len(big.Diff) > diffCap+len(truncatedMark) || !strings.HasSuffix(big.Diff, truncatedMark) || big.LinesAdded != 2000 {
		t.Errorf("big diff: %d bytes, added %d", len(big.Diff), big.LinesAdded)
	}
}

func TestToolResultDetailCommandsReadsTasksAndOthers(t *testing.T) {
	bash := detailOf(t, resultLine("t-bash", map[string]any{"stdout": "\x1b[32mok\x1b[0m\nline 2", "stderr": "", "interrupted": false, "isImage": false}))
	if bash.Tool != "bash" || bash.Stdout != "ok\nline 2" || bash.Stderr != "" || bash.Interrupted || bash.ExitCode != nil || bash.Summary != "2 lines" {
		t.Errorf("bash = %+v", bash)
	}
	killed := detailOf(t, resultLine("t-kill", map[string]any{"stdout": "", "stderr": "boom", "interrupted": true, "exitCode": 130.0}))
	if !killed.Interrupted || killed.Stderr != "boom" || killed.ExitCode == nil || *killed.ExitCode != 130 || killed.Summary != "exit 130, 0 lines, stderr, [interrupted]" {
		t.Errorf("interrupted = %+v", killed)
	}
	long := detailOf(t, resultLine("t-long", map[string]any{"stdout": strings.Repeat("é", 5000), "stderr": ""}))
	if len(long.Stdout) > outputCap+len(truncatedMark) || !strings.HasSuffix(long.Stdout, truncatedMark) || !strings.HasPrefix(long.Stdout, "éé") {
		t.Errorf("stdout cap: %d bytes", len(long.Stdout))
	}

	read := detailOf(t, resultLine("t-read", map[string]any{"type": "text", "file": map[string]any{"filePath": "/repo/README.md", "content": "…", "startLine": 1.0, "numLines": 50.0, "totalLines": 125.0}}))
	if read.Tool != "read" || read.FilePath != "/repo/README.md" || read.StartLine != 1 || read.NumLines != 50 || read.TotalLines != 125 || read.Span != "L1-50" || read.Summary != "README.md L1-50" {
		t.Errorf("read = %+v", read)
	}

	task := detailOf(t, resultLine("t-task", map[string]any{"agentId": "a1", "description": "Review the diff", "prompt": "…", "status": "completed", "content": []any{map[string]any{"type": "text", "text": "Looks fine."}}, "totalTokens": 12.0}))
	if task.Tool != "task" || task.Description != "Review the diff" || task.AgentID != "a1" || task.Status != "completed" || task.Result != "Looks fine." || task.Summary != "Review the diff (completed)" {
		t.Errorf("task = %+v", task)
	}
	async := detailOf(t, resultLine("t-async", map[string]any{"isAsync": true, "status": "async_launched", "agentId": "a2", "description": "Map Docker use", "prompt": "…"}))
	if async.Tool != "task" || async.Result != "" || async.Summary != "Map Docker use (async_launched)" {
		t.Errorf("async task = %+v", async)
	}

	text := detailOf(t, resultLine("t-text", "Error: Exit code 1\ncat: x: No such file"))
	if text.Tool != "text" || text.Text != "Error: Exit code 1\ncat: x: No such file" || text.Summary != "Error: Exit code 1" {
		t.Errorf("text = %+v", text)
	}
	other := detailOf(t, resultLine("t-web", map[string]any{"url": "https://example.com", "code": 200.0, "bytes": 10.0, "result": "…"}))
	if other.Tool != "other" || strings.Join(other.Keys, ",") != "bytes,code,result,url" || other.Summary != "bytes, code, result, url" {
		t.Errorf("other = %+v", other)
	}
	if got := marshalEnrichment(t, ToolResultDetailEnricher, resultLine("t-x", 42)); got != `{"tool":"other","summary":"","tool_use_id":"t-x"}` {
		t.Errorf("stored shape = %s", got)
	}
}
