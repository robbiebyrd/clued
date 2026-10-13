---
id: 0019-v59
title: Cut releases from CI when the plugin version changes on main
purpose: 'No tag is pushed by hand: a push to main with a new version in the plugin manifest releases binaries, image and tag once the checks pass.'
type: chor
status: in_progress
priority: "1"
effort: S
created: "2026-10-13T16:23:40.886Z"
updated: "2026-10-13T16:23:56.048Z"
started: "2026-10-13T16:23:40.886Z"
tags:
  - ci
  - release
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
progress:
  "1":
    status: complete
  "2":
    status: complete
---

# Cut releases from CI when the plugin version changes on main

## Problem Statement

The release workflow ran on a `v*` tag someone had to push by hand after merging a version bump, and it ran independently of the test workflow. The shim and the Docker stack pin the plugin's version, so a forgotten tag leaves every fresh install without a binary or image.

**Impact:** Releases depend on a manual step that is easy to forget and can be done on the wrong commit.

## Steps

### 1: Release job in the main workflow

`release.yml` is replaced by a `release` job in `mind-palace.yml` that needs `test`, `binaries` and `image`, runs on pushes to main (and `workflow_dispatch`), checks whether `v<version>` of `.claude-plugin/plugin.json` has a GitHub release, and when not builds the binaries, pushes the server image as `v<version>` and `latest`, and creates the release with `gh release create --target $GITHUB_SHA`, which creates the tag.

### 2: Docs

README, CLAUDE.md, docs/server.md and the module README describe the automatic release and that no tag is pushed by hand.

## Acceptance Criteria

- [x] VERIFY: `python3 -c 'import yaml; yaml.safe_load(open(".github/workflows/mind-palace.yml"))'` parses and the workflow has a `release` job needing `test`, `binaries` and `image`
- [ ] [MANUAL] The first push to main after this change creates release v1.1.0 with the seven binaries, SHA256SUMS and the image tags `v1.1.0` and `latest`; a later push without a version bump creates nothing

## Work Log
