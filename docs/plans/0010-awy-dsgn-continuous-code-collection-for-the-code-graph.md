---
id: 0010-awy
title: Continuous code collection on the clued server for the code graph
type: dsgn
status: pending
priority: "3"
effort: XL
created: "2026-10-16T19:52:58.427Z"
updated: "2026-10-19T17:26:10.498Z"
tags:
  - code-graph
  - server
  - cloud
  - capture
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
progress:
  "1":
    status: pending
  "1.1":
    status: pending
  "1.2":
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
  "3.3":
    status: pending
  "4":
    status: pending
  "4.1":
    status: pending
  "5":
    status: pending
  "5.1":
    status: pending
---

# Continuous code collection on the clued server for the code graph

## Summary

**Goal:** In remote mode, every repository/worktree/branch a Claude Code session works in is mirrored to the clued server (file contents plus a per-worktree tree and an ordered change log) as files are added, edited and deleted, ready for a later FalkorDB code-graph indexer, with nothing readable back out yet.

**Problem:** clued captures sessions, transcripts and Claude's own artifacts, but never the code those sessions change. The FileTracker enricher only records paths from PreToolUse events, and the catch-up artifacts are Claude's files (subagents, tool results, file-history), not the repository. A code graph (FalkorDB, chosen in the graph-database comparison) needs the current source of each worktree and branch on the server, kept up to date continuously, before any indexing or search can be built.

**Approach:** A content-addressed sync, the way git transfers objects: the client keeps a stamp cache of the worktree's files, sends a manifest of path to SHA-256 entries, uploads only the blobs the server reports missing, then commits the change set to a per-worktree tree and an append-only change log. It runs inside `hook relay` (remote mode) on edit, Bash, SessionStart, Stop and worktree events, bounded per run like catch-up, behind an opt-in config key. The server side is a new `code.Store` port with memory and Mongo stores, a conformance suite, and a write-only HTTP API under `/api/v1/projects/{project}/code/` namespaced like storeapi; clued-cloud wires it behind a plan entitlement, a byte quota, metrics and purgers.

---

# Part 1 — Design

## Current State

- `hooks/event-relay` runs async on all 29 hook events and, in remote mode, pipes each to `scripts/clued hook relay` (`cli/hook_relay.go`, 5s `relayBudget`), which runs `ingest.Service.Handle`, then `catchup.Run`, then flushes the WAL.
- Hook events carry `cwd`, `session_id`, `hook_event_name`, `tool_name` and `tool_input.file_path`; branch and origin are looked up from `cwd` (`session/gitinfo`). Nothing detects the worktree root or HEAD commit.
- `session/catchup` already has the patterns this needs: a per-key state file written by temp-file-and-rename, an `O_EXCL` lock that goes stale after 60s, `FileStamp{size, mtime}` change detection, and a `refused` list for 413s retried only when the file changes.
- Project identity is `clientcfg.ResolveProject` (the `remote.project` override, then git origin via `projects.Slug`, then the directory name); the server namespaces project stores through `CluedOptions.NamespaceCtx` (`u<account>__<slug>` / `w<workspace>__<slug>` in clued-cloud).
- `session/redact` redacts UTF-8 content and drops whole sessions whose cwd matches `excludePaths`; nothing reads `.gitignore`.
- Session blobs live in the Mongo `blobs` collection keyed by session; there is no project-scoped content store and no GridFS. The request body cap is 16 MiB (`sessionapi.DefaultMaxBody`, the nginx config).

## Decisions

- **Content-addressed blobs (SHA-256 of the redacted content) plus a manifest round trip.** — Branches, worktrees and repeated reconciles share almost every file; the server stores each version once and the client uploads only what is new, which keeps a continuous sync cheap enough to run on hook events.
- **An append-only change log next to the current trees.** — The future FalkorDB indexer needs to update the graph incrementally and to rebuild it from scratch; a sequenced log of upserts and deletes per (worktree, ref) gives both without the transport knowing anything about graphs.
- **Write-only HTTP API; reads exist only on the Go port.** — Meets 'collect, don't expose' and keeps the attack surface to writes, while tests and the in-process indexer can still read.
- **Run inside `hook relay`, budgeted, with state and a lock modelled on catch-up.** — Remote mode already has no resident process; hook events are exactly when Claude changes files, and the catch-up patterns (stamps, lock, refused list, atomic state) are proven.
- **Use git for enumeration (`ls-files -co --exclude-standard`, `status --porcelain`).** — Respects `.gitignore` for free, sees untracked new files, and catches renames and deletes done through Bash; repositories without git aren't synced (no stable project identity).
- **Opt-in, off by default, with a per-repository opt-out and a cloud entitlement.** — Uploading source code is a much bigger privacy step than session capture; users and operators have to choose it.
- **Redact before hashing, and never upload denied or binary content.** — The server should never hold a secret the client could have caught; hashing the redacted bytes keeps the content address honest.
- **The code graph will be partitioned as one base graph per namespaced project plus overlays per (worktree, ref), with file-local facts cached by content (Option B plus the easy part of C; see "Partitioning for the indexer").** — A full graph per branch per user repeats almost the same graph many times and re-indexes everything on a new branch; a base plus overlays costs storage and indexing proportional to the diff. File-local facts depend only on a file's bytes, so caching them by SHA-256 lets every branch and user share one parse. Only cross-file resolution, which depends on the whole tree, is per layer. Collection records what this needs now (owner, committed vs uncommitted, the default branch) so nothing has to be re-collected when the indexer ships.
- **The server stamps the owning account on worktrees and changes.** — In a workspace several members write into one namespace (`w<workspace>__<slug>`); overlays must belong to someone so their visibility can be decided, and the owner has to come from the token, never from the request body.
- **Each entry records git's blob id and whether it differs from HEAD.** — The base graph must be built from committed content only, never from someone's in-progress edits; the SHA-256 of redacted content can't tell committed from uncommitted, git's blob id and a dirty flag can.
- **Three layers, not two: base (default branch), branch (a branch at its pushed commit, shared), session overlay (one checkout's unpushed commits and uncommitted edits, private).** — Two people, or one person on two machines, often work on the same branch at once. Each sees the branch as pushed plus their own work on top; an overlay per (worktree, ref) straight on the base would duplicate the shared branch in every overlay and couldn't tell what is shared from what is still one person's. Splitting at the pushed commit gives a natural sharing rule: pushed is shared, unpushed is private.
- **Each entry also records whether it differs from the upstream commit (`unpushed`), each change records the upstream commit, and the server records which session last wrote each entry.** — These are the facts that place a file in the branch layer or the session overlay, and attribute overlay files to the session that produced them.

### Rejected alternatives

- **Push git objects (`git push` to a server-side bare repository or bundle).** — Misses uncommitted edits, which are what a session is producing; needs a git server in the stack and its own auth.
- **Reuse session blobs (`PUT /api/v1/session/blobs`).** — Session-scoped and keyed by session id; code belongs to a project and outlives sessions, and the account-scoped session API has no project namespace.
- **Write straight into FalkorDB from the client or the transport.** — Couples collection to the graph schema and parser choices that aren't made yet; the server stays a bare transport (CLAUDE.md conventions).
- **A resident fsnotify watcher.** — Remote mode deliberately has no resident process; hooks cover Claude's edits and the reconciles cover the rest.
- **Store blobs in plan/story stores (`store.Store`).** — That port persists Markdown documents with front matter and is implemented by many backends; code needs content addressing and a log, and only needs Mongo and memory now.

## Design

### Identity: project, worktree, ref

- **Project**: the existing slug (`clientcfg.ResolveProject`), so the server namespaces it exactly like plans and stories (`u<account>__<slug>` in clued-cloud). A directory-name slug (no origin, no override) is **not synced**: it is too weak an identity to merge code under.
- **Worktree**: `wt_` + the first 16 hex characters of SHA-256 of `host + "\x00" + absolute worktree root` (`git rev-parse --show-toplevel`). Linked git worktrees get their own id; the raw path and host are sent as metadata (`root`, `host`) because they are already captured on sessions.
- **Ref**: the branch (`git branch --show-current`), or `detached@<short sha>`; plus the HEAD commit (`git rev-parse HEAD`) on every commit of changes. A branch switch is just a new ref on the same worktree: the tree is re-reconciled and unchanged blobs dedupe.
- The server keys trees by (worktree, ref), so switching back to a branch resumes its tree instead of rewriting everything.
- **Owner**: the account of the token that wrote it (`auth` principal), stamped by the server on the worktree and on every change; a value sent by the client is ignored. In a personal namespace it always equals the namespace's user; in a workspace namespace it tells members' worktrees apart. A worktree keeps the owner that created it: a write to it from another account is 404, as an unknown worktree.
- **Default branch**: `gitprobe.DefaultBranch` (origin's HEAD, falling back to `main`/`master` when that ref is missing) sent with the worktree, so the indexer knows which branch the base graph follows.
- **Committed or not**: every entry carries `git_oid`, the blob id of that path in HEAD (`git ls-tree -r -z HEAD`, empty for untracked files), and `dirty`, true when the working file differs from HEAD (it appears in `git status --porcelain`, modified, added or untracked). Because the SHA-256 names the *redacted* bytes, `git_oid` plus `dirty: false` is what proves an entry is the committed content.
- **Pushed or not**: every change carries `upstream`, the commit the branch's upstream points at (`git rev-parse @{upstream}`, empty when the branch has never been pushed), and `ahead`, the number of local commits not on it. Every entry carries `unpushed`, true when its path differs between the upstream commit and HEAD (`git diff --name-only -z @{upstream} HEAD`) or the branch has no upstream. An entry with `dirty: false` and `unpushed: false` is exactly the pushed content of that branch.
- **Session**: the server sets each tree entry's `session` from the `session_id` of the change that last wrote it, so every overlay file can be attributed to the Claude Code session that produced it. Worktrees record the sessions seen on them with first and last activity (`SessionStart`, the last change, `SessionEnd`).

### Partitioning for the indexer

This plan only collects, but the layout is chosen now so the collected data fits it without re-collecting.

**Storage (this plan).** Namespace (`u<user>__<slug>` / `w<workspace>__<slug>`, the tenant boundary) → project → worktree (one checkout on one machine, with its owner) → ref → tree of entries; blobs are shared across the whole namespaced project by SHA-256, and the change log is per namespaced project.

**Graph (the indexer plan, recorded here as the decision).**
- **One FalkorDB graph per namespaced project** (the graph key is the namespace, e.g. `u…__github.com__owner__repo`), so tenancy stays physical: nothing in one key can reach another.
- **Base layer**: the default branch at its pushed commit, built only from entries with `dirty: false` and `unpushed: false` from worktrees on the default branch. Any member's worktree can supply it; it advances when a newer upstream commit is seen. In a workspace the base is shared by every member.
- **Branch layers**: one per (ref, upstream commit) for every other pushed branch, holding only the files whose pushed content differs from the base (by `git_oid`), plus deletions. Built from clean, pushed entries of any worktree on that branch, so two people or two machines on the same branch share one branch layer. It advances when a newer upstream commit is seen; the previous one is kept until no worktree's overlay sits on it.
- **Session overlays**: one per (worktree, ref), holding what that checkout has that the branch layer doesn't: files changed by unpushed commits (`unpushed: true`) and uncommitted edits (`dirty: true`), plus deletions. Every file is tagged with the session that last wrote it. It sits on the branch layer of its upstream commit, or directly on the base when the branch has never been pushed. Committing moves nothing (the file stays unpushed); pushing moves the files into the branch layer and they drop out of the overlay.
- **Why per worktree and not per session**: sessions in the same checkout share one disk, so they see the same files; one overlay per worktree is exactly what any of them sees, and the `session` tag on each file answers "what did this session change". Different checkouts (another person, or the same person on another machine or in a cloud container) get separate overlays on the same branch layer.
- **A new branch with no edits costs nothing; a feature branch costs its diff once (branch layer); each person's in-flight work costs only that work (overlay).**
- **File-local facts cached by content**: definitions, imports and the unresolved references a file makes are a pure function of its bytes, so they're parsed once per blob SHA-256 and reused by the base and every overlay, across branches and members.
- **Cross-file edges per layer**: resolving a reference to a definition depends on the whole tree, so CALLS / REFERENCES / IMPLEMENTS edges are resolved for the base and re-resolved in an overlay only for the files it touches and those that reference them.
- **A query for a session's view** (session → its worktree and ref) stacks the layers: base, then the branch layer, then the overlay; each layer hides the files the layer above replaces or deletes. Nodes and edges carry a `layer` property (`base`, `branch:<ref>@<commit>` or `wt:<worktree>:<ref>`) and `File.path`, which is what that filter uses. A branch view (no session) is base plus branch layer.
- **Two checkouts on one branch**: both overlays sit on the same branch layer and never see each other's files, and a file both are editing appears once in each overlay with each owner's content. A view across a user's own machines (the same account's overlays on one branch) is an indexer feature for later; collection already has what it needs (owner per worktree).
- **Idle overlays**: an overlay whose worktree has no session activity for 14 days and nothing unpushed or dirty is dropped from the graph; the collected data stays in the store, so it can be rebuilt.
- **Visibility**: the base and branch layers are visible to everyone who can read the project (it's pushed code); a session overlay is visible to its owner only, by default (see Open Questions).

### Server model and the `code.Store` port

Package `mind-palace/code` (model and port only; no rules beyond validation):

```go
type Blob struct { SHA256 string; Size int64; Content []byte } // content after client redaction
type Entry struct { Path string; SHA256 string; Size int64; Mode string; Lang string; GitOID string; Dirty, Unpushed bool; Session string } // Path: clean, slash-separated, filepath.IsLocal; GitOID: blob id in HEAD ("" if untracked); Session set by the server from the writing change
type Worktree struct { ID, Owner, Root, Host, Origin, DefaultBranch string; Refs []string; Sessions []SessionSeen; Removed bool; UpdatedAt time.Time } // Owner stamped by the server
type SessionSeen struct { ID string; FirstAt, LastAt time.Time; Ended bool }
type Change struct { Seq int64; Owner, Worktree, Ref, Head, Upstream string; Ahead int; SessionID string; Upserts []Entry; Deletes []string; Full bool; At time.Time } // Owner stamped by the server

type Store interface {
    MissingBlobs(ctx, shas []string) ([]string, error)
    PutBlob(ctx, b Blob) error                       // verifies SHA256(content) == b.SHA256
    PutWorktree(ctx, w Worktree) error
    Commit(ctx, c Change) (seq int64, err error)      // applies to the (worktree, ref) tree and appends to the log atomically; Full replaces the tree
    RemoveWorktree(ctx, id string) error              // marks removed, appends a tombstone change
    TouchSession(ctx, worktree, sessionID string, ended bool) error // records session activity on a worktree
    // read side, for codetest and the future indexer only (no HTTP route):
    Worktrees(ctx) ([]Worktree, error)
    Tree(ctx, worktree, ref string) ([]Entry, error)
    Changes(ctx, afterSeq int64, limit int) ([]Change, error)
    Blob(ctx, sha string) (Blob, error)
    Usage(ctx) (blobs int64, bytes int64, err error)
}
```

- One `Store` per namespaced project, opened by `CluedOptions.Code(ctx, namespacedProject)`, exactly like `Open` for plans and stories.
- **Mongo** (`code/mongocode`): collections `code_blobs` (`_id` sha, size, content), `code_trees` (`{worktree, ref, path}` unique, sha, size, lang, git_oid, dirty, unpushed, session; a second index on `git_oid` for building the base and branch layers), `code_worktrees` (with `owner`, `default_branch`, `sessions`), `code_changes` (`seq` from a per-project counter document, indexed). `Commit` runs in a transaction when the deployment supports one, otherwise orders the writes so a retried commit is idempotent (see Error handling). Collection names are prefixed by the namespaced project like the existing Mongo store plugin, or carry a `project` field; follow `store/mongostore`'s convention.
- **Memory** (`code/memcode`) for tests and `--store memory`.
- The change log is the contract for the indexer: it replays `Changes(afterSeq)` and fetches blobs; FalkorDB is never written by the transport.

### Transport: `server/codeapi` (write-only)

Mounted by `server.NewClued` under `/api/v1/projects/{project}/code/` when `CluedOptions.Code` is set, behind `auth.Middleware`, with the project passed through `NamespaceCtx` (fail closed, as storeapi). Envelope and error kinds as §1.1/§1.2 of PROTOCOL.md.

| Method and path | Body | Result |
|---|---|---|
| `POST manifest` | `{"shas": ["…"]}` (at most 5,000) | `{"missing": ["…"]}` |
| `PUT blobs/{sha256}` | raw bytes (`Content-Type: application/octet-stream`), at most `maxFileBytes` | `{}`; 400 `invalid` on hash mismatch, 413 `too_large` |
| `PUT worktrees/{id}` | `{root, host, origin, default_branch, ref, head}` | `{}`; 404 if the worktree exists under another owner |
| `POST worktrees/{id}/changes` | `{ref, head, upstream, ahead, session_id, full, upserts: [{path, sha256, size, mode, lang, git_oid, dirty, unpushed}], deletes: [path]}` (at most 5,000 entries) | `{"seq": n}`; 409 `conflict` if an upsert names a blob the server does not hold; 404 for another owner's worktree |
| `PUT worktrees/{id}/sessions/{session_id}` | `{"ended": false}` | `{}`; records session activity (SessionStart, SessionEnd); 404 for another owner's worktree |
| `DELETE worktrees/{id}` | | `{}`; 404 for another owner's worktree |
| `GET capabilities` | | `{"code": {"maxFileBytes": n, "maxBatch": n}}`, so a client can detect the feature and its limits without reading data |

- **No route returns code, paths or trees.** `capabilities` returns limits only. An unknown project is 404, as elsewhere.
- **Owner stamping.** The handler sets `Owner` on worktrees and changes from the request's principal (`auth` account); an `owner` field in a body is rejected as `invalid`. A worktree id already held by another owner answers 404, so one member can neither overwrite nor learn of another member's worktree.
- `git_oid`, `head` and `upstream` are validated as 40 or 64 lowercase hex (SHA-1 or SHA-256 repositories) or empty; an entry with `dirty: false` and an empty `git_oid` is `invalid`, and so is `unpushed: false` with an empty `upstream`. The server sets each upserted entry's `session` from the change's `session_id` (a `session` field in an entry is rejected).
- A server without `Code` returns 404 for the prefix; the client records `unsupported` and stops trying for 24 hours.
- Paths are validated inline (`filepath.IsLocal` on the slash path, no `..`, no NUL, at most 1,024 bytes) at the point of use, as the CodeQL convention requires.
- `server run --code-store mongo|memory|off` (default: the session store's backend) and `CLUED_SERVER_CODE_STORE`; `server init` turns it on in the generated stack (same `mongo:8`).

### Client collector: `session/codesync`

**What is collected.** `git ls-files -z -co --exclude-standard` from the worktree root (tracked plus untracked-but-not-ignored, so `.gitignore` is respected), then filtered:
- size at most `codeSyncMaxFileBytes` (default 1 MiB); larger files are recorded as entries with `sha256: ""` and `size` only, so the graph knows they exist;
- text only: a NUL byte in the first 8 KiB, or invalid UTF-8, skips the content (entry kept, as above);
- a built-in deny list that can't be switched off: `.env`, `.env.*`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `id_rsa*`, `id_ed25519*`, `*.kdbx`, `.npmrc`, `.pypirc`, `.netrc`, `credentials*.json`, `*secret*` under `config/`;
- `excludePaths` (the existing globs, now also matched per file) and `codeSyncExclude` (globs relative to the worktree root);
- symlinks are not followed; submodules are skipped (their own root syncs if a session works in them).

**Redaction.** Content passes through the same `redact` rules as captured sessions (`redactAtCapture`, `redactPatterns`, private spans) **before** hashing, so the SHA names what the server stores and no secret the rules match leaves the machine. `captureContent: false` turns code sync off entirely.

**State** (`<config dir>/codesync/<worktree id>.json`, temp-file and rename, mode 0600; `<id>.lock` with `O_EXCL`, stale after 60s, both modelled on `session/catchup/state.go`):
```json
{"worktree":"wt_…","root":"…","project":"github.com__owner__repo","defaultBranch":"main","ref":"main","head":"…","upstream":"…","ahead":0,
 "files":{"path":{"size":1,"mtime":"…","sha256":"…","gitOid":"…","dirty":false,"unpushed":false,"synced":true}},
 "pending":{"upserts":[…],"deletes":[…]},"lastFull":"…","refused":{"path":"size+mtime"},"unsupportedUntil":"…"}
```

**One sync run** (`Run(ctx, Request{Cwd, SessionID, Paths, Full, Budget})`):
1. Resolve root, ref, head, default branch, project, worktree id; skip if disabled, excluded, the slug is a bare directory name, or `unsupportedUntil` is in the future.
2. If the ref changed since the state, or `Full`, or the last full reconcile is older than 6 hours, enumerate everything; otherwise only `Paths` from the event (plus `git status --porcelain -z` after a Bash call).
3. Re-hash only files whose `(size, mtime)` stamp changed; vanished paths become deletes.
4. Fill `git_oid`, `dirty` and `unpushed`: when HEAD moved since the state, read `git ls-tree -r -z HEAD` once (path → blob id) and recompute `dirty` for every path from `git status --porcelain -z`; when HEAD or the upstream commit moved, recompute `unpushed` from `git diff --name-only -z @{upstream} HEAD` (every path is unpushed when there is no upstream); otherwise only for the paths in this run. A commit or a push changes no file contents, but the affected entries are sent again with their new flags, so the server learns they are now committed or pushed.
5. `PUT worktrees/{id}` once per ref change; `POST manifest` in batches; `PUT blobs/{sha}` for the missing ones; `POST changes` with upserts and deletes (`full: true` on a full reconcile so the server drops paths it no longer sees).
6. Mark entries synced and save the state. Whatever didn't fit the budget stays in `pending` for the next run.

**Budget.** `hook relay` has 5 seconds for everything. Code sync gets what is left after ingest and catch-up, capped at 3 seconds and 8 MiB per run, and always runs last. A full reconcile of a large repo therefore spreads over several hook events. `mind-palace session code-sync --full` runs it with no budget for a first import or a manual repair.

### Triggers in `hook relay`

| Hook event | Work |
|---|---|
| `SessionStart` | `PUT worktrees/{id}/sessions/{session_id}`, then a full reconcile if the ref changed or `lastFull` is older than 6 hours, else the pending queue |
| `SessionEnd` | the pending queue, then `PUT worktrees/{id}/sessions/{session_id}` with `ended: true` |
| `PostToolUse` with `Write`, `Edit`, `MultiEdit`, `NotebookEdit` | the edited path (`tool_input.file_path` / `notebook_path`), validated to sit inside the worktree root |
| `PostToolUse` with `Bash` | `git status --porcelain -z` delta (catches `mv`, `rm`, generators, `git checkout`), and the HEAD and upstream check that refreshes `git_oid` / `dirty` / `unpushed` after `git commit`, `git push`, `git pull`, `git fetch` or `git reset` |
| `FileChanged` | the reported path |
| `Stop`, `SubagentStop` | `git status` delta plus the pending queue |
| `CwdChanged` | resolve the new worktree; reconcile it like SessionStart |
| `WorktreeCreate` | register the worktree and queue a full reconcile |
| `WorktreeRemove` | `DELETE worktrees/{id}` and drop the local state |

- `PostToolUseFailure` is ignored (nothing was written).
- `CLUED_NO_CAPTURE=1` and the existing cloud-container rules apply unchanged; errors go to `relay.log` and never fail the hook (`hook relay` prints nothing and exits 0).

### Configuration

`~/.claude/plugins/data/clued/config.json` (existing keys unchanged):

| Key | Env | Default | Meaning |
|---|---|---|---|
| `codeSync` | `CLUED_CODE_SYNC` | off | turns collection on (remote mode only) |
| `codeSyncMaxFileBytes` | `CLUED_CODE_SYNC_MAX_FILE_BYTES` | 1048576 | content cap per file (the server's `capabilities` limit wins if lower) |
| `codeSyncExclude` | | `[]` | extra globs relative to the worktree root |

- `codeSync`, `codeSyncMaxFileBytes` and `codeSyncExclude` join `pulledConfigKeys`, so a server or clued-cloud can turn collection on for an account through `/api/v1/me/config` and `session config-pull`.
- `mind-palace.config.yaml` (per repository): `codeSync: false` opts that repository out, and wins over the machine config.
- `/clued-setup` asks about it in remote mode and says plainly that the source of every repository Claude works in will be uploaded.

### clued-cloud

After the public module lands on main, pin it (`go get github.com/robbiebyrd/clued/mind-palace@<sha>`), then:
- `cloud.go`: pass `Code: c.openCode` to `server.NewClued`, opening `mongocode` under `c.namespace` (the same fail-closed namespacing and workspace-membership check as `Open`), recording the project with `RecordProject` / touching the workspace project.
- `account/plans.go`: `Limits.CodeSync bool` (off in every default plan until the indexer ships; on for internal accounts) and `Limits.CodeBytes int64` quota per account, checked in `codeapi`'s write path through a cloud-supplied `BeforeCodeWrite` hook (or a decorator store) that returns `LimitError`; refused writes are 403 `forbidden` for no entitlement and 413 `quota` when full, and the client backs off on both.
- Metadata-only accounts (`captureContent` false in the account policy) are refused, the same way `captureStore` drops content.
- `metrics`: counters `code_blobs_stored`, `code_bytes_stored`, `code_changes_committed`, `code_writes_refused` (labels from a closed set: `reason=entitlement|quota|metadata_only|too_large|invalid`) with help text in `counterHelp`.
- Purgers: `purgeCode` registered in `Purgers` (account deletion), `WorkspacePurgers` (workspace deletion) and the `ContentPurger` path; retention drops `code_changes` older than the account's retention window but keeps the current trees and the blobs they reference.
- Audit: turning code sync on for an account (pulled config) records an `Audit*` kind with a sentence in `web/security.go`, since it changes what leaves the user's machine.

### Error Handling

- Network or 5xx: the run stops, the state keeps the work in `pending`, the next hook event retries. Nothing is written to the WAL (code state lives only in the codesync state file).
- 413 `too_large` on a blob: the path goes to `refused` with its stamp and is retried only when the file changes (as catch-up does); the entry is still committed with `sha256: ""`.
- 409 on commit (a blob the server lacks, e.g. after a server-side purge): clear the `synced` marks for those paths and re-upload next run.
- 404 on the prefix (server without code support) or 403 (no entitlement): set `unsupportedUntil` 24 hours ahead and stay quiet; 413 `quota`: same back-off, plus one line in `relay.log`.
- `Commit` is idempotent per (worktree, ref, paths, shas): a retry after a lost response re-applies the same upserts and appends at most a duplicate log entry, which the indexer tolerates because upserts are absolute.
- A stale lock (older than 60 s) is taken over; a held lock means another relay is syncing this worktree, so this run skips.
- Git missing or the cwd not in a repository: skip silently.

### Edge Cases

- Detached HEAD: ref `detached@<short sha>`; going back to a branch resumes that branch's tree.
- Rebase, `git checkout` and `git stash` through Bash: the `git status` delta after the Bash call plus the ref/HEAD check trigger the right reconcile.
- Renames: a delete plus an upsert of the same SHA (no blob upload).
- Files edited outside Claude Code: picked up by the next SessionStart, Stop or Bash reconcile, or by the 6-hour full reconcile.
- Several sessions in one worktree: the per-worktree lock serializes them; the stamp cache is shared.
- Two machines with the same repository: different worktree ids (host is part of the id), same project, deduped blobs.
- Origin changes (fork, rename): a new slug and a fresh namespace; the old one stays until purged.
- A file that becomes ignored: the full reconcile deletes it on the server.
- Very large monorepos: the first import is spread over hook runs or done once with `session code-sync --full`; the deny list and `codeSyncExclude` keep vendored trees out (`vendor/`, `node_modules/` are normally ignored already).
- Generated files that are not ignored are synced; that's intended, since the graph may want them, and `codeSyncExclude` removes them.
- Members with different redaction rules: the same committed file (one `git_oid`) can arrive as two SHA-256s. The base builder picks one blob per `git_oid`, preferring the one whose content hash the most members sent, and never mixes them; overlays always use their owner's own blob.
- A dirty file whose bytes equal HEAD again (an edit undone): `git status` no longer lists it, so it goes back to `dirty: false` with its `git_oid` and drops out of the overlay.
- A shallow clone or a default branch missing locally: `default_branch` falls back to `main`/`master`, or is empty; the indexer then can't build a base from that worktree, which is fine as long as another worktree supplies one.
- SHA-256 git repositories (`objectformat=sha256`): `git_oid` is 64 hex characters, which validation accepts.
- Two people (or two machines) on one branch: separate worktrees, so separate overlays over one shared branch layer. When one pushes, the branch layer advances; the other's overlay still sits on the older upstream commit until they pull, which keeps their view consistent with their disk.
- A push from outside Claude Code (a terminal, an IDE): the next reconcile sees the upstream move and re-sends the affected entries with `unpushed: false`.
- An upstream that was force-pushed or rebased: `unpushed` is recomputed against the new upstream commit, so files can move back into the overlay; nothing on the server is rewritten except those entries' flags.
- A branch that tracks a differently named upstream, or none: `upstream` is whatever `@{upstream}` resolves to; with none, everything the branch has beyond the base is in the overlay.
- Several sessions at once in one checkout: one overlay, each file tagged with the session that last wrote it; a session that only reads leaves no tags.
- A Claude Code cloud container: a new host, so a new worktree and overlay for every container; it goes idle and drops from the graph once the container is gone.

## Open Questions

- Should members of a workspace be able to opt in to sharing their session overlays (live, unpushed work) with each other? The layering already makes pushed work shared (branch layers) and unpushed work private (overlays); this plan only records owner and session, so either answer works without re-collecting. (owner: Robbie)

## Considerations

- **Security:**
- Source code is sensitive: off by default, explicit opt-in in `/clued-setup`, per-repository opt-out, cloud entitlement, audit event when an account turns it on.
- Owner is stamped from the token and a worktree can be written only by its owner, so a workspace member can't overwrite or probe another member's checkout.
- Built-in deny list for secret-bearing files, `.gitignore` respected, `excludePaths` per file, redaction rules applied before upload, binary content never uploaded.
- Tenancy: the code store is opened only through `NamespaceCtx` (fail closed), so code from one account or workspace never lands in another's namespace; unknown projects are 404.
- No read route, so a leaked client token can write code into the account but cannot read any back.
- Hash verification on `PUT blobs/{sha}` prevents a client poisoning another file's content address; paths are validated inline at the point of use.
- State files are mode 0600 and hold paths and hashes, never content or the token; nothing logs the token.
- **Performance:**
- The stamp cache makes an edit-triggered run one `stat`, one hash and three small requests; reconciles re-hash only changed stamps.
- The 3 s / 8 MiB budget per relay run and the `pending` queue keep hooks fast on large repositories; `session code-sync --full` does a first import without the budget.
- Manifest and change batches are capped at 5,000 entries; blobs at `maxFileBytes`, well under the 16 MiB body and Mongo document limits.
- Mongo indexes: `code_trees {worktree, ref, path}` unique, `code_trees {git_oid}`, `code_changes {seq}`, `code_blobs _id`.
- `git ls-tree -r HEAD` runs only when HEAD moves; otherwise `git_oid` comes from the state file, so an edit-triggered run adds no git call beyond the existing status check.
- Server load is bounded per account by the cloud quota and the existing rate limiting at the front-end.

## Testing

- `code/codetest` conformance suite: blob verification, missing-blob diff, commit applies upserts/deletes, `Full` replaces the tree, refs are isolated, the log is ordered and replayable, remove-worktree tombstones, idempotent retries, path validation, `git_oid`/`dirty`/`unpushed` round-trip, `upstream` and `ahead` on changes, `session` set from the writing change, `TouchSession` start and end, `Worktrees` lists owner, default branch and sessions. `memcode` and `mongocode` (with `MIND_PALACE_TEST_MONGODB_URI`) pass it; `remotecode` passes it over `httptest` against `codeapi` (reads in the suite go through the server-side store handle, since the API has none).
- `codeapi` tests: auth required, namespacing through `NamespaceCtx` (two accounts never see each other's trees), 404 when `Code` is nil, size and batch limits, hash mismatch, no GET route returns content; the owner comes from the token (a body `owner` is rejected), and two members of one workspace namespace get 404 on each other's worktrees.
- `session/codesync` unit tests on temporary git repositories: enumeration respects `.gitignore`, deny list, binary skip, size cap, redaction before hashing, stamp cache, ref switch, rename, delete, budget and `pending`, lock takeover, back-off on 404/403/413; `git_oid` and `dirty` for clean, modified, staged, untracked and reverted files, refreshed after `git commit` and `git reset`; `unpushed` for a branch with no upstream, ahead of it, after `git push` (all back to false) and after a force-pushed upstream; default branch from origin's HEAD and the fallback; two clones of one remote on one branch produce two worktrees whose clean, pushed entries agree.
- `plugin/` tests (real bash hooks, real server in the test, `jq` required): a session's Write/Edit/Bash/Stop events produce the expected trees and change log on the server; with `codeSync` off nothing is sent.
- clued-cloud: entitlement and quota refusals, metadata-only refusal, purgers remove code for deleted accounts and workspaces, metrics counters increment.

## Scope

**In scope:**

- A `code` domain package in `mind-palace`: model (worktree, tree entry, blob, change), the `code.Store` port, `memcode` and `mongocode` stores, the `codetest` conformance suite.
- A write-only transport: `server/codeapi` under `/api/v1/projects/{project}/code/`, a `CluedOptions.Code` opener namespaced like storeapi, `server run` and `server init` wiring, and the `code/remotecode` client adapter passing `codetest` over `httptest`.
- The client collector `session/codesync`: file enumeration (`git ls-files -co --exclude-standard`), filters, stamp cache, hashing, redaction, the manifest/upload/commit cycle, state and lock files under `<config dir>/codesync/`.
- Triggers in `hook relay`: PostToolUse (Write, Edit, MultiEdit, NotebookEdit), PostToolUse Bash, FileChanged, SessionStart, SessionEnd, Stop, CwdChanged, WorktreeCreate and WorktreeRemove.
- Config: `codeSync` (opt-in, default off), `codeSyncMaxFileBytes`, `codeSyncExclude`, the `CLUED_CODE_SYNC*` env overrides, `codeSync` in `pulledConfigKeys`, and a per-repository `codeSync: false` in `mind-palace.config.yaml`.
- CLI: `mind-palace session code-sync [--full] [--dry-run] [--path DIR]` and `session code-sync status` (local state only: last sync, pending, refused).
- clued-cloud: the `Code` opener under `cloud.Namespace`, a `Limits.CodeSync` entitlement and `Limits.CodeBytes` quota, metadata-only accounts refused, metrics counters, account, workspace and content purgers, retention.
- PROTOCOL.md, docs/server.md and README sections for the new routes, config keys, state files and privacy behaviour.

**Out of scope:**

- FalkorDB itself: the indexer, parsers (tree-sitter or Go's `go/packages`), graph schema and the FalkorDB container in the stack. — Collection comes first; the change log is the indexer's input, so it can be built and replayed later without re-collecting.
- Any read path for the collected code: GET routes, MCP tools, web pages, search, export. — Requested explicitly: collect only, expose nothing yet. Only the Go port (used by tests and the future indexer) reads.
- Local mode (the MongoDB daemon without a server). — The request is for the clued server; the `code.Store` port makes a local backend a small follow-up.
- Git history (past commits, blame). — We mirror the working state as Claude Code sees it; HEAD commit and branch are recorded per change so history can be added later.
- A filesystem watcher (fsnotify) for edits made outside Claude Code. — Out-of-band edits are picked up by the next reconcile (SessionStart, Stop, Bash, FileChanged); a resident watcher is a separate decision for the daemon.
- Blob garbage collection. — Unreferenced blobs are harmless for collection; a mark-and-sweep over trees is added with the indexer or when quotas need it.

---

# Part 2 — Implementation

**Architecture:** `hooks/event-relay` → `scripts/clued hook relay` → `ingest` → `catchup` → **`codesync.Run`** → `code/remotecode` → HTTPS → `server/codeapi` (auth, `NamespaceCtx`) → `code.Store` (`mongocode`) → collections `code_blobs`, `code_trees`, `code_worktrees`, `code_changes`. Later: an indexer reads `Changes(afterSeq)` and `Blob(sha)` through the port and writes FalkorDB.

**Tech Stack:** Go 1.26, existing `remote` client, `mongo-driver/v2`, the `git` CLI (already used by `gitinfo`), `crypto/sha256`. No new third-party dependency; FalkorDB is not added in this plan.

## Constraints

- `CGO_ENABLED=0` and the `go-licenses` policy: no new linked dependency needed.
- The transport holds no rules beyond validation and namespacing (CLAUDE.md); limits and entitlements come from options supplied by the composer (clued-cloud).
- `hook relay` prints nothing and always exits 0; code sync must never block or fail a hook.
- Existing config keys unchanged; new keys are optional and default off.
- Paths from hook requests are validated inline with `filepath.IsLocal` / `filepath.Clean` + prefix checks.
- Public-module changes land in `robbiebyrd/clued` first; clued-cloud pins main's commit.

## Phase 1: Code store port and stores

### 1.1: Model, port and conformance suite

**Files:**
- Create: `mind-palace/code/model.go`
- Create: `mind-palace/code/store.go`
- Create: `mind-palace/code/validate.go`
- Create: `mind-palace/code/codetest/codetest.go`

**Depends on:** none

**Interfaces:**
- Produces: `code.Store`, `code.Blob`, `code.Entry`, `code.Worktree`, `code.Change`, `code.ValidPath`, `code.ValidSHA`, `codetest.Run(t, factory)`

- [ ] **Step 1: Define the model and the `Store` port** — As in Design, including `Owner`, `DefaultBranch`, `GitOID`, `Dirty`, `Unpushed`, `Session`, `Upstream`, `Ahead` and `SessionSeen`; validation helpers for paths (slash-separated, `filepath.IsLocal`, ≤ 1,024 bytes, no NUL), SHA-256 hex, git object ids (40 or 64 hex), worktree ids (`wt_` + 16 hex) and refs.
- [ ] **Step 2: Write `codetest` covering every port behaviour listed under Testing**

### 1.2: Memory and Mongo stores

**Files:**
- Create: `mind-palace/code/memcode/memcode.go`
- Create: `mind-palace/code/mongocode/mongocode.go`
- Test: `mind-palace/code/memcode/memcode_test.go`
- Test: `mind-palace/code/mongocode/mongocode_test.go`

**Depends on:** 1.1

- [ ] **Step 1: Implement `memcode`**
- [ ] **Step 2: Implement `mongocode` with the four collections, indexes and a per-project sequence counter; transactional commit when available, idempotent ordering otherwise**
- [ ] **Step 3: Run the conformance suite** — `MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./code/...`

## Phase 2: Write-only transport

### 2.1: `server/codeapi` and wiring

**Files:**
- Create: `mind-palace/server/codeapi/codeapi.go`
- Modify: `mind-palace/server/clued.go`
- Modify: `mind-palace/cli/server_run.go`
- Modify: `mind-palace/cli/server_init.go`
- Modify: `mind-palace/server/stack/stack.go`
- Test: `mind-palace/server/codeapi/codeapi_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `code.Store`, `auth.Middleware`, `CluedOptions.NamespaceCtx`
- Produces: `CluedOptions.Code func(ctx, project string) (code.Store, error)`, `CluedOptions.BeforeCodeWrite`, routes under `/api/v1/projects/{project}/code/`

- [ ] **Step 1: Implement the seven routes with the envelope, limits, hash verification and inline path validation; set each entry's `session` from the change**
- [ ] **Step 2: Mount in `NewClued` when `Code` is set; namespace like storeapi; stamp `Owner` from the principal and answer 404 for another owner's worktree; add `BeforeCodeWrite(ctx, project, bytes)` for the composer's quota/entitlement check**
- [ ] **Step 3: `server run --code-store` / `CLUED_SERVER_CODE_STORE`; `server init` enables it in the generated stack**

### 2.2: Remote adapter

**Files:**
- Create: `mind-palace/code/remotecode/remotecode.go`
- Test: `mind-palace/code/remotecode/remotecode_test.go`

**Depends on:** 2.1

- [ ] **Step 1: Client for the write routes and `capabilities` over `remote.Client`, mapping 404/403/409/413 to typed errors**
- [ ] **Step 2: Run `codetest` over `httptest` against `codeapi`, reading through the server-side store**

## Phase 3: Client collector and triggers

### 3.1: Config keys

**Files:**
- Modify: `mind-palace/session/config.go`
- Modify: `mind-palace/cli/session_login.go`
- Modify: `mind-palace/config/config.go`

**Depends on:** none

- [ ] **Step 1: Add `codeSync` (*bool), `codeSyncMaxFileBytes`, `codeSyncExclude` with `CLUED_CODE_SYNC*` env overrides and a `CodeSyncEnabled()` helper (nil = off, false when `captureContent` is false)**
- [ ] **Step 2: Add the keys to `pulledConfigKeys`; add `codeSync: false` to `mind-palace.config.yaml`**

### 3.2: `session/codesync`

**Files:**
- Create: `mind-palace/session/codesync/codesync.go`
- Create: `mind-palace/session/codesync/enumerate.go`
- Create: `mind-palace/session/codesync/filter.go`
- Create: `mind-palace/session/codesync/state.go`
- Modify: `mind-palace/session/gitinfo/gitinfo.go`
- Modify: `mind-palace/collect/gitprobe/gitprobe.go`
- Modify: `mind-palace/session/redact/redact.go`
- Test: `mind-palace/session/codesync/codesync_test.go`

**Depends on:** 2.2, 3.1

- [ ] **Step 1: gitinfo: `Root`, `Head`, `StatusPaths`, `ListFiles`, `TreeOIDs` (`ls-tree -r -z HEAD`), `Upstream` (`rev-parse @{upstream}`, `rev-list --count @{upstream}..HEAD`), `UnpushedPaths` (`diff --name-only -z @{upstream} HEAD`) and `DefaultBranch` (shared with `gitprobe` rather than duplicated), all with the existing 2 s timeout**
- [ ] **Step 2: Filters: deny list, `excludePaths` per file, `codeSyncExclude`, size cap, binary detection**
- [ ] **Step 3: State and lock (reuse catch-up's helpers if they can be lifted into a shared package, otherwise mirror them)**
- [ ] **Step 4: `Run` as in Design, with budget, `pending`, `refused` and back-off**

### 3.3: Relay triggers and CLI

**Files:**
- Create: `mind-palace/cli/session_codesync.go`
- Modify: `mind-palace/cli/hook_relay.go`
- Modify: `mind-palace/cli/session.go`
- Test: `mind-palace/plugin/codesync_test.go`

**Depends on:** 3.2

- [ ] **Step 1: Map hook events to `codesync.Request` (table in Design) and call it last within the relay budget**
- [ ] **Step 2: `mind-palace session code-sync [--full] [--dry-run] [--path DIR]` and `session code-sync status`**
- [ ] **Step 3: Plugin test: real hooks against a test server; nothing sent when `codeSync` is off**

## Phase 4: Documentation and setup

### 4.1: Docs and `/clued-setup`

**Files:**
- Modify: `PROTOCOL.md`
- Modify: `docs/server.md`
- Modify: `README.md`
- Modify: `commands/clued-setup.md`
- Modify: `mind-palace/README.md`
- Modify: `.claude/CLAUDE.md`

**Depends on:** 3.3

- [ ] **Step 1: PROTOCOL.md: the code routes (new §7.x), the codesync state format (next to §4.4), the config keys (§11.1)**
- [ ] **Step 2: docs/server.md: enabling the code store; README: what is uploaded, the deny list, how to opt out**
- [ ] **Step 3: `/clued-setup`: ask about code sync in remote mode, with a plain statement of what leaves the machine**
- [ ] **Step 4: CLAUDE.md layout table: `code/`, `server/codeapi/`, `session/codesync/`**

## Phase 5: clued-cloud

### 5.1: Wiring, entitlement, quota, metrics, purgers

**Files:**
- Create: `clued-cloud/cloud/code.go`
- Modify: `clued-cloud/go.mod`
- Modify: `clued-cloud/cloud/cloud.go`
- Modify: `clued-cloud/cmd/clued-cloud/main.go`
- Modify: `clued-cloud/account/plans.go`
- Modify: `clued-cloud/metrics/metrics.go`
- Modify: `clued-cloud/cloud/retention.go`
- Modify: `clued-cloud/web/security.go`
- Modify: `clued-cloud/README.md`
- Test: `clued-cloud/cloud/code_test.go`

**Depends on:** 2.1

- [ ] **Step 1: Pin the public module at the merged commit** — `go get github.com/robbiebyrd/clued/mind-palace@<sha>`
- [ ] **Step 2: `Code` opener under `c.namespace` with project recording; `BeforeCodeWrite` checks `Limits.CodeSync`, `Limits.CodeBytes` and the metadata-only policy**
- [ ] **Step 3: Metrics counters with help text; purgers for account, workspace and content; retention of `code_changes`**
- [ ] **Step 4: Audit kind and sentence when code sync is turned on for an account**

## Acceptance Criteria

- [ ] With `codeSync` on in remote mode, a Write or Edit in a Claude Code session results, within the same hook run, in the file's redacted content on the server and a change-log entry for that worktree and branch.
- [ ] Deletes and renames (including through Bash) and branch switches are reflected in the server's tree for that (worktree, ref) by the next Stop at the latest.
- [ ] A first full reconcile of a repository completes across hook runs without any hook exceeding the relay budget, and `session code-sync --full` imports it in one go.
- [ ] Ignored files, files on the deny list, files matching `excludePaths`/`codeSyncExclude`, binaries and files above the size cap never have their content uploaded.
- [ ] With `codeSync` off (the default), per-repository `codeSync: false`, `captureContent: false` or `CLUED_NO_CAPTURE=1`, nothing is sent.
- [ ] No HTTP route, MCP tool or web page returns collected code, paths or trees; `capabilities` returns limits only.
- [ ] Code written by one account or workspace is stored only under its namespace; `codetest` passes for `memcode`, `mongocode` and `remotecode`.
- [ ] Every worktree and change records the owning account from the token, and a workspace member can't write to or detect another member's worktree.
- [ ] Every entry records `git_oid` and `dirty` correctly, refreshed when HEAD moves, and every worktree records its default branch, so a base graph can be built from committed content only.
- [ ] Every entry records `unpushed` and every change its `upstream` and `ahead`, refreshed after a push, pull, fetch or force-push, so pushed content (base and branch layers) and each checkout's unpushed and uncommitted work (its session overlay) can be told apart; every entry names the session that last wrote it, and each worktree records its sessions' start and end.
- [ ] Two checkouts of one branch (two people, or one person on two machines) produce identical clean, pushed entries and separate, owner-tagged overlays.
- [ ] clued-cloud refuses code writes without the entitlement, past the quota or for metadata-only accounts, counts them in metrics, and purges code with the account or workspace.
- [ ] PROTOCOL.md, docs/server.md and README describe the routes, keys, state files and what is uploaded; `gofmt`, `go vet` and `go test -race ./...` pass in both repositories, with and without MongoDB.

## Self-Review

| Requirement | Delivered by |
|---|---|
| Send the current repo/worktree/branch code to the clued server | 1.1, 2.1, 3.2 |
| Continuously, as files are edited, added and deleted | 3.3, Step 1 |
| Don't expose any of the collected data yet | 2.1, Step 1 (write-only routes) |
| Ready for FalkorDB indexing later | 1.1 (change log and read side of the port) |
| Hosted service tenancy and limits | 5.1 |
| Partitioning: base per project plus overlays per (worktree, ref), file-local facts by content | Design, "Partitioning for the indexer"; 1.1, Step 1 |
| Owner per worktree and change (workspace members) | 2.1, Step 2 |
| Committed vs uncommitted (`git_oid`, `dirty`) and the default branch | 1.1, Step 1; 3.2, Steps 1 and 4 |
| Session overlay over the branch for two people or two machines on one branch (`unpushed`, `upstream`, `session`, session activity) | Design, "Partitioning for the indexer"; 1.1, Step 1; 2.1, Step 1; 3.2, Steps 1 and 4; 3.3, Step 1 |

**Residual risk:** Uploading source is a significant trust step even opt-in; the deny list and redaction reduce but cannot eliminate secret exposure in code. Mongo storage of every file version grows without GC until a sweep is added. Edits outside Claude Code lag until the next reconcile.

## Next Steps

1. Plan the FalkorDB indexer on the partitioning decided here: one graph per namespaced project, a base layer for the default branch and branch layers per (ref, upstream commit) built from clean, pushed entries, session overlays per (worktree, ref) for unpushed and uncommitted work, file-local facts cached by blob SHA-256, cross-file edges resolved per layer; graph schema (Repo, File, Package, Symbol; CONTAINS, DEFINES, IMPORTS, CALLS, REFERENCES, IMPLEMENTS), parsers, and the FalkorDB container in the `server init` stack and in clued-cloud.
2. Blob garbage collection and per-account usage reporting.
3. A local-mode backend for `code.Store`.
4. Read APIs and visualizations once the graph exists.
