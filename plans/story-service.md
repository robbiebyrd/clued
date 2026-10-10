I want to create a backend-only story/issue tracker. The tool doesn't write plans, but rather works as a service that other tools can use to save, retrieve and monitor plans and issues (as stories).

# Stories

Stories are executable, tangible chunks of work, usually created from a Plan. Where a Plan describes an outcome and the phases that reach it, a Story is one piece of that work small enough to be picked up, carried out and verified on its own. Stories come in five types (bug, feature, improvement, chore and task) that share one format; the type only changes the filename and how the Story is filtered.

## Story Format

Each Story's contents and filename must adhere to a pre-defined pattern.

### File Name

File names should be formatted as the following:
    `AAAA-BBB-CCCC-DDDDD-DDD-DDDDDD.md`

Examples:
* 0002-a3f-bugs-description-of-bug.md
* 0913-b33-feat-add-new-feature.md

Filename parts:

* AAAA (0002): The 4-digit sequence number of the story. Where possible the sequence number should be incremented from the last-known sequence number. Otherwise, start with 0001.
* BBB (a3f): A three-character alphanumeric randomly generated ID, to prevent clashes of the sequence number.
* CCCC (`bugs`): The type of Story. See Story Types for values.
* DD... (description-of-bug): A description of the Story, with spaces replaced by hyphens.

### Front Matter

Regardless of its type, every Story requires a standardized front matter section.

#### Example
File: 0913-b33-bugs-fix-new-bug-in-application.md

```yaml
id: 0003-ff1
title: Unspecified Bug Fix
purpose: Makes a code change to the repo to fix a known issue
type: bugs
status: in_progress
priority: 1
effort: L
created: "2026-09-09T14:07:05.352Z"
updated: "2026-09-09T14:35:37.046Z"
started: "2026-09-09T14:20:11.418Z"
links:
    web:
        jira: "https://jira.atlassian.net/browse/ACME-3455"
    repo:
        remote: "git@github.com:robbiebyrd/Project.git"
        local: "~/Projects/project"
        pull_request: "https://github.com/robbiebyrd/clued/pull/1"
        files:
            - "src/app/handler.ts"
            - "src/app/handler.test.ts"
    specs:
        - "./docs/reviews/review-2026-09-09.md"
    plans:
        - ["0023-de3", "included", ["1.1", "1.2"]]
        - ["0005-a10", "depends"]
        - ["0007-f30", "blocks"]
    stories:
        - ["0001-abc", "included"]
        - ["0002-def", "depends"]
        - ["0003-fed", "blocks"]
progress:
  1.1:
    status: completed
  1.2:
    status: completed
  1.3:
    status: completed
  2:
    status: completed
  3.1:
    status: completed
  3.2:
    status: completed
  3.3:
    status: in_progress
  4:
    status: blocked
  5:
    status: validated
```


#### Story Status

While Story Statuses are unique from Plan Statuses and can be configured individually, the default values for Story Statuses should match the values for Plan Statuses.

##### Story Status Progression

While Story Status Progressions are unique from Plan Status Progressions and can be configured individually, the default values for Story Status Progressions should match the values for Plan Status Progressions.

##### Archiving

Archiving is both a status and a location. When a Story moves to Archived, the service sets `status: archived` and moves the file into the `archive` subfolder of the stories folder (`docs/stories/archive` by default), keeping its filename. When an archived Story moves to any other status, the service moves the file back. The service reads both folders, so an archived Story is still found by id or path. `archive` is the only subfolder the service uses.

#### Story Priorities

While Story Priorities are unique from Plan Priorities and can be configured individually, the default values for Story Priorities should match the values for Plan Priorities.

Priority labels and synonyms (`P1`, `p1`, `Critical`) are matched case-insensitively on input and stored as the number.

#### Story Effort Levels

While Story Effort Levels are unique from Plan Effort Levels and can be configured individually, the default values for Story Effort Levels should match the values for Plan Effort Levels.


#### Story Types

Story Types can be configured, but by default provide the values below. Each type is given as a user-friendly label, then its system names. The system name marked with a * is the primary system name and is what is stored; the others are synonyms accepted on input. Synonyms are matched case-insensitively and normalized to the primary system name before validating.

Valid Story Types:

* Bug         (*bugs, bug, fix)
* Feature     (*feat, feature)
* Improvement (*impr, improvement, refactor)
* Chore       (*chor, chore)
* Task        (*task)

#### Dates

All date/time fields are RFC 3339 timestamps in UTC with millisecond precision, written as quoted YAML strings, for example `"2026-09-09T14:07:05.352Z"`. On input the service also accepts RFC 3339 without milliseconds, `YYYY-MM-DD HH:MM:SS UTC` and date-only `YYYY-MM-DD`, and normalizes them to the canonical form before saving.

* `created`: set once when the Story is created.
* `updated`: set on every write.
* `started`: set when the Story moves to In Progress. Moving away from In Progress does not remove it; moving to In Progress again overwrites it.
* `completed`: set when the Story moves to Complete. Moving away from Complete does not remove it; moving to Complete again overwrites it.

`started` and `completed` are omitted from the front matter until the Story first reaches the corresponding status. They are never written as empty strings. `status` is the source of truth for whether a Story is in progress or complete; `started` and `completed` only record when that last happened.

#### Link Relationships

Stories have a "links" object that defines a plan or stories connections to other plans, stories or web locations. Currently, we accept the following types of links: Repo, Specs, Plans, Web and Stories.

Where not specified, the same specification for a linking field can be borrowed from the Plan Service.

Every reference to a Plan or Story in links is the full id (`0003-ff1`), never the bare sequence number (`0003`).

##### Web Links (web)

Web Links are a collection of labels with a simple URL as the label's value, for example the issue tracker ticket the Story mirrors.

##### Specs Links (specs)

A list of paths or URLs to documents the Story came from or must satisfy: review reports, changelog entries, design documents. If a Story is related to another document or specification, include it here.

##### Repo Links (repo)

As in the Plan Service, with two additions: `pull_request`, the URL of the pull request that delivers the Story, and `files`, an optional list of repository-relative paths the Story touches.

##### Plans Links (plans)

Each item is a tuple: the Plan id, the relationship, and optionally a list of the Plan's section keys that the Story implements. Section keys are the keys of the Plan's `progress` map (`"2"`, `"1.1"`), not free text. Valid relationships from a Story to a Plan are `included`, `depends` and `blocks`; a Story's parent is always another Story, never a Plan, so `parent` is not used here.

##### Stories Links (stories)

Each item is a tuple: the other Story's id and the relationship. Valid relationships from a Story to a Story are `parent`, `included`, `depends` and `blocks`.

##### Relationship values

* Parent (parent): Denotes the referenced Plan or Story is owned by another Plan. In Plans, a Parent refers to another plan; for Stories, the parent is another story.
* Included (included): A Story can be linked to a Plan as its parent. Plans can be included in other plans when the original plan was split into smaller pieces.
* Depends On (depends): When a Plan or Story requires another Plan or Story to be finished before it can consider itself complete, then the Plan or Story Depends On that other Plan or Story. Plans can depend on other Plans; Stories can depend on other Stories.
* Blocks (blocks): When a Plan or Story blocks another Plan or Story and it is aware it is blocking during creation, it should note that with the blocks property. Whereas Depends On indicates another piece of work should be completed before this Story or Plan is complete, the Blocks property denotes that this Story or Plan is blocking another and that it can't be complete until this Story or Plan is complete.

#### Progress

A Story's content lists its work as numbered steps under its `## Steps` section, written as numbered headings the same way a Plan's sections are (`### 1: <Step name>`, `#### 1.1: <Sub-step name>`, `##### 1.1.1: <Sub-step name>`). Each step number, at any depth, is a key in the front matter `progress` map, and its status is updated there as work proceeds. Step statuses use the Story Status values and synonyms.

The `progress` map holds only steps that exist as numbered headings in the content. A step's status describes that step alone, with one rule linking the two: when the last remaining step moves to Complete, the Story is In Progress and every Acceptance Criterion is checked, the service moves the Story to Complete as if `setStatus` had been called, so the workflow rules apply and `completed` is set. In any other case the Story is left unchanged. Changing the Story's `status` never changes its steps.

##### Acceptance Criteria gate

A Story cannot move to Complete while any item under `## Acceptance Criteria` is unchecked. `setStatus` rejects the move with `IncompleteCriteria`, listing the unchecked items, and `force` does not override it. An item counts as checked when marked `[x]` or `[~]` (not applicable).

Example:

```yaml
progress:
  1:
    status: complete
  2.1:
    status: complete
  2.2:
    status: in_progress
  3:
    status: pending
```

### Story Contents

A Story's Contents appear below the Front Matter and are formatted as follows. Section names in the corpus of existing stories varied; the "Also known as" names are the variants a reader should recognize as the same section.

```markdown
# <Story title>
<!-- Always matches `title` in the front matter. -->

## Problem Statement
<!-- Required. 1–3 short paragraphs. Also known as: Description, Problem, Context.
     What is wrong or missing, who it affects, and why this Story exists.
     Stories raised from a review or audit carry the finding as bold labels. -->

**Issue:** <!-- (optional) the finding, with file:line -->
**Impact:** <!-- (optional) what goes wrong if it is not fixed -->

## Steps
<!-- Required. The actionable work, in execution order. Each step is a numbered heading
     and its number is a key in the front matter `progress` map. Nest sub-steps (1.1,
     1.1.1) when a step has distinct parts; a step with no sub-steps is tracked under
     its own number. Under each heading, one or two sentences: the task to do, or what
     is true when it is done. Include literal commands or code for non-obvious parts. -->

### 1: <Step name>
<Task to complete, or expected outcome.>

### 2: <Step name>

#### 2.1: <Sub-step name>
<Task to complete, or expected outcome.>

#### 2.2: <Sub-step name>
<Task to complete, or expected outcome.>

## Acceptance Criteria
<!-- Required. Observable outcomes, not activities. Every item must be checked (`[x]`,
     or `[~]` for not applicable) before the Story can move to Complete.
     Prefix `VERIFY:` for a command whose exit status proves the item, and `[MANUAL]`
     for a check a person performs. -->

- [ ] <Observable outcome>
- [ ] VERIFY: `<command>`
- [ ] [MANUAL] <check a person performs>

## Files
<!-- (optional) Also known as: Context Files, Location. Repository-relative paths the
     Story touches, one per line with what changes. Mirrors `links.repo.files`. -->

- `path/to/file.ts` - <what changes>

## Proof
<!-- (optional) One checked item per quality dimension, with the evidence in parentheses.
     `[x]` proven, `[~]` not applicable with the reason. Dimensions: completeness,
     feature-availability, robustness, resilience, security, defense-in-depth,
     input-validation, thread-safety, configurability. -->

- [ ] [completeness] Completeness (<evidence>)
- [ ] [security] Security (<evidence, or why not applicable>)

## QA
<!-- (optional) How the finished work was verified end to end, in prose: test counts,
     type checks, manual or on-device checks and who performed them. -->

## Work Log
<!-- Required, append-only. One H3 per entry, newest last, written by the service and by
     whoever does the work: status changes, findings, root causes, verification results. -->

### <timestamp> - <what happened, and what was learned>
```

### Creation

When creating a Story, we should adhere to a JSON Schema. Creating a Story should follow this example JSON Schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.com/schemas/story.schema.json",
  "title": "Story",
  "description": "Input for creating a new Story from the story template. `frontMatter` becomes the YAML front matter; `body` fills the template's Markdown sections. The file is written as `<frontMatter.id>-<frontMatter.type>-<slug>.md`, e.g. `0002-a3f-bugs-description-of-bug.md`. Enumerated values (types, statuses, priorities, effort) are the defaults and can be replaced by configuration. Synonyms are normalized to these primary values before validating.",
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
        "description-of-bug"
      ]
    },
    "frontMatter": {
      "$ref": "#/$defs/frontMatter"
    },
    "body": {
      "$ref": "#/$defs/body"
    }
  },
  "$defs": {
    "storyId": {
      "description": "AAAA-BBB: 4-digit sequence number and a 3-character random alphanumeric ID. Always the full id, never the bare sequence number.",
      "type": "string",
      "pattern": "^[0-9]{4}-[a-z0-9]{3}$",
      "examples": [
        "0003-ff1"
      ]
    },
    "planId": {
      "description": "A Plan's id, in the same AAAA-BBB form.",
      "type": "string",
      "pattern": "^[0-9]{4}-[a-z0-9]{3}$",
      "examples": [
        "0023-de3"
      ]
    },
    "stepNumber": {
      "description": "A step number at any depth: \"2\", \"2.1\", \"2.1.3\". Also the step's `progress` key.",
      "type": "string",
      "pattern": "^[0-9]+(\\.[0-9]+)*$"
    },
    "planSectionNumber": {
      "description": "A Plan phase number (\"2\") or a section within a phase (\"1.1\"), as used in the Plan's `progress` map.",
      "type": "string",
      "pattern": "^[0-9]+(\\.[0-9]+)?$"
    },
    "storyType": {
      "description": "Primary type names. On input, synonyms map to these: bugs (bug, fix); feat (feature); impr (improvement, refactor); chor (chore); task. Matched case-insensitively.",
      "enum": [
        "bugs",
        "feat",
        "impr",
        "chor",
        "task"
      ]
    },
    "status": {
      "description": "Primary status names, shared with Plans. On input, synonyms map to these: pending (new, draft, todo); validated (validate, valid); ready (approve, approved); in_progress (start, started, active, working, progress, in-progress); complete (done, completed, finish, finished, close, closed); blocked (block, hold, on_hold); rejected (reject, skip, skipped, cancel, cancelled, wontdo, wontfix); archived (archive, shelve, shelved, park, parked).",
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
      "description": "How this Story relates to the referenced Plan. included: the Plan owns this Story. depends: the Plan must finish before this Story can. blocks: the Plan can't finish until this Story does.",
      "enum": [
        "included",
        "depends",
        "blocks"
      ]
    },
    "storyRelation": {
      "description": "How this Story relates to the referenced Story. parent: the referenced Story owns this one. included: this Story was split out of the referenced one. depends: the referenced Story must finish before this one can. blocks: the referenced Story can't finish until this one does.",
      "enum": [
        "parent",
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
    "repoPath": {
      "description": "Repository-relative file path.",
      "type": "string",
      "minLength": 1,
      "pattern": "^[^\\s/][^\\s]*$"
    },
    "markdown": {
      "description": "Markdown text inserted as-is.",
      "type": "string",
      "minLength": 1
    },
    "frontMatter": {
      "title": "Story front matter",
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
          "$ref": "#/$defs/storyId"
        },
        "title": {
          "type": "string",
          "minLength": 1,
          "description": "Also used as the document's H1 heading."
        },
        "purpose": {
          "type": "string",
          "minLength": 1,
          "description": "One sentence: why this Story exists."
        },
        "type": {
          "$ref": "#/$defs/storyType"
        },
        "status": {
          "$ref": "#/$defs/status",
          "default": "pending"
        },
        "priority": {
          "description": "0 Emergency \u00b7 1 Critical \u00b7 2 High \u00b7 3 Medium \u00b7 4 Low \u00b7 5 Informational. Written as a quoted string. Labels (P1, Critical) are matched case-insensitively and stored as the number.",
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
          "description": "Optional. XS (2) \u00b7 S (3) \u00b7 M (5) \u00b7 L (8) \u00b7 XL (13).",
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
        "started": {
          "description": "Omitted until the Story first moves to in_progress; never an empty string. Kept when the Story leaves in_progress, overwritten when it returns.",
          "$ref": "#/$defs/timestamp"
        },
        "completed": {
          "description": "Omitted until the Story first moves to complete; never an empty string. Kept when the Story leaves complete, overwritten when it returns.",
          "$ref": "#/$defs/timestamp"
        },
        "links": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "web": {
              "description": "Named web links, e.g. { \"jira\": \"https://\u2026\" }.",
              "type": "object",
              "additionalProperties": {
                "type": "string",
                "format": "uri",
                "pattern": "^https?://"
              }
            },
            "repo": {
              "description": "The git repository this Story's work happens in.",
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
                },
                "pull_request": {
                  "description": "URL of the pull request that delivers this Story.",
                  "type": "string",
                  "format": "uri",
                  "pattern": "^https?://"
                },
                "files": {
                  "description": "Repository-relative paths the Story touches. Mirrors the body's Files section.",
                  "type": "array",
                  "minItems": 1,
                  "uniqueItems": true,
                  "items": {
                    "$ref": "#/$defs/repoPath"
                  }
                }
              }
            },
            "specs": {
              "description": "Paths or URLs of documents the Story came from or must satisfy: review reports, changelog entries, design documents.",
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
                    "format": "uri",
                    "pattern": "^https?://",
                    "description": "Web URL."
                  }
                ]
              }
            },
            "plans": {
              "description": "Related Plans as [plan-id, relation] pairs, optionally with a third element listing the Plan section numbers this Story implements.",
              "type": "array",
              "items": {
                "type": "array",
                "anyOf": [
                  {
                    "description": "[plan-id, relation]",
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
                  },
                  {
                    "description": "[plan-id, relation, [section numbers this Story implements]]",
                    "prefixItems": [
                      {
                        "$ref": "#/$defs/planId"
                      },
                      {
                        "$ref": "#/$defs/planRelation"
                      },
                      {
                        "type": "array",
                        "minItems": 1,
                        "uniqueItems": true,
                        "items": {
                          "$ref": "#/$defs/planSectionNumber"
                        }
                      }
                    ],
                    "minItems": 3,
                    "items": false
                  }
                ]
              }
            },
            "stories": {
              "description": "Related Stories as [story-id, relation] pairs.",
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
          "description": "Status of each numbered step in the body's Steps section. Keys must match step numbers at any depth and must be written as quoted strings in YAML.",
          "type": "object",
          "propertyNames": {
            "$ref": "#/$defs/stepNumber"
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
              }
            }
          }
        }
      },
      "allOf": [
        {
          "description": "A Story in progress records when it started.",
          "if": {
            "properties": {
              "status": {
                "const": "in_progress"
              }
            },
            "required": [
              "status"
            ]
          },
          "then": {
            "required": [
              "started"
            ],
            "properties": {
              "started": {
                "$ref": "#/$defs/timestamp"
              }
            }
          }
        },
        {
          "description": "A complete Story records when it completed.",
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
      "title": "Story body",
      "type": "object",
      "required": [
        "problemStatement",
        "steps",
        "acceptanceCriteria"
      ],
      "additionalProperties": false,
      "properties": {
        "problemStatement": {
          "type": "object",
          "required": [
            "statement"
          ],
          "additionalProperties": false,
          "properties": {
            "statement": {
              "$ref": "#/$defs/markdown",
              "description": "1\u20133 short paragraphs: what is wrong or missing, who it affects, and why this Story exists."
            },
            "issue": {
              "$ref": "#/$defs/markdown",
              "description": "For Stories raised from a review: the finding, with file:line."
            },
            "impact": {
              "$ref": "#/$defs/markdown",
              "description": "What goes wrong if it is not fixed."
            }
          }
        },
        "steps": {
          "description": "Rendered as numbered headings under ## Steps, in execution order. Top-level step numbers are \"1\", \"2\", \u2026; nested steps extend their parent's number.",
          "type": "array",
          "minItems": 1,
          "items": {
            "$ref": "#/$defs/step"
          }
        },
        "acceptanceCriteria": {
          "description": "Rendered as a checklist. Every item must be done or not applicable before the Story can move to complete.",
          "type": "array",
          "minItems": 1,
          "items": {
            "$ref": "#/$defs/criterion"
          }
        },
        "files": {
          "description": "Rendered as a list under ## Files.",
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "object",
            "required": [
              "path"
            ],
            "additionalProperties": false,
            "properties": {
              "path": {
                "$ref": "#/$defs/repoPath"
              },
              "change": {
                "$ref": "#/$defs/markdown",
                "description": "What changes in this file."
              }
            }
          }
        },
        "proof": {
          "description": "Rendered as a checklist under ## Proof, one item per quality dimension.",
          "type": "array",
          "minItems": 1,
          "items": {
            "$ref": "#/$defs/proofItem"
          }
        },
        "qa": {
          "$ref": "#/$defs/markdown",
          "description": "How the finished work was verified end to end: test counts, type checks, manual or on-device checks and who performed them."
        },
        "workLog": {
          "description": "Rendered as one ### <timestamp> - <entry> per item, oldest first. Append-only.",
          "type": "array",
          "items": {
            "type": "object",
            "required": [
              "timestamp",
              "entry"
            ],
            "additionalProperties": false,
            "properties": {
              "timestamp": {
                "$ref": "#/$defs/timestamp"
              },
              "entry": {
                "$ref": "#/$defs/markdown"
              }
            }
          }
        }
      }
    },
    "step": {
      "description": "Rendered as a numbered heading (### for depth 1, #### for depth 2, and so on) followed by its description. Its number is also its `progress` key.",
      "type": "object",
      "required": [
        "number",
        "name",
        "description"
      ],
      "additionalProperties": false,
      "properties": {
        "number": {
          "$ref": "#/$defs/stepNumber"
        },
        "name": {
          "type": "string",
          "minLength": 1
        },
        "description": {
          "$ref": "#/$defs/markdown",
          "description": "One or two sentences: the task to complete, or what is true when it is done. Include literal commands or code for non-obvious parts."
        },
        "steps": {
          "description": "Sub-steps. Each number must extend this step's number (\"2\" \u2192 \"2.1\").",
          "type": "array",
          "minItems": 1,
          "items": {
            "$ref": "#/$defs/step"
          }
        }
      }
    },
    "criterion": {
      "description": "Rendered as `- [ ] <text>`, `- [x] <text>` or `- [~] <text>`, with `VERIFY: ` or `[MANUAL] ` prefixed according to `kind`.",
      "type": "object",
      "required": [
        "text"
      ],
      "additionalProperties": false,
      "properties": {
        "text": {
          "$ref": "#/$defs/markdown",
          "description": "An observable outcome, not an activity. For kind `verify`, the command whose exit status proves it."
        },
        "kind": {
          "description": "verify: a command proves the item. manual: a person performs the check.",
          "enum": [
            "verify",
            "manual"
          ]
        },
        "state": {
          "description": "open renders as [ ], done as [x], not_applicable as [~].",
          "enum": [
            "open",
            "done",
            "not_applicable"
          ],
          "default": "open"
        }
      }
    },
    "proofItem": {
      "description": "Rendered as `- [x] [<dimension>] <Dimension> (<evidence>)`; [~] when not applicable, [ ] when still open.",
      "type": "object",
      "required": [
        "dimension",
        "state"
      ],
      "additionalProperties": false,
      "properties": {
        "dimension": {
          "enum": [
            "completeness",
            "feature-availability",
            "robustness",
            "resilience",
            "security",
            "defense-in-depth",
            "input-validation",
            "thread-safety",
            "configurability"
          ]
        },
        "state": {
          "enum": [
            "open",
            "proven",
            "not_applicable"
          ]
        },
        "evidence": {
          "$ref": "#/$defs/markdown",
          "description": "What proves the dimension, or why it does not apply."
        }
      }
    }
  }
}
```

#### Story Content Templates

The Service allows Story Templates to be created, updated and deleted under a template id, using the Templates operations in the Story Service Interface. The template above is the default and is used when `create` is called without a template id. A template may rename or omit the optional sections (Files, Proof, QA) and add sections of its own, but it must keep the Problem Statement, Steps, Acceptance Criteria and Work Log sections, because the service reads those.

## Story Service

The Go(lang) service for plans should be extended to include a command line CRUD interface for creating Stories.

**NOTE**: We need to re-moduralize the application to support the two current types (Plan, Story) and perhaps more in the future.


### Story Service Interface

`:storyIdentifier` is either a Story ID (`0002-a3f`) or the path to a Story file.

When requesting a Story for a read or update call, either the path to the Story or the Story ID should be accepted (noted as :storyIdenttifier)

When creating a Story, the Front Matter properties and the Content of the Story are provided. To make changes to Front Matter, use the appropriate Service calls.

Once the Story is given an ID (e.g. AAAA-BBB), it should not change.

Rules that apply to every operation:

- **Every write is validated.** Before saving, the result is checked against the Story JSON Schema. A write that fails is not saved.
- **Every write updates `updated`** to the current time and returns the updated Story.
- **`id` and `created` are set once by `create`** and never change.
- **`updated`, `started` and `completed` are managed by the service** and have no setters.
- **Content and front matter are separate.** Content (the Markdown below the front matter) is changed only by `update` and the Acceptance Criteria and Work Log operations below. Front matter is changed only by the setters below.
- **The content's H1 always matches `title`.**
- **Synonyms and labels are accepted on input** for type, status, priority and effort, and are stored as their primary value (e.g. `refactor` → `impr`, `approved` → `ready`, `P1` → `1`, `Medium` → `M`).


#### Stories

| Operation | Inputs | Description | Example |
|---|---|---|---|
| create | :storyInput, :templateId? | Create a new Story from input matching the Creation JSON Schema, using the named Story Template or the default. Assigns `id` (next sequence number + random 3-character ID), sets `created` and `updated`, defaults `status` to `pending`, fills the template from `body`, and writes `<id>-<type>-<slug>.md`. | `story create --input story.json` |
| get | :storyIdentifier | Retrieve a Story: its path, front matter and content. | `story get 0002-a3f` |
| getFrontMatter | :storyIdentifier | Retrieve only the front matter. | `story get 0002-a3f --front-matter` |
| getContent | :storyIdentifier | Retrieve only the content. | `story get 0002-a3f --content` |
| list | :filter? | List Stories (path and front matter), optionally filtered by type, status, priority, or a linked plan or story. Archived Stories are included only when asked for. | `story list --type bugs --status in_progress` |
| update | :storyIdentifier, :storyContent | Replace the Story's content. Front matter is unchanged apart from `updated`. `progress` keys that no longer match a numbered step heading are rejected. | `story update 0002-a3f --content body.md` |
| delete | :storyIdentifier, :force? | Delete the Story file. Fails if other Stories or Plans link to it unless forced. To retire a Story, prefer `setStatus` with `archived`. | `story delete 0002-a3f` |
| validate | :storyIdentifier | Check a Story without changing it. Checks the schema, that the filename matches `id` and `type`, that every `progress` key matches a numbered step heading in the content, and that the Acceptance Criteria section parses as a checklist. Returns a list of problems. | `story validate 0002-a3f` |

#### Templates

| Operation | Inputs | Description | Example |
|---|---|---|---|
| getTemplate | :templateId (optional) | Retrieve a Story Template (not the Front Matter); used for an agent to get the template they must follow. If no templateId is provided, use the default. | `story template get` |
| createTemplate | :templateId, :templateContent | Create a new Story Template with the given ID. | `story template create review --content review.md` |
| updateTemplate | :templateId, :templateContent | Update a Story Template. | `story template update review --content review.md` |
| deleteTemplate | :templateId | Delete a Story Template. The default template cannot be deleted. | `story template delete review` |

#### Front Matter: Fields

| Operation | Inputs | Description | Example |
|---|---|---|---|
| setTitle | :storyIdentifier, :title, :renameFile? | Change `title` and the content's H1. The filename keeps its slug unless `renameFile` is set. | `story set-title 0002-a3f "New title"` |
| setPurpose | :storyIdentifier, :purpose | Set or replace `purpose`. | `story set-purpose 0002-a3f "Fix the empty-payload crash"` |
| setType | :storyIdentifier, :storyType | Change `type` (a primary value or synonym, e.g. `refactor` → `impr`) and rename the file's type part. `id` is unchanged. | `story set-type 0002-a3f impr` |
| setStatus | :storyIdentifier, :status, :force? | Move the Story to a new status. Rejects moves the workflow doesn't allow unless forced. Moving to `in_progress` sets `started`; moving to `complete` sets `completed`; neither is cleared by moving away, and both are overwritten on return. Moving to `complete` requires every Acceptance Criterion to be checked; `force` does not override this. Moving to `archived` moves the file into the archive folder; leaving `archived` moves it back. | `story set-status 0002-a3f approved` |
| getTransitions | :storyIdentifier | List the statuses the Story can move to from its current status. | `story transitions 0002-a3f` |
| setPriority | :storyIdentifier, :priority | Set `priority` from a number (`0`–`5`) or label (`P1`, `p1`, `Critical`). | `story set-priority 0002-a3f P1` |
| setEffort | :storyIdentifier, :effort | Set `effort` from a size (`XS`–`XL`), label (`Medium`) or points (`5`). | `story set-effort 0002-a3f M` |
| clearEffort | :storyIdentifier | Remove `effort`. | `story clear-effort 0002-a3f` |
| patchFrontMatter | :storyIdentifier, :frontMatterPatch | Set several fields in one write, following the same rules as the individual setters. Changes to `id`, `created`, `updated`, `started` or `completed` are rejected. | `story patch 0002-a3f --priority 1 --effort L` |

#### Front Matter: Links

| Operation | Inputs | Description | Example |
|---|---|---|---|
| addPlanLink | :storyIdentifier, :planId, :relation, :sections? | Add `[planId, relation]` to `links.plans`, with an optional list of the Plan's section numbers this Story implements. Relation: `included`, `depends` or `blocks`. The Plan must exist, and each section must exist in the Plan's `progress` map; duplicates are ignored. | `story link-plan 0002-a3f 0023-de3 included --sections 1.1,1.2` |
| setPlanSections | :storyIdentifier, :planId, :sections | Replace the section list on an existing plan link. An empty list removes the third element. | `story plan-sections 0002-a3f 0023-de3 1.1,1.2,1.3` |
| removePlanLink | :storyIdentifier, :planId, :relation? | Remove a plan link. Without a relation, removes every link to that Plan. | `story unlink-plan 0002-a3f 0023-de3` |
| addStoryLink | :storyIdentifier, :targetStoryId, :relation | Add `[targetStoryId, relation]` to `links.stories`. Relation: `parent`, `included`, `depends` or `blocks`. The target Story must exist; duplicates are ignored. | `story link 0002-a3f 0001-abc depends` |
| removeStoryLink | :storyIdentifier, :targetStoryId, :relation? | Remove a story link. Without a relation, removes every link to that Story. | `story unlink 0002-a3f 0001-abc` |
| addSpec | :storyIdentifier, :spec | Add a path or URL to `links.specs`. | `story add-spec 0002-a3f ./docs/reviews/review-2026-09-09.md` |
| removeSpec | :storyIdentifier, :spec | Remove an entry from `links.specs`. | `story remove-spec 0002-a3f ./docs/reviews/review-2026-09-09.md` |
| setWebLink | :storyIdentifier, :label, :url | Add a named link to `links.web`, replacing any existing link with that label. | `story set-web 0002-a3f jira https://jira.atlassian.net/browse/ACME-3455` |
| removeWebLink | :storyIdentifier, :label | Remove a named web link. | `story remove-web 0002-a3f jira` |
| setRepo | :storyIdentifier, :remote?, :local?, :pullRequest? | Set `links.repo.remote`, `links.repo.local`, `links.repo.pull_request`, or any combination. | `story set-repo 0002-a3f --pull-request https://github.com/org/p/pull/1` |
| clearRepo | :storyIdentifier | Remove `links.repo`, including `files`. | `story clear-repo 0002-a3f` |
| addFile | :storyIdentifier, :path | Add a repository-relative path to `links.repo.files`; duplicates are ignored. | `story add-file 0002-a3f src/app/handler.ts` |
| removeFile | :storyIdentifier, :path | Remove a path from `links.repo.files`. | `story remove-file 0002-a3f src/app/handler.ts` |

#### Front Matter: Progress

| Operation | Inputs | Description | Example |
|---|---|---|---|
| getProgress | :storyIdentifier | Retrieve the status of every numbered step. | `story progress 0002-a3f` |
| setProgress | :storyIdentifier, :step, :status | Set the status of a step (`"2"`, `"1.1"`, `"2.1.3"`). The step must exist as a numbered heading in the content. When this completes the last remaining step and the Story is `in_progress` with every Acceptance Criterion checked, the Story moves to `complete` as if `setStatus` had been called. | `story set-progress 0002-a3f 1.1 in_progress` |
| removeProgress | :storyIdentifier, :step | Remove a step's progress entry. | `story remove-progress 0002-a3f 1.1` |

#### Content: Acceptance Criteria

Acceptance Criteria live in the content, but the service reads and edits them directly because they gate completion.

| Operation | Inputs | Description | Example |
|---|---|---|---|
| getCriteria | :storyIdentifier | List every item under `## Acceptance Criteria` with its 1-based position, text, kind and state (`open`, `done`, `not_applicable`). | `story criteria 0002-a3f` |
| setCriterion | :storyIdentifier, :position, :state | Set an item's state; rendered as `[ ]`, `[x]` or `[~]`. | `story check 0002-a3f 2 done` |
| addCriterion | :storyIdentifier, :text, :kind? | Append an item, optionally marked `verify` or `manual`. | `story add-criterion 0002-a3f "pnpm test" --kind verify` |
| removeCriterion | :storyIdentifier, :position | Remove an item. | `story remove-criterion 0002-a3f 2` |

#### Content: Work Log

| Operation | Inputs | Description | Example |
|---|---|---|---|
| appendWorkLog | :storyIdentifier, :entry | Append `### <now> - <entry>` under `## Work Log`. The service supplies the timestamp. Entries are never edited or removed through the service; the section is append-only. | `story log 0002-a3f "Fixed the guard; 12/12 tests pass"` |

#### Errors

| Error | Raised when |
|---|---|
| NotFound | No Story matches the identifier, or a linked Story or Plan doesn't exist. |
| ValidationError | The result of a write fails the JSON Schema. Includes the list of problems. |
| InvalidTransition | The workflow doesn't allow the requested status change. |
| IncompleteCriteria | A move to `complete` is requested while an item under `## Acceptance Criteria` is unchecked. Includes the unchecked items. Not overridden by `force`. |
| ImmutableField | A write tries to change `id`, `created`, `updated`, `started` or `completed`. |
| UnknownStep | A progress operation names a step that isn't a numbered heading in the content. |
| UnknownSection | A plan link names a section that isn't in the linked Plan's `progress` map. |
| UnknownCriterion | A criteria operation names a position that doesn't exist. |
| LinkedStory | `delete` is called on a Story that other Stories or Plans link to, without `force`. |

### Story Service Storage

Stories use the same pluggable, fan-out storage as Plans: every enabled storage plugin receives every write, and the same force-sync command with the same conflict options (error, skip and report, overwrite) moves Stories between plugins.

#### File-based storage

By default, Stories are stored as Markdown files in `docs/stories` in the working directory where the service was called. All files except Archived Stories live in that folder; Archived Stories are moved to `docs/stories/archive` when they are archived and moved back when they leave that status. The service reads both folders.

File-based storage is the only storage plugin enabled by default. It can only be disabled when another plugin is enabled.

#### Database and Document Database storage

The same relational (MySQL/MariaDB, PostgreSQL, Clickhouse, SQLite) and document (MongoDB, Firestore) plugins written for Plans store Stories as well. A plugin supports both document types or neither.

### Story Service Entrypoints

Stories are exposed through the same entrypoints as Plans (CLI, HTTP/REST, WebSockets, MCP/Streamable HTTP), with the same function names, inputs and return shapes as the interface above. The CLI uses the `story` command where Plans use `plan`; the other entrypoints namespace Story operations the same way. Subscriptions deliver updates for one or more Stories exactly as they do for Plans.
