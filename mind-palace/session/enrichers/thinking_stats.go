package enrichers

import (
	"context"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// ThinkingStats measures the thinking and text of an assistant line.
var ThinkingStats = session.Enricher{
	Name:       "thinking-stats",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(doc session.Doc) bool { return doc.Line()["type"] == "assistant" },
	Enrich:     enrichThinkingStats,
}

func init() { session.Register(ThinkingStats) }

type thinkingStatsResult struct {
	ThinkingChars int `json:"thinking_chars" bson:"thinking_chars"`
	TextChars     int `json:"text_chars" bson:"text_chars"`
}

func enrichThinkingStats(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
	var r thinkingStatsResult
	if role, _ := doc.Message().String("role"); role != "assistant" {
		return r, nil
	}
	for _, block := range doc.Content() {
		switch block["type"] {
		case "thinking":
			if s, ok := block.String("thinking"); ok {
				r.ThinkingChars += utf16Length(s)
			}
		case "text":
			if s, ok := block.String("text"); ok {
				r.TextChars += utf16Length(s)
			}
		}
	}
	return r, nil
}
