I want to create a backend-only plan manager inside of the clued reop. The tool doesn't write plans, but rather works as a service that other tools can use to save, retrieve and monitor plans and issues (as stories).

# Plans

Plans come in two flavors: Design Plan, which is a brainstorming plan that describes the intended outcomes; and an Implementation Plan, which describes the actual work to be done, in phases where possible.

## Plan Format

Each Plan's contents and filename must adhere to a pre-defined pattern.

### File Name

File names should be formatted as the following:
    `AAAA-BBB-CCCC-DDDDD-DDD-DDDDDD.md`

Examples:
* 0002-a3f-DSGN-description-of-plan.md
* 0913-b33-IMPL-add-new-feature.md

Filename parts:

* AAAA (0002): The 4-digit sequence number of the plan. Where possible the sequence number should be incremented from the last-known sequence number. Otherwise, start with 0001.
* BBB (a3f): A three-character alphanumeric randomly generated ID, to prevent clashes of the sequence number.
* CCCC (`dsgn`): The type of Plan. Current values are `drft` (Draft), `dsgn` (Design) and `impl` (Implemenation)
* DD... (description-of-plan): A description of the plan, with spaces replaced by hyphens.

### Front Matter

Regardless of whether a Plan is a Design Plan or a Implementation Plan, it requires a standardized front matter section.

#### Example
File: 0002-a3f-dsgn-description-of-plan.md

```yaml
---
id: "0002-a3f"
title: "Description of plan"
type: "dsgn"
status: "ready"
priority: "1"
created: "2026-09-09T14:07:05.352Z"
updated: "2026-09-09T14:35:37.046Z"
links:
    repo:
        remote: "git@github.com:robbiebyrd/Project.git"
        local: "~/Projects/project"
    specs:
      - "./docs/design/design-doc-overview.md"
    web:
        jira: "https://jira.atlassian.net/browse/ACME-123"
    stories:
        - ["0001-abc", "included"]
        - ["0002-def", "depends"]
        - ["0003-fed", "blocks"]
    plans:
      - ["0031-34c", "blocks"]
      - ["0001-3sd", "depends"]
      - ["0002-4ad", "parent"]
progress:
  1.1:
    status: completed
    stories:
      - "0001-abc"
  1.2:
    status: completed
    stories:
      - "0002-def"
  1.3:
    status: completed
  2:
    status: completed
  3.1:
    status: completed
  3.2:
    status: completed
    stories:
      - "0003-fed"
  3.3:
    status: in_progress
  4:
    status: blocked
    stories:
      - "0003-fed"
  5:
    status: validated
---
```

#### Link Relationships

Both Plans and Stories have a "links" object that defines a plan or stories connections to other plans, stories or web locations. Currently, we accept the following types of links: Repo, Specs, Web and Stories.


##### Web Links (web)

Web Links are a collection of labels with a simple URL as the label's value.


##### Story Links (stories)

While Web Links are text, A Plan's Stories linking objects are nested. Each item is a tuple, with the left-hand item referencing the Story or Plan ID, and the right-hand value repsenting how the current Plan or Story is linked to the referenced it.


##### Repo

The Repo link allows the Plan to speecify the repo that the plan was crafted in. The local label should be the project's git root folder, and the remote label should be either a git@ or HTTP(S) URL to a git repository.


##### Plans

If a plan is related to another document or specification, include it here.


Valid values for linking are:

* Parent (parent): Denotes the referenced Plan or Story is owned by another Plan. In Plans, a Parent refers to another plan; for Stories, the parent is another story.
* Included (included): A Story can be linked to a Plan as its parent. Plans can be included in other plans when the original plan was split into smaller pieces.
* Depends On (depends): When a Plan or Story requires another Plan or Story to be finished before it can consider itself complete, then the Plan or Story Depends On that other Plan or Story. Plans can depend on other Plans; Stories can depend on other Stories.
* Blocks (blocks): When a Plan or Story blocks another Plan or Story and it is aware it is blocking during creation, it should note that with the blocks property. Whereas Depends On indicates another piece of work should be completed before this Story or Plan is complete, the Blocks property denotes that this Story or Plan is blocking another and that it can't be complete until this Story or Plan is complete.

#### Plan Types

Plans Types can be configured, but by default provide the following values:

* `drft` (Draft)
* `dsgn` (Design)
* `impl` (Implemenation)


#### Plan Status

Each plan has a status, retrieved from the default list below. A user should be able to change the available status via configuration. A new Plan is created with "draft" when no status is provided.

The statuses below are provided as a user-friendly label, then the system names for each status. The system name marked with a * is the primary system name, while others are synonyms that can be provided. Synonyms should be matched to the system name.

Default Plan Statuses:
* Pending (*pending, new, draft, todo)
* Validated (*validated, validate, valid)
* Ready (*ready, approve, approved)
* In Progress (*in_progress, start, started, active, working, progress, start, in-progress)
* Complete (*complete, done, completed, finish, finished, close, closed)
* Blocked (*blocked, block, hold, on_hold)
* Rejected (*rejected, reject, skip, skipped, cancel, cancelled, wontdo, wontfix)
* Archived (*archived, archive, shelve, shelved, park, parked)


##### Plan Status Progression

The Story Status should progress based on a defined workflow. This workflow should be configurable, with a default provided.

Default Plan Status Workflow:
* (new) -> Pending
* Pending -> Blocked
* Pending -> Rejected
* Pending -> Validated
* Validated -> Ready
* Validated -> Pending
* Validated -> Blocked
* Validated -> Rejeceted
* Validated -> Archived
* Ready -> In Progress
* Ready -> Validated
* Ready -> Blocked
* Ready -> Rejected
* Ready -> In Progress
* In Progress -> Blocked
* In Progress -> Rejected
* In Progress -> Complete
* In Progress -> Ready
* Blocked -> Pending
* Blocked -> Ready
* Blocked -> Rejected
* Rejected -> Blocked
* Rejected -> Archived
* Rejected -> Pending
* Rejected -> Ready
* Complete -> Archived
* Complete -> In Progress
* Archived -> Pending
* Archived -> Ready

The default status workflow should be as follows:
Pending -> Validated -> Ready -> In Progress -> Complete -> Archived

#### Plan Priorities

The actual label for each Priority should be configurable by the user, however, priority levels should be given in numbers.

Valid Plan Priorities:

* 0 (P0, Emergency)
* 1 (P1, Critical, Block)
* 2 (P2, Major, High)
* 3 (P3, Moderate, Medium)
* 4 (P4, Minor, Low)
* 5 (P5, Feature Request, Informational)

#### Plan Effort Levels

The effort of a plan is an optional property. It describes the amount of work necessary to complete a task along with its complexity and risk of unknowns.

Valid Effort Levels:

* XS (Extra Small, 2)
* S (Small, 3)
* M (Medium, 5)
* L (Large, 8)
* XL (Extra Large, 13)

#### Plan Status

For plans that have numbered "steps" or sections that an agent will work on in pieces, the progress of each numbered section or step should be recorded, along with a status.

Example:

```yaml
progress:
  1.1:
    status: completed # The status of the plan section, not the stories within
    stories: ["0001-abc"] # A list of any stories that are related to this plan step
  1.2:
    status: completed
    stories: ["0002-def"]
  1.3:
    status: completed
  2:
    status: completed
  3.1:
    status: completed
  3.2:
    status: completed
    stories: ["0003-fed"]
  3.3:
    status: in_progress
  4:
    status: blocked
    stories: ["0003-fed"]
  5:
    status: validated
```

### Plan Contents

A Plan's Contents should appear below the Front Matter and should be formatted as follows:

```markdown
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
```

#### Plan Content Templates

The Service should allow for creating Plan Templates, and save them using the

### Creation

When creating a Plan, we should adhere to a JSON Schema. Creating a ticket should follow this example JSON Schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.com/schemas/plan.schema.json",
  "title": "Plan",
  "description": "Input for creating a new Plan from the plan template. `frontMatter` becomes the YAML front matter; `body` fills the template's Markdown sections. The file is written as `<frontMatter.id>-<frontMatter.type>-<slug>.md`, e.g. `0002-a3f-dsgn-description-of-plan.md`. Enumerated values (types, statuses, priorities, effort) are the defaults and can be replaced by configuration.",
  "type": "object",
  "required": [
    "frontMatter",
    "body"
  ],
  "additionalProperties": false,
  "properties": {
    "slug": {
      "description": "The description part of the filename. If omitted, derive it from `frontMatter.title`: lowercase, with runs of non-alphanumeric characters replaced by a single hyphen.",
      "type": "string",
      "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$",
      "examples": [
        "description-of-plan"
      ]
    },
    "frontMatter": {
      "$ref": "#/$defs/frontMatter"
    },
    "body": {
      "$ref": "#/$defs/body"
    }
  },
  "allOf": [
    {
      "description": "Design plans fill Part 1 (Design) and need its required sections.",
      "if": {
        "required": [
          "frontMatter"
        ],
        "properties": {
          "frontMatter": {
            "required": [
              "type"
            ],
            "properties": {
              "type": {
                "const": "dsgn"
              }
            }
          }
        }
      },
      "then": {
        "properties": {
          "body": {
            "required": [
              "design"
            ],
            "properties": {
              "design": {
                "required": [
                  "decisions",
                  "components",
                  "testing",
                  "scope"
                ]
              }
            }
          }
        }
      }
    },
    {
      "description": "Drafts are partial designs: Part 1 is expected but nothing in it is required yet.",
      "if": {
        "required": [
          "frontMatter"
        ],
        "properties": {
          "frontMatter": {
            "required": [
              "type"
            ],
            "properties": {
              "type": {
                "const": "drft"
              }
            }
          }
        }
      },
      "then": {
        "properties": {
          "body": {
            "required": [
              "design"
            ]
          }
        }
      }
    },
    {
      "description": "Implementation plans fill Part 2 and must link the design they execute.",
      "if": {
        "required": [
          "frontMatter"
        ],
        "properties": {
          "frontMatter": {
            "required": [
              "type"
            ],
            "properties": {
              "type": {
                "const": "impl"
              }
            }
          }
        }
      },
      "then": {
        "properties": {
          "body": {
            "required": [
              "implementation"
            ]
          },
          "frontMatter": {
            "required": [
              "links"
            ],
            "properties": {
              "links": {
                "required": [
                  "specs"
                ]
              }
            }
          }
        }
      }
    }
  ],
  "$defs": {
    "planId": {
      "description": "AAAA-BBB: 4-digit sequence number and a 3-character random alphanumeric ID.",
      "type": "string",
      "pattern": "^[0-9]{4}-[a-z0-9]{3}$",
      "examples": [
        "0002-a3f"
      ]
    },
    "storyId": {
      "description": "AAAA-BBB: 4-digit sequence number and a 3-character random alphanumeric ID. Always the full id, never the bare sequence number.",
      "type": "string",
      "pattern": "^[0-9]{4}-[a-z0-9]{3}$",
      "examples": [
        "0001-abc"
      ]
    },
    "sectionNumber": {
      "description": "A phase number (\"2\") or a section within a phase (\"1.1\").",
      "type": "string",
      "pattern": "^[0-9]+(\\.[0-9]+)?$"
    },
    "planType": {
      "description": "drft = Draft, dsgn = Design, impl = Implementation.",
      "enum": [
        "drft",
        "dsgn",
        "impl"
      ]
    },
    "status": {
      "description": "Primary status names. On input, synonyms map to these: pending (new, draft, todo); validated (validate, valid); ready (approve, approved); in_progress (start, started, active, working, progress, in-progress); complete (done, completed, finish, finished, close, closed); blocked (block, hold, on_hold); rejected (reject, skip, skipped, cancel, cancelled, wontdo, wontfix); archived (archive, shelve, shelved, park, parked). Normalize synonyms before validating.",
      "enum": [
        "pending",
        "validated",
        "ready",
        "in_progress",
        "complete",
        "blocked",
        "rejected",
        "archived"
      ]
    },
    "planRelation": {
      "description": "parent: the referenced plan owns this one. included: this plan was split out of the referenced one. depends: the referenced plan must finish before this one can. blocks: the referenced plan can't finish until this one does.",
      "enum": [
        "parent",
        "included",
        "depends",
        "blocks"
      ]
    },
    "storyRelation": {
      "enum": [
        "included",
        "depends",
        "blocks"
      ]
    },
    "timestamp": {
      "description": "RFC 3339 UTC timestamp with millisecond precision, e.g. 2026-09-09T14:07:05.352Z. Other accepted input forms are normalized to this before validating.",
      "type": "string",
      "format": "date-time",
      "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\\.[0-9]{3}Z$"
    },
    "markdown": {
      "description": "Markdown text inserted as-is.",
      "type": "string",
      "minLength": 1
    },
    "markdownList": {
      "type": "array",
      "items": {
        "$ref": "#/$defs/markdown"
      }
    },
    "frontMatter": {
      "title": "Plan front matter",
      "type": "object",
      "required": [
        "title",
        "type",
        "status",
        "priority"
      ],
      "additionalProperties": false,
      "properties": {
        "id": {
          "$ref": "#/$defs/planId"
        },
        "title": {
          "type": "string",
          "minLength": 1,
          "description": "Also used as the document's H1 heading."
        },
        "type": {
          "$ref": "#/$defs/planType"
        },
        "status": {
          "$ref": "#/$defs/status",
          "default": "pending"
        },
        "priority": {
          "description": "0 Emergency · 1 Critical · 2 High · 3 Medium · 4 Low · 5 Informational. Written as a quoted string.",
          "enum": [
            "0",
            "1",
            "2",
            "3",
            "4",
            "5"
          ]
        },
        "effort": {
          "description": "Optional. XS (2) · S (3) · M (5) · L (8) · XL (13).",
          "enum": [
            "XS",
            "S",
            "M",
            "L",
            "XL"
          ]
        },
        "created": {
          "$ref": "#/$defs/timestamp"
        },
        "updated": {
          "$ref": "#/$defs/timestamp"
        },
        "completed": {
          "description": "Omitted until the plan is first complete; never an empty string.",
          "$ref": "#/$defs/timestamp"
        },
        "plans": {
          "description": "Related plans as [plan-id, relation] pairs.",
          "type": "array",
          "items": {
            "type": "array",
            "prefixItems": [
              {
                "$ref": "#/$defs/planId"
              },
              {
                "$ref": "#/$defs/planRelation"
              }
            ],
            "minItems": 2,
            "items": false
          }
        },
        "links": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "specs": {
              "description": "File paths of the designs/specs this plan builds on.",
              "type": "array",
              "minItems": 1,
              "items": {
                "anyOf": [
                  {
                    "type": "string",
                    "pattern": "^\\.{0,2}/?[^\\s]+\\.[A-Za-z0-9]+$",
                    "description": "Relative file path."
                  },
                  {
                    "type": "string",
                    "pattern": "^[A-Za-z0-9._~-]+@[A-Za-z0-9.-]+:[^\\s]+$",
                    "description": "File URL."
                  }
                ]
              }
            },
            "repo": {
              "description": "The git repository this plan's work happens in.",
              "type": "object",
              "additionalProperties": false,
              "minProperties": 1,
              "properties": {
                "remote": {
                  "description": "Git remote URL: scp-style (user@host:path, e.g. git@github.com:org/project.git) or a git://, ssh://, git+ssh://, http:// or https:// URL.",
                  "type": "string",
                  "anyOf": [
                    {
                      "pattern": "^[A-Za-z0-9._~-]+@[A-Za-z0-9.-]+:[^\\s]+$"
                    },
                    {
                      "pattern": "^(git|ssh|git\\+ssh|https?)://[^\\s]+$"
                    }
                  ],
                  "examples": [
                    "git@github.com:org/project.git",
                    "https://github.com/org/project.git"
                  ]
                },
                "local": {
                  "description": "Local folder holding a checkout of the repository. May be absolute, relative, or start with ~ (expanded by the tool).",
                  "type": "string",
                  "minLength": 1,
                  "pattern": "^\\S(.*\\S)?$",
                  "examples": [
                    "~/Projects/project"
                  ]
                }
              }
            },
            "web": {
              "description": "Named web links, e.g. { \"jira\": \"https://…\" }.",
              "type": "object",
              "additionalProperties": {
                "type": "string",
                "format": "uri",
                "pattern": "^https?://"
              }
            },
            "stories": {
              "description": "Related stories as [story-id, relation] pairs.",
              "type": "array",
              "items": {
                "type": "array",
                "prefixItems": [
                  {
                    "$ref": "#/$defs/storyId"
                  },
                  {
                    "$ref": "#/$defs/storyRelation"
                  }
                ],
                "minItems": 2,
                "items": false
              }
            }
          }
        },
        "progress": {
          "description": "Status of each numbered phase/section in Part 2. Keys must match `body.implementation` phase and section numbers, and must be written as quoted strings in YAML.",
          "type": "object",
          "propertyNames": {
            "$ref": "#/$defs/sectionNumber"
          },
          "additionalProperties": {
            "type": "object",
            "required": [
              "status"
            ],
            "additionalProperties": false,
            "properties": {
              "status": {
                "$ref": "#/$defs/status"
              },
              "stories": {
                "type": "array",
                "items": {
                  "$ref": "#/$defs/storyId"
                }
              }
            }
          }
        }
      },
      "allOf": [
        {
          "description": "A complete plan records when it completed.",
          "if": {
            "properties": {
              "status": {
                "const": "complete"
              }
            },
            "required": [
              "status"
            ]
          },
          "then": {
            "required": [
              "completed"
            ],
            "properties": {
              "completed": {
                "$ref": "#/$defs/timestamp"
              }
            }
          }
        }
      ]
    },
    "body": {
      "title": "Plan body",
      "type": "object",
      "required": [
        "summary"
      ],
      "additionalProperties": false,
      "properties": {
        "summary": {
          "type": "object",
          "required": [
            "goal",
            "problem"
          ],
          "additionalProperties": false,
          "properties": {
            "goal": {
              "$ref": "#/$defs/markdown",
              "description": "One sentence: the outcome when this ships."
            },
            "problem": {
              "$ref": "#/$defs/markdown",
              "description": "What is wrong or missing today, and who it affects."
            },
            "approach": {
              "$ref": "#/$defs/markdown",
              "description": "One or two sentences on the chosen solution."
            }
          }
        },
        "design": {
          "$ref": "#/$defs/design"
        },
        "implementation": {
          "$ref": "#/$defs/implementation"
        }
      }
    },
    "design": {
      "title": "Part 1 — Design",
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "currentState": {
          "$ref": "#/$defs/markdown",
          "description": "What exists today (Research Findings, Root Cause, What Already Exists)."
        },
        "decisions": {
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "object",
            "required": [
              "decision",
              "rationale"
            ],
            "additionalProperties": false,
            "properties": {
              "decision": {
                "$ref": "#/$defs/markdown"
              },
              "rationale": {
                "$ref": "#/$defs/markdown"
              }
            }
          }
        },
        "rejectedAlternatives": {
          "type": "array",
          "items": {
            "type": "object",
            "required": [
              "alternative",
              "reason"
            ],
            "additionalProperties": false,
            "properties": {
              "alternative": {
                "$ref": "#/$defs/markdown"
              },
              "reason": {
                "$ref": "#/$defs/markdown"
              }
            }
          }
        },
        "components": {
          "description": "One H3 per component or area under ## Design.",
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "object",
            "required": [
              "name",
              "content"
            ],
            "additionalProperties": false,
            "properties": {
              "name": {
                "type": "string",
                "minLength": 1
              },
              "content": {
                "$ref": "#/$defs/markdown"
              }
            }
          }
        },
        "errorHandling": {
          "$ref": "#/$defs/markdown"
        },
        "edgeCases": {
          "$ref": "#/$defs/markdown"
        },
        "considerations": {
          "type": "object",
          "additionalProperties": false,
          "minProperties": 1,
          "properties": {
            "security": {
              "$ref": "#/$defs/markdown"
            },
            "performance": {
              "$ref": "#/$defs/markdown"
            }
          }
        },
        "testing": {
          "$ref": "#/$defs/markdown",
          "description": "What is tested at which level."
        },
        "scope": {
          "type": "object",
          "required": [
            "outOfScope"
          ],
          "additionalProperties": false,
          "properties": {
            "inScope": {
              "$ref": "#/$defs/markdownList"
            },
            "outOfScope": {
              "type": "array",
              "items": {
                "type": "object",
                "required": [
                  "item"
                ],
                "additionalProperties": false,
                "properties": {
                  "item": {
                    "$ref": "#/$defs/markdown"
                  },
                  "reason": {
                    "$ref": "#/$defs/markdown"
                  }
                }
              }
            }
          }
        },
        "openQuestions": {
          "type": "array",
          "items": {
            "type": "object",
            "required": [
              "question"
            ],
            "additionalProperties": false,
            "properties": {
              "question": {
                "$ref": "#/$defs/markdown"
              },
              "owner": {
                "type": "string"
              }
            }
          }
        }
      }
    },
    "implementation": {
      "title": "Part 2 — Implementation",
      "type": "object",
      "required": [
        "phases",
        "acceptanceCriteria"
      ],
      "additionalProperties": false,
      "properties": {
        "architecture": {
          "$ref": "#/$defs/markdown"
        },
        "techStack": {
          "$ref": "#/$defs/markdown"
        },
        "constraints": {
          "$ref": "#/$defs/markdownList"
        },
        "files": {
          "type": "array",
          "items": {
            "type": "object",
            "required": [
              "path",
              "change"
            ],
            "additionalProperties": false,
            "properties": {
              "path": {
                "type": "string",
                "minLength": 1
              },
              "change": {
                "enum": [
                  "create",
                  "modify",
                  "delete"
                ]
              },
              "responsibility": {
                "$ref": "#/$defs/markdown"
              }
            }
          }
        },
        "phases": {
          "description": "Rendered as ## Phase N. A phase has either numbered sections or, if it is a single unit of work, its own checklist.",
          "type": "array",
          "minItems": 1,
          "items": {
            "$ref": "#/$defs/phase"
          }
        },
        "acceptanceCriteria": {
          "$ref": "#/$defs/markdownList",
          "minItems": 1
        },
        "selfReview": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "coverage": {
              "type": "array",
              "items": {
                "type": "object",
                "required": [
                  "requirement",
                  "deliveredBy"
                ],
                "additionalProperties": false,
                "properties": {
                  "requirement": {
                    "$ref": "#/$defs/markdown"
                  },
                  "deliveredBy": {
                    "type": "string",
                    "description": "Section number and step, e.g. \"1.1, Step 3\"."
                  }
                }
              }
            },
            "placeholderScan": {
              "$ref": "#/$defs/markdown"
            },
            "residualRisk": {
              "$ref": "#/$defs/markdown"
            }
          }
        },
        "nextSteps": {
          "$ref": "#/$defs/markdownList"
        }
      }
    },
    "phase": {
      "type": "object",
      "required": [
        "number",
        "name"
      ],
      "additionalProperties": false,
      "properties": {
        "number": {
          "type": "string",
          "pattern": "^[0-9]+$",
          "description": "Phase number; also its `progress` key."
        },
        "name": {
          "type": "string",
          "minLength": 1
        },
        "sections": {
          "type": "array",
          "minItems": 1,
          "items": {
            "$ref": "#/$defs/section"
          }
        },
        "checklist": {
          "$ref": "#/$defs/markdownList",
          "minItems": 1
        },
        "validation": {
          "type": "string",
          "description": "Command that proves the phase is done."
        }
      },
      "oneOf": [
        {
          "required": [
            "sections"
          ],
          "not": {
            "required": [
              "checklist"
            ]
          }
        },
        {
          "required": [
            "checklist"
          ],
          "not": {
            "required": [
              "sections"
            ]
          }
        }
      ]
    },
    "section": {
      "description": "Rendered as ### N.M. Its number is also its `progress` key and should start with its phase number.",
      "type": "object",
      "required": [
        "number",
        "name",
        "steps"
      ],
      "additionalProperties": false,
      "properties": {
        "number": {
          "type": "string",
          "pattern": "^[0-9]+\\.[0-9]+$"
        },
        "name": {
          "type": "string",
          "minLength": 1
        },
        "files": {
          "type": "object",
          "additionalProperties": false,
          "minProperties": 1,
          "properties": {
            "create": {
              "type": "array",
              "items": {
                "type": "string"
              }
            },
            "modify": {
              "type": "array",
              "items": {
                "type": "string"
              }
            },
            "delete": {
              "type": "array",
              "items": {
                "type": "string"
              }
            },
            "test": {
              "type": "array",
              "items": {
                "type": "string"
              }
            }
          }
        },
        "dependsOn": {
          "description": "Section or phase numbers within this plan.",
          "type": "array",
          "items": {
            "$ref": "#/$defs/sectionNumber"
          }
        },
        "interfaces": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "consumes": {
              "$ref": "#/$defs/markdown"
            },
            "produces": {
              "$ref": "#/$defs/markdown"
            }
          }
        },
        "steps": {
          "description": "Rendered as `- [ ] **Step N: <title>** — <detail>`. The usual sequence is: write the failing test, confirm it fails, implement, confirm it passes, commit.",
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "object",
            "required": [
              "title"
            ],
            "additionalProperties": false,
            "properties": {
              "title": {
                "type": "string",
                "minLength": 1
              },
              "detail": {
                "$ref": "#/$defs/markdown"
              },
              "command": {
                "type": "string",
                "description": "Shell command for this step, rendered in backticks."
              }
            }
          }
        },
        "validation": {
          "type": "string",
          "description": "Command that proves the section is done."
        }
      }
    }
  }
}
```

## Plan Service

A Go(lang) service should be created that works as a command line CRUD interface for creating Plans.


### Plan Service Interface

`:planIdentifier` is either a Plan ID (`0002-a3f`) or the path to a Plan file.

When requesting a plan for a read or update call, either the path to the Plan or the Plan ID should be accepted (noted as :planIdenttifier)

When creating a Plan, the Front Matter properties and the Content of the plan are provided. To make changes to Front Matter, use the appropriate Service calls.

Once the Plan is given an ID (e.g. AAAA-BBB), it should not change.

Rules that apply to every operation:

- **Every write is validated.** Before saving, the result is checked against the Plan JSON Schema. A write that fails is not saved.
- **Every write updates `updated`** to the current time and returns the updated Plan.
- **`id` and `created` are set once by `create`** and never change.
- **`updated` and `completed` are managed by the service** and have no setters.
- **Content and front matter are separate.** Content (the Markdown below the front matter) is changed only by `update`. Front matter is changed only by the setters below.
- **The content's H1 always matches `title`.**
- **Synonyms and labels are accepted on input** for status, priority and effort, and are stored as their primary value (e.g. `approved` → `ready`, `P1` → `1`, `Medium` → `M`).


#### Plans

| Operation | Inputs | Description | Example |
|---|---|---|---|
| create | :planInput | Create a new Plan from input matching the Creation JSON Schema. Assigns `id` (next sequence number + random 3-character ID), sets `created` and `updated`, defaults `status` to `pending`, fills the template from `body`, and writes `<id>-<type>-<slug>.md`. | `plan create --input plan.json` |
| get | :planIdentifier | Retrieve a Plan: its path, front matter and content. | `plan get 0002-a3f` |
| getFrontMatter | :planIdentifier | Retrieve only the front matter. | `plan get 0002-a3f --front-matter` |
| getContent | :planIdentifier | Retrieve only the content. | `plan get 0002-a3f --content` |
| list | :filter? | List Plans (path and front matter), optionally filtered by type, status, priority, or a linked plan or story. | `plan list --type impl --status in_progress` |
| update | :planIdentifier, :planContent | Replace the Plan's content. Front matter is unchanged apart from `updated`. | `plan update 0002-a3f --content body.md` |
| delete | :planIdentifier, :force? | Delete the Plan file. Fails if other Plans link to it unless forced. To retire a Plan, prefer `setStatus` with `archived`. | `plan delete 0002-a3f` |
| validate | :planIdentifier | Check a Plan without changing it. Checks the schema, that the filename matches `id` and `type`, and that every `progress` key matches a numbered heading in the content. Returns a list of problems. | `plan validate 0002-a3f` |

#### Templates

| Operation | Inputs | Description | Example |
|---|---|---|---|
| getTemplate | :templateId (optional) | Retrieve's a Plan Template (not the Front Matter); used for an agent to get the template they must follow. If no templateId is provided, use the default. | `plan get 0002-a3f` |
| updateTemplate | :templateId | Updates a Plan Template (not the Front Matter). | `plan get 0002-a3f` |
| deleteTemplate | :templateId | Delete a Plan Template (not the Front Matter). | `plan get 0002-a3f` |
| createTemplate | :templateId | Create a new Plan Template (not the Front Matter) with the given ID. | `plan get 0002-a3f` |


#### Front Matter: Fields

| Operation | Inputs | Description | Example |
|---|---|---|---|
| setTitle | :planIdentifier, :title, :renameFile? | Change `title` and the content's H1. The filename keeps its slug unless `renameFile` is set. | `plan set-title 0002-a3f "New title"` |
| setType | :planIdentifier, :planType | Change `type` (e.g. `drft` → `dsgn`) and rename the file's type part. `id` is unchanged. | `plan set-type 0002-a3f dsgn` |
| setStatus | :planIdentifier, :status, :force? | Move the Plan to a new status. Rejects moves the workflow doesn't allow unless forced. Moving to `complete` sets `completed`; moving away from `complete` does not clear it, and moving to `complete` again overwrites it. | `plan set-status 0002-a3f approved` |
| getTransitions | :planIdentifier | List the statuses the Plan can move to from its current status. | `plan transitions 0002-a3f` |
| setPriority | :planIdentifier, :priority | Set `priority` from a number (`0`–`5`) or label (`P1`, `Critical`). | `plan set-priority 0002-a3f P1` |
| setEffort | :planIdentifier, :effort | Set `effort` from a size (`XS`–`XL`), label (`Medium`) or points (`5`). | `plan set-effort 0002-a3f M` |
| clearEffort | :planIdentifier | Remove `effort`. | `plan clear-effort 0002-a3f` |
| patchFrontMatter | :planIdentifier, :frontMatterPatch | Set several fields in one write, following the same rules as the individual setters. Changes to `id`, `created`, `updated` or `completed` are rejected. | `plan patch 0002-a3f --priority 1 --effort L` |


#### Front Matter: Links

| Operation | Inputs | Description | Example |
|---|---|---|---|
| addPlanLink | :planIdentifier, :targetPlanId, :relation | Add `[targetPlanId, relation]` to `plans`. Relation: `parent`, `included`, `depends` or `blocks`. The target Plan must exist; duplicates are ignored. | `plan link 0002-a3f 0031-34c blocks` |
| removePlanLink | :planIdentifier, :targetPlanId, :relation? | Remove a plan link. Without a relation, removes every link to that Plan. | `plan unlink 0002-a3f 0031-34c` |
| addStoryLink | :planIdentifier, :storyId, :relation | Add `[storyId, relation]` to `links.stories`. Relation: `included`, `depends` or `blocks`. | `plan link-story 0002-a3f 0001-abc included` |
| removeStoryLink | :planIdentifier, :storyId, :relation? | Remove a story link. Without a relation, removes every link to that story. | `plan unlink-story 0002-a3f 0001-abc` |
| addSpec | :planIdentifier, :spec | Add a spec path to `links.specs`. | `plan add-spec 0002-a3f ./docs/design/overview.md` |
| removeSpec | :planIdentifier, :spec | Remove a spec from `links.specs`. | `plan remove-spec 0002-a3f ./docs/design/overview.md` |
| setWebLink | :planIdentifier, :label, :url | Add a named link to `links.web`, replacing any existing link with that label. | `plan set-web 0002-a3f jira https://jira.atlassian.net/browse/ACME-123` |
| removeWebLink | :planIdentifier, :label | Remove a named web link. | `plan remove-web 0002-a3f jira` |
| setRepo | :planIdentifier, :remote?, :local? | Set `links.repo.remote`, `links.repo.local`, or both. | `plan set-repo 0002-a3f --remote git@github.com:org/p.git --local ~/Projects/p` |
| clearRepo | :planIdentifier | Remove `links.repo`. | `plan clear-repo 0002-a3f` |


#### Front Matter: Progress

| Operation | Inputs | Description | Example |
|---|---|---|---|
| getProgress | :planIdentifier | Retrieve the status of every numbered phase and section. | `plan progress 0002-a3f` |
| setProgress | :planIdentifier, :section, :status | Set the status of a phase or section (`"2"`, `"1.1"`). The section must exist as a numbered heading in the content. | `plan set-progress 0002-a3f 1.1 in_progress` |
| addProgressStory | :planIdentifier, :section, :storyId | Link a story to a phase or section. | `plan progress-story 0002-a3f 1.1 0001-abc` |
| removeProgressStory | :planIdentifier, :section, :storyId | Unlink a story from a phase or section. | `plan progress-unstory 0002-a3f 1.1 0001-abc` |
| removeProgress | :planIdentifier, :section | Remove a phase or section's progress entry. | `plan remove-progress 0002-a3f 1.1` |


#### Errors

| Error | Raised when |
|---|---|
| NotFound | No Plan matches the identifier, or a linked Plan doesn't exist. |
| ValidationError | The result of a write fails the JSON Schema. Includes the list of problems. |
| InvalidTransition | The workflow doesn't allow the requested status change. |
| ImmutableField | A write tries to change `id`, `created`, `updated` or `completed`. |
| UnknownSection | A progress operation names a section that isn't a numbered heading in the content. |
| LinkedPlan | `delete` is called on a Plan that other Plans link to, without `force`. |

### Plan Service Storage

The Plan Service should allow for storing the Plans in one or more places simultaneously, with each copy getting updates.

The Plan Storage service should allow pluggable modules to allow any type of data storage. The plug-ins should adhere to the interface above, and the Plan Storage Service should fan out changes and keep all copies up to date.

The Service should also allow an option to force sync Plans from one storage plugin to the other. For instance, if a user has a PostgreSQL database with Plans stored on it, the user should be able to download a copy of all the plans in the DB to Markdown files on the local machine, or similarly, sync a PostgreSQL database with a MongoDB database. The user should be allowed to set an override flag that will either error on conflicts, skip conflicts and report them, or overwrite any conflicts from the source storage plugin.

#### File-based storage

By default, plans should be stored as Markdown files in `docs/plans` in the working directory where the service was called. All files except Archived Plans should live in that folder; archived plans should go in `docs/plans/archive` once they are archived.

File-based storage is the only enabled storage plugin enabled by default. It can only be disabled when another plugin is enabled.

#### Database storage

A user should be able to select from one of several relational database plugins. We will be creating MySQL/MariaDB, PostgreSQL, Clickhouse and SQLite plugins.

#### Document Database Storage

A user should be able to save Plans into a document-oriented database. We will be creating MongoDB and Firestore plugins.

### Plan Service Entrypoints

The Plan Service will support multiple request entrypoints, depending on the need. The same functions available in the interface should be availble to all entrypoints. All entrypoints will accept the same function names, inputs and representatively return the same data.

* **CLI**: The primary entrypoint for the application should be a cli script. This will primarily be used by AI Agents, Skills and Tools to interact with plans.
* **HTTP/REST**: The service should provide a web server with HTTP/REST endpoints for the service interface.
* **WebSockets**: The service should provide a WebSocket server that allows commands to be sent, as well as subscribe to a stream of updates from one or more plans.
* **MCP/Streamable HTTP**: Similar to the WebSockets entrypoint, the service should provide an MCP server and tools for interacting with Plans, as well as subscribe to a stream of updates from one or more plans.

# Stories

Additionally, this service will allow the creation, tracking and management of Stories. Stories are exectuable, tangible chunks of work that are created from a plan. It is not yet necessary to stub any code out for the Stories feature, but it is worth noting that architecturally they will be equals.
