# mind-palace as the storage layer for superpowers plans

## Purpose

Superpowers' `writing-plans` skill saves implementation plans as loose
markdown under `docs/superpowers/plans/`. mind-palace already manages plans as
validated documents with front matter, templates, status workflow and
multi-store sync. This change makes mind-palace the only place a plan is
written, while leaving the superpowers skills in charge of how a plan is
thought through.

Design docs produced by `brainstorming` are not tracked by mind-palace. They
stay as files under `docs/superpowers/specs/` and are linked from a plan by
path.

## Scope

In scope, all inside the `clued` plugin (repo root):

1. A PreToolUse hook that blocks `Write` and `Edit` under
   `docs/superpowers/plans/` and tells the model which MCP tool to use instead.
2. A skill that routes plan writing, spec linking and plan reading through the
   mind-palace MCP tools.
3. An MCP server entry that launches `mind-palace mcp`.
4. A `check-setup` extension that reports when the `mind-palace` binary is not
   on PATH.

Out of scope: any change to how superpowers plans or brainstorms; any change to
mind-palace's own behaviour beyond the file-store rule already landed.

## Components

### Hook: `hooks/guard-superpowers-plans`

- Registered in `hooks.json` as a second `PreToolUse` group with matcher
  `Write|Edit`, next to the existing unmatched `event-relay` group.
- Reads the hook input JSON from stdin and extracts `tool_input.file_path`.
  Claude Code passes this as an absolute path.
- If the path contains `docs/superpowers/plans/`, prints a one-line reason to
  stderr and exits 2. The reason names `plan_create` (new plan) and
  `plan_update` (existing plan) as the replacements.
- Any other path, or input without a `file_path`, exits 0 with no output.
  `docs/superpowers/specs/` is deliberately not matched.
- Exit 2 with a stderr reason is the documented contract for a policy-enforcing
  PreToolUse hook; no JSON output is needed.

### Skill: `skills/plan-storage/SKILL.md`

- Description keyed on: plan, brainstorm, template.
- Body covers exactly three cases and nothing else:
  - Writing a plan: fetch the layout with `plan_getTemplate` (or pick one via
    `plan_listTemplates`), run `superpowers:writing-plans` as normal, then save
    with `plan_create` for a new plan or `plan_update` for existing content.
  - Linking a design doc: when a brainstorm produced a spec under
    `docs/superpowers/specs/`, attach its path with `plan_addSpec`.
  - Reading a plan: when executing, reviewing or resuming, the plan lives in the
    configured plans directory (default `docs/plans`), not
    `docs/superpowers/plans/`. Fetch it with `plan_get` / `plan_getContent` or
    read the file there.
- The skill does not restate any of superpowers' planning guidance.
- Uses the documented `skills/<name>/SKILL.md` layout. The existing flat
  `skills/clued-setup.md` is left as is.

### MCP: `.mcp.json`

- Adds a `mind-palace` server next to the existing `clued` entry, in the same
  flat format the file already uses.
- Command: `mind-palace mcp`, no flags. The server finds a config file by
  walking up from the working directory and otherwise defaults to `docs/plans`
  and `docs/stories`, which matches this repo.
- Requires the `mind-palace` binary on PATH.

### Setup check: `hooks/check-setup`

- Already prints a `systemMessage` when clued's config is missing.
- Also prints one when `mind-palace` is not on PATH, giving the
  `go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest`
  command.

## Testing

- `test/guard-superpowers-plans.test.ts`, following the existing
  `register-hooks.test.ts` pattern (run the bash script with JSON on stdin):
  - a `Write` to `docs/superpowers/plans/x.md` exits 2 and stderr names
    `plan_create` and `plan_update`;
  - an `Edit` to the same path exits 2;
  - a `Write` to `docs/superpowers/specs/x.md` exits 0 with empty output;
  - a `Write` to an unrelated path exits 0;
  - input with no `file_path` exits 0.
- `check-setup` test: with `mind-palace` absent from PATH the output contains
  the install command; with a stub binary on PATH it does not.
- MCP wiring is verified live: a fresh session lists the `plan_*` tools and a
  `plan_getTemplate` call returns the default template.
- The skill has no unit test; it is checked by loading in a fresh session.

## Dogfooding

The implementation plan for this work is created in mind-palace with the CLI
(`mind-palace plan create`), not written to `docs/superpowers/plans/`, and
links this spec with `plan addSpec`.
