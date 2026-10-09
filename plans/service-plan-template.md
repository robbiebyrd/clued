---
id: "0000-xxx"                          # AAAA-BBB from the filename: 4-digit sequence + 3-char random ID
title: "Description of plan"
type: "dsgn"                            # drft | dsgn | impl  (configurable)
status: "pending"                       # pending | validated | ready | in_progress | complete | blocked | rejected | archived
priority: "3"                           # 0 Emergency · 1 Critical · 2 High · 3 Medium · 4 Low · 5 Informational
effort: "M"                             # optional: XS (2) · S (3) · M (5) · L (8) · XL (13)
created: "YYYY-MM-DDTHH:MM:SS.sssZ"
updated: "YYYY-MM-DDTHH:MM:SS.sssZ"
completed: ""                           # set when status becomes complete
plans:                                  # [plan-id, relation] — relation: parent | included | depends | blocks
  - ["0000-xxx", "depends"]
links:
  specs:                                # plan IDs or paths of the designs/specs this plan builds on
    - "0000-xxx"
  repo:
    remote: "g@g.co:u/p.git"            # a git URL; can be a git or http(s):// url.
    local: "~/Projects/project"         # the local folder where the 
  web:                                  # name: URL
    jira: "https://jira.com/browse/A3"
  stories:                              # [story-id, relation] — relation: included | depends | blocks
    - ["000-xxx", "included"]
progress:                               # one entry per numbered section in Part 2; keys MUST be quoted
  "1.1":
    status: "pending".                  # same values as the plan status, for this section only
    stories: ["000-xxx"]                # optional: stories related to this section
  "1.2":
    status: "pending"
  "2":
    status: "pending"
---

<!--
HOW TO USE THIS TEMPLATE

File name:  AAAA-BBB-CCCC-description-of-plan.md
  AAAA  4-digit sequence number, incremented from the last plan (start at 0001)
  BBB   3-character random alphanumeric ID, to avoid sequence clashes
  CCCC  plan type: drft | dsgn | impl
  e.g.  0002-a3f-dsgn-description-of-plan.md
The front matter `id` is AAAA-BBB, and `type` matches CCCC.

Plan types:
  drft  Draft — an early, partial design. Fill what you can of Part 1.
  dsgn  Design Plan — the intended outcomes. Fill Part 1; delete Part 2.
  impl  Implementation Plan — the actual work, in phases. Fill Part 2; delete
        Part 1 and link the design under `links.specs`.

Front matter:
  - Status synonyms are accepted on input (draft→pending, approved→ready,
    done→complete, …) but write the primary name shown above.
  - `progress` keys must be quoted strings ("1.1", not 1.1): unquoted, YAML reads
    them as numbers, so "1.10" would silently overwrite "1.1".
  - `progress` keys match the numbered headings in Part 2 ("## Phase 1", "### 1.1").
    Design plans with no numbered sections can delete `progress`.
  - Delete optional fields and empty lists you don't use.

Sections marked (optional) appear in only some plans — delete them rather than
writing "N/A". Delete these HTML comments when done; they do not render.
-->

# Description of plan

## Summary

<!-- Required. 1–3 short paragraphs. Also known as: Goal, Problem, Overview, Context. -->

**Goal:** <One sentence: the outcome when this ships.>

**Problem:** <What is wrong or missing today, and who it affects.>

**Approach:** <One or two sentences on the chosen solution.>

---

# Part 1 — Design
<!-- Design Plans (dsgn) and Drafts (drft). -->

## Current State
<!-- (optional) Also known as: Research Findings, Root Cause, What Already Exists,
     Architecture Context. What exists today, with file:line references.
     A table of "piece | where | state" works well. -->

## Decisions
<!-- Also known as: Design Decisions, Resolved Decisions, Approach.
     One bullet per decision: the choice, then why. Keep rejected options short. -->

- **<Decision>** — <rationale>.

### Rejected alternatives
<!-- (optional) -->

- **<Alternative>** — <why not>.

## Design
<!-- Also known as: Architecture. One H3 per component or area — this is where
     component-specific sections go (Frontend, Backend, Data model, Wire format…). -->

### <Component / area>

<How it works. Interfaces, data shapes, sequence. Code or diagrams as needed.>

### Error Handling
<!-- (optional) Failure modes and what the user/system does in each. -->

### Edge Cases
<!-- (optional) -->

## Considerations
<!-- (optional) Include only the ones that apply. -->

- **Security:** <new exposure, auth, secrets, untrusted input — or "no new exposure">.
- **Performance:** <hot paths, extra network/IO, expected cost>.

## Testing
<!-- Required for designs. Also known as: Test Strategy, Verification.
     What is tested at which level (unit / integration / e2e / manual). -->

## Scope
<!-- Required. Also known as: Out of Scope, Non-goals. -->

**In scope:** <list>

**Out of scope:**

- <Thing deliberately not done> — <why, or where it is tracked>.

## Open Questions
<!-- (optional) Also known as: Questions to Resolve, Risks. Remove once resolved
     (move the answer into Decisions). -->

- [ ] <Question> — <owner / what unblocks it>

---

# Part 2 — Implementation
<!-- Implementation Plans (impl). Work is split into numbered phases and sections;
     each number is a key in the front matter `progress` map, and its status is
     updated there as work proceeds. -->

**Architecture:** <2–3 sentences: how the pieces fit, for someone who hasn't read the design.>

**Tech Stack:** <languages, frameworks, test runners involved>

## Constraints
<!-- Also known as: Global Constraints, Prerequisites, Design Principles.
     Rules every section must obey (conventions, things never to touch, setup steps). -->

- <Constraint>

## Files
<!-- Also known as: File Structure, Related Files, Files Modified. -->

| File | Change | Responsibility |
|---|---|---|
| `path/to/file.ts` | create / modify / delete | <what it does in this change> |

## Phase 1: <Name>
<!-- One H2 per phase, in execution order. Each numbered section should leave the
     build green and be committable on its own. -->

### 1.1: <Section name>

**Files:**
- Modify: `path/to/file.ts`
- Test: `path/to/file.test.ts` *(create)*

**Depends on:** none <!-- or another section number, e.g. "1.1" -->

**Interfaces:** <!-- (optional) what this section consumes from and produces for later sections -->
- Consumes: <…>
- Produces: <…>

- [ ] **Step 1: Write the failing test** — <what it asserts>
- [ ] **Step 2: Run it and confirm it fails** — `<test command>`
- [ ] **Step 3: Implement** — <the change; include literal code for non-obvious parts>
- [ ] **Step 4: Run it and confirm it passes** — `<test command>`
- [ ] **Step 5: Commit** — `<type(scope): message>`

**Validation:** `<command that proves this section is done>`

### 1.2: <Section name>

<!-- … repeat … -->

## Phase 2: <Name>
<!-- A phase with no sub-sections is tracked under its own number ("2"). -->

- [ ] Full test suite, typecheck and lint pass
- [ ] <Manual / end-to-end check in the running app>

**Validation:** `<command>`

## Acceptance Criteria
<!-- Required for implementation plans. Observable outcomes, not activities.
     Also known as: Checklist, Definition of Done. -->

- [ ] <Observable outcome>
- [ ] All existing tests pass; new tests cover <…>

## Self-Review
<!-- (optional) Map each design requirement to the section that delivers it. -->

| Requirement | Delivered by |
|---|---|
| <requirement from the linked design> | 1.1, Step 3 |

**Placeholder scan:** <none — every step carries real code/commands>
**Residual risk:** <what is not proven by these sections>

## Next Steps
<!-- (optional) Also known as: Follow-ups. What happens after this ships;
     record follow-up plans under `plans` with the "blocks" relation. -->

1. <Follow-up>
