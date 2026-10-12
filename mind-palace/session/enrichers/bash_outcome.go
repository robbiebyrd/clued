package enrichers

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

type bashOutcomeResult struct {
	Success     bool `json:"success" bson:"success"`
	HasError    bool `json:"has_error" bson:"has_error"`
	OutputLines int  `json:"output_lines" bson:"output_lines"`
	Truncated   bool `json:"truncated" bson:"truncated"`
}

// BashOutcome records whether a Bash call wrote to stderr and how much it printed.
var BashOutcome = session.Enricher{
	Name:       "bash-outcome",
	Collection: "hook_events",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		event, _ := doc.String("hook_event_name")
		tool, _ := doc.String("tool_name")
		return event == "PostToolUse" && tool == "Bash"
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		response := doc.ToolResponse()
		stdout, _ := response.String("stdout")
		success := isFalsy(response["stderr"])
		return bashOutcomeResult{
			Success:     success,
			HasError:    !success,
			OutputLines: countTextLines(stdout),
			Truncated:   strings.Contains(stdout, "[truncated]"),
		}, nil
	},
}

func init() { session.Register(BashOutcome) }

// isFalsy mirrors JavaScript truthiness for decoded JSON/BSON values.
func isFalsy(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case float64:
		return x == 0
	case int:
		return x == 0
	case int32:
		return x == 0
	case int64:
		return x == 0
	}
	return false
}
