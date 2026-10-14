package enrichers

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/structpatch"
)

type diffStatsResult struct {
	LinesAdded   int `json:"lines_added" bson:"lines_added"`
	LinesRemoved int `json:"lines_removed" bson:"lines_removed"`
	NetChange    int `json:"net_change" bson:"net_change"`
	// FilePath is the edited file; Span names the lines the edit touched
	// in the new file ("L10-12"), from the tool response's structuredPatch
	// when the hook carried one.
	FilePath string `json:"file_path,omitempty" bson:"file_path,omitempty"`
	Span     string `json:"span,omitempty" bson:"span,omitempty"`
}

// EditDiffStats counts the lines an Edit or Write call added and removed,
// and names the span it touched when the response carries the patch.
var EditDiffStats = session.Enricher{
	Name:       "edit-diff-stats",
	Collection: "hook_events",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		event, _ := doc.String("hook_event_name")
		tool, _ := doc.String("tool_name")
		return event == "PostToolUse" && (tool == "Edit" || tool == "Write")
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		input := doc.ToolInput()
		var r diffStatsResult
		if tool, _ := doc.String("tool_name"); tool == "Write" {
			content, _ := input.String("content")
			r.LinesAdded = countTextLines(content)
		} else {
			newString, _ := input.String("new_string")
			oldString, _ := input.String("old_string")
			r.LinesAdded, r.LinesRemoved = countTextLines(newString), countTextLines(oldString)
		}
		r.NetChange = r.LinesAdded - r.LinesRemoved
		r.FilePath, _ = input.String("file_path")
		if s, ok := structpatch.Parse(doc.ToolResponse()["structuredPatch"]); ok && len(s.Hunks) > 0 {
			r.Span = s.Span
			if p, ok := doc.ToolResponse().String("filePath"); ok && p != "" {
				r.FilePath = p
			}
		}
		return r, nil
	},
}

func init() { session.Register(EditDiffStats) }

// countTextLines counts newline-separated lines; the empty string has none and
// a trailing newline starts one more.
func countTextLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
