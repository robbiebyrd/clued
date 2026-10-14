package enrichers

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/structpatch"
)

// ToolResultDetailName is the name of the tool-result-detail enricher.
const ToolResultDetailName = "tool-result-detail"

// The caps on the texts the enricher keeps.
const (
	diffCap   = 16 * 1024
	outputCap = 4 * 1024
)

// truncatedMark ends a text the caps cut.
const truncatedMark = "\n[truncated]"

// The tools a result's shape reveals.
const (
	ResultEdit  = "edit"
	ResultWrite = "write"
	ResultBash  = "bash"
	ResultRead  = "read"
	ResultTask  = "task"
	ResultText  = "text"
	ResultOther = "other"
)

// ToolResultDetail is what the tool-result-detail enricher stores: the
// facts of one tool call's structured result (the line's toolUseResult),
// by the tool its shape reveals, with a one-line Summary a timeline shows.
type ToolResultDetail struct {
	Tool      string `json:"tool" bson:"tool"`
	Summary   string `json:"summary" bson:"summary"`
	ToolUseID string `json:"tool_use_id,omitempty" bson:"tool_use_id,omitempty"`
	// An edit or write.
	FilePath     string             `json:"file_path,omitempty" bson:"file_path,omitempty"`
	Hunks        []structpatch.Hunk `json:"hunks,omitempty" bson:"hunks,omitempty"`
	LinesAdded   int                `json:"lines_added,omitempty" bson:"lines_added,omitempty"`
	LinesRemoved int                `json:"lines_removed,omitempty" bson:"lines_removed,omitempty"`
	Span         string             `json:"span,omitempty" bson:"span,omitempty"`
	Diff         string             `json:"diff,omitempty" bson:"diff,omitempty"`
	// A command.
	Stdout      string `json:"stdout,omitempty" bson:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty" bson:"stderr,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty" bson:"exit_code,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty" bson:"interrupted,omitempty"`
	// A read (FilePath and Span as well).
	StartLine  int `json:"start_line,omitempty" bson:"start_line,omitempty"`
	NumLines   int `json:"num_lines,omitempty" bson:"num_lines,omitempty"`
	TotalLines int `json:"total_lines,omitempty" bson:"total_lines,omitempty"`
	// A sub-agent.
	Description string `json:"description,omitempty" bson:"description,omitempty"`
	Status      string `json:"status,omitempty" bson:"status,omitempty"`
	AgentID     string `json:"agent_id,omitempty" bson:"agent_id,omitempty"`
	Result      string `json:"result,omitempty" bson:"result,omitempty"`
	// A plain-text result (a refusal, an error).
	Text string `json:"text,omitempty" bson:"text,omitempty"`
	// Any other result: its top-level keys.
	Keys []string `json:"keys,omitempty" bson:"keys,omitempty"`
}

// ToolResultDetailEnricher reduces each tool result's toolUseResult to the
// facts a timeline shows.
var ToolResultDetailEnricher = session.Enricher{
	Name:       ToolResultDetailName,
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(doc session.Doc) bool { return doc.Line()["toolUseResult"] != nil },
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		return summarizeToolResult(doc), nil
	},
}

func init() { session.Register(ToolResultDetailEnricher) }

// summarizeToolResult reads the line's toolUseResult.
func summarizeToolResult(doc session.Doc) ToolResultDetail {
	var d ToolResultDetail
	for _, block := range doc.Content() {
		if block["type"] == "tool_result" {
			d.ToolUseID, _ = block.String("tool_use_id")
			break
		}
	}
	raw := doc.Line()["toolUseResult"]
	if text, ok := raw.(string); ok {
		d.Tool, d.Text = ResultText, capText(stripANSI(text), outputCap)
		d.Summary = activityDetail(firstLine(text))
		return d
	}
	r := session.Doc(nil)
	switch m := raw.(type) {
	case map[string]any:
		r = session.Doc(m)
	case session.Doc:
		r = m
	}
	if r == nil {
		d.Tool = ResultOther
		return d
	}
	switch {
	case r["structuredPatch"] != nil:
		d.FilePath, _ = r.String("filePath")
		if s, ok := structpatch.Parse(r["structuredPatch"]); ok {
			d.Hunks, d.LinesAdded, d.LinesRemoved, d.Span, d.Diff = s.Hunks, s.LinesAdded, s.LinesRemoved, s.Span, capText(s.Diff, diffCap)
		}
		d.Tool = ResultEdit
		if kind, _ := r.String("type"); kind == "create" {
			d.Tool = ResultWrite
		}
		d.Summary = strings.TrimSpace(fmt.Sprintf("%s %s +%d -%d", baseName(d.FilePath), d.Span, d.LinesAdded, d.LinesRemoved))
	case has(r, "stdout") || has(r, "stderr") || has(r, "interrupted"):
		d.Tool = ResultBash
		stdout, _ := r.String("stdout")
		stderr, _ := r.String("stderr")
		d.Stdout, d.Stderr = capText(stripANSI(stdout), outputCap), capText(stripANSI(stderr), outputCap)
		d.Interrupted, _ = r["interrupted"].(bool)
		if code, ok := intField(r, "exitCode", "exit_code", "code"); ok {
			d.ExitCode = &code
		}
		var parts []string
		if d.ExitCode != nil {
			parts = append(parts, fmt.Sprintf("exit %d", *d.ExitCode))
		}
		parts = append(parts, fmt.Sprintf("%d lines", countTextLines(stdout)))
		if stderr != "" {
			parts = append(parts, "stderr")
		}
		if d.Interrupted {
			parts = append(parts, "[interrupted]")
		}
		d.Summary = strings.Join(parts, ", ")
	case r.Map("file") != nil && (has(r.Map("file"), "startLine") || has(r.Map("file"), "numLines")):
		file := r.Map("file")
		d.Tool = ResultRead
		d.FilePath, _ = file.String("filePath")
		d.StartLine, _ = intField(file, "startLine")
		d.NumLines, _ = intField(file, "numLines")
		d.TotalLines, _ = intField(file, "totalLines")
		if d.NumLines > 0 {
			d.Span = structpatch.SpanOf(d.StartLine, d.StartLine+d.NumLines-1)
		}
		d.Summary = strings.TrimSpace(baseName(d.FilePath) + " " + d.Span)
	case has(r, "agentId") || has(r, "description") && has(r, "prompt"):
		d.Tool = ResultTask
		d.Description, _ = r.String("description")
		d.Status, _ = r.String("status")
		d.AgentID, _ = r.String("agentId")
		result, _ := r.String("result")
		if result == "" {
			result = resultText(r["content"])
		}
		d.Result = capText(result, outputCap)
		d.Summary = activityDetail(d.Description)
		if d.Status != "" {
			d.Summary = strings.TrimSpace(d.Summary + " (" + d.Status + ")")
		}
	default:
		d.Tool = ResultOther
		d.Keys = sortedKeys(r)
		d.Summary = strings.Join(d.Keys, ", ")
	}
	return d
}

// has reports whether the key is present.
func has(d session.Doc, key string) bool {
	_, ok := d[key]
	return ok
}

// intField reads the first of the keys present as a number.
func intField(d session.Doc, keys ...string) (int, bool) {
	for _, k := range keys {
		if v, ok := d[k]; ok {
			if n, ok := seqOf(v); ok {
				return n, true
			}
		}
	}
	return 0, false
}

// capText keeps at most n bytes of text (cut at a rune boundary) and marks
// the cut.
func capText(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + truncatedMark
}
