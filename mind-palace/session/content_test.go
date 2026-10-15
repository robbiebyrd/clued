package session

import (
	"reflect"
	"testing"
)

func TestWithoutContentStripsHookContent(t *testing.T) {
	ev := Doc{
		"_id": "x", "session_id": "s", "hook_event_name": "PostToolUse", "tool_name": "Edit", "tool_use_id": "t1", "cwd": "/w",
		"prompt": "p", "tool_response": Doc{"out": "o"}, "message": "m", "last_assistant_message": "a", "custom_instructions": "c",
		"tool_input":  map[string]any{"file_path": "/w/f.go", "old_string": "secret", "new_string": "code"},
		"enrichments": Doc{"edit-diff-stats": Doc{"added": 1}},
	}
	got, changed := WithoutContent(ev)
	want := Doc{
		"_id": "x", "session_id": "s", "hook_event_name": "PostToolUse", "tool_name": "Edit", "tool_use_id": "t1", "cwd": "/w",
		"tool_input": Doc{"file_path": "/w/f.go"}, "enrichments": Doc{"edit-diff-stats": Doc{"added": 1}},
	}
	if !changed || !reflect.DeepEqual(got, want) {
		t.Errorf("WithoutContent = %v, %v; want %v, true", got, changed, want)
	}
	if ev["prompt"] != "p" || len(ev["tool_input"].(map[string]any)) != 3 {
		t.Errorf("the input was changed: %v", ev)
	}
	if again, changed := WithoutContent(got); changed || !reflect.DeepEqual(again, got) {
		t.Errorf("a second pass changed %v into %v", got, again)
	}
	bash, _ := WithoutContent(Doc{"hook_event_name": "PreToolUse", "tool_input": Doc{"command": "make"}})
	if _, ok := bash["tool_input"]; ok {
		t.Errorf("a tool_input without file_path was kept: %v", bash)
	}
}

func TestWithoutContentEmptiesMemoryText(t *testing.T) {
	mem := Doc{"kind": "entry", "text": "the plan", "hash": "h", "id": "m1"}
	ev := Doc{"hook_event_name": MemoryEventName, "memory": mem, "enrichments": Doc{"privacy-redact-memory": Doc{"redacted_text": "the plan"}, "other": 1}}
	got, changed := WithoutContent(ev)
	if !changed || got.Map("memory")["text"] != "" || got.Map("memory")["hash"] != "h" || got.Map("memory")["id"] != "m1" {
		t.Errorf("memory = %v (changed %v)", got.Map("memory"), changed)
	}
	if _, ok := got.Map("enrichments")["privacy-redact-memory"]; ok || got.Map("enrichments")["other"] != 1 {
		t.Errorf("enrichments = %v", got.Map("enrichments"))
	}
	if mem["text"] != "the plan" {
		t.Error("the input's memory was changed")
	}
	if _, changed := WithoutContent(Doc{"hook_event_name": "Stop", "session_id": "s"}); changed {
		t.Error("an event without content reported a change")
	}
}
