package enrichers

import (
	"context"
	"sort"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// ToolUseSummary lists the tools an assistant line calls and whether it thinks.
var ToolUseSummary = session.Enricher{
	Name:       "tool-use-summary",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich:     enrichToolUseSummary,
}

func init() { session.Register(ToolUseSummary) }

type toolSummary struct {
	ID        string   `json:"id" bson:"id"`
	Name      string   `json:"name" bson:"name"`
	InputKeys []string `json:"inputKeys" bson:"inputKeys"`
}

type toolUseSummaryResult struct {
	Tools       []toolSummary `json:"tools" bson:"tools"`
	HasThinking bool          `json:"has_thinking" bson:"has_thinking"`
}

func enrichToolUseSummary(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
	r := toolUseSummaryResult{Tools: []toolSummary{}}
	if role, _ := doc.Message().String("role"); role != "assistant" {
		return r, nil
	}
	for _, block := range doc.Content() {
		switch block["type"] {
		case "thinking":
			r.HasThinking = true
		case "tool_use":
			id, _ := block.String("id")
			name, _ := block.String("name")
			r.Tools = append(r.Tools, toolSummary{ID: id, Name: name, InputKeys: sortedKeys(block.Map("input"))})
		}
	}
	return r, nil
}

// sortedKeys returns the keys of d in alphabetical order (never nil), since a
// decoded Go map has no insertion order.
func sortedKeys(d session.Doc) []string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
