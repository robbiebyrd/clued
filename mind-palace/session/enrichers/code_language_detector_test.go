package enrichers

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func assistantTextDoc(text string) session.Doc {
	return session.Doc{"line": map[string]any{"message": map[string]any{
		"role":    "assistant",
		"content": []any{map[string]any{"type": "text", "text": text}},
	}}}
}

func TestCodeLanguageDetectorNameAndCollection(t *testing.T) {
	requireTranscriptEnricher(t, CodeLanguageDetector, "code-language-detector")
}

func TestCodeLanguageDetectorMatchesAllDocs(t *testing.T) {
	if !CodeLanguageDetector.Matches(session.Doc{}) {
		t.Error("Matches(empty doc) = false, want true")
	}
}

func TestCodeLanguageDetectorReturnsEmptyLanguagesForNonAssistantLine(t *testing.T) {
	got := marshalEnrichment(t, CodeLanguageDetector, session.Doc{"line": map[string]any{"display": "hello", "sessionId": "x"}})
	if want := `{"languages":[]}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCodeLanguageDetectorExtractsSingleLanguageFromFencedBlock(t *testing.T) {
	got := marshalEnrichment(t, CodeLanguageDetector, assistantTextDoc("Here:\n```typescript\nconst x = 1;\n```"))
	if want := `{"languages":["typescript"]}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCodeLanguageDetectorExtractsMultipleLanguagesDeduplicatedLowercased(t *testing.T) {
	got := marshalEnrichment(t, CodeLanguageDetector,
		assistantTextDoc("```Bash\nls\n```\nand\n```BASH\necho hi\n```\nand\n```json\n{}\n```"))
	if want := `{"languages":["bash","json"]}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCodeLanguageDetectorReturnsEmptyForAssistantTextWithNoCodeBlocks(t *testing.T) {
	got := marshalEnrichment(t, CodeLanguageDetector, assistantTextDoc("No code here."))
	if want := `{"languages":[]}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCodeLanguageDetectorHandlesEmptyContentArray(t *testing.T) {
	got := marshalEnrichment(t, CodeLanguageDetector, session.Doc{"line": map[string]any{"message": map[string]any{
		"role": "assistant", "content": []any{},
	}}})
	if want := `{"languages":[]}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// The JavaScript pattern is /^```(\w+)\s*$/gm: ^ and $ honour every JavaScript
// line terminator and \s covers the Unicode spaces.
func TestCodeLanguageDetectorFenceMatchingFollowsJavaScriptLineRules(t *testing.T) {
	cases := map[string]struct {
		text string
		want string
	}{
		"CRLF line endings":          {"```go\r\nx\r\n```\r\n```py\r\n", `{"languages":["go","py"]}`},
		"bare CR line endings":       {"a\r```go\rx\r```py\r", `{"languages":["go","py"]}`},
		"line separator":             {"a ```go x", `{"languages":["go"]}`},
		"trailing unicode space":     {"```go 　\n", `{"languages":["go"]}`},
		"trailing vertical tab":      {"```go\v\n", `{"languages":["go"]}`},
		"info string is not a fence": {"```go title=x\n", `{"languages":[]}`},
		"indented fence is ignored":  {"  ```go\n", `{"languages":[]}`},
		"mid-line backticks":         {"see ```go\n", `{"languages":[]}`},
		"bare fence has no language": {"```\ncode\n```\n", `{"languages":[]}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := marshalEnrichment(t, CodeLanguageDetector, assistantTextDoc(c.text)); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestCodeLanguageDetectorSkipsNonTextBlocks(t *testing.T) {
	got := marshalEnrichment(t, CodeLanguageDetector, session.Doc{"line": map[string]any{"message": map[string]any{
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "tool_use", "text": "```go\n"},
			map[string]any{"type": "text", "text": 5},
			map[string]any{"type": "text", "text": "```rust\n"},
		},
	}}})
	if want := `{"languages":["rust"]}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCodeLanguageDetectorResultTagsMatch(t *testing.T) {
	requireBSONTagsMirrorJSON(t, CodeLanguages{})
}
