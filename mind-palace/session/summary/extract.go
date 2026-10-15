package summary

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// The sections of a session's material, in order. ExtractEngine keeps all
// but the timeline.
const (
	sectionRequest  = "## First request"
	sectionCommits  = "## Commits"
	sectionFiles    = "## Files edited"
	sectionErrors   = "## Unresolved errors"
	sectionReply    = "## Last reply"
	sectionTimeline = "## Timeline"
)

// Bounds of the material.
const (
	requestChars = 300
	replyChars   = 400
	rowChars     = 160
	maxFiles     = 10
	maxCommits   = 10
	maxErrors    = 5
)

// Material renders a session's activities (newest first, as
// query.Service.Activities returns them) as the input of its note: the
// first request, the commit subjects, the ten files edited most, the
// errors no later run of the same call fixed, the head of the last reply
// and the timeline, oldest first. It is deterministic.
func Material(sessionID string, acts []query.Activity) string {
	ordered := make([]query.Activity, len(acts))
	for i, a := range acts {
		ordered[len(acts)-1-i] = a
	}
	var request, reply string
	var commits []string
	edits := map[string]int{}
	calls := map[string]query.Activity{}
	var rows []string
	for _, a := range ordered {
		switch a.Kind {
		case "user":
			if request == "" {
				request = cut(a.Detail, requestChars)
			}
		case "response":
			reply = a.Detail
		case "commit":
			if len(commits) < maxCommits {
				commits = append(commits, cut(a.Detail, rowChars))
			}
		case "edit":
			if a.Detail != "" {
				edits[a.Detail] += max(a.Count, 1)
			}
		}
		if a.ToolUseID != "" && a.Kind != "result" {
			calls[a.ToolUseID] = a
		}
		if a.Kind == "state" {
			continue
		}
		rows = append(rows, fmt.Sprintf("[%s] %s: %s", activityTime(a).UTC().Format("15:04"), label(a), cut(a.Detail, rowChars)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<session id=%q>\n", sessionID)
	section := func(head string, lines []string) {
		if len(lines) == 0 {
			return
		}
		b.WriteString(head + "\n")
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	if request != "" {
		section(sectionRequest, []string{request})
	}
	section(sectionCommits, bullets(commits))
	section(sectionFiles, topFiles(edits))
	section(sectionErrors, unresolved(ordered, calls))
	if reply != "" {
		section(sectionReply, []string{cut(reply, replyChars)})
	}
	section(sectionTimeline, rows)
	b.WriteString("</session>")
	return b.String()
}

func label(a query.Activity) string {
	if a.Label != "" && a.Label != a.Kind {
		return a.Kind + " " + a.Label
	}
	return a.Kind
}

func bullets(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "- " + l
	}
	return out
}

// topFiles lists the files edited most, at most maxFiles, by count then
// name.
func topFiles(edits map[string]int) []string {
	files := make([]string, 0, len(edits))
	for f := range edits {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		if edits[files[i]] != edits[files[j]] {
			return edits[files[i]] > edits[files[j]]
		}
		return files[i] < files[j]
	})
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = fmt.Sprintf("- %s (%d)", f, edits[f])
	}
	return out
}

// unresolved lists the failed calls (a result marked as an error) whose
// last outcome is still a failure: no later successful result of the same
// call (same kind and detail) fixed them; oldest first, at most maxErrors.
func unresolved(ordered []query.Activity, calls map[string]query.Activity) []string {
	var order []string
	last := map[string]string{} // key → failure text, "" once fixed
	for _, a := range ordered {
		if a.Kind != "result" {
			continue
		}
		call, known := calls[a.ToolUseID]
		key := "id\x00" + a.ToolUseID
		if known {
			key = call.Kind + "\x00" + call.Detail
		}
		if !a.IsError {
			if _, seen := last[key]; seen {
				last[key] = ""
			}
			continue
		}
		what := "a call"
		if known {
			what = label(call) + " " + cut(call.Detail, 80)
		}
		if _, seen := last[key]; !seen {
			order = append(order, key)
		}
		last[key] = fmt.Sprintf("- %s: %s", what, cut(a.Detail, rowChars))
	}
	var out []string
	for _, k := range order {
		if last[k] != "" {
			out = append(out, last[k])
		}
	}
	if len(out) > maxErrors {
		out = out[len(out)-maxErrors:]
	}
	return out
}

func activityTime(a query.Activity) time.Time {
	if at, err := time.Parse(time.RFC3339Nano, a.Timestamp); err == nil {
		return at
	}
	return a.CreatedAt
}

// cut keeps at most n characters of the text on one line, marking the cut.
func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Clip fits material into at most n bytes: the sections before the
// timeline are kept, and the timeline keeps its newest rows that fit after
// a line saying how many earlier ones were left out. Material that still
// does not fit is cut.
func Clip(material string, n int) string {
	if len(material) <= n {
		return material
	}
	if n <= 0 {
		return ""
	}
	head, timeline, found := strings.Cut(material, sectionTimeline+"\n")
	if found {
		rows := strings.Split(strings.TrimSuffix(strings.TrimSuffix(timeline, "</session>"), "\n"), "\n")
		tail := "</session>"
		for keep := len(rows); keep >= 0; keep-- {
			out := head + sectionTimeline + "\n"
			if dropped := len(rows) - keep; dropped > 0 {
				out += fmt.Sprintf("… (%d earlier rows left out)\n", dropped)
			}
			if keep > 0 {
				out += strings.Join(rows[len(rows)-keep:], "\n") + "\n"
			}
			out += tail
			if len(out) <= n {
				return out
			}
		}
		material = head + tail
	}
	if len(material) <= n {
		return material
	}
	cutAt := n
	for cutAt > 0 && !utf8.RuneStart(material[cutAt]) {
		cutAt--
	}
	return material[:cutAt]
}

// ExtractEngine is the free engine: it writes a note without a model,
// deterministically, from the prompt's <input> block. For a session's
// material it keeps every section but the timeline; for roll-up input
// (<note label="…"> blocks) it keeps the label and the first lines of each
// note. It costs nothing.
type ExtractEngine struct{}

// Name is "extract".
func (ExtractEngine) Name() string { return "extract" }

// Lines kept of each note in a roll-up, and the longest roll-up.
const (
	extractNoteLines = 8
	extractMaxChars  = 6000
)

// Run returns the note of the prompt's input.
func (ExtractEngine) Run(_ context.Context, prompt, _ string) (string, float64, error) {
	input := prompt
	if _, rest, ok := strings.Cut(prompt, "<input>\n"); ok {
		input, _, _ = strings.Cut(rest, "</input>")
	}
	input = strings.TrimSpace(input)
	if strings.Contains(input, "<note ") {
		return extractNotes(input), 0, nil
	}
	if head, _, ok := strings.Cut(input, sectionTimeline+"\n"); ok {
		input = head
	} else if head, _, ok := strings.Cut(input, sectionTimeline); ok {
		input = head
	}
	var lines []string
	for _, l := range strings.Split(input, "\n") {
		if strings.HasPrefix(l, "<session ") || l == "</session>" {
			continue
		}
		lines = append(lines, l)
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		text = "(no activity captured)"
	}
	return text, 0, nil
}

// extractNotes condenses <note label="…">…</note> blocks: a "### label"
// heading and the note's first lines each, headings turned into labels.
func extractNotes(input string) string {
	var b strings.Builder
	for rest := input; ; {
		start := strings.Index(rest, "<note ")
		if start < 0 {
			break
		}
		rest = rest[start:]
		headEnd := strings.Index(rest, ">")
		if headEnd < 0 {
			break
		}
		head := rest[:headEnd]
		body := rest[headEnd+1:]
		end := strings.Index(body, "</note>")
		if end < 0 {
			end = len(body)
		}
		rest = body[min(end+len("</note>"), len(body)):]
		label := attr(head, "label")
		if label == "" {
			label = attr(head, "id")
		}
		fmt.Fprintf(&b, "### %s\n", label)
		kept := 0
		for _, l := range strings.Split(strings.TrimSpace(body[:end]), "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			if h, ok := strings.CutPrefix(strings.TrimLeft(l, "#"), " "); ok && strings.HasPrefix(l, "#") {
				l = h + ":"
			}
			b.WriteString(l + "\n")
			if kept++; kept >= extractNoteLines {
				break
			}
		}
		b.WriteString("\n")
	}
	out := strings.TrimSpace(b.String())
	if len(out) > extractMaxChars {
		out = Clip(out, extractMaxChars)
	}
	return out
}

// attr reads name="value" from a tag head.
func attr(head, name string) string {
	_, rest, ok := strings.Cut(head, name+`="`)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, `"`)
	return v
}
