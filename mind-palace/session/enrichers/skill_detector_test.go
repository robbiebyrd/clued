package enrichers

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func skillDetectorOf(t *testing.T, line map[string]any) skillDetectorResult {
	t.Helper()
	r, err := SkillDetector.Enrich(context.Background(), session.Doc{"line": line}, nil)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return r.(skillDetectorResult)
}

func lineWithAssistantBlocks(blocks ...any) map[string]any {
	return map[string]any{"message": map[string]any{"role": "assistant", "content": blocks}}
}

func jsonString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(b)
}

func skillBlock(id string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": "Skill", "input": input}
}

func TestSkillDetectorMetadata(t *testing.T) {
	if SkillDetector.Name != "skill-detector" || SkillDetector.Collection != "transcript_lines" {
		t.Fatalf("name/collection = %q/%q", SkillDetector.Name, SkillDetector.Collection)
	}
	if !SkillDetector.Enabled || SkillDetector.BatchLimit != 0 {
		t.Fatalf("enabled/batch = %v/%d", SkillDetector.Enabled, SkillDetector.BatchLimit)
	}
}

func TestSkillDetectorMatchesAllDocs(t *testing.T) {
	if !SkillDetector.Matches(session.Doc{}) {
		t.Fatal("expected match")
	}
}

func TestSkillDetectorEmptyForNonAssistantLine(t *testing.T) {
	r := skillDetectorOf(t, map[string]any{"display": "hello", "sessionId": "x"})
	if r.Skills == nil || len(r.Skills) != 0 {
		t.Fatalf("got %#v, want empty non-nil", r.Skills)
	}
}

func TestSkillDetectorDetectsSkillInvocation(t *testing.T) {
	r := skillDetectorOf(t, lineWithAssistantBlocks(skillBlock("x", map[string]any{"skill": "brainstorming", "args": "feature X"})))
	want := []skillRef{{Name: "brainstorming", Args: skillArgs("feature X")}}
	if !reflect.DeepEqual(r.Skills, want) {
		t.Fatalf("got %#v", r.Skills)
	}
}

func TestSkillDetectorIgnoresNonSkillToolUse(t *testing.T) {
	r := skillDetectorOf(t, lineWithAssistantBlocks(
		map[string]any{"type": "tool_use", "id": "a", "name": "Bash", "input": map[string]any{"command": "ls"}},
		skillBlock("b", map[string]any{"skill": "tdd"}),
	))
	if !reflect.DeepEqual(r.Skills, []skillRef{{Name: "tdd"}}) {
		t.Fatalf("got %#v", r.Skills)
	}
}

func TestSkillDetectorMissingArgs(t *testing.T) {
	r := skillDetectorOf(t, lineWithAssistantBlocks(skillBlock("x", map[string]any{"skill": "no-args-skill"})))
	if !reflect.DeepEqual(r.Skills, []skillRef{{Name: "no-args-skill"}}) {
		t.Fatalf("got %#v", r.Skills)
	}
}

func TestSkillDetectorOmitsEmptyArgsFromStoredShape(t *testing.T) {
	// A missing args key must be absent from the stored document, not "".
	r := skillDetectorOf(t, lineWithAssistantBlocks(skillBlock("x", map[string]any{"skill": "a"})))
	if got := jsonString(t, r); got != `{"skills":[{"name":"a"}]}` {
		t.Fatalf("got %s", got)
	}
}

func TestSkillDetectorKeepsPresentEmptyArgs(t *testing.T) {
	r := skillDetectorOf(t, lineWithAssistantBlocks(skillBlock("x", map[string]any{"skill": "a", "args": ""})))
	if got := jsonString(t, r); got != `{"skills":[{"name":"a","args":""}]}` {
		t.Fatalf("got %s", got)
	}
}

func TestSkillDetectorSkipsNonStringSkillName(t *testing.T) {
	r := skillDetectorOf(t, lineWithAssistantBlocks(skillBlock("x", map[string]any{"skill": 5})))
	if len(r.Skills) != 0 {
		t.Fatalf("got %#v", r.Skills)
	}
}

func TestSkillDetectorMultipleInvocations(t *testing.T) {
	r := skillDetectorOf(t, lineWithAssistantBlocks(
		skillBlock("1", map[string]any{"skill": "alpha"}),
		skillBlock("2", map[string]any{"skill": "beta", "args": "x"}),
	))
	if len(r.Skills) != 2 {
		t.Fatalf("got %#v", r.Skills)
	}
}

func skillArgs(s string) *string { return &s }
