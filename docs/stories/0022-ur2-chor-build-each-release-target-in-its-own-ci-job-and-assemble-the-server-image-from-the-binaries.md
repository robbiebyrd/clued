---
id: 0022-ur2
title: Build each release target in its own CI job and assemble the server image from the binaries
type: chor
status: complete
priority: "1"
effort: S
created: "2026-10-13T18:21:11.717Z"
updated: "2026-10-14T19:12:20.315Z"
started: "2026-10-13T18:21:11.717Z"
completed: "2026-10-14T19:12:20.315Z"
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
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Build each release target in its own CI job and assemble the server image from the binaries

## Problem Statement

The binaries job compiled all seven targets in one step, and the server image compiled the module again in its own build stage, so a release built everything twice on one runner.

**Impact:** Slow pushes and releases; the image's binary was not the release asset.

## Steps

### 1: Per-target build

`scripts/build-binaries.sh --target os/arch` builds one target without SHA256SUMS; `--sums DIR` writes the checksums over collected binaries; `--targets` lists them.

### 2: Matrix job

The `binaries` job is a matrix over the targets (a step checks it matches the script), each uploading its binary as `mind-palace-binary-<os>-<arch>`.

### 3: Image from binaries

`Dockerfile.server` copies `mind-palace_linux_<arch>` from `BINARIES` (dist/ by default) with mode 755; the `image` job downloads the Linux artifacts and the `release` job builds from `release-assets`.

## Acceptance Criteria

- [x] VERIFY: diff <(scripts/build-binaries.sh --targets) <(python3 -c 'import yaml; print("\n".join(yaml.safe_load(open(".github/workflows/mind-palace.yml"))["jobs"]["binaries"]["strategy"]["matrix"]["target"]))')
- [x] [MANUAL] The next push runs seven `binaries` jobs side by side, `image` assembles both platforms from their artifacts, and a release carries the same bytes in the image and in the assets

## Work Log

### 2026-10-13T18:37:13.201Z - Merged main's automatic patch releases (robbiebyrd/clued#25). scripts/release-plan.sh now holds the release decision: a version job runs it before the matrix so the binaries are stamped with the version the release will carry, and the release job runs it again on the latest main and publishes the collected binaries only when main's code is still the pushed commit's and the version agrees; otherwise a later push's run releases. The bump, superseded and mismatch paths were simulated against a scratch repository and a fake gh.
