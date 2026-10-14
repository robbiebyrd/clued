package render

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

// Markdown renders the model as Markdown: tool calls as list items, tool
// results as fenced blocks (in <details> so they stay folded where the
// Markdown is shown as HTML).
func Markdown(m Model) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", oneLine(m.Title))
	for _, f := range m.Facts {
		fmt.Fprintf(&b, "- **%s:** %s\n", f[0], oneLine(f[1]))
	}
	if m.Redacted {
		b.WriteString("- **Redacted:** secrets in this session were masked before it was stored\n")
	}
	for _, t := range m.Turns {
		heading := strings.ToUpper(t.Role[:1]) + t.Role[1:]
		if t.Time != "" {
			heading += " · " + t.Time
		}
		fmt.Fprintf(&b, "\n## %s\n", heading)
		for _, e := range t.Entries {
			switch e.Kind {
			case Text:
				fmt.Fprintf(&b, "\n%s\n", e.Body)
			case Tool:
				fmt.Fprintf(&b, "\n- 🔧 **%s**", oneLine(e.Name))
				if e.Summary != "" {
					fmt.Fprintf(&b, " %s", inlineCode(e.Summary))
				}
				b.WriteString("\n")
			case Result:
				label := "Result"
				if e.Error {
					label = "⚠️ Error"
				}
				fence := fenceFor(e.Body)
				fmt.Fprintf(&b, "\n<details><summary>%s</summary>\n\n%s\n%s\n%s\n\n</details>\n", label, fence, e.Body, fence)
			}
		}
	}
	return []byte(b.String())
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// inlineCode wraps s in enough backticks that none inside closes it.
func inlineCode(s string) string {
	s = oneLine(s)
	ticks := "`"
	for strings.Contains(s, ticks) {
		ticks += "`"
	}
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	return ticks + pad + s + pad + ticks
}

// fenceFor is a code fence longer than any run of backticks in s.
func fenceFor(s string) string {
	fence := "```"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	return fence
}

var page = template.Must(template.New("session").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root { --bg: #fff; --fg: #1d1d1f; --muted: #6b6b70; --line: #e3e3e6; --user: #f2f5fb; --tool: #f7f7f8; --err: #b42318; }
@media (prefers-color-scheme: dark) { :root { --bg: #161618; --fg: #ececee; --muted: #9a9aa0; --line: #2c2c30; --user: #1d2433; --tool: #1f1f22; --err: #f97066; } }
body { margin: 0; background: var(--bg); color: var(--fg); font: 15px/1.55 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 860px; margin: 0 auto; padding: 24px 16px 64px; }
h1 { font-size: 22px; margin: 0 0 12px; overflow-wrap: anywhere; }
dl { display: grid; grid-template-columns: max-content 1fr; gap: 4px 16px; margin: 0 0 24px; color: var(--muted); font-size: 13px; }
dt { font-weight: 600; } dd { margin: 0; overflow-wrap: anywhere; }
section { border-top: 1px solid var(--line); padding: 14px 0; }
section.user { background: var(--user); margin: 0 -16px; padding: 14px 16px; border-radius: 8px; border-top: 0; }
h2 { font-size: 13px; text-transform: uppercase; letter-spacing: .04em; color: var(--muted); margin: 0 0 8px; }
.text { white-space: pre-wrap; overflow-wrap: anywhere; }
.tool { font: 13px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace; background: var(--tool); border-radius: 6px; padding: 4px 8px; margin: 6px 0; overflow-wrap: anywhere; }
details { margin: 6px 0; } summary { cursor: pointer; color: var(--muted); font-size: 13px; }
details.error summary { color: var(--err); font-weight: 600; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; font: 12.5px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace; background: var(--tool); border-radius: 6px; padding: 8px; margin: 6px 0 0; }
.note { color: var(--muted); font-size: 13px; }
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
<dl>{{range .Facts}}<dt>{{index . 0}}</dt><dd>{{index . 1}}</dd>{{end}}</dl>
{{if .Redacted}}<p class="note">Secrets in this session were masked before it was stored.</p>{{end}}
{{range .Turns}}<section class="{{.Role}}">
<h2>{{.Role}}{{if .Time}} · {{.Time}}{{end}}</h2>
{{range .Entries}}{{if eq .Kind "text"}}<div class="text">{{.Body}}</div>
{{else if eq .Kind "tool"}}<div class="tool">🔧 <strong>{{.Name}}</strong>{{if .Summary}} {{.Summary}}{{end}}</div>
{{else}}<details{{if .Error}} class="error"{{end}}><summary>{{if .Error}}⚠ Error{{else}}Result{{end}}</summary><pre>{{.Body}}</pre></details>
{{end}}{{end}}</section>
{{end}}</main>
</body>
</html>
`))

// HTML renders the model as one self-contained page: inline CSS that
// follows the reader's light or dark preference, no scripts, every value
// escaped.
func HTML(m Model) []byte {
	var b bytes.Buffer
	if err := page.Execute(&b, m); err != nil {
		return []byte("<!doctype html><title>error</title><p>" + template.HTMLEscapeString(err.Error()))
	}
	return b.Bytes()
}
