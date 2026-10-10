package markdown

import (
	"fmt"
	"regexp"
	"strings"
)

// Section is an H2 region of the content: the heading line and the lines
// up to (not including) the next H1/H2 heading.
type Section struct {
	// Start is the index of the heading line, End the index after the last line.
	Start, End int
}

var h2Re = regexp.MustCompile(`^##[ \t]+(.*?)[ \t]*#*[ \t]*$`)

// FindSection locates the H2 section whose heading text matches (case-insensitive).
func FindSection(content, heading string) (Section, bool) {
	lines := Lines(content)
	start := -1
	for i, l := range lines {
		if l.InFence {
			continue
		}
		if start < 0 {
			if m := h2Re.FindStringSubmatch(l.Text); m != nil && strings.EqualFold(strings.TrimSpace(m[1]), heading) {
				start = i
			}
			continue
		}
		t := strings.TrimSpace(l.Text)
		if strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ") || t == "#" || t == "##" {
			return Section{Start: start, End: i}, true
		}
	}
	if start < 0 {
		return Section{}, false
	}
	return Section{Start: start, End: len(lines)}, true
}

// Checklist markers.
const (
	StateOpen          = "open"
	StateDone          = "done"
	StateNotApplicable = "not_applicable"
)

// Criterion kinds.
const (
	KindVerify = "verify"
	KindManual = "manual"
)

// CriteriaHeading is the H2 the acceptance criteria live under.
const CriteriaHeading = "Acceptance Criteria"

// WorkLogHeading is the H2 the work log lives under.
const WorkLogHeading = "Work Log"

// Criterion is one acceptance-criteria checklist item.
type Criterion struct {
	Position int    `json:"position"`
	Text     string `json:"text"`
	Kind     string `json:"kind,omitempty"`
	State    string `json:"state"`
	line     int
}

var checkItemRe = regexp.MustCompile(`^(\s*[-*+]\s+)\[([ xX~])\](\s+)(.*)$`)

// Criteria lists the checklist items under "## Acceptance Criteria". The
// second result reports whether the section exists at all.
func Criteria(content string) ([]Criterion, bool) {
	sec, ok := FindSection(content, CriteriaHeading)
	if !ok {
		return nil, false
	}
	lines := Lines(content)
	var out []Criterion
	for i := sec.Start + 1; i < sec.End; i++ {
		if lines[i].InFence {
			continue
		}
		m := checkItemRe.FindStringSubmatch(lines[i].Text)
		if m == nil {
			continue
		}
		c := Criterion{Position: len(out) + 1, State: stateOf(m[2]), line: i}
		c.Kind, c.Text = splitCriterion(m[4])
		out = append(out, c)
	}
	return out, true
}

// CriteriaProblems reports lines under the section that are neither
// checklist items nor blank (the section must parse as a checklist), and
// whether the section is missing.
func CriteriaProblems(content string) []string {
	sec, ok := FindSection(content, CriteriaHeading)
	if !ok {
		return []string{"/content: no \"## " + CriteriaHeading + "\" section"}
	}
	lines := Lines(content)
	var problems []string
	items := 0
	for i := sec.Start + 1; i < sec.End; i++ {
		if lines[i].InFence {
			continue
		}
		t := strings.TrimSpace(lines[i].Text)
		if t == "" || strings.HasPrefix(t, "<!--") {
			continue
		}
		if checkItemRe.MatchString(lines[i].Text) {
			items++
			continue
		}
		problems = append(problems, fmt.Sprintf("/content: line %d under %s is not a checklist item: %q", i+1, CriteriaHeading, t))
	}
	if items == 0 {
		problems = append(problems, "/content: "+CriteriaHeading+" has no checklist items")
	}
	return problems
}

func stateOf(mark string) string {
	switch mark {
	case "x", "X":
		return StateDone
	case "~":
		return StateNotApplicable
	}
	return StateOpen
}

func markOf(state string) string {
	switch state {
	case StateDone:
		return "x"
	case StateNotApplicable:
		return "~"
	}
	return " "
}

func splitCriterion(text string) (kind, rest string) {
	t := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(strings.ToUpper(t), "VERIFY:"):
		return KindVerify, strings.TrimSpace(t[len("VERIFY:"):])
	case strings.HasPrefix(strings.ToUpper(t), "[MANUAL]"):
		return KindManual, strings.TrimSpace(t[len("[MANUAL]"):])
	}
	return "", t
}

func formatCriterion(state, kind, text string) string {
	prefix := ""
	switch kind {
	case KindVerify:
		prefix = "VERIFY: "
	case KindManual:
		prefix = "[MANUAL] "
	}
	return "- [" + markOf(state) + "] " + prefix + strings.TrimSpace(text)
}

// SetCriterion rewrites the marker of the item at a 1-based position.
func SetCriterion(content string, position int, state string) (string, error) {
	items, ok := Criteria(content)
	if !ok {
		return "", fmt.Errorf("no %q section", CriteriaHeading)
	}
	if position < 1 || position > len(items) {
		return "", fmt.Errorf("no acceptance criterion at position %d (have %d)", position, len(items))
	}
	it := items[position-1]
	lines := strings.Split(content, "\n")
	m := checkItemRe.FindStringSubmatch(lines[it.line])
	lines[it.line] = m[1] + "[" + markOf(state) + "]" + m[3] + m[4]
	return strings.Join(lines, "\n"), nil
}

// AddCriterion appends an item to the section, creating the section at the
// end of the content when it is missing.
func AddCriterion(content, text, kind string) string {
	item := formatCriterion(StateOpen, kind, text)
	sec, ok := FindSection(content, CriteriaHeading)
	if !ok {
		return strings.TrimRight(content, "\n") + "\n\n## " + CriteriaHeading + "\n\n" + item + "\n"
	}
	marked := Lines(content)
	lines := make([]string, len(marked))
	for i, l := range marked {
		lines[i] = l.Text
	}
	// Insert after the last checklist item (outside code fences), or after the heading.
	at := sec.Start + 1
	for i := sec.Start + 1; i < sec.End && i < len(marked); i++ {
		if !marked[i].InFence && checkItemRe.MatchString(marked[i].Text) {
			at = i + 1
		}
	}
	if at == sec.Start+1 {
		// No items yet: leave one blank line after the heading.
		lines = append(lines[:at], append([]string{"", item}, lines[at:]...)...)
	} else {
		lines = append(lines[:at], append([]string{item}, lines[at:]...)...)
	}
	return strings.Join(lines, "\n")
}

// RemoveCriterion deletes the item at a 1-based position.
func RemoveCriterion(content string, position int) (string, error) {
	items, ok := Criteria(content)
	if !ok {
		return "", fmt.Errorf("no %q section", CriteriaHeading)
	}
	if position < 1 || position > len(items) {
		return "", fmt.Errorf("no acceptance criterion at position %d (have %d)", position, len(items))
	}
	lines := strings.Split(content, "\n")
	at := items[position-1].line
	lines = append(lines[:at], lines[at+1:]...)
	return strings.Join(lines, "\n"), nil
}

// AppendWorkLog adds "### <timestamp> - <entry>" at the end of the Work Log
// section, creating the section at the end of the content when missing.
func AppendWorkLog(content, timestamp, entry string) string {
	line := "### " + timestamp + " - " + strings.TrimSpace(entry)
	sec, ok := FindSection(content, WorkLogHeading)
	if !ok {
		return strings.TrimRight(content, "\n") + "\n\n## " + WorkLogHeading + "\n\n" + line + "\n"
	}
	lines := strings.Split(content, "\n")
	end := sec.End
	if end > len(lines) {
		end = len(lines)
	}
	// Trim trailing blank lines inside the section so the entry follows the last one.
	insert := end
	for insert > sec.Start+1 && strings.TrimSpace(lines[insert-1]) == "" {
		insert--
	}
	block := []string{"", line}
	if insert == sec.Start+1 {
		block = []string{"", line}
	}
	tail := append([]string{}, lines[insert:]...)
	if end == len(lines) && (len(tail) == 0 || strings.TrimSpace(strings.Join(tail, "")) == "") {
		tail = []string{""}
	}
	lines = append(append(append([]string{}, lines[:insert]...), block...), tail...)
	return strings.Join(lines, "\n")
}

// WorkLogEntries returns the H3 entries under the Work Log section.
func WorkLogEntries(content string) []string {
	sec, ok := FindSection(content, WorkLogHeading)
	if !ok {
		return nil
	}
	lines := Lines(content)
	var out []string
	for i := sec.Start + 1; i < sec.End; i++ {
		if lines[i].InFence {
			continue
		}
		if t := strings.TrimSpace(lines[i].Text); strings.HasPrefix(t, "### ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(t, "### ")))
		}
	}
	return out
}
