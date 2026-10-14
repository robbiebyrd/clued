---
name: clued-uninstall
description: Stop the clued daemon (local mode) or the server stack this machine hosts, remove the MongoDB launchd plist, clean up config, catch-up state and logs, and tell you how to uninstall the plugin itself
---

You are helping the user uninstall or clean up the clued Claude Code plugin.

## Step 1 — Detect platform and current state

!`uname -s 2>/dev/null || echo "Unknown"`

Run these checks to discover what exists on this machine:

!`test -f ~/.claude/plugins/data/clued/config.json && jq -r 'if (.serverUrl // "") != "" then "mode: clued server (\(.serverUrl))" else "mode: local MongoDB" end' ~/.claude/plugins/data/clued/config.json 2>/dev/null || echo "mode: unknown (no config.json)"`
!`test -f ~/Library/LaunchAgents/org.mongodb.mongod.plist && echo "plist: present" || echo "plist: absent"`
!`curl -sf --max-time 1 http://127.0.0.1:8085/health 2>/dev/null && echo "daemon: running" || echo "daemon: stopped"`
!`test -d ~/.claude/plugins/data/clued && echo "config dir: present" || echo "config dir: absent"`
!`test -d ~/.claude/plugins/data/clued/bin && echo "binary dir: present" || echo "binary dir: absent"`
!`test -d ~/.claude/plugins/data/clued/catchup && echo "catch-up state: present" || echo "catch-up state: absent"`
!`test -f ~/.claude/plugins/data/clued/relay.log && echo "relay log: present" || echo "relay log: absent"`
!`test -d ~/mongodb && echo "mongodb tarball: present at ~/mongodb" || echo "mongodb tarball: absent"`
!`docker ps -a --filter name=transcripts-mongo --format '{{.Names}} {{.Status}}' 2>/dev/null | grep -q . && echo "docker mongodb: container present" || echo "docker mongodb: absent"`
!`test -f ~/.clued-server/docker-compose.yml && echo "server stack: present at ~/.clued-server ($(docker compose -f ~/.clued-server/docker-compose.yml ps --status running -q 2>/dev/null | wc -l | tr -d ' ') containers running)" || echo "server stack: absent"`

`CLUED_SERVER_URL` in the environment also puts the plugin in server mode, whatever `config.json` says.

## Step 2 — Ask the user what to remove

Based on the detection results above, present only the items that are actually present.
Use AskUserQuestion to confirm scope before touching anything. Example options:

- **Services** (only if the daemon is running; in clued server mode nothing runs on this machine): stop the daemon (port 8085); the MCP server is started by Claude Code and ends with the session
- **launchd plist** (macOS, only if plist is present): unload and remove `~/Library/LaunchAgents/org.mongodb.mongod.plist`
- **Config** (only if present): remove `~/.claude/plugins/data/clued/` (includes `bin/`, which holds the downloaded release binary, and in server mode the token in `config.json`, the WAL of events the server has not received yet, the catch-up state and the relay log) and remove any legacy `mcpServers.clued` entry from `~/.claude/settings.json`
- **Relay state** (clued server mode, when the user keeps the config): remove `~/.claude/plugins/data/clued/catchup/` (per-session transcript offsets and lock files) and `~/.claude/plugins/data/clued/relay.log`; the next hook resumes from the start of each transcript, which the server deduplicates
- **MongoDB binaries** (only if `~/mongodb/` is present): remove the tarball install at `~/mongodb/`
  - Note: MongoDB data at `~/data/db/` contains session transcripts — offer to keep or remove separately
- **MongoDB data** (only if `~/data/db/` exists): remove `~/data/db/` — warn that this deletes all captured session transcripts
- **Docker MongoDB** (only if the `transcripts-mongo` container is present): stop and remove the container; separately offer to remove its `transcripts_data` volume — warn that the volume holds all captured session transcripts
- **Server stack** (only if `~/.clued-server/docker-compose.yml` is present): stop its containers (`nginx`, `clued-server`, `mongo`); separately offer to remove its `clued_mongo_data` volume and the `~/.clued-server` directory (tokens, CA, certificates), which also takes the CA out of this machine's trust stores — warn that the volume holds every session, plan and story the server received from every machine, and that other machines using this server lose it

In clued server mode the sessions, plans and stories on the server are not touched by anything here; revoking the token is the operator's `mind-palace server token revoke <name>`.

Wait for the user's answers before doing anything destructive.

## Step 3 — Stop services (local MongoDB mode, if a daemon is running and the user confirmed)

Skip this step in clued server mode or when the daemon check in Step 1 said `stopped`: there is nothing to stop and `pkill` would find nothing.

Stop the daemon:
```bash
pkill -f 'mind-palace session daemon$' 2>/dev/null; echo "daemon stopped"
```


## Step 4 — Remove launchd plist (macOS, if present and confirmed)

Skip this step when Step 1 said `plist: absent` (always the case for a setup that never installed a local MongoDB).

```bash
launchctl unload ~/Library/LaunchAgents/org.mongodb.mongod.plist 2>/dev/null || true
rm ~/Library/LaunchAgents/org.mongodb.mongod.plist
echo "launchd plist removed"
```

## Step 5 — Remove config and unregister MCP (if confirmed)

The stdio MCP server is declared by the plugin's `.mcp.json` and goes away with the plugin. Older setups may have left a `"clued"` key under `mcpServers` in `~/.claude/settings.json`; if present, remove it with the Edit tool. If `mcpServers` becomes empty, remove it entirely.

Then remove the config directory, which also holds `bin/` with the downloaded release binary, and in server mode `catchup/` and `relay.log`. A `mind-palace` installed with `go install` lives in `$(go env GOPATH)/bin` and is not touched:
```bash
rm -rf ~/.claude/plugins/data/clued
echo "config and binary dirs removed"
```

If the user keeps the config but asked for the relay state to go:
```bash
rm -rf ~/.claude/plugins/data/clued/catchup
rm -f ~/.claude/plugins/data/clued/relay.log
echo "catch-up state and relay log removed"
```

The non-interactive `hooks/uninstall` does the same: it stops the daemon only in local mode and only when one answers, stops the server stack when `~/.clued-server/docker-compose.yml` exists (unless `--keep-server`; `--purge-server` also removes its volume and directory, after `server trust --remove`, printing the commands that still need root and keeping the CA as `~/clued-server-ca.pem` for them), removes the plist only when present, always removes `catchup/` and `relay.log`, and removes the config directory unless `--keep-config` is given.

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

## Step 7b — Remove the Docker MongoDB (if confirmed)

Stop and remove the container started by the setup wizard:

```bash
docker rm -f transcripts-mongo 2>/dev/null && echo "container removed"
```

Only if the user confirmed deleting the data too (re-confirm first: "This will permanently delete all captured session transcripts in the Docker volume. Are you sure?"):

```bash
docker volume rm $(docker volume ls -q --filter name=transcripts_data) 2>/dev/null && echo "volume removed"
```

## Step 7c — Stop the server stack (if present and confirmed)

Stop the containers `mind-palace server init` set up; the data volume and the directory stay:

```bash
docker compose -f ~/.clued-server/docker-compose.yml down && echo "server stack stopped"
```

Only if the user confirmed deleting the server's data too (re-confirm first: "This will permanently delete every session, plan and story on this server, for every machine that uses it. Are you sure?"), first take its CA out of this machine's trust stores (it is harmless when it was never trusted), while `~/.clued-server/tls/ca.pem` still exists:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" server trust --remove
```

If its result lists `pending` commands (the Linux system store needs root, or the macOS keychain could not show its dialog), show them to the user and ask them to run them in their own terminal, or in this prompt prefixed with `!`, before you continue; once the directory is gone the commands cannot find the CA. Then:

```bash
docker compose -f ~/.clued-server/docker-compose.yml down --volumes && rm -rf ~/.clued-server && echo "server stack and its data removed"
```

Plans and stories keep their Markdown files in each repository either way.

## Step 8 — Confirm to user

Summarise what was removed and what was kept (in clued server mode: that the data on the server is untouched and the token is still valid until the operator revokes it).

## Step 9 — Tell the user to uninstall the Claude Code plugin

Nothing above removes the plugin itself: its hooks, the `/clued-*` commands and the `clued` and `mind-palace` MCP servers stay registered until the plugin is uninstalled, and you cannot uninstall it from here. End by telling the user, in so many words, that the cleanup is not complete until they do this themselves:

1. Uninstall the plugin: `/plugin uninstall clued@clued` in this prompt (or open `/plugin`, pick clued under **Installed** and choose **Uninstall**), or `claude plugin uninstall clued@clued` in a terminal. Uninstall it from the scope it was installed in; a project-scope install is listed in the repository's `.claude/settings.json` and goes for every collaborator once that change is committed.
2. Optionally remove the marketplace too: `/plugin marketplace remove clued`.
3. Run `/reload-plugins`, or start a new session.

Until the plugin is uninstalled its hooks keep firing in this session. In local mode a hook that finds no daemon writes the event to `~/.claude/plugins/data/clued/events.wal`, so the config directory removed in Step 5 can reappear; if it does after the plugin is gone, remove it again with `rm -rf ~/.claude/plugins/data/clued`.
