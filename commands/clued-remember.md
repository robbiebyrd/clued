---
name: clued-remember
description: Write a handoff note for the next session on this project (clued's native memory) — what was done, what is half-done, what to do next
argument-hint: "[what the next session should know]"
---

You are writing a handoff note that clued keeps for this project and injects into the next sessions (for seven days) through the prime report's MEMORY section.

## Step 1 — Write the note

When the user gave text with the command ($ARGUMENTS), use it as the note, tidied but not reworded. Otherwise write it yourself from this session, in at most 150 words of Markdown:

- **Done**: what this session finished, naming files, commands and decisions
- **In progress**: what is half-done and where it stands
- **Next**: the first thing the next session should do

Write only what happened in this session. Leave secrets out; anything between `<private>` and `</private>` is dropped before the note is stored, and API keys and tokens are masked.

## Step 2 — Store it

Call the `remember` MCP tool of the `clued` server with `kind` `handoff` and the note as `text` (the project is the working directory's). If the MCP tool is not available, run instead:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" session memory remember --kind handoff - <<'NOTE'
<the note>
NOTE
```

## Step 3 — Confirm

Tell the user in one line that the handoff is stored, with its `id`. Mention that running it again adds another handoff (the newest one under seven days old is the one injected), that `search_memory` finds earlier ones, and that `mind-palace session memory edit` edits or deletes them.
