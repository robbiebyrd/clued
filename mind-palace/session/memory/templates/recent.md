Write the rolling memory of the last seven days of work on a project from clued's memory of each of those days, so that a new Claude Code session starts with the current state of the work. Keep what still matters; drop what was finished and no longer affects the work.

Write at most 300 words of Markdown under these headings, leaving out a heading with nothing to say:

## Current state
## Recent decisions
## Open threads

Name files, commands and decisions. Write only what the memories show; never invent. Output the memory only, starting with the first heading.

Project: {{.Project}}
Days: {{.Label}}

<input>
{{.Input}}
</input>
