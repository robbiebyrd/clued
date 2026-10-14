package session

import (
	"encoding/json"
	"strings"
)

// LineText is the readable text of a transcript line: the message's text
// (a string, or its text, tool_use and tool_result blocks), else the line's
// own content or summary. A tool call reads "<name> <input JSON>" with the
// input cut to toolInputChars; thinking blocks are left out. Blocks are
// joined by newlines.
func LineText(d Doc) string {
	const toolInputChars = 500
	var parts []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	if c, ok := d.Message()["content"].(string); ok {
		add(c)
	}
	for _, b := range d.Content() {
		switch kind, _ := b.String("type"); kind {
		case "text":
			s, _ := b.String("text")
			add(s)
		case "tool_use":
			name, _ := b.String("name")
			input, _ := json.Marshal(b["input"])
			add(name + " " + cut(string(input), toolInputChars))
		case "tool_result":
			add(resultText(b["content"]))
		}
	}
	if len(parts) == 0 {
		line := d.Line()
		for _, k := range []string{"content", "summary"} {
			if s, ok := line.String(k); ok {
				add(s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// resultText is the text of a tool_result's content: a string, or the text
// of its blocks.
func resultText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var out []string
		for _, item := range c {
			if b := asDoc(item); b != nil {
				if s, ok := b.String("text"); ok {
					out = append(out, s)
				}
			}
		}
		return strings.Join(out, "\n")
	case []Doc:
		var out []string
		for _, b := range c {
			if s, ok := b.String("text"); ok {
				out = append(out, s)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// cut shortens s to at most n bytes, at a rune boundary, marking the cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// Snippet is up to max bytes of text around the first case-insensitive
// occurrence of any of terms (the start of text when none occurs), with …
// marking cut ends and whitespace runs collapsed.
func Snippet(text string, terms []string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= max {
		return text
	}
	lower := strings.ToLower(text)
	at := -1
	for _, t := range terms {
		if t = strings.ToLower(strings.TrimSpace(t)); t == "" {
			continue
		}
		if i := strings.Index(lower, t); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	start := 0
	if at > max/3 {
		start = at - max/3
	}
	for start > 0 && !isRuneStart(text[start]) {
		start--
	}
	end := min(start+max, len(text))
	for end < len(text) && !isRuneStart(text[end]) {
		end--
	}
	out := text[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}
