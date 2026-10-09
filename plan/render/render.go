// Package render fills a plan content template from a structured plan body.
// Templates are Go text/templates; the default one reproduces the standard
// plan layout (Summary, Part 1 — Design, Part 2 — Implementation).
package render

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed default.md.tmpl
var defaultTemplate string

// DefaultTemplateID names the built-in template.
const DefaultTemplateID = "default"

// DefaultTemplate returns the built-in template text.
func DefaultTemplate() string { return defaultTemplate }

// Body is the structured plan content (mirrors the schema's `body`).
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

// Data is what a template receives.
type Data struct {
	ID    string
	Title string
	Type  string
	Body  Body
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
	"join": strings.Join,
	"inc":  func(i int) int { return i + 1 },
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

// tidy collapses runs of more than one blank line and ensures a trailing newline.
func tidy(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var out []string
	blank := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, strings.TrimRight(l, " \t"))
	}
	res := strings.TrimLeft(strings.Join(out, "\n"), "\n")
	res = strings.TrimRight(res, "\n") + "\n"
	return res
}
