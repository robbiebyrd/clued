// Package structpatch reads the structuredPatch Claude Code attaches to the
// result of an Edit, Write, MultiEdit or NotebookEdit call (in the
// transcript line's toolUseResult and the PostToolUse hook's
// tool_response): a list of hunks, each with its old and new start line
// and length and its lines prefixed by " ", "+" or "-". It reduces them to
// what a timeline shows: the line span touched, the lines added and
// removed, and the unified diff.
package structpatch

import (
	"fmt"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Hunk is one hunk of a structured patch.
type Hunk struct {
	OldStart int `json:"old_start" bson:"old_start"`
	OldLines int `json:"old_lines" bson:"old_lines"`
	NewStart int `json:"new_start" bson:"new_start"`
	NewLines int `json:"new_lines" bson:"new_lines"`
}

// Summary is what the hunks of one patch amount to.
type Summary struct {
	Hunks        []Hunk
	LinesAdded   int
	LinesRemoved int
	// Span names the lines the patch touches in the new file, "L10-12" or
	// "L10" for one line, from the first hunk's start to the last hunk's
	// end; "" when the patch has no hunks.
	Span string
	// Diff is the unified diff of the hunks, each introduced by its
	// "@@ -a,b +c,d @@" header; "" when the patch has no hunks.
	Diff string
}

// Parse reads a decoded structuredPatch (a list of hunk objects); anything
// that is not one yields an empty summary and false.
func Parse(v any) (Summary, bool) {
	var hunks []session.Doc
	switch list := v.(type) {
	case []session.Doc:
		hunks = list
	case []any:
		for _, item := range list {
			switch h := item.(type) {
			case map[string]any:
				hunks = append(hunks, session.Doc(h))
			case session.Doc:
				hunks = append(hunks, h)
			}
		}
	default:
		return Summary{}, false
	}
	var s Summary
	var diff strings.Builder
	for _, h := range hunks {
		hunk := Hunk{OldStart: intOf(h["oldStart"]), OldLines: intOf(h["oldLines"]), NewStart: intOf(h["newStart"]), NewLines: intOf(h["newLines"])}
		s.Hunks = append(s.Hunks, hunk)
		fmt.Fprintf(&diff, "@@ -%d,%d +%d,%d @@\n", hunk.OldStart, hunk.OldLines, hunk.NewStart, hunk.NewLines)
		for _, l := range lines(h["lines"]) {
			switch {
			case strings.HasPrefix(l, "+"):
				s.LinesAdded++
			case strings.HasPrefix(l, "-"):
				s.LinesRemoved++
			}
			diff.WriteString(l)
			diff.WriteByte('\n')
		}
	}
	if len(s.Hunks) == 0 {
		return s, true
	}
	first, last := s.Hunks[0], s.Hunks[len(s.Hunks)-1]
	s.Span = SpanOf(first.NewStart, last.NewStart+max(last.NewLines, 1)-1)
	s.Diff = diff.String()
	return s, true
}

// SpanOf names a line range: "L10-12", or "L10" when from and to are the
// same line.
func SpanOf(from, to int) string {
	if to <= from {
		return fmt.Sprintf("L%d", from)
	}
	return fmt.Sprintf("L%d-%d", from, to)
}

// lines reads a hunk's lines whatever their decoded type.
func lines(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// intOf reads a number whatever its decoded type.
func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
