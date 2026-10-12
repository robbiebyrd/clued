// Package session is the domain of the clued session capture: the documents
// stored for Claude Code sessions (hook events, transcript lines, blobs), the
// Store port that persists them, the enricher registry and the capture
// configuration. It depends on the standard library only; adapters live in
// other packages.
package session

// Doc is a stored document (hook event, transcript line, session…) as decoded
// JSON/BSON.
type Doc map[string]any

// asDoc converts a decoded JSON/BSON object to a Doc; nil when v is not one.
func asDoc(v any) Doc {
	switch m := v.(type) {
	case Doc:
		return m
	case map[string]any:
		return Doc(m)
	}
	return nil
}

// Map returns the nested object at key, or nil when it is absent or not an object.
func (d Doc) Map(key string) Doc { return asDoc(d[key]) }

// String returns the string at key and whether it was present as a string.
func (d Doc) String(key string) (string, bool) {
	s, ok := d[key].(string)
	return s, ok
}

// Line is the transcript line stored under "line".
func (d Doc) Line() Doc { return d.Map("line") }

// Message is the transcript line's "message" object.
func (d Doc) Message() Doc { return d.Line().Map("message") }

// Content returns the message's content blocks, skipping entries that are not
// objects. It is nil when message.content is missing or not a list.
func (d Doc) Content() []Doc {
	var blocks []Doc
	switch list := d.Message()["content"].(type) {
	case []Doc:
		return list
	case []any:
		for _, item := range list {
			if b := asDoc(item); b != nil {
				blocks = append(blocks, b)
			}
		}
	}
	return blocks
}

// ToolInput is the hook event's "tool_input" object.
func (d Doc) ToolInput() Doc { return d.Map("tool_input") }

// ToolResponse is the hook event's "tool_response" object.
func (d Doc) ToolResponse() Doc { return d.Map("tool_response") }

// Attachment is the transcript line's "attachment" object.
func (d Doc) Attachment() Doc { return d.Line().Map("attachment") }
