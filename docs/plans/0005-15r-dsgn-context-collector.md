---
id: 0005-15r
title: 'Context collector: `clued context` probes and `prime` for skills'
type: dsgn
status: pending
priority: "2"
effort: L
created: "2026-10-12T14:20:39.754Z"
updated: "2026-10-12T14:20:39.754Z"
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  plans:
    - ["0003-7lv", "depends"]
progress:
  "1":
    status: pending
  "1.1":
    status: pending
  "1.2":
    status: pending
  "1.3":
    status: pending
  "1.4":
    status: pending
  "2":
    status: pending
  "2.1":
    status: pending
  "2.2":
    status: pending
  "3":
    status: pending
  "3.1":
    status: pending
  "3.2":
    status: pending
  "4":
    status: pending
  "4.1":
    status: pending
  "4.2":
    status: pending
  "5":
    status: pending
  "5.1":
    status: pending
  "6":
    status: pending
  "6.1":
    status: pending
---

# Context collector: `clued context` probes and `prime` for skills

## Summary

**Goal:** A skill or slash command runs `${CLAUDE_PLUGIN_ROOT}/bin/clued context <probe>` or `bin/clued context prime` and gets a plain-text answer about the working directory, the git repository, GitHub, project config files, the toolchain, and what clued already knows about the current session, its plans and its stories, with no Node runtime and no shell logic.

**Problem:** clued captures cwd, project path, git origin and branch, account id, host details and every hook event into MongoDB, but nothing hands that knowledge back to Claude when work starts. The reference collector `wiz-context.js` covers the filesystem, git, gh, JSON and environment probes a skill needs, but it is Node (the plugin just retired its Node runtime), passes arbitrary `git`/`gh` arguments through, hardcodes `main`, a sibling `stories-cli.js` and `.claude/active-plan.json`, and knows nothing about captured sessions or mind-palace plans and stories.

**Approach:** Add a `context` domain to mind-palace in the same hexagonal layout as `session`: probe adapters behind a command-runner port, a `prime` use case that composes them with the session store and the plan/story services, and entrypoints through the existing CLI (`mind-palace context`, reached as `bin/clued context`) and the clued MCP server. Every `wiz-context.js` subcommand is ported one-for-one with the same name and output contract, clued-native probes are added, and a `clued-context` skill plus a `/clued-prime` command teach Claude to use them.

---

# Part 1 — Design

## Current State

**What clued already collects.** The daemon (`session/daemon`) reads `session_id`, `transcript_path` and `cwd` from every hook payload, upserts a `session.Session` with `cwd`, `project_path`, `git_origin`, `git_branch` (from `session/gitinfo`: `git remote get-url origin` and `git branch --show-current`, 2 s timeout), `account_id` (`session.ReadAccountID`, the Claude desktop app's `lastKnownAccountUuid`, `unknown` otherwise) and `host` (`session/hostinfo`: hostname, ip, mac, username, uid, platform, arch, os_release, os_type), stamps every hook event with account and host, tails the transcript, and runs eighteen enrichers over hook events and transcript lines. `query.Service.SessionContext` returns a six-field session summary, the ten most recent distinct Bash commands and the first and last twenty transcript lines; `FindSessions` filters by regex on project path, cwd and git origin. `session.EncodeProjectPath` maps a cwd to its `~/.claude/projects` directory name.

**What mind-palace already offers.** `service.Palace` holds the plan and story services (`List(ctx, ListFilter)` with type and status filters, `Resolve`, content access) over `store.Store`; `cli.App.init` loads `mind-palace.config.yaml` and opens the stores, and `filestore.Init` creates `docs/plans` and `docs/stories` in the working directory as a side effect (noted in 0003-7lv). The CLI prints every result in a JSON `{ok, result}` envelope; `cli/session.go` shows the pattern for a command group with lazily connected state, and `cli/session_test.go` drives `App` with buffers. `sessionmcp.Tools` is a `ToolProvider` on the clued stdio server that `.mcp.json` starts in the project directory. `plugin/` holds Go tests of the bash hooks and the `bin/clued` shim; CI (`.github/workflows/mind-palace.yml`) runs gofmt, vet, build and `go test -race` with a MongoDB service.

**The reference collector.** `wiz-context.js` is a dependency-free Node CLI: `node wiz-context.js <subcommand> [args]` prints one plain-text answer and always exits 0. Groups: filesystem (`file-exists`, `dir-exists`, `count-files`, `list-files`, `file-head --lines`, `count-lines`, `find-files --name --maxdepth --count`, `count-files-with-content`, `pwd`), git (`git <args> --limit`, `git-is-clean`, `git-commits-since-tag`), GitHub (`gh <args> --limit`, `gh-pr-for-branch`), JSON (`json-field`, `json-count`, `json-count-where`, `json-count-with-key`, `mcp-configured`), environment (`command-exists`, `detect-stack`, `detect-coverage`, `detect-frontend`, `detect-css-framework`, `detect-build-tool`, `detect-port`) and the composite `prime-context` (README/SPEC/CHANGELOG/AGENTS heads, story and review counts, branch and status, last 15 commits on `main`, last 10 on HEAD, diff stat against `main`, `stories-cli.js list`, the plan named by `.claude/active-plan.json`, build tool and versions). Sentinels: `yes`/`no`, `none`, `0`, `unknown`, `not configured`, `not detected`, `not a git repo`, `[FALLBACK: …]`. Nothing in the plugin provides any of this today.

## Decisions

- **The collector is a `context` domain inside the mind-palace module (`mind-palace/context/...`), exposed as `mind-palace context <probe>` and therefore as `bin/clued context <probe>`.** — The plugin ships one Go binary and the shim already resolves it, so a skill needs no new install step; the domain is neither a document kind nor session data, so it gets its own package tree like `session` did.
- **Every `wiz-context.js` subcommand is ported one-for-one: same name, same arguments and flags (`--limit`, `--lines`, `--name`, `--maxdepth`, `--count`), same sentinel outputs, plain text on stdout, exit code 0 even on failure.** — Skills written against the reference collector port with a path change, and the sentinel contract is what lets a skill paste the output into a prompt without error handling. Plain text is a deliberate exception to the JSON envelope the other commands print.
- **Probes are adapters behind a `context.Runner` port (`Run(ctx, dir, name string, args ...string) (string, error)` with a per-call timeout); `gitprobe` and `ghprobe` take a Runner, `fsprobe`, `jsonprobe` and `envprobe` take a root directory.** — Keeps the use case free of `os/exec`, lets `ghprobe` be tested with a scripted runner and `gitprobe` against real temporary repositories, and matches the no-mocks rule the session port followed.
- **`git` and `gh` passthrough accept only a read-only allowlist. git: `log`, `status`, `diff`, `show`, `rev-parse`, `branch`, `describe`, `remote`, `ls-files`, `tag`, `blame`, `shortlog`, `stash list`, `rev-list`, `symbolic-ref`, `config --get`. gh: `pr`, `issue`, `run`, `release`, `repo` with `list`, `view`, `status`, `checks` or `diff`, and `api` without `--method`/`-X` or `-f`/`-F`. Anything else prints `[FALLBACK: git …]` or `[FALLBACK: gh …]`.** — The reference collector passes arbitrary arguments to both tools; a collector that skills run without a permission prompt must not be a way to push, reset or write.
- **The base branch is detected (`refs/remotes/origin/HEAD`, then a local `main`, then `master`) instead of hardcoded, and the prime sections are labelled with the detected name.** — The reference's `LOG:main` and `DIFF-STAT` sections are empty on every repository whose default branch is not `main`.
- **`prime` includes what clued already captures: cwd and its encoded project path, git origin and branch via `session/gitinfo`, the account id via `session.ReadAccountID`, a host summary via `session/hostinfo` (hostname, username, platform, arch, os release; never ip or mac), daemon health on the configured port, and the most recent captured session for this cwd (session id, started, last seen, top commands) through `query.Service`.** — This is the clued-specific value of the tool: the session mirror finally feeds context back into a session. The store is opened lazily with a 2 s timeout and the section degrades to `store unavailable`, so prime stays useful on a machine without MongoDB.
- **Stories and plans come from the palace services, not from globs, `stories-cli.js` or `.claude/active-plan.json`: active stories are those with status `pending`, `ready` or `in_progress`; active plans are those with status `in_progress` (then `ready`), each shown with id, type, title and the first 100 lines of the top in-progress plan. The palace is opened only when `mind-palace.config.yaml`/`.json` or the plans directory already exists.** — The plugin stores plans and stories in mind-palace, so that is the truth. Opening the palace runs `filestore.Init`, which creates `docs/plans` and `docs/stories`; guarding on existence keeps prime side-effect free in an unrelated project.
- **Globbing supports `**` through a walker in `fsprobe` (hidden directories skipped, results relative to the root, forward slashes for display), with no new dependency.** — `filepath.Glob` has no `**`; the reference relies on it for `stories/*-pending-*.md`-style patterns and recursive finds. Mirroring the reference's walker keeps behaviour identical and the dependency list unchanged.
- **Every probe has its own timeout (git 2 s as in `gitinfo`, gh 15 s, port 1 s, version commands 5 s), prime has a 20 s budget, and each prime section is computed independently so one failure prints that section's sentinel and the others still render.** — The collector runs at the start of work; a hung `gh` or an unreachable MongoDB must cost a sentinel line, not the session.
- **Entry points are the CLI group and one read-only MCP tool, `prime_context`, on the clued stdio server (`sessionmcp`), taking an optional `cwd`. Individual probes stay CLI-only.** — Skills run probes from Bash, where the plain-text contract matters; `.mcp.json` already starts the clued server in the project directory, so prime is one tool call away without a Bash permission prompt.
- **The plugin ships `skills/clued-context/SKILL.md` (every probe with its sentinel, and the instruction to run `prime` before starting a task) and `commands/clued-prime.md`; `prime` is not wired into `SessionStart`.** — A SessionStart hook would inject a few hundred lines into every session whether or not the task needs them; a skill and a command let the work opt in. The hook can be added later without changing the collector.

### Rejected alternatives

- **Vendor `wiz-context.js` into the plugin and run it with `node`.** — The plugin removed its Node runtime in 0004-b1m; the plugin cache has no toolchain and `node` is not a prerequisite any more.
- **One bash script per probe under `hooks/` or `scripts/`.** — Needs `jq`, breaks on Windows, and is only testable through process-level tests; the reference collector exists precisely because shell probes were not portable.
- **Inject the prime output from the `SessionStart` hook.** — Cost on every session, including ones that never touch the repository; the skill and command opt in instead, and the hook remains possible later.
- **Model collected context as a new mind-palace document kind.** — Kinds are Markdown documents with front matter, templates and a workflow; a context snapshot is transient output.
- **Store every prime snapshot in MongoDB as a hook event.** — Out of scope for the first version: a snapshot is derivable from the hook events the daemon already stores, and a later enricher can do it without changing the collector.
- **Honour `.claude/active-plan.json` for the PLAN section.** — The plugin's plans live in mind-palace with an `in_progress` status; a second source of truth would drift.

## Design

### `context` — domain (package `context`)

Pure package. `Runner` port and its default `ExecRunner{Timeout}` (argv only, never a shell; `Run` returns trimmed stdout, and on a non-zero exit returns stdout with the error so callers can keep partial output like the reference does). Sentinel constants (`yes`/`no` for existence checks, `none` for empty lists and absent JSON values, `0` for empty counts, `unknown` for undetectable stacks and tag distances, `not configured`, `not detected`, `no build tool detected`, `not a git repo`, `no <basename>` for an unreadable head, `store unavailable`, `no plans dir`, and `[FALLBACK: <subcommand> <args>]` when a probe itself fails). `Report{Sections []Section}` with `Section{Name, Body string}` rendered as `=== NAME ===` blocks separated by a blank line. Helpers shared by the probes: `ExpandHome`, `Resolve(root, p)` (absolute or root-relative), `DisplayPath` (forward slashes), `Truncate(s string, limit int)` (line limit, `limit <= 0` means unlimited), `ParseLimit`. Option types for the CLI flags so the probes never read `os.Args`.

### `context/fsprobe` — filesystem probes

`Probe{Root string}` with `FileExists`, `DirExists`, `CountFiles(globs)`, `ListFiles(globs, limit)`, `FileHead(path, lines)` (bufio line reader, never the whole file), `CountLines(path)` (newline count, like `wc -l`), `FindFiles(dir, names []string, maxDepth, limit int, countOnly bool)` (depth starts at 1, hidden directories skipped, inaccessible directories ignored), `CountFilesWithContent(glob, pattern)` (Go `regexp`), `Pwd`. `Glob(root, pattern)` handles `*`, `?` and `**` with the same semantics as the reference's walker; `MatchName(name, pattern)` is the per-segment matcher with a compiled-pattern cache. Outputs are root-relative, forward-slashed and sorted for stable tests.

### `context/gitprobe` — git probes

`Probe{Root string; Runner context.Runner}`. `Git(args, limit)` applies the read-only allowlist then runs `git -C root <args>` and truncates; `IsClean` (`status --porcelain`); `CommitsSinceTag` (`describe --tags --abbrev=0` then `rev-list --count`); `Origin` and `Branch` delegate to `session/gitinfo`; `DefaultBranch` (origin/HEAD, `main`, `master`, else empty); `Log(ref, n)`; `DiffStat(base)` (`diff base...HEAD --stat`); `StatusSummary` (`clean` or `N changed files`). Every failure maps to the reference sentinels (`not a git repo`, `unknown`, `no`).

### `context/ghprobe` — GitHub CLI probes

`Probe{Root string; Runner context.Runner}`. `GH(args, limit)` applies the allowlist, runs `gh`, returns stdout or `[FALLBACK: gh <args>]`; `PRForBranch(branch)` runs `gh pr list --head <branch> --state open --json number,title` and formats `#<number>: <title>` in Go (no `-q` jq expression), `none` when empty or when gh is absent or unauthenticated.

### `context/jsonprobe` — JSON and config probes

`Field(file, dotPath)` (objects re-encoded compactly), `Count`, `CountWhere(file, dotPath, key, value)`, `CountWithKey(file, key)` (recursive), and `MCPConfigured(keyword, cwd, home)` searching `<cwd>/.mcp.json`, `~/.claude/.mcp.json`, `~/.claude/settings.json` and every `*.mcp.json` under `~/.claude/plugins` by substring, reporting only `configured`/`not configured`. Dot paths accept a leading `.`.

### `context/envprobe` — environment probes

`CommandExists` (`exec.LookPath`), `DetectStack` (go.mod → Go, package.json → TS, Cargo.toml → Rust, pom.xml/build.gradle → Java, *.csproj/*.sln → C#, requirements.txt/pyproject.toml → Python; space-joined, `unknown`), `DetectCoverage`, `DetectFrontend`, `DetectCSSFramework` (substring search of package.json in the reference's order), `DetectBuildTool` (first match wins; versions through the Runner, missing tools skipped), `DetectPort(ports)` (in-process `net.DialTimeout` to 127.0.0.1, 1 s, first open port or `not detected`).

### `context/prime` — composite use case

`Collector{Root, Home string; FS *fsprobe.Probe; Git *gitprobe.Probe; GH *ghprobe.Probe; Env *envprobe.Probe; Sessions SessionSource; Docs DocSource; Host func() session.HostInfo; AccountID func() string; DaemonPort int; Budget time.Duration}` and `Collect(ctx) Report`. Ports: `SessionSource{Current(ctx, cwd) (*SessionSnapshot, error)}` and `DocSource{ActiveStories(ctx) ([]DocSummary, error); ActivePlans(ctx) ([]DocSummary, error); PlanContent(ctx, id) (string, error)}`; nil sources print their sentinel. Sections, in order: `ENV` (cwd, project_path, git_origin, git_branch, account_id, host line); `CLUED` (config path, daemon `healthy`/`not running` on the port, enrichers disabled); `FILES` (README.md 80, SPEC.md 120, CHANGELOG.md 30, AGENTS.md 60, CLAUDE.md 60 lines with `… (N more lines)`, else `<file>: no`; `docs/plans: N files`, `docs/stories: N files`); `BRANCH` (`<branch> (<status summary>)`, `detached HEAD`); `LOG:<default>` (15 commits) and `LOG:HEAD` (10); `DIFF-STAT` (against the default branch, `no diff (on <default>)`); `STORIES` (one line per active story: id, type, status, title); `PLANS` (one line per active plan, then the first 100 lines of the top one); `SESSION` (id, started, last seen, up to ten top commands, or `store unavailable`/`none`); `BUILD` (`DetectBuildTool`). Sections run in a bounded `errgroup`-free loop with per-section contexts so the budget holds.

### `context/sources` — adapters for the palace and the session store

`sessionsource.New(connect func(ctx) (session.Store, error), accountID string)` implements `SessionSource` over `query.Service`: `FindSessions` with `Query` set to `^` + `regexp.QuoteMeta(cwd)` + `$`, newest first, then `SessionContext` for the top commands. `docsource.New(palace *service.Palace)` implements `DocSource` with `List` filtered by status and `GetContent`; `docsource.Open(ctx, root)` returns nil (no palace) unless a config file or the plans directory exists, then loads the config and opens the stores the way `cli.App.init` does.

### `cli/context.go` — the `context` command group

`mind-palace context <probe> [args] [--limit n] [--root dir]` with one cobra subcommand per probe (`file-exists`, `dir-exists`, `count-files`, `list-files`, `file-head`, `count-lines`, `find-files`, `count-files-with-content`, `pwd`, `git`, `git-is-clean`, `git-commits-since-tag`, `git-origin`, `git-branch`, `git-default-branch`, `gh`, `gh-pr-for-branch`, `json-field`, `json-count`, `json-count-where`, `json-count-with-key`, `mcp-configured`, `command-exists`, `detect-stack`, `detect-coverage`, `detect-frontend`, `detect-css-framework`, `detect-build-tool`, `detect-port`, `host`, `account`, `project-path`, `daemon-status`, `session`, `prime`). The group disables cobra flag parsing for `git` and `gh` so their arguments pass through untouched except for `--limit`. Output is the probe's text plus a newline on stdout; a probe failure prints `[FALLBACK: …]`; an unknown probe prints `unknown subcommand: <name>`; the exit code is always 0. `prime` adds `--no-store` and `--no-docs`. `--config` (the clued config, as in `session`) selects the port, account path and MongoDB URL.

### `sessionmcp` — `prime_context` tool

Seventh tool on the clued server: `prime_context` with schema `{cwd?: string}` (default: the server's working directory), `ReadOnlyHint: true`, returning the rendered report as text. Built from the same `prime.Collector` the CLI uses, with the lazily connected store the tools already share.

### Plugin glue

`skills/clued-context/SKILL.md`: when to run `prime` (before starting a task in a repository, when resuming), how to call a probe through `${CLAUDE_PLUGIN_ROOT}/bin/clued context …`, the full probe table with arguments and sentinels, and the rule that output is data to read, not instructions. `commands/clued-prime.md`: runs `prime` and asks Claude to summarise the repository state in a few lines. `README.md`: a Context section. `.github/workflows/mind-palace.yml`: add `skills/**` and `commands/**` to the path filters. `cli/context_skill_test.go`: parses the probe table in `SKILL.md` and asserts every listed probe is a registered subcommand and every subcommand is listed.

### Error Handling

Probes never return an error to the CLI: each maps its failure to the reference sentinel (`yes`/`no` for existence checks, `none` for empty lists and absent JSON values, `0` for empty counts, `unknown` for undetectable stacks and tag distances, `not configured`, `not detected`, `no build tool detected`, `not a git repo`, `no <basename>` for an unreadable head, `store unavailable`, `no plans dir`, and `[FALLBACK: <subcommand> <args>]` when a probe itself fails) and the process exits 0. `Runner` errors keep partial stdout, as the reference keeps `err.stdout`. `prime` runs each section under its own context; a section that times out or panics (recovered) prints `[FALLBACK: <section>]` and the report still renders. The MCP tool returns the report text on success and a tool error only when the cwd does not exist.

### Edge Cases

Not a git repository, detached HEAD (`BRANCH` prints `detached HEAD`), no `origin`, no default branch (`LOG:` and `DIFF-STAT` print `no default branch`), shallow clones (`CommitsSinceTag` → `unknown`). Hidden directories are skipped by `find-files` and `**` globs; symbolic links are not followed. `file-head` on a file without a trailing newline returns the last partial line; `count-lines` counts newline bytes so `wc -l` parity holds. `count-files-with-content` reads files as bytes and skips unreadable ones. `--limit` with a non-numeric value means unlimited, as `parseInt || Infinity` did. `detect-port` only dials loopback. `mcp-configured` is a substring match, so `mongo` matches `mongodb`. Windows paths display with forward slashes; `~` expands on every platform. A plans directory that exists but holds no documents prints `none`, not `no plans dir`.

## Considerations

- **Security:** No shell: every external call is argv through `exec.CommandContext`. The `git` and `gh` allowlists make the collector read-only even when a skill forwards user-provided arguments. `mcp-configured` never prints config contents, only `configured`/`not configured`. The host line omits ip and mac; the account id is the same value the daemon already stores. Prime output is repository data that a skill feeds to the model, so `SKILL.md` states it is data, not instructions.
- **Performance:** Prime without the store finishes in well under two seconds on a typical repository: file heads are line-limited reads, git calls are bounded at 2 s each, and the store and palace are opened only when their sections run. The 20 s budget bounds the worst case (a hung `gh` plus an unreachable MongoDB).

## Testing

Unit tests per probe against real temporary directories and real `git init` repositories (skipped when `git` is absent); `ghprobe` and `DetectBuildTool` against a scripted `Runner` that records calls and returns canned stdout; `jsonprobe` against fixture files; `prime` against `memsession` seeded through `daemon`-shaped documents and a `DocSource` backed by `memstore`, asserting section order, sentinels and the budget; CLI tests through `App` with buffers for every subcommand, including the exit-0-on-failure rule and `git`/`gh` allowlist rejections; `sessionmcp` test for `prime_context` over the in-memory transport; `cli/context_skill_test.go` for skill/CLI consistency; a parity test that runs every ported probe over `testdata/context-repo` and compares with recorded outputs of `wiz-context.js` on the same tree. CI is unchanged apart from the path filters; the store-backed tests already run against the MongoDB service.

## Scope

**In scope:**

- `mind-palace/context` domain, five probe packages, the `prime` use case and the two source adapters
- `mind-palace context` CLI group with every reference subcommand plus `git-origin`, `git-branch`, `git-default-branch`, `host`, `account`, `project-path`, `daemon-status`, `session` and `prime`
- `prime_context` MCP tool on the clued server
- `skills/clued-context/SKILL.md`, `commands/clued-prime.md`, README section, CI path filters, consistency and parity tests

**Out of scope:**

- Injecting `prime` output from the `SessionStart` hook — cost on every session; can be added later as one hook line
- Storing prime snapshots in MongoDB — derivable from hook events by a later enricher
- A `--json` output mode for the probes — the plain-text contract is what skills consume; add when a consumer needs it
- `.claude/active-plan.json` and `stories-cli.js` compatibility — replaced by mind-palace plans and stories
- Windows CI — the code is portable but the workflow stays on ubuntu like the rest of mind-palace

## Open Questions

- [ ] Should `prime` also POST a synthetic `ContextCollected` event to the daemon so the snapshot lands in `hook_events` alongside the session? — Boss
- [ ] Should `PLANS` include `ready` plans when no plan is `in_progress`, or only `in_progress`? — Boss
- [ ] Is `gh api` worth keeping in the allowlist at all, given it is the hardest verb to keep read-only? — Boss

---

# Part 2 — Implementation

**Architecture:** Hexagonal `context` domain in mind-palace: `context` (ports, sentinels, report), probe adapters in `context/{fsprobe,gitprobe,ghprobe,jsonprobe,envprobe}`, source adapters in `context/sources/{sessionsource,docsource}`, the `context/prime` use case, and entrypoints in `cli/context.go` and `session/sessionmcp`. Sections marked with the same wave letter are independent and can run as concurrent subagents in `.worktrees/<section>` on branches off `wip/context-collector`; the orchestrator merges a wave only when its sections pass their own validation, then runs the full gate (`cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go test ./...`). Waves: **A** = 1.1; **B** = 1.2 ∥ 1.3 ∥ 1.4 ∥ 2.1 ∥ 2.2 ∥ 3.1; **C** = 3.2; **D** = 4.1 ∥ 4.2; **E** = 5.1; **F** = 6.1.

**Tech Stack:** Go 1.26 (module `github.com/robbiebyrd/clued/mind-palace`), standard library `os/exec`, `path/filepath`, `regexp`, `net`, `bufio`, `encoding/json`; existing dependencies `github.com/spf13/cobra` and `github.com/modelcontextprotocol/go-sdk`. No new dependencies. Plugin files are Markdown and YAML.

## Constraints

- Every section is test-first: write the failing tests named in Step 1, watch them fail, implement, watch them pass, then commit. Tests use real temporary directories, real git repositories and the in-memory stores; no mocks.
- A subagent edits only the files its section lists and never reformats files it does not own.
- All Go commands run from `mind-palace/`; `gofmt -l .` must print nothing and `go vet ./...` must pass before every commit.
- Output parity with `wiz-context.js` is the acceptance test for every ported probe: when a port decision is unclear, the reference's behaviour wins, except where a decision above overrides it (allowlists, default branch, palace-backed stories and plans).
- Probes never exit non-zero and never print to stdout anything but the answer; diagnostics go to stderr.
- Nothing is pushed and no pull request is opened unless Boss asks.
- The orchestrator tracks progress with `mind-palace plan set-progress <this plan> <section> <status>` after each merge.

## Files

| File | Change | Responsibility |
|---|---|---|
| `mind-palace/context/context.go` | create | `Runner`, `ExecRunner`, sentinels, `Report`/`Section`, path and limit helpers |
| `mind-palace/context/fsprobe/fsprobe.go` | create | Filesystem probes and `**` glob walker |
| `mind-palace/context/gitprobe/gitprobe.go` | create | Allowlisted git passthrough and git facts |
| `mind-palace/context/ghprobe/ghprobe.go` | create | Allowlisted gh passthrough and PR lookup |
| `mind-palace/context/jsonprobe/jsonprobe.go` | create | JSON dot-path, counts and `mcp-configured` |
| `mind-palace/context/envprobe/envprobe.go` | create | Command, stack, coverage, frontend, CSS, build tool and port detection |
| `mind-palace/context/sources/sessionsource/sessionsource.go` | create | `SessionSource` over `query.Service` |
| `mind-palace/context/sources/docsource/docsource.go` | create | `DocSource` over `service.Palace`, guarded open |
| `mind-palace/context/prime/prime.go` | create | `Collector`, section builders, budget |
| `mind-palace/cli/context.go` | create | `context` command group |
| `mind-palace/cli/cli.go` | modify | Register `contextCmd` on the root |
| `mind-palace/session/sessionmcp/sessionmcp.go` | modify | `prime_context` tool |
| `mind-palace/testdata/context-repo/` | create | Fixture tree plus recorded `wiz-context.js` outputs for the parity test |
| `skills/clued-context/SKILL.md` | create | Probe reference and usage rules for Claude |
| `commands/clued-prime.md` | create | `/clued-prime` slash command |
| `README.md` | modify | Context section |
| `.github/workflows/mind-palace.yml` | modify | Add `skills/**` and `commands/**` path filters |

## Phase 1: Domain and pure probes

### 1.1: Domain package (wave A)

**Files:**
- Create: `mind-palace/context/context.go`
- Test: `mind-palace/context/context_test.go`

**Depends on:** none

**Interfaces:**
- Consumes: nothing beyond the standard library
- Produces: `Runner`, `ExecRunner{Timeout}`, sentinel constants, `Report`, `Section`, `Report.String()`, `ExpandHome`, `Resolve`, `DisplayPath`, `Truncate`, `ParseLimit`, `Fallback(name string, args []string) string`

- [ ] **Step 1: Write the failing tests** — `ExecRunner` returns trimmed stdout, keeps partial stdout with the error on non-zero exit, honours the timeout (a `sleep` child) and runs in the given dir; `Report.String()` renders `=== NAME ===` blocks joined by one blank line; `Truncate` keeps N lines and passes through on `limit <= 0`; `ParseLimit` maps a non-number to unlimited; `ExpandHome`, `Resolve`, `DisplayPath` on absolute, relative and `~/` inputs; `Fallback` formats `[FALLBACK: name a b]`.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./context`
- [ ] **Step 3: Implement** — `ExecRunner.Run` with `exec.CommandContext`, `cmd.Dir`, separate stdout/stderr buffers, `context.WithTimeout`; the helpers as pure functions.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./context && go test ./context`
- [ ] **Step 5: Commit** — `git commit -m 'feat(context): runner port, sentinels and report rendering'`

**Validation:** `go test ./context`

### 1.2: Filesystem probes (wave B)

**Files:**
- Create: `mind-palace/context/fsprobe/fsprobe.go`
- Test: `mind-palace/context/fsprobe/fsprobe_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `context` helpers and sentinels; the reference's `resolveGlobs`, `matchGlob`, `find-files`, `file-head`, `count-lines` semantics
- Produces: `Probe{Root}` with `FileExists`, `DirExists`, `CountFiles`, `ListFiles`, `FileHead`, `CountLines`, `FindFiles`, `CountFilesWithContent`, `Pwd`; `Glob`, `MatchName`

- [ ] **Step 1: Write the failing tests** — Build a temp tree (nested dirs, a hidden dir, files with and without trailing newline, a CRLF file). Assert: existence checks print `yes`/`no` and `no` for a directory asked as a file; `Glob` with `*.md`, `docs/**/*.md`, `stories/*-pending-*.md`, `~`-prefixed patterns; `ListFiles` sorted, forward-slashed, `none` when empty, `--limit` honoured; `FileHead` N lines and `no <basename>` for a missing file; `CountLines` equals the newline count; `FindFiles` with multiple `--name`, `--maxdepth 1`, `--count`, hidden dirs skipped; `CountFilesWithContent` with a regex and an unreadable file skipped; `MatchName` table (`*.test.js`, `?.go`, `**`, literal dots).
- [ ] **Step 2: Run them and confirm they fail** — `go test ./context/fsprobe`
- [ ] **Step 3: Implement** — Walker-based `Glob` splitting the pattern at the first segment containing a wildcard; `MatchName` compiles the segment to a regexp with a cache; `FileHead` through `bufio.Reader.ReadString` with a line cap; `FindFiles` recursive with depth counting from 1.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./context/... && go test ./context/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(context): filesystem probes and glob walker'`

**Validation:** `go test ./context/fsprobe`

### 1.3: JSON and config probes (wave B)

**Files:**
- Create: `mind-palace/context/jsonprobe/jsonprobe.go`
- Test: `mind-palace/context/jsonprobe/jsonprobe_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `context` helpers; the reference's `getByDotPath`, `countKeyInJSON`, `mcp-configured` search paths
- Produces: `Field`, `Count`, `CountWhere`, `CountWithKey`, `MCPConfigured(keyword, cwd, home string)`

- [ ] **Step 1: Write the failing tests** — Fixture JSON with nested objects and arrays: `Field` on scalar, object (compact JSON), missing (`none`), with and without a leading `.`; `Count` on array, object and scalar (`0`); `CountWhere` string-compares values (`true` matches `"true"`); `CountWithKey` counts nested occurrences; `MCPConfigured` finds a keyword in `.mcp.json`, in `~/.claude/settings.json`, in a plugin `*.mcp.json` three levels deep, and prints `not configured` when absent or on an empty keyword.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./context/jsonprobe`
- [ ] **Step 3: Implement** — `encoding/json` into `any`; dot-path walk; recursive key count; `filepath.WalkDir` under the plugins dir for `*.mcp.json`.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./context/... && go test ./context/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(context): JSON and MCP configuration probes'`

**Validation:** `go test ./context/jsonprobe`

### 1.4: Environment probes (wave B)

**Files:**
- Create: `mind-palace/context/envprobe/envprobe.go`
- Test: `mind-palace/context/envprobe/envprobe_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `context.Runner` for version commands; the reference's detection tables
- Produces: `Probe{Root string; Runner context.Runner}` with `CommandExists`, `DetectStack`, `DetectCoverage`, `DetectFrontend`, `DetectCSSFramework`, `DetectBuildTool`, `DetectPort`

- [ ] **Step 1: Write the failing tests** — Temp roots with marker files for every stack combination (`Go TS`, `C#` from a `.sln`, `Python` from `pyproject.toml`, `unknown`); coverage markers; frontend yes/no; CSS framework order (`tailwindcss` before `sass`); `DetectBuildTool` with a scripted Runner returning versions and with a Runner that fails (label only); `DetectPort` against a listener opened in the test and a closed port; `CommandExists` for `go` and a nonsense name.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./context/envprobe`
- [ ] **Step 3: Implement** — Table-driven marker checks; `net.DialTimeout("tcp", "127.0.0.1:<port>", time.Second)`; first-match build tool with versions via the Runner.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./context/... && go test ./context/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(context): environment and toolchain probes'`

**Validation:** `go test ./context/envprobe`

## Phase 2: Command-backed probes

### 2.1: Git probes (wave B)

**Files:**
- Create: `mind-palace/context/gitprobe/gitprobe.go`
- Test: `mind-palace/context/gitprobe/gitprobe_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `context.Runner`, `session/gitinfo` for origin and branch
- Produces: `Probe{Root; Runner}` with `Git(args, limit)`, `IsClean`, `CommitsSinceTag`, `Origin`, `Branch`, `DefaultBranch`, `Log(ref, n)`, `DiffStat(base)`, `StatusSummary`; `Allowed(args) bool`

- [ ] **Step 1: Write the failing tests** — Helper that `git init`s a temp repo with two commits, a tag, a dirty file and a second branch (skip when `git` is missing). Assert: `Git(["log","--oneline"], 1)` returns one line; `Git(["push"])` and `Git(["reset","--hard"])` return the fallback without running; `IsClean` flips after committing; `CommitsSinceTag` is `1` and `unknown` without tags; `DefaultBranch` prefers `origin/HEAD` (set up with a bare clone), then `main`, then `master`, else empty; `DiffStat` across branches; `StatusSummary` counts porcelain lines; outside a repository everything maps to the reference sentinels.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./context/gitprobe`
- [ ] **Step 3: Implement** — Allowlist on the first argument (and the second for `stash` and `config`); every call through `Runner.Run(ctx, root, "git", args...)` with a 2 s timeout; keep partial stdout on error.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./context/... && go test ./context/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(context): read-only git probes'`

**Validation:** `go test ./context/gitprobe`

### 2.2: GitHub CLI probes (wave B)

**Files:**
- Create: `mind-palace/context/ghprobe/ghprobe.go`
- Test: `mind-palace/context/ghprobe/ghprobe_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `context.Runner`
- Produces: `Probe{Root; Runner}` with `GH(args, limit)`, `PRForBranch(branch)`; `Allowed(args) bool`

- [ ] **Step 1: Write the failing tests** — Scripted Runner recording argv: `GH(["pr","list"])` passes through and truncates; `GH(["pr","merge","1"])`, `GH(["api","-X","DELETE",...])` and `GH(["repo","delete"])` return `[FALLBACK: gh …]` without a call; `PRForBranch` parses `[{"number":12,"title":"Add x"}]` into `#12: Add x`, prints `none` on `[]`, on a Runner error and on invalid JSON.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./context/ghprobe`
- [ ] **Step 3: Implement** — Allowlist table by noun and verb; JSON decode of the `--json number,title` output; 15 s timeout.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./context/... && go test ./context/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(context): read-only gh probes'`

**Validation:** `go test ./context/ghprobe`

## Phase 3: Sources and the prime use case

### 3.1: Session and document sources (wave B)

**Files:**
- Create: `mind-palace/context/sources/sessionsource/sessionsource.go`
- Create: `mind-palace/context/sources/docsource/docsource.go`
- Test: `mind-palace/context/sources/sessionsource/sessionsource_test.go`
- Test: `mind-palace/context/sources/docsource/docsource_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `session.Store`, `query.Service`, `memsession` (tests); `service.Palace`, `config.Load`, `store.OpenAll`, `memstore` (tests)
- Produces: `prime.SessionSource` and `prime.DocSource` implementations (the interfaces are declared in `context/prime` as types in a small `context/prime/ports.go` file created here so 3.1 and 3.2 can proceed in parallel), `docsource.Open(ctx, root) (*DocSource, error)`

- [ ] **Step 1: Write the failing tests** — `sessionsource`: seed `memsession` with three sessions (two cwds, one older) and Bash hook events; `Current(ctx, cwd)` returns the newest session for that exact cwd with its top commands, `nil` for an unknown cwd, and the error from a failing `connect`. `docsource`: with a `memstore`-backed palace holding stories in every status and plans in `pending`/`in_progress`, `ActiveStories` returns pending, ready and in_progress sorted by id, `ActivePlans` returns in_progress first; `Open` on an empty temp dir returns `nil` and creates nothing; `Open` on a dir with `docs/plans` returns a palace.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./context/sources/...`
- [ ] **Step 3: Implement** — `sessionsource` uses `query.Service.FindSessions` with an anchored, quoted cwd regex and `SessionContext`; `docsource` uses `Service.List` with status filters and `GetContent`; `Open` checks for the config file and the plans dir before `config.Load` and `store.OpenAll`.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./context/... && go test ./context/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(context): session and document sources for prime'`

**Validation:** `go test ./context/sources/...`

### 3.2: Prime collector (wave C)

**Files:**
- Create: `mind-palace/context/prime/prime.go`
- Modify: `mind-palace/context/prime/ports.go`
- Test: `mind-palace/context/prime/prime_test.go`

**Depends on:** 1.2, 1.4, 2.1, 2.2, 3.1

**Interfaces:**
- Consumes: every probe, both sources, `session/hostinfo`, `session.ReadAccountID`, `session.EncodeProjectPath`
- Produces: `Collector`, `Collect(ctx) context.Report`, `Options{NoStore, NoDocs bool}`

- [ ] **Step 1: Write the failing tests** — A temp git repository with README.md (90 lines), AGENTS.md, a `docs/plans` dir, plus `memsession` and `memstore`-backed sources. Assert the eleven section names in order; README truncated at 80 with `… (10 more lines)`; `SPEC.md: no`; `LOG:main` labelled with the detected branch; `DIFF-STAT` prints `no diff (on main)` on the default branch; `STORIES` lists the active story line; `PLANS` shows the in-progress plan and its first 100 lines; `SESSION` prints the seeded session and commands, `store unavailable` with a failing connect, `none` with no match; `CLUED` prints `not running` for a closed port and `healthy` against a test listener serving `/health`; a nil `Docs` prints `no plans dir`; a section whose probe blocks past the budget prints `[FALLBACK: SESSION]` while the rest render; host line omits ip and mac.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./context/prime`
- [ ] **Step 3: Implement** — One builder per section, each wrapped in `withBudget(ctx, name, fn)` that recovers panics and maps timeouts to the section fallback; health check via `net/http` with a 1 s client; the report assembled in fixed order.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./context/... && go test ./context/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(context): prime collector'`

**Validation:** `go test ./context/prime`

## Phase 4: Entrypoints

### 4.1: CLI group (wave D)

**Files:**
- Create: `mind-palace/cli/context.go`
- Modify: `mind-palace/cli/cli.go`
- Test: `mind-palace/cli/context_test.go`

**Depends on:** 3.2

**Interfaces:**
- Consumes: every probe, `prime.Collector`, `App.sessionConfig`, `openSessionStore`
- Produces: `mind-palace context …` with the subcommand list in the design; `(*App).contextCmd()`

- [ ] **Step 1: Write the failing tests** — Through `App` with buffers and a temp cwd (`--root`): every subcommand prints its answer plus one newline and exits 0; `context git push` prints the fallback and exits 0; `context nonsense` prints `unknown subcommand: nonsense` and exits 0; `--limit 2` truncates `list-files`; `file-head --lines 3`; `find-files --name '*.go' --count`; `prime --no-store --no-docs` prints the eleven sections with `SESSION` reading `store disabled`; `session` prints `none` with the in-memory store empty (reusing `session_test.go`'s store override); `project-path` encodes the root; `daemon-status` on a closed port.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./cli -run Context`
- [ ] **Step 3: Implement** — Cobra group with `DisableFlagParsing` on `git` and `gh`, manual `--limit` extraction (as `extractLimit` did), `SilenceErrors`/`SilenceUsage`, and `RunE` always returning nil after writing the answer; register in `cli.go` next to `sessionCmd`.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./cli && go test ./cli`
- [ ] **Step 5: Commit** — `git commit -m 'feat(cli): context command group'`

**Validation:** `go test ./cli -run Context`

### 4.2: MCP tool (wave D)

**Files:**
- Modify: `mind-palace/session/sessionmcp/sessionmcp.go`
- Test: `mind-palace/session/sessionmcp/sessionmcp_test.go`

**Depends on:** 3.2

**Interfaces:**
- Consumes: `prime.Collector`, the tools' lazy store
- Produces: `prime_context` tool; `Instructions` mentions it; `Tools.New` gains a `Prime func(ctx, cwd string) (string, error)` option so the CLI wires the collector

- [ ] **Step 1: Write the failing tests** — Over the in-memory transport: `tools/list` includes `prime_context` with `readOnlyHint`; calling it with a temp cwd returns text starting with `=== ENV ===`; a non-existent cwd returns a tool error; the existing six-tool tests still pass.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/sessionmcp`
- [ ] **Step 3: Implement** — Seventh `toolSpec`; the handler resolves `cwd` (default `os.Getwd`) and calls the injected collector.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./session/... && go test ./session/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(sessionmcp): prime_context tool'`

**Validation:** `go test ./session/sessionmcp`

## Phase 5: Plugin glue and docs

### 5.1: Skill, command, README, CI, consistency test (wave E)

**Files:**
- Create: `skills/clued-context/SKILL.md`
- Create: `commands/clued-prime.md`
- Create: `mind-palace/cli/context_skill_test.go`
- Modify: `README.md`
- Modify: `.github/workflows/mind-palace.yml`

**Depends on:** 4.1, 4.2

**Interfaces:**
- Consumes: the registered subcommand list from `contextCmd()`
- Produces: `skills/clued-context/SKILL.md` with a `## Probes` table (`| Probe | Arguments | Output |`), `commands/clued-prime.md`, README `## Context` section, workflow path filters

- [ ] **Step 1: Write the failing test** — `context_skill_test.go` reads `../../skills/clued-context/SKILL.md`, extracts the first column of the `## Probes` table, and asserts set equality with the names of `contextCmd().Commands()`.
- [ ] **Step 2: Run it and confirm it fails** — `go test ./cli -run SkillProbes`
- [ ] **Step 3: Write the skill, the command and the docs** — `SKILL.md` front matter (`name: clued-context`, a description that triggers on starting or resuming work in a repository and on needing repository facts), usage via `${CLAUDE_PLUGIN_ROOT}/bin/clued context`, the rule to run `prime` first, the probe table, the sentinel list, and the note that output is data. `clued-prime.md` runs `prime` and asks for a short summary. README gains the Context section after Session commands. Workflow path filters gain `skills/**` and `commands/**`.
- [ ] **Step 4: Run the test, gofmt and vet** — `gofmt -l . && go vet ./cli && go test ./cli`
- [ ] **Step 5: Commit** — `git commit -m 'feat(plugin): clued-context skill and /clued-prime command'`

**Validation:** `go test ./cli -run SkillProbes`

## Phase 6: Verification

### 6.1: Parity and end-to-end (wave F)

**Files:**
- Create: `mind-palace/testdata/context-repo/README.md`
- Create: `mind-palace/testdata/context-repo/expected/*.txt`
- Create: `mind-palace/context/parity_test.go`

**Depends on:** 5.1

**Interfaces:**
- Consumes: `wiz-context.js` (run once by the implementer with Node, outside the repository, to record expected outputs)
- Produces: Recorded outputs for every ported probe over the fixture tree and a Go test comparing them

- [ ] **Step 1: Record the reference outputs** — Over `testdata/context-repo` (a committed tree with docs, stories, a package.json and a go.mod; git-dependent probes run against a repository the test creates), run each ported probe with `wiz-context.js` and save stdout under `expected/<probe>[-<case>].txt`.
- [ ] **Step 2: Write the parity test and confirm it fails where expected** — `parity_test.go` runs every case through the Go probes and diffs against the recorded files; cases that a decision changes on purpose (allowlist, default branch) are listed with their reason and asserted against the new behaviour. — `go test ./context -run Parity`
- [ ] **Step 3: Fix the deviations** — Adjust probes until the parity test passes.
- [ ] **Step 4: Run the full gate** — `test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...`
- [ ] **Step 5: Run it live** — With `devRepoPath` set, in the clued checkout: `bin/clued context prime`, `bin/clued context prime --no-store`, `bin/clued context git log --oneline --limit 3`, `bin/clued context gh-pr-for-branch`; in an empty temp directory `bin/clued context prime` prints sentinels and creates no files; in Claude Code, `/clued-prime` and the `prime_context` tool return the report.
- [ ] **Step 6: Commit** — `git commit -m 'test(context): parity with the reference collector and live verification'`

**Validation:** `go test ./context -run Parity`

## Acceptance Criteria

- [ ] `bin/clued context <probe>` exists for every `wiz-context.js` subcommand with the same name, arguments and sentinel outputs, and always exits 0.
- [ ] `git` and `gh` passthrough refuse anything outside the read-only allowlists with a `[FALLBACK: …]` line and no process spawned.
- [ ] `bin/clued context prime` prints the ENV, CLUED, FILES, BRANCH, LOG:<default>, LOG:HEAD, DIFF-STAT, STORIES, PLANS, SESSION and BUILD sections, with the default branch detected.
- [ ] The ENV and SESSION sections carry what clued already captures: cwd, project path, git origin and branch, account id, host summary without ip or mac, and the most recent captured session for the cwd with its top commands.
- [ ] STORIES and PLANS come from mind-palace, and prime creates no directory in a project that has no mind-palace config or plans directory.
- [ ] `prime` degrades to sentinels without MongoDB, without `gh`, outside a git repository, and finishes within its budget when a probe hangs.
- [ ] `prime_context` is listed by the clued MCP server and returns the same report.
- [ ] `skills/clued-context/SKILL.md` lists every registered probe and nothing else, enforced by a test; `/clued-prime` runs the report.
- [ ] The parity test passes against recorded `wiz-context.js` outputs, with every intentional deviation listed with its reason.
- [ ] `gofmt -l .` is empty, `go vet ./...` and `go test -race ./...` pass locally and in CI; no new Go dependency.

## Self-Review

| Requirement | Delivered by |
|---|---|
| Collect everything clued already collects (cwd, project path, git origin/branch, account, host, session) | 3.1, 3.2 (ENV, CLUED, SESSION sections) |
| Every reference probe, same contract | 1.2, 1.3, 1.4, 2.1, 2.2, 4.1, 6.1 |
| Composite prime for skills | 3.2, 4.1, 5.1 |
| Stories and plans from mind-palace | 3.1, 3.2 |
| Safe to run without a prompt | 2.1, 2.2 allowlists; 1.1 argv-only runner |
| Usable from a skill and from MCP | 4.2, 5.1 |

**Placeholder scan:** No section leaves a stub: every probe has a full implementation and tests in its section; `line-summary`-style disabled placeholders do not exist here.
**Residual risk:** Exact parity on `find-files` ordering depends on directory read order in Node versus Go; the parity test sorts both sides. `gh` behaviour differs across versions; `PRForBranch` only depends on `--json number,title`.

## Next Steps

1. Boss reviews the decisions and the open questions, in particular the `gh api` allowlist entry and the PLANS status rule.
2. Move the plan to `ready`, create the `wip/context-collector` integration branch, and dispatch wave A.
3. After 6.1, decide whether to add the `SessionStart` injection and the snapshot event as a follow-up plan.
