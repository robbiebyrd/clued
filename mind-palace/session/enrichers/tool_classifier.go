package enrichers

import (
	"context"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

type toolClassResult struct {
	Category   string `json:"category" bson:"category"`
	IsReadOnly bool   `json:"is_read_only" bson:"is_read_only"`
}

var toolClasses = map[string]toolClassResult{
	"Read":      {"file_read", true},
	"Write":     {"file_write", false},
	"Edit":      {"file_edit", false},
	"Bash":      {"shell_exec", false},
	"WebFetch":  {"web_fetch", true},
	"WebSearch": {"web_fetch", true},
	"Grep":      {"code_search", true},
	"Glob":      {"code_search", true},
	"LS":        {"code_search", true},
	"Agent":     {"agent_dispatch", false},
	"Task":      {"agent_dispatch", false},
}

// ToolClassifier sorts each tool call into a category and read-only flag.
var ToolClassifier = session.Enricher{
	Name:       "tool-classifier",
	Collection: "hook_events",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		event, _ := doc.String("hook_event_name")
		return event == "PreToolUse"
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		tool, _ := doc.String("tool_name")
		if class, known := toolClasses[tool]; known {
			return class, nil
		}
		return toolClassResult{Category: "other"}, nil
	},
}

func init() { session.Register(ToolClassifier) }
