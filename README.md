# clued

MongoDB session mirror for Claude Code. Captures every hook event and stores it in MongoDB with an enricher pipeline.

## Prerequisites

- [Claude Code](https://claude.ai/code) installed
- A running or accessible MongoDB instance (local, Docker, or remote — setup guides you through this)

---

## 1. Add the Marketplace

Register the clued marketplace with Claude Code so it can find the plugin:

```
/plugin marketplace add robbiebyrd/clued
```

---

## 2. Install the Plugin

```
/plugin install clued@clued
```

Claude Code will ask you to choose an installation scope:

| Scope | Effect |
|-------|--------|
| **User** | Installs for you across all projects (recommended) |
| **Project** | Installs for all collaborators in this repo (writes to `.claude/settings.json`) |
| **Local** | Installs for you in this repo only (writes to `.claude/settings.local.json`) |

---

## 3. Reload Plugins

If Claude Code prompts you to reload, run:

```
/reload-plugins
```

---

## 4. Configure

Run the setup command and follow the prompts:

```
/clued-setup
```

The setup wizard will:

1. Detect your platform
2. Ask how you want to run MongoDB (local binary, Docker, or remote)
3. Install and start MongoDB if needed
4. Write a config file to `~/.claude/plugins/data/clued/config.json`
5. Start the daemon and MCP server
6. Verify everything is running

The MCP tools (`find_sessions`, `get_session_context`, `search_commands`, `read_transcript`) are provided by the plugin's `.mcp.json`, which launches the server over stdio — no settings edit needed.

---

## 5. Verify

After setup, two services should be healthy:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon running"
curl -sf http://127.0.0.1:8086/health && echo "mcp server running"
```

---

## Manual Configuration Reference

For environments where `/clued-setup` isn't available or you prefer to configure by hand.

### Config file

`~/.claude/plugins/data/clued/config.json`:

```json
{
  "mongoUrl": "mongodb://localhost:27017",
  "dbName": "claude_sessions",
  "port": 8085,
  "mcpPort": 8086,
  "projectsDir": "~/.claude/projects",
  "disabledEnrichers": []
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `mongoUrl` | `mongodb://localhost:27017` | MongoDB connection string |
| `dbName` | `claude_sessions` | Database name |
| `port` | `8085` | Daemon HTTP port |
| `mcpPort` | `8086` | MCP server SSE port |
| `projectsDir` | `~/.claude/projects` | Path Claude Code stores project data |
| `disabledEnrichers` | `[]` | Enricher names to skip (e.g. `["privacy-redact"]`) |

**Docker MongoDB** uses port `27018` by default to avoid clashing with a local mongod on `27017`.

### MCP server registration

Add to `~/.claude/settings.json` under `mcpServers`:

```json
"mcpServers": {
  "clued": { "type": "sse", "url": "http://127.0.0.1:8086/sse" }
}
```

### Starting the daemon manually

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/register-hooks"
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

---

## Uninstall

```
/clued-uninstall
```

The uninstall wizard detects what's present on your machine and asks before removing anything.
