I want to create a backend-only story/issue tracker. The tool doesn't write plans, but rather works as a service that other tools can use to save, retrieve and monitor plans and issues (as stories).

# Stories

## Story Format

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

#### Story Status

While Story Statuses are unique from Plan Statuses and can be configured individually, the default values for Story Statuses should match the values for Plan Statuses.

##### Story Status Progression

While Story Status Progressions are unique from Plan Status Progressions and can be configured individually, the default values for Story Status Progressions should match the values for Plan Status Progressions.

#### Story Priorities

While Story Priorities are unique from Plan Priorities and can be configured individually, the default values for Story Priorities should match the values for Plan Priorities.


#### Story Types

Valid Story Types:

* bug         (bugs)
* feature     (feat)
* improvement (impr)
* chore       (chor)
* task        (task)

#### Completed Date/Time
Completed is only marked when the ticket is marked as Complete; removing the ticket from Complete does not remove this field, but moving to Complete again will overwrite the field.

### Example Story Front Matter
```yaml
---
id: 0023-a3v
title: Unspecified Bug Fix
purpose: Makes a code change to the repo to fix a known issue
type: bug
status: ready
priority: 1
created: "2026-09-09T14:07:05.352Z"
updated: "2026-09-09T14:35:37.046Z"
completed: ""
dependencies: ["005-34c"]
plan: 023-de3
depends_on: ["001-3sd"]
links:
    web:
        jira: "https://jira.atlassian.net/browse/ACME-3455"
        repo: "https://github.com/robbiebyrd/clued"
        pull_request: "https://github.com/robbiebyrd/clued/pull/1"
    plans:
        - ["023-de3", "parent"]
---
```
