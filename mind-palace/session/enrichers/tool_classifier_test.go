package enrichers

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func toolClassOf(t *testing.T, doc session.Doc) toolClassResult {
	t.Helper()
	got, err := ToolClassifier.Enrich(context.Background(), doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(toolClassResult)
}

func expectToolClass(t *testing.T, tool, category string, readOnly bool) {
	t.Helper()
	got := toolClassOf(t, session.Doc{"hook_event_name": "PreToolUse", "tool_name": tool})
	if want := (toolClassResult{Category: category, IsReadOnly: readOnly}); got != want {
		t.Errorf("%s: got %+v, want %+v", tool, got, want)
	}
}

func TestToolClassifierNameAndCollection(t *testing.T) {
	if ToolClassifier.Name != "tool-classifier" || ToolClassifier.Collection != "hook_events" || !ToolClassifier.Enabled {
		t.Errorf("unexpected metadata: %+v", ToolClassifier)
	}
}

func TestToolClassifierMatchesPreToolUseOnly(t *testing.T) {
	cases := []struct {
		doc  session.Doc
		want bool
	}{
		{session.Doc{"hook_event_name": "PreToolUse"}, true},
		{session.Doc{"hook_event_name": "PostToolUse"}, false},
		{session.Doc{"hook_event_name": "UserPromptSubmit"}, false},
		{session.Doc{}, false},
	}
	for _, c := range cases {
		if got := ToolClassifier.Matches(c.doc); got != c.want {
			t.Errorf("%v: got %v, want %v", c.doc, got, c.want)
		}
	}
}

func TestToolClassifierReadIsFileRead(t *testing.T) { expectToolClass(t, "Read", "file_read", true) }
func TestToolClassifierWriteIsFileWrite(t *testing.T) {
	expectToolClass(t, "Write", "file_write", false)
}
func TestToolClassifierEditIsFileEdit(t *testing.T)  { expectToolClass(t, "Edit", "file_edit", false) }
func TestToolClassifierBashIsShellExec(t *testing.T) { expectToolClass(t, "Bash", "shell_exec", false) }
func TestToolClassifierWebFetchIsWebFetch(t *testing.T) {
	expectToolClass(t, "WebFetch", "web_fetch", true)
}
func TestToolClassifierWebSearchIsWebFetch(t *testing.T) {
	expectToolClass(t, "WebSearch", "web_fetch", true)
}
func TestToolClassifierGrepIsCodeSearch(t *testing.T) {
	expectToolClass(t, "Grep", "code_search", true)
}
func TestToolClassifierGlobIsCodeSearch(t *testing.T) {
	expectToolClass(t, "Glob", "code_search", true)
}
func TestToolClassifierLSIsCodeSearch(t *testing.T) { expectToolClass(t, "LS", "code_search", true) }
func TestToolClassifierAgentIsAgentDispatch(t *testing.T) {
	expectToolClass(t, "Agent", "agent_dispatch", false)
}
func TestToolClassifierTaskIsAgentDispatch(t *testing.T) {
	expectToolClass(t, "Task", "agent_dispatch", false)
}
func TestToolClassifierUnknownToolIsOther(t *testing.T) {
	expectToolClass(t, "SomeFutureTool", "other", false)
}

func TestToolClassifierMissingToolNameIsOther(t *testing.T) {
	got := toolClassOf(t, session.Doc{"hook_event_name": "PreToolUse"})
	if want := (toolClassResult{Category: "other"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestToolClassifierResultShape(t *testing.T) {
	hookEventTagsMatch(t, toolClassResult{})
	j := hookEventJSON(t, toolClassResult{Category: "file_read", IsReadOnly: true})
	if j != `{"category":"file_read","is_read_only":true}` {
		t.Errorf("got %s", j)
	}
}
