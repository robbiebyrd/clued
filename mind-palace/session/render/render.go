// Package render turns a captured session into a document people read: a
// Markdown file or a self-contained HTML page (inline CSS, no scripts). Both
// come from one model of the conversation, so they say the same thing:
// a header (project, repository, branch, times, usage) and the turns, with
// tool calls reduced to their name and a summary, tool results collapsed
// and errors marked. Thinking, snapshots and other bookkeeping lines are
// left out.
package render

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Usage is the usage summary the header shows.
type Usage struct {
	Messages     int64
	InputTokens  int64
	OutputTokens int64
	CacheTokens  int64
	CostUSD      *float64
}

// Input is what is rendered: the session record (as the store returns it),
// its transcript lines in seq order, and optionally its usage.
type Input struct {
	Session session.Doc
	Lines   []session.Doc
	Usage   *Usage
}

// Kind is what an entry of a turn is.
type Kind string

// The kinds of entry.
const (
	Text   Kind = "text"
	Tool   Kind = "tool"
	Result Kind = "result"
)

// Entry is one part of a turn: text, a tool call (Name and Summary), or a
// tool result (Body, Error).
type Entry struct {
	Kind    Kind
	Name    string
	Summary string
	Body    string
	Error   bool
}

// Turn is the consecutive entries of one role.
type Turn struct {
	Role    string
	Time    string
	Entries []Entry
}

// Model is the document both formats render.
type Model struct {
	Title    string
	Facts    [][2]string
	Turns    []Turn
	Redacted bool
}

// resultChars caps a tool result's body; the rest is summarised.
const resultChars = 2000

// Build reads the input into the model.
func Build(in Input) Model {
	s := in.Session
	m := Model{Title: str(s, "project_path")}
	if m.Title == "" {
		m.Title = str(s, "session_id")
	}
	fact := func(label, value string) {
		if value != "" {
			m.Facts = append(m.Facts, [2]string{label, value})
		}
	}
	fact("Session", str(s, "session_id"))
	fact("Repository", str(s, "git_origin"))
	fact("Branch", str(s, "git_branch"))
	fact("Started", when(s["started_at"]))
	fact("Last seen", when(s["last_seen"]))
	if u := in.Usage; u != nil && u.Messages > 0 {
		usage := fmt.Sprintf("%d messages, %d input, %d cache and %d output tokens", u.Messages, u.InputTokens, u.CacheTokens, u.OutputTokens)
		if u.CostUSD != nil {
			usage += fmt.Sprintf(", about $%.2f", *u.CostUSD)
		}
		fact("Usage", usage)
	}
	for _, doc := range in.Lines {
		if _, ok := doc["redacted"]; ok {
			m.Redacted = true
		}
		role, entries, at := entriesOf(doc)
		if len(entries) == 0 {
			continue
		}
		// A user line made only of tool results belongs to the assistant's
		// turn that called the tools.
		if role == "user" && allResults(entries) && len(m.Turns) > 0 && m.Turns[len(m.Turns)-1].Role == "assistant" {
			role = "assistant"
		}
		if n := len(m.Turns); n > 0 && m.Turns[n-1].Role == role {
			m.Turns[n-1].Entries = append(m.Turns[n-1].Entries, entries...)
			continue
		}
		m.Turns = append(m.Turns, Turn{Role: role, Time: at, Entries: entries})
	}
	return m
}

func allResults(entries []Entry) bool {
	for _, e := range entries {
		if e.Kind != Result {
			return false
		}
	}
	return true
}

// entriesOf reads one transcript line.
func entriesOf(doc session.Doc) (role string, entries []Entry, at string) {
	line := doc.Line()
	typ := str(line, "type")
	if typ != "user" && typ != "assistant" {
		return "", nil, ""
	}
	if meta, _ := line["isMeta"].(bool); meta {
		return "", nil, ""
	}
	role = str(doc.Message(), "role")
	if role == "" {
		role = typ
	}
	at = when(line["timestamp"])
	if c, ok := doc.Message()["content"].(string); ok && strings.TrimSpace(c) != "" {
		entries = append(entries, Entry{Kind: Text, Body: strings.TrimSpace(c)})
	}
	for _, b := range doc.Content() {
		switch str(b, "type") {
		case "text":
			if t := strings.TrimSpace(str(b, "text")); t != "" {
				entries = append(entries, Entry{Kind: Text, Body: t})
			}
		case "tool_use":
			entries = append(entries, Entry{Kind: Tool, Name: str(b, "name"), Summary: toolSummary(b["input"])})
		case "tool_result":
			isErr, _ := b["is_error"].(bool)
			body := strings.TrimRight(session.ResultText(b["content"]), "\n")
			entries = append(entries, Entry{Kind: Result, Body: clip(body, resultChars), Error: isErr})
		}
	}
	return role, entries, at
}

// toolSummary is the one argument that says what a tool call did, else its
// input cut short.
func toolSummary(input any) string {
	in := session.Doc{}
	if m, ok := input.(map[string]any); ok {
		in = m
	} else if d, ok := input.(session.Doc); ok {
		in = d
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt"} {
		if v := strings.TrimSpace(str(in, k)); v != "" {
			return clip(firstLine(v), 200)
		}
	}
	raw, _ := json.Marshal(input)
	if string(raw) == "null" || string(raw) == "{}" {
		return ""
	}
	return clip(string(raw), 200)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// clip cuts s to n runes, saying how much was left out.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf(" …[%d more characters]", len(r)-n)
}

func str(d session.Doc, key string) string {
	s, _ := d.String(key)
	return s
}

// when formats a stored time (time.Time or RFC 3339 text) in UTC.
func when(v any) string {
	switch t := v.(type) {
	case time.Time:
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04 UTC")
	case string:
		if at, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return at.UTC().Format("2006-01-02 15:04 UTC")
		}
	}
	return ""
}
