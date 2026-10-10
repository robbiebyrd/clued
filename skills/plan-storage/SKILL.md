---
name: plan-storage
description: Use when writing, saving, linking or reading an implementation plan, when a brainstorm has produced a design doc that a plan should reference, or when a plan template is needed. Routes plan storage through the mind-palace MCP tools instead of files under docs/superpowers/plans.
---

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
