package enrichers

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// ActivityKindName is the name of the activity-kind enricher: the key under
// enrichments its result is stored at.
const ActivityKindName = "activity-kind"

// activityDetailChars bounds the detail of an activity (a prompt, a reply, a
// command) so a timeline stays readable.
const activityDetailChars = 300

// The activity kinds. A line's kind is what the agent was doing when it was
// written: the user spoke, the agent replied or thought, or it called a tool
// of a family a timeline tells apart (a shell command, a file edit, a read or
// search, a sub-agent, a commit); the tool's result and everything else
// (system, meta, hook and sidecar lines) have kinds of their own.
const (
	ActivityUser     = "user"
	ActivityResponse = "response"
	ActivityThinking = "thinking"
	ActivityBash     = "bash"
	ActivityCommit   = "commit"
	ActivityEdit     = "edit"
	ActivityRead     = "read"
	ActivityTask     = "task"
	ActivityTool     = "tool"
	ActivityResult   = "result"
	ActivityOther    = "other"
)

// ActivityKindResult is what the activity-kind enricher stores. Kind is one
// of the Activity* constants; Label names the activity the way a timeline
// prints it (the tool's name for a tool call, the line's type for an
// "other" line, else the kind); Detail is what it was about: the prompt or
// reply's text, the command, the file's base name, the search pattern, the
// sub-agent's description or the commit message's first line, at most 300
// characters. ToolName and ToolUseID are set for a tool call, ToolUseID and
// IsError for a tool result. Blocks lists the kinds of the content blocks
// after the first when a line holds several.
type ActivityKindResult struct {
	Kind      string   `json:"kind" bson:"kind"`
	Label     string   `json:"label" bson:"label"`
	Detail    string   `json:"detail" bson:"detail"`
	ToolName  string   `json:"tool_name" bson:"tool_name"`
	ToolUseID string   `json:"tool_use_id" bson:"tool_use_id"`
	IsError   bool     `json:"is_error,omitempty" bson:"is_error,omitempty"`
	Blocks    []string `json:"blocks,omitempty" bson:"blocks,omitempty"`
}

// ActivityKind labels every transcript line with what the agent was doing.
var ActivityKind = session.Enricher{
	Name:       ActivityKindName,
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		return classifyActivity(doc), nil
	},
}

func init() { session.Register(ActivityKind) }

// classifyActivity is the activity of one transcript line.
func classifyActivity(doc session.Doc) ActivityKindResult {
	line := doc.Line()
	lineType, _ := line.String("type")
	role, _ := doc.Message().String("role")
	if lineType != "user" && lineType != "assistant" || role != "user" && role != "assistant" {
		return otherActivity(line)
	}
	// A prompt typed by the user is a string message.
	if text, ok := doc.Message().String("content"); ok {
		if role == "user" {
			return ActivityKindResult{Kind: ActivityUser, Label: ActivityUser, Detail: activityDetail(text)}
		}
		return ActivityKindResult{Kind: ActivityResponse, Label: ActivityResponse, Detail: activityDetail(text)}
	}
	blocks := doc.Content()
	if len(blocks) == 0 {
		return otherActivity(line)
	}
	first := blockActivity(blocks[0], role)
	for _, b := range blocks[1:] {
		first.Blocks = append(first.Blocks, blockActivity(b, role).Kind)
	}
	return first
}

// otherActivity is the activity of a line that is neither a prompt nor a
// message of the agent: its label is the line's type (or subtype).
func otherActivity(line session.Doc) ActivityKindResult {
	label, _ := line.String("type")
	if sub, ok := line.String("subtype"); ok && sub != "" {
		label += ":" + sub
	}
	if att, ok := line.Map("attachment").String("type"); ok && att != "" {
		label += ":" + att
	}
	if label == "" {
		label = ActivityOther
	}
	return ActivityKindResult{Kind: ActivityOther, Label: label}
}

// blockActivity is the activity of one content block of a message.
func blockActivity(block session.Doc, role string) ActivityKindResult {
	blockType, _ := block.String("type")
	switch blockType {
	case "text":
		text, _ := block.String("text")
		if role == "user" {
			return ActivityKindResult{Kind: ActivityUser, Label: ActivityUser, Detail: activityDetail(text)}
		}
		return ActivityKindResult{Kind: ActivityResponse, Label: ActivityResponse, Detail: activityDetail(text)}
	case "thinking", "redacted_thinking":
		text, _ := block.String("thinking")
		return ActivityKindResult{Kind: ActivityThinking, Label: ActivityThinking, Detail: activityDetail(text)}
	case "tool_use":
		return toolUseActivity(block)
	case "tool_result":
		id, _ := block.String("tool_use_id")
		isErr, _ := block["is_error"].(bool)
		return ActivityKindResult{Kind: ActivityResult, Label: ActivityResult, Detail: activityDetail(resultText(block["content"])), ToolUseID: id, IsError: isErr}
	}
	if blockType == "" {
		blockType = ActivityOther
	}
	return ActivityKindResult{Kind: ActivityOther, Label: blockType}
}

// toolUseActivity sorts a tool call into the family a timeline tells apart.
func toolUseActivity(block session.Doc) ActivityKindResult {
	name, _ := block.String("name")
	id, _ := block.String("id")
	input := block.Map("input")
	r := ActivityKindResult{Kind: ActivityTool, Label: name, ToolName: name, ToolUseID: id}
	if r.Label == "" {
		r.Label = ActivityTool
	}
	switch name {
	case "Bash":
		command, _ := input.String("command")
		command = stripANSI(command)
		if message, ok := commitMessage(command); ok {
			r.Kind, r.Detail = ActivityCommit, activityDetail(message)
		} else {
			r.Kind, r.Detail = ActivityBash, activityDetail(command)
		}
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		r.Kind, r.Detail = ActivityEdit, baseName(firstString(input, "file_path", "notebook_path"))
	case "Read":
		r.Kind, r.Detail = ActivityRead, baseName(firstString(input, "file_path"))
	case "Glob", "Grep":
		r.Kind, r.Detail = ActivityRead, activityDetail(firstString(input, "pattern", "path"))
	case "Task", "Agent":
		r.Kind, r.Detail = ActivityTask, activityDetail(firstString(input, "description", "prompt"))
	default:
		r.Detail = activityDetail(firstString(input, "url", "query", "pattern", "description", "skill", "command", "file_path", "prompt"))
	}
	return r
}

// resultText is the text of a tool_result's content: a string, or the text
// blocks of a list joined.
func resultText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, item := range c {
			var b session.Doc
			switch m := item.(type) {
			case map[string]any:
				b = session.Doc(m)
			case session.Doc:
				b = m
			}
			if text, ok := b.String("text"); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// firstString is the first of the keys present in input as a non-empty
// string.
func firstString(input session.Doc, keys ...string) string {
	for _, k := range keys {
		if s, ok := input.String(k); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// baseName is the last element of a file path, with the path's own
// separator or a slash.
func baseName(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return path.Base(p)
}

// activityDetail is text collapsed to one line of at most
// activityDetailChars.
func activityDetail(text string) string {
	return session.Snippet(text, nil, activityDetailChars)
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// stripANSI removes terminal escape sequences from a command.
func stripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

var (
	// gitCommit finds a `git commit` stage of a command (after any global
	// git options such as -C <dir>).
	gitCommit = regexp.MustCompile(`(?s)(?:^|[\s;&|(])git\s+(?:-[-\w]+(?:[=\s]\S+)?\s+)*commit\b(.*)`)
	// commitMessageFlag finds the message flag of a git commit: -m, --message
	// or --message=, followed by the message as a quoted string, a $(cat
	// <<'EOF' … EOF) here-document or a bare word.
	commitMessageFlag = regexp.MustCompile(`(?s)(?:^|\s)(?:-m|--message)(?:=|\s+)?("\$\(cat\s*<<-?\s*['"]?\w+['"]?\n(.*?)\n\w+\n\s*\)"|"((?:[^"\\]|\\.)*)"|'([^']*)'|(\S+))`)
)

// commitMessage reports whether the command is a git commit and the first
// line of its message when one is given inline; a commit without one (an
// editor, -F) reports the command's commit stage.
func commitMessage(command string) (string, bool) {
	m := gitCommit.FindStringSubmatch(command)
	if m == nil {
		return "", false
	}
	rest := m[1]
	if f := commitMessageFlag.FindStringSubmatch(rest); f != nil {
		for _, candidate := range f[2:] {
			if candidate != "" {
				return firstLine(candidate), true
			}
		}
	}
	return firstLine(strings.TrimSpace("git commit" + rest)), true
}

// firstLine is the first non-empty line of text, trimmed.
func firstLine(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return strings.ReplaceAll(l, `\"`, `"`)
		}
	}
	return ""
}
