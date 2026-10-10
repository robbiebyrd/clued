package enrichers

import (
	"context"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// SkillDetector lists the Skill tool invocations of an assistant line.
var SkillDetector = session.Enricher{
	Name:       "skill-detector",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich:     enrichSkillDetector,
}

func init() { session.Register(SkillDetector) }

type skillRef struct {
	Name string  `json:"name" bson:"name"`
	Args *string `json:"args,omitempty" bson:"args,omitempty"`
}

type skillDetectorResult struct {
	Skills []skillRef `json:"skills" bson:"skills"`
}

func enrichSkillDetector(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
	skills := []skillRef{}
	if role, _ := doc.Message().String("role"); role != "assistant" {
		return skillDetectorResult{Skills: skills}, nil
	}
	for _, block := range doc.Content() {
		if block["type"] != "tool_use" || block["name"] != "Skill" {
			continue
		}
		input := block.Map("input")
		name, ok := input.String("skill")
		if !ok {
			continue
		}
		ref := skillRef{Name: name}
		if args, ok := input.String("args"); ok {
			ref.Args = &args
		}
		skills = append(skills, ref)
	}
	return skillDetectorResult{Skills: skills}, nil
}
