package enrichers

import (
	"context"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// HookLinker links a line to the hook events of the tool calls it mentions.
var HookLinker = session.Enricher{
	Name:       "hook-linker",
	Collection: "transcript_lines",
	Enabled:    true,
	BatchLimit: 20,
	Matches:    func(session.Doc) bool { return true },
	Enrich:     enrichHookLinker,
}

func init() { session.Register(HookLinker) }

type hookLinkerResult struct {
	ToolUseIDs   []string `json:"tool_use_ids" bson:"tool_use_ids"`
	HookEventIDs []string `json:"hook_event_ids" bson:"hook_event_ids"`
}

func enrichHookLinker(ctx context.Context, doc session.Doc, lookup session.Lookup) (any, error) {
	ids := []string{}
	seen := map[string]bool{}
	add := func(id string, ok bool) {
		if ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	add(doc.Attachment().String("toolUseID"))
	for _, block := range doc.Content() {
		if block["type"] == "tool_use" {
			add(block.String("id"))
		}
	}
	if len(ids) == 0 {
		return hookLinkerResult{ToolUseIDs: ids, HookEventIDs: []string{}}, nil
	}

	sessionID, _ := doc.String("session_id")
	hookEventIDs, err := lookup.HookEventIDsByToolUse(ctx, sessionID, ids)
	if err != nil {
		return nil, err
	}
	if hookEventIDs == nil {
		hookEventIDs = []string{}
	}
	return hookLinkerResult{ToolUseIDs: ids, HookEventIDs: hookEventIDs}, nil
}
