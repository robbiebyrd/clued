// Package render fills a content template from a structured body. Templates
// are Go text/templates; each kind has a built-in default that reproduces its
// standard layout (Plans: Summary, Part 1 — Design, Part 2 — Implementation;
// Stories: Problem Statement, Steps, Acceptance Criteria, Work Log).
package render

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
)

//go:embed plan.md.tmpl
var planTemplate string

//go:embed story.md.tmpl
var storyTemplate string

// DefaultTemplateID names the built-in template of every kind.
const DefaultTemplateID = "default"

// DefaultTemplate returns the built-in template text for a kind.
func DefaultTemplate(k string) string {
	if k == kind.Story {
		return storyTemplate
	}
	return planTemplate
}

// Body is the structured plan content (mirrors the plan schema's `body`).
type Body struct {
	Summary        Summary         `json:"summary"`
	Design         *Design         `json:"design,omitempty"`
	Implementation *Implementation `json:"implementation,omitempty"`
}

type Summary struct {
	Goal     string `json:"goal"`
	Problem  string `json:"problem"`
	Approach string `json:"approach,omitempty"`
}

type Design struct {
	CurrentState         string          `json:"currentState,omitempty"`
	Decisions            []Decision      `json:"decisions,omitempty"`
	RejectedAlternatives []Alternative   `json:"rejectedAlternatives,omitempty"`
	Components           []Component     `json:"components,omitempty"`
	ErrorHandling        string          `json:"errorHandling,omitempty"`
	EdgeCases            string          `json:"edgeCases,omitempty"`
	Considerations       *Considerations `json:"considerations,omitempty"`
	Testing              string          `json:"testing,omitempty"`
	Scope                *Scope          `json:"scope,omitempty"`
	OpenQuestions        []OpenQuestion  `json:"openQuestions,omitempty"`
}

type Decision struct {
	Decision  string `json:"decision"`
	Rationale string `json:"rationale"`
}

type Alternative struct {
	Alternative string `json:"alternative"`
	Reason      string `json:"reason"`
}

type Component struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type Considerations struct {
	Security    string `json:"security,omitempty"`
	Performance string `json:"performance,omitempty"`
}

type Scope struct {
	InScope    []string    `json:"inScope,omitempty"`
	OutOfScope []ScopeItem `json:"outOfScope"`
}

type ScopeItem struct {
	Item   string `json:"item"`
	Reason string `json:"reason,omitempty"`
}

type OpenQuestion struct {
	Question string `json:"question"`
	Owner    string `json:"owner,omitempty"`
}

type Implementation struct {
	Architecture       string      `json:"architecture,omitempty"`
	TechStack          string      `json:"techStack,omitempty"`
	Constraints        []string    `json:"constraints,omitempty"`
	Files              []FileEntry `json:"files,omitempty"`
	Phases             []Phase     `json:"phases"`
	AcceptanceCriteria []string    `json:"acceptanceCriteria"`
	SelfReview         *SelfReview `json:"selfReview,omitempty"`
	NextSteps          []string    `json:"nextSteps,omitempty"`
}

type FileEntry struct {
	Path           string `json:"path"`
	Change         string `json:"change"`
	Responsibility string `json:"responsibility,omitempty"`
}

type Phase struct {
	Number     string    `json:"number"`
	Name       string    `json:"name"`
	Sections   []Section `json:"sections,omitempty"`
	Checklist  []string  `json:"checklist,omitempty"`
	Validation string    `json:"validation,omitempty"`
}

type Section struct {
	Number     string        `json:"number"`
	Name       string        `json:"name"`
	Files      *SectionFiles `json:"files,omitempty"`
	DependsOn  []string      `json:"dependsOn,omitempty"`
	Interfaces *Interfaces   `json:"interfaces,omitempty"`
	Steps      []Step        `json:"steps"`
	Validation string        `json:"validation,omitempty"`
}

type SectionFiles struct {
	Create []string `json:"create,omitempty"`
	Modify []string `json:"modify,omitempty"`
	Delete []string `json:"delete,omitempty"`
	Test   []string `json:"test,omitempty"`
}

type Interfaces struct {
	Consumes string `json:"consumes,omitempty"`
	Produces string `json:"produces,omitempty"`
}

type Step struct {
	Title   string `json:"title"`
	Detail  string `json:"detail,omitempty"`
	Command string `json:"command,omitempty"`
}

type SelfReview struct {
	Coverage        []Coverage `json:"coverage,omitempty"`
	PlaceholderScan string     `json:"placeholderScan,omitempty"`
	ResidualRisk    string     `json:"residualRisk,omitempty"`
}

type Coverage struct {
	Requirement string `json:"requirement"`
	DeliveredBy string `json:"deliveredBy"`
}

// Data is what a template receives. Body is the kind's body type (Body for
// plans, StoryBody for stories).
type Data struct {
	ID    string
	Title string
	Type  string
	Body  any
}

// Sectioned is implemented by bodies that declare numbered headings.
type Sectioned interface {
	SectionNumbers() []string
}

// DecodeBody parses a JSON body into the kind's body type.
func DecodeBody(k string, raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	switch k {
	case kind.Story:
		var b StoryBody
		if err := dec.Decode(&b); err != nil {
			return nil, err
		}
		return b, nil
	default:
		var b Body
		if err := dec.Decode(&b); err != nil {
			return nil, err
		}
		return b, nil
	}
}

// SectionNumbers lists every phase and section number declared in the body.
func (b Body) SectionNumbers() []string {
	if b.Implementation == nil {
		return nil
	}
	var out []string
	for _, p := range b.Implementation.Phases {
		out = append(out, p.Number)
		for _, s := range p.Sections {
			out = append(out, s.Number)
		}
	}
	return out
}

var funcs = template.FuncMap{
	"join":            strings.Join,
	"inc":             func(i int) int { return i + 1 },
	"heading":         heading,
	"box":             box,
	"proofBox":        box,
	"criterionPrefix": criterionPrefix,
	"dimensionTitle":  dimensionTitle,
}

// Compile parses a template text.
func Compile(id, text string) (*template.Template, error) {
	return template.New(id).Funcs(funcs).Option("missingkey=zero").Parse(text)
}

// Validate reports whether a template text parses.
func Validate(text string) error {
	_, err := Compile("check", text)
	return err
}

// Render fills the template with the plan data and normalises blank lines.
func Render(text string, data Data) (string, error) {
	t, err := Compile("plan", text)
	if err != nil {
		return "", fmt.Errorf("template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	return tidy(buf.String()), nil
}

// tidy collapses runs of more than one blank line outside fenced code blocks
// (template conditionals leave them behind) and ensures a trailing newline.
// Lines are otherwise left as supplied, so hard breaks (trailing spaces) and
// code samples in body Markdown survive unchanged.
func tidy(s string) string {
	var out []string
	blank := 0
	for _, l := range markdown.Lines(strings.ReplaceAll(s, "\r\n", "\n")) {
		if !l.InFence && strings.TrimSpace(l.Text) == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, l.Text)
	}
	res := strings.TrimLeft(strings.Join(out, "\n"), "\n")
	res = strings.TrimRight(res, "\n") + "\n"
	return res
}
