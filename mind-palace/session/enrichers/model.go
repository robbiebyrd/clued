package enrichers

import (
	"context"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The sources of a stored model: the line named it itself, it was carried
// forward from the nearest preceding assistant line of the session, it came
// from the session's latest assistant line (a hook event no line preceded),
// the event named it itself, or none was found.
const (
	ModelSourceLine    = "line"
	ModelSourceCarried = "carried"
	ModelSourceSession = "session"
	ModelSourceEvent   = "event"
	ModelSourceUnknown = "unknown"
)

// ModelResult is what the model and hook-model enrichers store: the model
// serving the session when the document was written, and where that
// knowledge came from.
type ModelResult struct {
	Model  string `json:"model" bson:"model"`
	Source string `json:"source" bson:"source"`
}

// Model records the model in use on every transcript line: an assistant
// line's own message.model, else the model of the nearest preceding
// assistant line of the session.
var Model = session.Enricher{
	Name:       "model",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich: func(ctx context.Context, doc session.Doc, lookup session.Lookup) (any, error) {
		if model := session.LineModel(doc); model != "" {
			return ModelResult{Model: model, Source: ModelSourceLine}, nil
		}
		sessionID, _ := doc.String("session_id")
		seq, ok := seqOf(doc["seq"])
		if sessionID == "" || !ok || lookup == nil {
			return ModelResult{Source: ModelSourceUnknown}, nil
		}
		model, err := lookup.LastModelBefore(ctx, sessionID, seq, time.Time{})
		if err != nil {
			return nil, err
		}
		if model == "" {
			return ModelResult{Source: ModelSourceUnknown}, nil
		}
		return ModelResult{Model: model, Source: ModelSourceCarried}, nil
	},
}

// HookModel records the model in use when a hook event happened: the model
// the event names itself (a LimitsSampled sample), else that of the latest
// assistant line of the session stored by then, else the session's latest
// assistant line at all.
var HookModel = session.Enricher{
	Name:       "hook-model",
	Collection: "hook_events",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich: func(ctx context.Context, doc session.Doc, lookup session.Lookup) (any, error) {
		if id, ok := doc.Map("model").String("id"); ok && id != "" {
			return ModelResult{Model: id, Source: ModelSourceEvent}, nil
		}
		if model, ok := doc.String("model"); ok && model != "" {
			return ModelResult{Model: model, Source: ModelSourceEvent}, nil
		}
		sessionID, _ := doc.String("session_id")
		if sessionID == "" || lookup == nil {
			return ModelResult{Source: ModelSourceUnknown}, nil
		}
		if at, ok := doc["created_at"].(time.Time); ok {
			model, err := lookup.LastModelBefore(ctx, sessionID, -1, at)
			if err != nil {
				return nil, err
			}
			if model != "" {
				return ModelResult{Model: model, Source: ModelSourceCarried}, nil
			}
		}
		model, err := lookup.LastModelBefore(ctx, sessionID, -1, time.Time{})
		if err != nil {
			return nil, err
		}
		if model == "" {
			return ModelResult{Source: ModelSourceUnknown}, nil
		}
		return ModelResult{Model: model, Source: ModelSourceSession}, nil
	},
}

func init() {
	session.Register(Model)
	session.Register(HookModel)
}

// seqOf reads a stored seq whatever its decoded type.
func seqOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}
