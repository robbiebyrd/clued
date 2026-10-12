package enrichers

import (
	"context"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// LineClassification is the stored result of the line-classifier enricher.
// Type is user_prompt, assistant, tool_result, hook_success, hook_error,
// session_meta, or unknown.
type LineClassification struct {
	Type string `json:"type" bson:"type"`
}

// LineClassifier labels what kind of transcript line a document holds.
var LineClassifier = session.Enricher{
	Name:       "line-classifier",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		return LineClassification{Type: classifyLine(doc)}, nil
	},
}

func classifyLine(doc session.Doc) string {
	line := doc.Line()
	if line == nil {
		return "unknown"
	}

	lineType, _ := line.String("type")
	if lineType == "last-prompt" || lineType == "permission-mode" {
		return "session_meta"
	}

	switch attType, _ := doc.Attachment().String("type"); attType {
	case "hook_success", "hook_error":
		return attType
	}

	if line["display"] != nil && line["sessionId"] != nil {
		return "user_prompt"
	}

	role, _ := doc.Message().String("role")
	if role == "user" {
		for _, block := range doc.Content() {
			if blockType, _ := block.String("type"); blockType == "tool_result" {
				return "tool_result"
			}
		}
	}
	if role == "assistant" {
		return "assistant"
	}
	return "unknown"
}

func init() { session.Register(LineClassifier) }
