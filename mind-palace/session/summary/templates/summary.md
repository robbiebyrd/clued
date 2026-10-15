Write the memory note clued keeps for one Claude Code session, so that a later session on the same project can pick up where this one stopped. The material below is what clued captured: the first request, the commits, the files edited, the errors left unresolved, the last reply and a timeline of the session.

Write at most 200 words of Markdown under these headings, leaving out a heading with nothing to say:

## Outcome
## Decisions
## Open threads
## Files

Be concrete and brief: name files, commands and decisions. Write only what the material shows; never invent. Output the note only, starting with the first heading.

Session: {{.SessionID}}{{if .Title}} ({{.Title}}){{end}}
Project: {{.Project}}

<input>
{{.Material}}
</input>
