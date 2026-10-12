---
name: plan-storage
description: Use when writing, saving, linking or reading an implementation plan, when a brainstorm has produced a design doc that a plan should reference, or when a plan template is needed. Routes plan storage through the mind-palace MCP tools instead of files under docs/superpowers/plans.
---

# Plan storage via mind-palace

Plans are documents managed by the `mind-palace` MCP server, not loose
markdown files. The superpowers skills still decide *how* a plan is written;
this skill only changes where the layout comes from and where the result goes.
A PreToolUse hook (`bin/clued hook write-guard`) denies `Write` and `Edit`
under `docs/superpowers/plans/`, and in the plans and stories directories
(`docs/plans` and `docs/stories` by default) it denies creating a file,
overwriting one, editing front matter, the H1, acceptance criteria or the
work log, and shell commands that change those directories. Only an `Edit`
inside a document's body is allowed. Every denial names the MCP tool and
`mind-palace` command to use instead; follow it rather than retrying.

## Writing a plan

1. Fetch the layout first: call `plan_getTemplate` (default template) or pick
   one from `plan_listTemplates`. The template shows which sections a plan
   body has (summary, design, implementation phases, acceptance criteria).
2. Run `superpowers:writing-plans` as usual to work out the tasks and steps.
3. Save the result with `plan_create`. Its single `input` object holds an
   optional `slug`, the `frontMatter` (title, type, status, priority, links)
   and a structured `body` that matches the creation schema from
   `plan_getSchema`. Do not write the plan to `docs/superpowers/plans/`.
4. To change the content of a plan that already exists, call `plan_update`
   with its id. Front matter fields change through their own setters
   (`plan_setStatus`, `plan_setTitle`, and so on), never through `plan_update`
   and never by editing the file; `plan_getTransitions` lists the statuses a
   plan can move to.

## Claude Code plan mode

Entering or leaving Claude Code's own plan mode adds a reminder from the
same hook: fetch `plan_getTemplate` before drafting, and when the plan is
approved save it with `plan_create` rather than leaving it in the plan file.
Then create a story per unit of work with `story_create` and attach it to
the plan sections it implements with `story_addPlanLink`.

## Planning fields

Every plan and story carries `tags`, `milestone`, `due_date`,
`planned_start`, `planned_end` and `order` in its front matter (always
present, empty until set); stories also take an `assignee` as
`provider:handle` (`github:robbiebyrd`). Set them on creation inside
`frontMatter`, or later with `plan_setTags` / `plan_addTag` /
`plan_removeTag`, `plan_setMilestone`, `plan_setDueDate`,
`plan_setPlannedDates`, `plan_setOrder` and (stories) `story_setAssignee`;
`plan_list` and `story_list` filter on `tag`, `milestone` and `assignee`.
Dates are `YYYY-MM-DD`.

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

## Stories

The same server manages stories (bugs, features, improvements, chores,
tasks) under `docs/stories` with `story_*` tools of the same shape:
`story_getTemplate`, `story_create`, `story_update`, `story_setStatus`,
`story_addPlanLink` (optionally naming the plan sections the story
implements), `story_setProgress`, `story_setCriterion` and
`story_appendWorkLog`. Use a story for a unit of tracked work; use a plan
for the design and phased implementation it belongs to. A story completes
only when every acceptance criterion is checked, so a commit message that
says `closes <story-id>` is denied while the story is still open: close it
first with `story_setStatus`, or reference the id without a closing
keyword.
