// Package markdown reads and writes plan documents: a YAML front matter block
// followed by Markdown content.
package markdown

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/robbiebyrd/clued/plan/model"
)

const delimiter = "---"

// Split separates a document into its raw front matter YAML and content.
func Split(doc string) (frontMatter, content string, err error) {
	s := strings.ReplaceAll(doc, "\r\n", "\n")
	s = strings.TrimPrefix(s, "\xEF\xBB\xBF")
	if !strings.HasPrefix(s, delimiter+"\n") {
		if s == delimiter {
			return "", "", fmt.Errorf("front matter block is not closed")
		}
		return "", "", fmt.Errorf("document does not start with a front matter block")
	}
	rest := s[len(delimiter)+1:]
	end := -1
	for i := 0; i <= len(rest); {
		j := strings.Index(rest[i:], "\n")
		var line string
		if j < 0 {
			line = rest[i:]
		} else {
			line = rest[i : i+j]
		}
		if strings.TrimRight(line, " \t") == delimiter {
			end = i
			if j < 0 {
				content = ""
			} else {
				content = rest[i+j+1:]
			}
			break
		}
		if j < 0 {
			break
		}
		i += j + 1
	}
	if end < 0 {
		return "", "", fmt.Errorf("front matter block is not closed")
	}
	return rest[:end], strings.TrimLeft(content, "\n"), nil
}

// Parse decodes a whole plan document.
func Parse(doc string) (model.FrontMatter, string, error) {
	raw, content, err := Split(doc)
	if err != nil {
		return model.FrontMatter{}, "", err
	}
	fm, err := ParseFrontMatter(raw)
	if err != nil {
		return model.FrontMatter{}, "", err
	}
	return fm, content, nil
}

// ParseFrontMatter decodes YAML front matter. A top-level `plans` list (the
// layout used before plan links moved under `links.plans`) is migrated into
// links.plans so older files keep parsing; they are rewritten in the current
// layout on their next write.
func ParseFrontMatter(raw string) (model.FrontMatter, error) {
	var fm model.FrontMatter
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return fm, fmt.Errorf("front matter: %w", err)
	}
	var legacy []model.Link
	if len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		m := doc.Content[0]
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "plans" {
				if err := m.Content[i+1].Decode(&legacy); err != nil {
					return fm, fmt.Errorf("front matter: plans: %w", err)
				}
				m.Content = append(m.Content[:i], m.Content[i+2:]...)
				break
			}
		}
		if len(m.Content) > 0 {
			out, err := yaml.Marshal(&doc)
			if err != nil {
				return fm, fmt.Errorf("front matter: %w", err)
			}
			raw = string(out)
		} else {
			raw = ""
		}
	}
	dec := yaml.NewDecoder(strings.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) {
		return fm, fmt.Errorf("front matter: %w", err)
	}
	if len(legacy) > 0 {
		fm.SetPlanLinks(append(legacy, fm.PlanLinks()...))
	}
	return fm, nil
}

// RenderFrontMatter encodes front matter as YAML (without delimiters).
func RenderFrontMatter(fm model.FrontMatter) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Render assembles a whole document.
func Render(fm model.FrontMatter, content string) (string, error) {
	raw, err := RenderFrontMatter(fm)
	if err != nil {
		return "", err
	}
	content = strings.TrimLeft(content, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return delimiter + "\n" + raw + delimiter + "\n\n" + content, nil
}

// RenderPlan serialises a plan to its on-disk form.
func RenderPlan(p *model.Plan) (string, error) {
	return Render(p.FrontMatter, p.Content)
}

var (
	h1Re = regexp.MustCompile(`(?m)^#[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	// numberedHeadingRe matches "## Phase 1: Name", "### 1.1: Section", "## 2. Name", "### 3.2 Name".
	numberedHeadingRe = regexp.MustCompile(`(?m)^#{1,6}[ \t]+(?:[Pp]hase[ \t]+|[Ss]ection[ \t]+)?([0-9]+(?:\.[0-9]+)*)(?:[:.\-–—)][ \t]*|[ \t]+|$)`)
	fenceRe           = regexp.MustCompile(`^[ ]{0,3}(\x60{3,}|~{3,})(.*)$`)
)

// FirstH1 returns the first level-1 heading text, if any.
func FirstH1(content string) (string, bool) {
	for _, line := range contentLines(content) {
		if m := h1Re.FindStringSubmatch(line.text); m != nil {
			return strings.TrimSpace(m[1]), true
		}
	}
	return "", false
}

// SetH1 rewrites the first H1 to the title, or prepends one.
func SetH1(content, title string) string {
	lines := Lines(content)
	for i, l := range lines {
		if l.InFence {
			continue
		}
		if h1Re.MatchString(l.Text) {
			out := make([]string, len(lines))
			for j, x := range lines {
				out[j] = x.Text
			}
			out[i] = "# " + title
			return strings.Join(out, "\n")
		}
	}
	heading := "# " + title + "\n\n"
	return heading + strings.TrimLeft(content, "\n")
}

// SectionNumbers returns the numbered phase/section headings found in the
// content, in document order ("1", "1.1", "1.2", "2", …).
func SectionNumbers(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range contentLines(content) {
		if m := numberedHeadingRe.FindStringSubmatch(line.text); m != nil {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

// HasSection reports whether a numbered heading exists in the content.
func HasSection(content, number string) bool {
	for _, n := range SectionNumbers(content) {
		if n == number {
			return true
		}
	}
	return false
}

// Line is one line of content with its fenced-code-block state.
type Line struct {
	Text string
	// InFence is true for lines inside a fenced code block, fences included.
	InFence bool
}

// Lines splits content into lines and marks those inside fenced code blocks.
// A fence opens with three or more backticks or tildes and closes only on a
// fence of the same character that is at least as long and carries no info
// string (CommonMark rules), so a shorter or different fence inside a block
// does not end it.
func Lines(content string) []Line {
	var out []Line
	var fenceChar byte
	fenceLen := 0
	for _, l := range strings.Split(content, "\n") {
		if fenceLen == 0 {
			if m := fenceRe.FindStringSubmatch(l); m != nil {
				fenceChar, fenceLen = m[1][0], len(m[1])
				out = append(out, Line{Text: l, InFence: true})
				continue
			}
			out = append(out, Line{Text: l})
			continue
		}
		out = append(out, Line{Text: l, InFence: true})
		if m := fenceRe.FindStringSubmatch(l); m != nil && m[1][0] == fenceChar && len(m[1]) >= fenceLen && strings.TrimSpace(m[2]) == "" {
			fenceLen = 0
		}
	}
	return out
}

type line struct {
	text string
}

// contentLines yields lines outside fenced code blocks.
func contentLines(content string) []line {
	var out []line
	for _, l := range Lines(content) {
		if !l.InFence {
			out = append(out, line{text: l.Text})
		}
	}
	return out
}
