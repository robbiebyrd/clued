---
name: clued-setup
description: First-time setup for the clued plugin. Run this after installing clued to configure your MongoDB connection and register hook relays.
---

# clued Setup

You are helping the user configure the `clued` Claude Code plugin.

## Steps

### 1. Gather MongoDB config

Ask the user:
- MongoDB connection URL (default: `mongodb://localhost:27018`)
- Database name (default: `claude_sessions`)

If the user has the docker-compose.transcripts.yml MongoDB running locally, the defaults work without changes.

### 2. Write config file

Write the config to `~/.claude/plugins/data/clued/config.json` (create parent directories if needed):

```json
{
  "mongoUrl": "<user's URL>",
  "dbName": "<user's DB name>",
  "port": 8085,
  "projectsDir": "~/.claude/projects",
  "disabledEnrichers": []
}
```

### 3. Add relay hooks to user settings

Read `~/.claude/settings.json`. Add a hook entry for each of these event types, pointing at `${CLAUDE_PLUGIN_ROOT}/hooks/event-relay` with `async: true`:

`PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Notification`,
`UserPromptSubmit`, `Stop`, `SubagentStop`, `WorktreeCreate`, `WorktreeRemove`,
`InstructionsLoaded`, `CwdChanged`, `FileChanged`

Each entry uses this shape:
```json
{
  "hooks": [
    {
      "type": "command",
      "command": "${CLAUDE_PLUGIN_ROOT}/hooks/event-relay",
      "async": true
    }
  ]
}
```

Do not duplicate entries if they already exist.

### 4. Verify daemon

Run the session-start hook manually to confirm the daemon starts:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

Then check health:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon is running"
```

### 5. Confirm to user

Tell the user:
- Config written to `~/.claude/plugins/data/clued/config.json`
- Relay hooks added to `~/.claude/settings.json`
- Daemon status (running / failed to start)
- How to add custom enrichers: drop a `.mjs` file in `<plugin-root>/enrichers/` and restart the daemon
- How to enable the privacy-redact enricher: remove `privacy-redact` from `disabledEnrichers` in `config.json` (or set `enabled: true` in the file)
