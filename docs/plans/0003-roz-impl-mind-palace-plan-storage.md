---
id: 0003-roz
title: Expose mind-palace plan storage through the clued plugin
type: impl
status: pending
priority: "2"
effort: S
created: "2026-10-10T02:46:02.952Z"
updated: "2026-10-10T02:46:22.332Z"
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  specs:
    - ./docs/superpowers/specs/2026-10-09-mind-palace-plan-storage-design.md
progress:
  "1":
    status: pending
  "1.1":
    status: pending
  "1.2":
    status: pending
  "2":
    status: pending
  "2.1":
    status: pending
  "3":
    status: pending
  "3.1":
    status: pending
  "4":
    status: pending
  "4.1":
    status: pending
  "5":
    status: pending
  "5.1":
    status: pending
---

# Expose mind-palace plan storage through the clued plugin

## Summary

**Goal:** Superpowers plans are created, updated and read through mind-palace's MCP tools from inside the clued plugin, and a write to docs/superpowers/plans/ is blocked with a reason that names the replacement tools.

**Problem:** superpowers:writing-plans saves plans as loose markdown under docs/superpowers/plans/, outside mind-palace's validation, templates, status workflow and store sync. Nothing stops the model from writing there, and nothing tells it about the mind-palace tools.

**Approach:** Add to the clued plugin a blocking PreToolUse hook, a thin skill that routes plan writing, spec linking and plan reading through the plan_* MCP tools, a .mcp.json entry that launches mind-palace mcp, and a check-setup warning when the binary is missing.


# Part 2 — Implementation

**Architecture:** Everything lives in the clued plugin at the repo root. The hook is a bash script registered both in hooks.json (plugin install) and by hooks/register-hooks (manual settings.json install). The skill carries no planning guidance of its own; it only names the MCP tools and the order to call them. The MCP entry relies on mind-palace resolving its config from the working directory.

**Tech Stack:** bash + jq for hooks (matching the existing hooks), node:test + tsx for tests (matching test/*.test.ts), Markdown SKILL.md, JSON for hooks.json and .mcp.json.

## Constraints

- TDD for every script change: write the failing test, watch it fail, implement, watch it pass, commit.
- The guard matches only paths containing docs/superpowers/plans/. docs/superpowers/specs/ must keep working.
- The guard uses exit 2 with the reason on stderr, the documented contract for a policy-enforcing PreToolUse hook. No JSON output.
- A hook prints at most one JSON object on stdout, so check-setup joins its messages into one systemMessage.
- Hooks and MCP servers load only at session start; every live check needs a fresh session.
- Run the whole suite (pnpm test) before each commit, not just the new file.
- Do not push until the final step; then open the PR.

## Files

| File | Change | Responsibility |
|---|---|---|
| `hooks/guard-superpowers-plans` | create | PreToolUse script: block Write/Edit under docs/superpowers/plans/ with a reason naming plan_create and plan_update |
| `test/guard-superpowers-plans.test.ts` | create | Exercise the guard with JSON on stdin: block, allow specs, allow unrelated, allow missing file_path |
| `hooks.json` | modify | Register the guard as a second PreToolUse group with matcher Write|Edit |
| `hooks/register-hooks` | modify | Register the same guard in settings.json for manual installs; replace stale guard entries on re-run |
| `test/register-hooks.test.ts` | modify | Assert the guard is registered with its matcher, the relay stays, and re-running keeps one guard |
| `hooks/check-setup` | modify | Also warn, in the same single systemMessage, when mind-palace is not on PATH |
| `test/check-setup.test.ts` | create | Exercise check-setup with controlled HOME and PATH |
| `.mcp.json` | modify | Add the mind-palace stdio MCP server |
| `skills/plan-storage/SKILL.md` | create | Route plan writing, spec linking and plan reading through the plan_* MCP tools |
| `README.md` | modify | Prerequisite for the mind-palace binary and a section describing plan storage |

## Phase 1: Plan guard hook

### 1.1: Guard script

**Files:**
- Create: `hooks/guard-superpowers-plans`
- Test: `test/guard-superpowers-plans.test.ts`

**Depends on:** none

**Interfaces:**
- Produces: `hooks/guard-superpowers-plans`: reads PreToolUse JSON on stdin; exits 2 with a stderr reason containing `plan_create` and `plan_update` when `tool_input.file_path` contains `docs/superpowers/plans/`; exits 0 silently otherwise.

- [ ] **Step 1: Write the failing tests** — Create `test/guard-superpowers-plans.test.ts`:
```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { spawnSync } from 'child_process';

const ROOT  = join(dirname(fileURLToPath(import.meta.url)), '..');
const GUARD = join(ROOT, 'hooks/guard-superpowers-plans');

function run(input: unknown) {
  return spawnSync('/bin/bash', [GUARD], { input: JSON.stringify(input), encoding: 'utf8' });
}
const call = (tool_name: string, file_path: string) => ({ tool_name, tool_input: { file_path } });

test('guard blocks Write under docs/superpowers/plans and names the MCP tools', () => {
  const r = run(call('Write', '/repo/docs/superpowers/plans/2026-10-09-feature.md'));
  assert.equal(r.status, 2);
  assert.match(r.stderr, /plan_create/);
  assert.match(r.stderr, /plan_update/);
  assert.equal(r.stdout, '');
});

test('guard blocks Edit under docs/superpowers/plans', () => {
  const r = run(call('Edit', '/repo/docs/superpowers/plans/2026-10-09-feature.md'));
  assert.equal(r.status, 2);
});

test('guard allows Write under docs/superpowers/specs', () => {
  const r = run(call('Write', '/repo/docs/superpowers/specs/2026-10-09-feature-design.md'));
  assert.equal(r.status, 0);
  assert.equal(r.stdout, '');
  assert.equal(r.stderr, '');
});

test('guard allows an unrelated path', () => {
  const r = run(call('Write', '/repo/src/index.ts'));
  assert.equal(r.status, 0);
});

test('guard allows input without a file_path', () => {
  const r = run({ tool_name: 'Write', tool_input: {} });
  assert.equal(r.status, 0);
  assert.equal(r.stderr, '');
});
```
- [ ] **Step 2: Run them and confirm they fail because the script does not exist** — `node --import tsx/esm --test test/guard-superpowers-plans.test.ts`
- [ ] **Step 3: Create the script** — Create `hooks/guard-superpowers-plans`:
```bash
#!/usr/bin/env bash
# Superpowers plans are stored by mind-palace, not written to disk by Claude.
# Blocks Write/Edit under docs/superpowers/plans/ and points at the MCP tools.
input=$(cat)
file_path=$(printf '%s' "$input" | jq -r '.tool_input.file_path // empty')

case "$file_path" in
  *docs/superpowers/plans/*)
    echo "Plans are stored by mind-palace, not written to docs/superpowers/plans/. Save a new plan with the plan_create MCP tool, or change an existing plan's content with plan_update." >&2
    exit 2
    ;;
esac
exit 0
```
Then `chmod +x hooks/guard-superpowers-plans`. `jq` is already a dependency of the other hooks.
- [ ] **Step 4: Run the new tests and the whole suite** — `pnpm test`
- [ ] **Step 5: Commit** — `git add hooks/guard-superpowers-plans test/guard-superpowers-plans.test.ts && git commit -m 'feat(hooks): block superpowers plan writes in favour of mind-palace'`

**Validation:** `pnpm test`

### 1.2: Register the guard in both hook paths

**Files:**
- Modify: `hooks.json`
- Modify: `hooks/register-hooks`
- Test: `test/register-hooks.test.ts`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `hooks/guard-superpowers-plans` from 1.1.
- Produces: A PreToolUse entry `{matcher: "Write|Edit", hooks: [{type: command, command: .../hooks/guard-superpowers-plans}]}` in both `hooks.json` and the settings.json written by `register-hooks`.

- [ ] **Step 1: Write the failing tests** — Append to `test/register-hooks.test.ts`, reusing its existing `run()` helper:
```ts
test('register-hooks adds the plan guard to PreToolUse with a Write|Edit matcher', () => {
  writeFileSync(SETTINGS_PATH, '{}');
  const hooks = run().hooks as Record<string, Array<{ matcher?: string; hooks: Array<{ command: string }> }>>;
  const guard = hooks.PreToolUse.find(e => e.hooks.some(h => h.command.endsWith('guard-superpowers-plans')));
  assert.ok(guard, 'guard missing from PreToolUse');
  assert.equal(guard!.matcher, 'Write|Edit');
  assert.equal(hooks.PreToolUse.filter(e => e.hooks.some(h => h.command.endsWith('event-relay'))).length, 1, 'relay must stay registered');
});

test('register-hooks keeps exactly one plan guard when run twice', () => {
  writeFileSync(SETTINGS_PATH, '{}');
  run();
  const hooks = run().hooks as Record<string, Array<{ hooks: Array<{ command: string }> }>>;
  const guards = hooks.PreToolUse.filter(e => e.hooks.some(h => h.command.endsWith('guard-superpowers-plans')));
  assert.equal(guards.length, 1);
});
```
- [ ] **Step 2: Run them and confirm the first fails with 'guard missing from PreToolUse'** — `node --import tsx/esm --test test/register-hooks.test.ts`
- [ ] **Step 3: Update register-hooks** — In `hooks/register-hooks`:
1. Add `GUARD="${HOOKS_DIR}/guard-superpowers-plans"` next to the other path variables and pass it to jq with `--arg guard "$GUARD"`.
2. Extend the `is_clued` regex so stale guard entries are replaced on re-run: `test("/hooks/(event-relay|session-start|check-setup|guard-superpowers-plans)$")`.
3. Change `replace` to take a list so one event can carry several clued entries, and update every call site to pass a one-element list:
```jq
def replace(ev; entries):
  .hooks[ev] = ((.hooks[ev] // []) | map(select(is_clued | not))) + entries;
```
4. Replace the PreToolUse line with:
```jq
replace("PreToolUse"; [
  relay_hook($relay),
  {"matcher": "Write|Edit", "hooks": [{"type": "command", "command": $guard}]}
]) |
```
- [ ] **Step 4: Update hooks.json** — In `hooks.json`, the `PreToolUse` array currently holds one object (the unmatched `event-relay` group). Append a second object to that array:
```json
{
  "matcher": "Write|Edit",
  "hooks": [
    {
      "type": "command",
      "command": "${CLAUDE_PLUGIN_ROOT}/hooks/guard-superpowers-plans"
    }
  ]
}
```
No `async`: a blocking hook must run synchronously.
- [ ] **Step 5: Check hooks.json is still valid JSON** — `jq . hooks.json > /dev/null`
- [ ] **Step 6: Run the whole suite** — `pnpm test`
- [ ] **Step 7: Commit** — `git add hooks.json hooks/register-hooks test/register-hooks.test.ts && git commit -m 'feat(hooks): register the superpowers plan guard'`

**Validation:** `pnpm test && jq . hooks.json > /dev/null`

## Phase 2: Setup check for the mind-palace binary

### 2.1: check-setup reports a missing binary

**Files:**
- Modify: `hooks/check-setup`
- Test: `test/check-setup.test.ts`

**Depends on:** none

**Interfaces:**
- Produces: `hooks/check-setup` prints `{"systemMessage": "..."}` once, listing every problem joined by `; `, or nothing when all is well. The install command is exactly `go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest`.

- [ ] **Step 1: Write the failing tests** — Create `test/check-setup.test.ts`:
```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { tmpdir } from 'os';
import { spawnSync } from 'child_process';

const ROOT        = join(dirname(fileURLToPath(import.meta.url)), '..');
const CHECK_SETUP = join(ROOT, 'hooks/check-setup');
const INSTALL_CMD = 'go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest';

function run(env: Record<string, string>) {
  return spawnSync('/bin/bash', [CHECK_SETUP], { env: { ...process.env, ...env }, encoding: 'utf8' });
}
function homeWithConfig(): string {
  const home = mkdtempSync(join(tmpdir(), 'clued-check-setup-'));
  const dir = join(home, '.claude/plugins/data/clued');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'config.json'), '{}');
  return home;
}
function pathWithStubBinary(): string {
  const bin = mkdtempSync(join(tmpdir(), 'clued-bin-'));
  writeFileSync(join(bin, 'mind-palace'), '#!/bin/sh\n', { mode: 0o755 });
  return bin;
}

test('check-setup tells the user to install mind-palace when it is not on PATH', () => {
  const home = homeWithConfig();
  const r = run({ HOME: home, PATH: '/usr/bin:/bin' });
  rmSync(home, { recursive: true, force: true });
  const msg = (JSON.parse(r.stdout) as { systemMessage: string }).systemMessage;
  assert.ok(msg.includes(INSTALL_CMD), `install command missing from: ${msg}`);
  assert.ok(!msg.includes('/clued-setup'), 'config is present, so no setup nag expected');
});

test('check-setup is silent when config exists and mind-palace is on PATH', () => {
  const home = homeWithConfig();
  const bin = pathWithStubBinary();
  const r = run({ HOME: home, PATH: `${bin}:/usr/bin:/bin` });
  rmSync(home, { recursive: true, force: true });
  rmSync(bin, { recursive: true, force: true });
  assert.equal(r.stdout, '');
});

test('check-setup reports both problems in one JSON message', () => {
  const home = mkdtempSync(join(tmpdir(), 'clued-check-setup-'));
  const r = run({ HOME: home, PATH: '/usr/bin:/bin' });
  rmSync(home, { recursive: true, force: true });
  const msg = (JSON.parse(r.stdout) as { systemMessage: string }).systemMessage;
  assert.ok(msg.includes('/clued-setup'));
  assert.ok(msg.includes(INSTALL_CMD));
});
```
`PATH` is set to `/usr/bin:/bin` rather than empty so `jq` stays reachable; `/usr/bin:/bin` is where Homebrew does not install, so `mind-palace` is absent there. If `jq` lives elsewhere on the test machine, extend that PATH with `dirname $(command -v jq)` in the test rather than loosening the assertion.
- [ ] **Step 2: Run them and confirm the install-command and both-problems tests fail** — `node --import tsx/esm --test test/check-setup.test.ts`
- [ ] **Step 3: Rewrite check-setup** — Replace `hooks/check-setup` with:
```bash
#!/usr/bin/env bash
# Session-start health check: one systemMessage listing whatever is missing.
CONFIG_FILE="${HOME}/.claude/plugins/data/clued/config.json"
INSTALL_CMD="go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest"

problems=()
if [[ ! -f "$CONFIG_FILE" ]]; then
  problems+=("clued: run /clued-setup to finish configuring this plugin")
fi
if ! command -v mind-palace &>/dev/null; then
  problems+=("clued: mind-palace is not on PATH, so the plan storage MCP server cannot start. Install it with: ${INSTALL_CMD}")
fi

if (( ${#problems[@]} > 0 )); then
  jq -cn --arg m "$(printf '%s; ' "${problems[@]}" | sed 's/; $//')" '{systemMessage: $m}'
fi
```
A hook may print only one JSON object, which is why the two problems are joined into a single `systemMessage`.
- [ ] **Step 4: Run the whole suite** — `pnpm test`
- [ ] **Step 5: Commit** — `git add hooks/check-setup test/check-setup.test.ts && git commit -m 'feat(hooks): warn at session start when mind-palace is not installed'`

**Validation:** `pnpm test`

## Phase 3: MCP server entry

### 3.1: Launch mind-palace mcp from the plugin

**Files:**
- Modify: `.mcp.json`
- Modify: `README.md`

**Depends on:** 2.1

**Interfaces:**
- Produces: An MCP server named `mind-palace` exposing `plan_*` and `story_*` tools in any session where the plugin is enabled and the binary is on PATH.

- [ ] **Step 1: Add the server entry** — In `.mcp.json` (plugin format, no `mcpServers` wrapper, matching the existing `clued` entry), add:
```json
"mind-palace": {
  "command": "mind-palace",
  "args": ["mcp"]
}
```
No `--config` or `--dir` flags: the server walks up from the working directory for `mind-palace.config.yaml`/`.json` and otherwise uses `docs/plans` and `docs/stories`, which is this repo's layout.
- [ ] **Step 2: Check .mcp.json is still valid JSON** — `jq . .mcp.json > /dev/null`
- [ ] **Step 3: Add the prerequisite to the README** — In `README.md` under `## Prerequisites`, add a third bullet:
```markdown
- `mind-palace` on your PATH for plan storage: `go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest`
```
- [ ] **Step 4: Install the binary locally so the live check can run** — `cd mind-palace && go install ./cmd/mind-palace && command -v mind-palace`
- [ ] **Step 5: Verify live in a fresh session** — Exit this Claude Code session and start a new one in the repo root. Hooks and MCP servers only load at session start. Then: (1) run `/mcp` and confirm a `mind-palace` server is connected; (2) call the `plan_getTemplate` tool with no arguments and confirm it returns the default template starting with `# {{ .Title }}`; (3) call `plan_get` with id `0002-2ub` and confirm the existing implementation plan comes back. Paste the tool names seen into the PR description.
- [ ] **Step 6: Commit** — `git add .mcp.json README.md && git commit -m 'feat(mcp): launch mind-palace from the clued plugin'`

**Validation:** `jq . .mcp.json > /dev/null`

## Phase 4: Plan storage skill

### 4.1: Write and verify the skill

**Files:**
- Create: `skills/plan-storage/SKILL.md`

**Depends on:** 1.2, 3.1

**Interfaces:**
- Consumes: The `plan_*` tools from 3.1 and the guard from 1.2.

- [ ] **Step 1: Create the skill** — Create `skills/plan-storage/SKILL.md`:
```markdown
name: plan-storage
description: Use when writing, saving, linking or reading an implementation plan, when a brainstorm has produced a design doc that a plan should reference, or when a plan template is needed. Routes plan storage through the mind-palace MCP tools instead of files under docs/superpowers/plans.

# Plan storage via mind-palace

Plans are documents managed by the `mind-palace` MCP server, not loose
markdown files. The superpowers skills still decide *how* a plan is written;
this skill only changes where the layout comes from and where the result goes.
A PreToolUse hook blocks `Write` and `Edit` under `docs/superpowers/plans/`,
so writing there will fail.

## Writing a plan

1. Fetch the layout first: call `plan_getTemplate` (default template) or pick
   one from `plan_listTemplates`. The template shows which sections a plan
   body has (summary, design, implementation phases, acceptance criteria).
2. Run `superpowers:writing-plans` as usual to work out the tasks and steps.
3. Save the result with `plan_create`, passing `frontMatter` (title, type,
   status, priority) and a structured `body` that matches the creation schema
   from `plan_getSchema`. Do not write the plan to `docs/superpowers/plans/`.
4. To change the content of a plan that already exists, call `plan_update`
   with its id. Front matter fields change through their own setters
   (`plan_setStatus`, `plan_setTitle`, and so on), never through `plan_update`.

## Linking a design doc

Design docs from `superpowers:brainstorming` stay as files under
`docs/superpowers/specs/`; mind-palace does not store them. After creating
the plan, attach the spec's repo-relative path with `plan_addSpec` so the
plan records what it implements.

## Reading a plan

When executing, reviewing or resuming a plan, it lives in mind-palace's
plans directory (`docs/plans` by default), not `docs/superpowers/plans/`.
Fetch it with `plan_get` (front matter and content) or `plan_getContent`
(markdown only), or read the file at the path `plan_get` returns.
```
Verify the tool names against `mind-palace ops` before committing: the MCP tools are the ops with a `plan_` prefix.
- [ ] **Step 2: Verify the tool names exist** — `mind-palace ops | grep -E '"(create|update|getTemplate|listTemplates|getSchema|addSpec|get|getContent|setStatus|setTitle)"'`
- [ ] **Step 3: Verify live in a fresh session** — In a fresh session, ask: "Create a plan for adding a --version flag to mind-palace." Confirm that (1) the `clued:plan-storage` skill appears in the loaded skills list, (2) the model calls `plan_getTemplate` before drafting, and (3) it saves with `plan_create` rather than attempting a Write. If it attempts the Write, confirm the guard hook's denial text appears and the model recovers by calling `plan_create`. Record what happened in the PR description. Then delete the throwaway plan with `mind-palace plan delete <id>`.
- [ ] **Step 4: Commit** — `git add skills/plan-storage/SKILL.md && git commit -m 'feat(skills): route plan storage through mind-palace'`

## Phase 5: Documentation and pull request

### 5.1: README section and PR

**Files:**
- Modify: `README.md`

**Depends on:** 4.1

- [ ] **Step 1: Document the plan storage components** — Append to the `## Mind palace (`mind-palace/`)` section of `README.md`:
```markdown
### Plan storage for superpowers

With the plugin installed, implementation plans written by the superpowers
`writing-plans` skill are stored in mind-palace rather than under
`docs/superpowers/plans/`:

- `hooks/guard-superpowers-plans` is a PreToolUse hook that blocks `Write` and
  `Edit` under `docs/superpowers/plans/` and names the `plan_create` /
  `plan_update` MCP tools to use instead. Design docs under
  `docs/superpowers/specs/` are not affected.
- `skills/plan-storage` tells Claude to fetch a template with
  `plan_getTemplate`, save with `plan_create` or `plan_update`, link a design
  doc with `plan_addSpec`, and read plans back with `plan_get`.
- `.mcp.json` starts `mind-palace mcp` in the project directory, so it needs
  the `mind-palace` binary on PATH (see Prerequisites). `check-setup` warns at
  session start when it is missing.
```
- [ ] **Step 2: Run the whole suite, lint and typecheck one last time** — `pnpm test && pnpm lint && pnpm typecheck`
- [ ] **Step 3: Commit** — `git add README.md && git commit -m 'docs: describe plan storage through mind-palace'`
- [ ] **Step 4: Mark this plan complete in mind-palace** — `mind-palace plan set-status 0003-roz complete`
- [ ] **Step 5: Push the branch and open the PR** — Push `wip/mind-palace-plugin` and open a PR against `main`. The body lists: the file-store rule change already on the branch, the four plugin components, the live verification results from 3.1 and 4.1 (tool names seen, the guard's denial text as shown by Claude Code), and the spec path. — `git push -u origin wip/mind-palace-plugin && gh pr create --fill`

**Validation:** `pnpm test && pnpm lint && pnpm typecheck`

## Acceptance Criteria

- [ ] A Write or Edit whose file_path contains docs/superpowers/plans/ is blocked in a live session, and the block message names plan_create and plan_update.
- [ ] A Write under docs/superpowers/specs/ is not blocked.
- [ ] The guard is registered in hooks.json and by register-hooks, with matcher Write|Edit, and re-running register-hooks leaves exactly one guard entry.
- [ ] check-setup prints one systemMessage that includes the go install command when mind-palace is not on PATH, and prints nothing when config exists and the binary is present.
- [ ] A fresh session with the plugin lists a connected mind-palace MCP server and plan_getTemplate returns the default template.
- [ ] The plan-storage skill loads in a fresh session and a plan request results in plan_getTemplate followed by plan_create, with no file written under docs/superpowers/plans/.
- [ ] pnpm test, pnpm lint and pnpm typecheck pass.
- [ ] A PR is open against main with the live verification results in its body.

## Self-Review

| Requirement | Delivered by |
|---|---|
| Spec: hook blocks Write/Edit under docs/superpowers/plans/ with reason naming the tools | 1.1, Steps 1-3 |
| Spec: docs/superpowers/specs/ untouched | 1.1, Step 1 (allow-specs test) |
| Spec: registered in hooks.json with matcher Write|Edit | 1.2, Step 4 |
| Spec: skill with three cases, no superpowers guidance | 4.1, Step 1 |
| Spec: .mcp.json runs mind-palace mcp with no flags | 3.1, Step 1 |
| Spec: check-setup reports missing binary with the go install command | 2.1, Steps 1-3 |
| Spec: hook tests for block, Edit, specs, unrelated, no file_path | 1.1, Step 1 |
| Spec: check-setup tests for absent and present binary | 2.1, Step 1 |
| Spec: MCP verified live by listing tools and calling plan_getTemplate | 3.1, Step 5 |
| Spec: skill verified by loading in a fresh session | 4.1, Step 3 |
| Prompt: open a PR when done | 5.1, Step 5 |

**Placeholder scan:** No TBD/TODO. Every code step carries its full content. The plan id in 5.1 is this plan's own id, 0003-roz.
**Residual risk:** Review focus, inputs the spec implies but no test pins: (1) a relative file_path such as docs/superpowers/plans/x.md is matched by the substring case, but Claude Code documents file_path as always absolute, so this is belt-and-braces; (2) a path with the segment spelled differently (docs/superpowers/plan/, uppercase) passes through, which is intended since only the exact superpowers directory is reserved; (3) register-hooks silently skips when jq is missing, so the guard is not registered on such machines, matching existing behaviour for every clued hook; (4) check-setup's test pins PATH to /usr/bin:/bin, which fails if jq is not there; the test notes the fix; (5) if mind-palace is missing from PATH the MCP server fails to start and Claude Code reports a connection failure; check-setup is the only hint, so its message must stay accurate.

## Next Steps

1. Once the skill proves itself, consider the same treatment for stories so bugs and features captured during work land in mind-palace too.
