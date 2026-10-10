package render

import "strings"

// StoryBody is the structured story content (mirrors the story schema's `body`).
type StoryBody struct {
	ProblemStatement   ProblemStatement `json:"problemStatement"`
	Steps              []StoryStep      `json:"steps"`
	AcceptanceCriteria []Criterion      `json:"acceptanceCriteria"`
	Files              []StoryFile      `json:"files,omitempty"`
	Proof              []ProofItem      `json:"proof,omitempty"`
	QA                 string           `json:"qa,omitempty"`
	WorkLog            []WorkLogEntry   `json:"workLog,omitempty"`
}

type ProblemStatement struct {
	Statement string `json:"statement"`
	Issue     string `json:"issue,omitempty"`
	Impact    string `json:"impact,omitempty"`
}

// StoryStep is a numbered unit of work; sub-steps extend the parent's number.
type StoryStep struct {
	Number      string      `json:"number"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Steps       []StoryStep `json:"steps,omitempty"`
}

type Criterion struct {
	Text  string `json:"text"`
	Kind  string `json:"kind,omitempty"`
	State string `json:"state,omitempty"`
}

type StoryFile struct {
	Path   string `json:"path"`
	Change string `json:"change,omitempty"`
}

type ProofItem struct {
	Dimension string `json:"dimension"`
	State     string `json:"state"`
	Evidence  string `json:"evidence,omitempty"`
}

type WorkLogEntry struct {
	Timestamp string `json:"timestamp"`
	Entry     string `json:"entry"`
}

// FlatStep is a step with its nesting depth, for rendering headings.
type FlatStep struct {
	Number      string
	Name        string
	Description string
	Depth       int
}

// FlatSteps returns every step in document order with its depth (1 = top).
func (b StoryBody) FlatSteps() []FlatStep {
	var out []FlatStep
	var walk func(steps []StoryStep, depth int)
	walk = func(steps []StoryStep, depth int) {
		for _, s := range steps {
			out = append(out, FlatStep{Number: s.Number, Name: s.Name, Description: s.Description, Depth: depth})
			walk(s.Steps, depth+1)
		}
	}
	walk(b.Steps, 1)
	return out
}

// SectionNumbers lists every step number declared in the body, any depth.
func (b StoryBody) SectionNumbers() []string {
	var out []string
	for _, s := range b.FlatSteps() {
		out = append(out, s.Number)
	}
	return out
}

// Checklist markers.
func box(state string) string {
	switch state {
	case "done", "proven":
		return "[x]"
	case "not_applicable":
		return "[~]"
	}
	return "[ ]"
}

func criterionPrefix(kind string) string {
	switch kind {
	case "verify":
		return "VERIFY: "
	case "manual":
		return "[MANUAL] "
	}
	return ""
}

func dimensionTitle(d string) string {
	parts := strings.Split(d, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// heading returns the Markdown heading marker for a step depth (### for 1).
func heading(depth int) string {
	if depth < 1 {
		depth = 1
	}
	return strings.Repeat("#", depth+2)
}
