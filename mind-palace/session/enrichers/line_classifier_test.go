package enrichers

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func lineTypeJSON(lineType string) string { return `{"type":"` + lineType + `"}` }

func TestLineClassifierNameAndCollection(t *testing.T) {
	requireTranscriptEnricher(t, LineClassifier, "line-classifier")
}

func TestLineClassifierMatchesAllDocs(t *testing.T) {
	if !LineClassifier.Matches(session.Doc{}) {
		t.Error("Matches(empty doc) = false, want true")
	}
	if !LineClassifier.Matches(session.Doc{"anything": true}) {
		t.Error("Matches(doc with other keys) = false, want true")
	}
}

func TestLineClassifierClassifiesSessionMetaLastPrompt(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"type": "last-prompt", "sessionId": "abc"}})
	if want := lineTypeJSON("session_meta"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesSessionMetaPermissionMode(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"type": "permission-mode"}})
	if want := lineTypeJSON("session_meta"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesHookSuccess(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"attachment": map[string]any{"type": "hook_success"}}})
	if want := lineTypeJSON("hook_success"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesHookError(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"attachment": map[string]any{"type": "hook_error"}}})
	if want := lineTypeJSON("hook_error"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesUserPrompt(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"display": "hello", "sessionId": "abc", "timestamp": 123}})
	if want := lineTypeJSON("user_prompt"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierDisplayWithoutSessionIdIsNotUserPrompt(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"display": "hello", "sessionId": nil}})
	if want := lineTypeJSON("unknown"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesToolResult(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"message": map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "x", "content": []any{}}},
	}}})
	if want := lineTypeJSON("tool_result"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesAssistant(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"message": map[string]any{
		"role":    "assistant",
		"content": []any{map[string]any{"type": "text", "text": "hello"}},
	}}})
	if want := lineTypeJSON("assistant"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesAssistantWithToolUseBlocks(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"message": map[string]any{
		"role":    "assistant",
		"content": []any{map[string]any{"type": "tool_use", "id": "x", "name": "Bash", "input": map[string]any{}}},
	}}})
	if want := lineTypeJSON("assistant"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesUnknownForUnrecognizedShapes(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"foo": "bar"}})
	if want := lineTypeJSON("unknown"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierClassifiesUnknownWhenLineIsNull(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": nil})
	if want := lineTypeJSON("unknown"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierUserMessageWithoutToolResultIsUnknown(t *testing.T) {
	got := marshalEnrichment(t, LineClassifier, session.Doc{"line": map[string]any{"message": map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "text", "text": "hi"}},
	}}})
	if want := lineTypeJSON("unknown"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLineClassifierResultTagsMatch(t *testing.T) {
	requireBSONTagsMirrorJSON(t, LineClassification{})
}
