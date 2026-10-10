package session

import "testing"

func TestDocAccessorsOnNestedDocument(t *testing.T) {
	d := Doc{
		"session_id":    "s1",
		"tool_input":    map[string]any{"command": "ls"},
		"tool_response": map[string]any{"stdout": "x"},
		"line": map[string]any{
			"attachment": map[string]any{"type": "file"},
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": "hi"},
					"not an object",
					map[string]any{"type": "tool_use"},
				},
			},
		},
	}

	if got, ok := d.String("session_id"); !ok || got != "s1" {
		t.Fatalf("String = %q, %v", got, ok)
	}
	if got, _ := d.ToolInput().String("command"); got != "ls" {
		t.Fatalf("ToolInput command = %q", got)
	}
	if got, _ := d.ToolResponse().String("stdout"); got != "x" {
		t.Fatalf("ToolResponse stdout = %q", got)
	}
	if got, _ := d.Attachment().String("type"); got != "file" {
		t.Fatalf("Attachment type = %q", got)
	}
	if got, _ := d.Content()[0].String("text"); got != "hi" {
		t.Fatalf("first content text = %q", got)
	}
	content := d.Content()
	if len(content) != 2 {
		t.Fatalf("Content len = %d, want 2 (non-objects skipped)", len(content))
	}
	if got, _ := content[1].String("type"); got != "tool_use" {
		t.Fatalf("second content type = %q", got)
	}
}

func TestDocAcceptsDocValuedNesting(t *testing.T) {
	d := Doc{"line": Doc{"message": Doc{"content": []Doc{{"type": "text"}}}}}
	if got := d.Message(); got == nil {
		t.Fatal("Message = nil for Doc-typed nesting")
	}
}

func TestDocMissingKeys(t *testing.T) {
	d := Doc{}
	if d.Map("nope") != nil {
		t.Fatal("Map of missing key should be nil")
	}
	if _, ok := d.String("nope"); ok {
		t.Fatal("String of missing key should not be ok")
	}
	if d.Line() != nil || d.Message() != nil || d.ToolInput() != nil || d.ToolResponse() != nil || d.Attachment() != nil {
		t.Fatal("accessors on empty doc should be nil")
	}
	if d.Content() != nil {
		t.Fatal("Content of empty doc should be nil")
	}
	var nilDoc Doc
	if nilDoc.Message() != nil || nilDoc.Content() != nil {
		t.Fatal("accessors on nil doc should be nil")
	}
}

func TestDocWronglyTypedKeys(t *testing.T) {
	d := Doc{
		"line":       "a string",
		"tool_input": 42,
		"count":      7,
	}
	if d.Line() != nil || d.ToolInput() != nil {
		t.Fatal("non-object values should give nil Doc")
	}
	if _, ok := d.String("count"); ok {
		t.Fatal("String of a number should not be ok")
	}
	if got := (Doc{"line": map[string]any{"message": map[string]any{"content": "text"}}}).Content(); got != nil {
		t.Fatalf("Content with non-list content = %v, want nil", got)
	}
}
