---
name: clued-uninstall
description: Stop the clued daemon, remove the MongoDB launchd plist, and clean up config
---

You are helping the user uninstall or clean up the clued Claude Code plugin.

## Step 1 — Detect platform and current state

!`uname -s 2>/dev/null || echo "Unknown"`

Run these checks to discover what exists on this machine:

!`test -f ~/Library/LaunchAgents/org.mongodb.mongod.plist && echo "plist: present" || echo "plist: absent"`
!`curl -sf --max-time 1 http://127.0.0.1:8085/health 2>/dev/null && echo "daemon: running" || echo "daemon: stopped"`
!`test -d ~/.claude/plugins/data/clued && echo "config dir: present" || echo "config dir: absent"`
!`test -d ~/.claude/plugins/data/clued/bin && echo "binary dir: present" || echo "binary dir: absent"`
!`test -d ~/mongodb && echo "mongodb tarball: present at ~/mongodb" || echo "mongodb tarball: absent"`

## Step 2 — Ask the user what to remove

Based on the detection results above, present only the items that are actually present.
Use AskUserQuestion to confirm scope before touching anything. Example options:

- **Services** (always offer): stop the daemon (port 8085); the MCP server is started by Claude Code and ends with the session
- **launchd plist** (macOS, only if plist is present): unload and remove `~/Library/LaunchAgents/org.mongodb.mongod.plist`
- **Config** (only if present): remove `~/.claude/plugins/data/clued/` (includes the downloaded binary directory `bin/`) and remove any legacy `mcpServers.clued` entry from `~/.claude/settings.json`
- **MongoDB binaries** (only if `~/mongodb/` is present): remove the tarball install at `~/mongodb/`
  - Note: MongoDB data at `~/data/db/` contains session transcripts — offer to keep or remove separately
- **MongoDB data** (only if `~/data/db/` exists): remove `~/data/db/` — warn that this deletes all captured session transcripts

Wait for the user's answers before doing anything destructive.

## Step 3 — Stop services (if user confirmed)

Stop the daemon:
```bash
pkill -f 'mind-palace session daemon$' 2>/dev/null; echo "daemon stopped"
```


## Step 4 — Remove launchd plist (macOS, if present and confirmed)

```bash
launchctl unload ~/Library/LaunchAgents/org.mongodb.mongod.plist 2>/dev/null || true
rm ~/Library/LaunchAgents/org.mongodb.mongod.plist
echo "launchd plist removed"
```

## Step 5 — Remove config and unregister MCP (if confirmed)

The stdio MCP server is declared by the plugin's `.mcp.json` and goes away with the plugin. Older setups may have left a `"clued"` key under `mcpServers` in `~/.claude/settings.json`; if present, remove it with the Edit tool. If `mcpServers` becomes empty, remove it entirely.

Then remove the config directory, which also holds the downloaded binary directory `bin/`. A `mind-palace` installed with `go install` lives in `$(go env GOPATH)/bin` and is not touched:
```bash
rm -rf ~/.claude/plugins/data/clued
echo "config and binary dirs removed"
```

## Step 6 — Remove MongoDB binaries (if confirmed)

```bash
rm -rf ~/mongodb
echo "~/mongodb removed"
```

## Step 7 — Remove MongoDB data (if confirmed — warn first)

Before running this, re-confirm: "This will permanently delete all captured session transcripts in ~/data/db. Are you sure?"

```bash
rm -rf ~/data/db
echo "~/data/db removed"
```

## Step 8 — Confirm to user

Summarise what was removed and what was kept. Remind them to run `/plugin` and then uninstall clued from the plugin manager to complete the removal.
