package enrichers

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

type diffStatsResult struct {
	LinesAdded   int `json:"lines_added" bson:"lines_added"`
	LinesRemoved int `json:"lines_removed" bson:"lines_removed"`
	NetChange    int `json:"net_change" bson:"net_change"`
}

// EditDiffStats counts the lines an Edit or Write call added and removed.
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
		if tool, _ := doc.String("tool_name"); tool == "Write" {
			content, _ := input.String("content")
			added := countTextLines(content)
			return diffStatsResult{LinesAdded: added, NetChange: added}, nil
		}
		newString, _ := input.String("new_string")
		oldString, _ := input.String("old_string")
		added, removed := countTextLines(newString), countTextLines(oldString)
		return diffStatsResult{LinesAdded: added, LinesRemoved: removed, NetChange: added - removed}, nil
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
