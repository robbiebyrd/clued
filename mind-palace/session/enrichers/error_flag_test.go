package enrichers

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	noError       = `{"is_error":false,"error_type":null}`
	hookError     = `{"is_error":true,"error_type":"hook_error"}`
	toolFailure   = `{"is_error":true,"error_type":"tool_failure"}`
	errorTextFlag = `{"is_error":true,"error_type":"error_text"}`
)

func assistantErrorTextDoc(text string) session.Doc {
	return session.Doc{"line": map[string]any{"message": map[string]any{
		"role":    "assistant",
		"content": []any{map[string]any{"type": "text", "text": text}},
	}}}
}

func TestErrorFlagNameAndCollection(t *testing.T) {
	requireTranscriptEnricher(t, ErrorFlag, "error-flag")
}

func TestErrorFlagMatchesAllDocs(t *testing.T) {
	if !ErrorFlag.Matches(session.Doc{}) {
		t.Error("Matches(empty doc) = false, want true")
	}
}

func TestErrorFlagNoErrorForEmptyLine(t *testing.T) {
	if got := marshalEnrichment(t, ErrorFlag, session.Doc{"line": map[string]any{}}); got != noError {
		t.Errorf("got %s, want %s", got, noError)
	}
}

func TestErrorFlagNoErrorWhenLineIsMissing(t *testing.T) {
	if got := marshalEnrichment(t, ErrorFlag, session.Doc{}); got != noError {
		t.Errorf("got %s, want %s", got, noError)
	}
}

func TestErrorFlagDetectsHookError(t *testing.T) {
	got := marshalEnrichment(t, ErrorFlag, session.Doc{"line": map[string]any{
		"attachment": map[string]any{"type": "hook_error"},
	}})
	if got != hookError {
		t.Errorf("got %s, want %s", got, hookError)
	}
}

func TestErrorFlagDetectsToolFailureViaIsErrorOnToolResultBlock(t *testing.T) {
	got := marshalEnrichment(t, ErrorFlag, session.Doc{"line": map[string]any{"message": map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "x", "is_error": true, "content": "failed"},
		},
	}}})
	if got != toolFailure {
		t.Errorf("got %s, want %s", got, toolFailure)
	}
}

func TestErrorFlagNoToolFailureWhenIsErrorIsFalseOnToolResult(t *testing.T) {
	got := marshalEnrichment(t, ErrorFlag, session.Doc{"line": map[string]any{"message": map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "x", "is_error": false, "content": "ok"},
		},
	}}})
	if got != noError {
		t.Errorf("got %s, want %s", got, noError)
	}
}

func TestErrorFlagDetectsErrorTextENOENT(t *testing.T) {
	got := marshalEnrichment(t, ErrorFlag, assistantErrorTextDoc("ENOENT: no such file or directory"))
	if got != errorTextFlag {
		t.Errorf("got %s, want %s", got, errorTextFlag)
	}
}

func TestErrorFlagDetectsErrorTextErrorColon(t *testing.T) {
	got := marshalEnrichment(t, ErrorFlag, assistantErrorTextDoc("Error: connection refused"))
	if got != errorTextFlag {
		t.Errorf("got %s, want %s", got, errorTextFlag)
	}
}

func TestErrorFlagNoFalsePositiveOnNormalAssistantText(t *testing.T) {
	got := marshalEnrichment(t, ErrorFlag, assistantErrorTextDoc("The function works correctly."))
	if got != noError {
		t.Errorf("got %s, want %s", got, noError)
	}
}

func TestErrorFlagHookErrorTakesPrecedenceOverTextPattern(t *testing.T) {
	got := marshalEnrichment(t, ErrorFlag, session.Doc{"line": map[string]any{
		"attachment": map[string]any{"type": "hook_error"},
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": "Error: something"}},
		},
	}})
	if got != hookError {
		t.Errorf("got %s, want %s", got, hookError)
	}
}

func TestErrorFlagToolFailureTakesPrecedenceOverTextPattern(t *testing.T) {
	got := marshalEnrichment(t, ErrorFlag, session.Doc{"line": map[string]any{"message": map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "Error: something"},
			map[string]any{"type": "tool_result", "is_error": true},
		},
	}}})
	if got != toolFailure {
		t.Errorf("got %s, want %s", got, toolFailure)
	}
}

// ERROR_TEXT_RE is /\b(Error:|ENOENT|EACCES|exception|stack trace|exit code [^0])/.
func TestErrorFlagTextPatternAlternatives(t *testing.T) {
	cases := map[string]bool{
		"EACCES: permission denied": true,
		"an exception occurred":     true,
		"see the stack trace below": true,
		"exit code 2":               true,
		"exit code 0":               false,
		"exit code ":                false,
		"MyError: boom":             false,
		"error: lowercase":          false,
		"EXCEPTION":                 false,
		"all good":                  false,
	}
	for text, want := range cases {
		t.Run(text, func(t *testing.T) {
			wantJSON := noError
			if want {
				wantJSON = errorTextFlag
			}
			if got := marshalEnrichment(t, ErrorFlag, assistantErrorTextDoc(text)); got != wantJSON {
				t.Errorf("got %s, want %s", got, wantJSON)
			}
		})
	}
}

func TestErrorFlagResultTagsMatch(t *testing.T) {
	requireBSONTagsMirrorJSON(t, ErrorFlagResult{})
}
